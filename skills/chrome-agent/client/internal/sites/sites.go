// Package sites resolves the per-domain site definitions (ADR 0004).
//
// Resolution order, highest first — this IS the design, not an implementation detail:
//
//	$CHROME_AGENT_SITES            explicit override (tests, CI)
//	~/.config/chrome-agent/sites/  INSTALLED copy — editable, WINS
//	embedded                       shipped with the binary (go:embed)
//
// The installed copy winning is the point: when a site re-skins at 2am on a server, the fix is one
// JSON file in ~/.config, with no redeploy and no repo checkout. `Sync` writes the embedded copies
// out and REFUSES to clobber a file someone edited, because silently reverting an operator's hotfix
// is the same class of failure as silently ageing a last_verified stamp.
package sites

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deemwarhq/chrome-agent/assets"
	"github.com/deemwarhq/chrome-agent/internal/paths"
)

var embedded = assets.Sites

const embedRoot = "sites"

// Definition is one domain's page-level truth. Only the fields the client acts on are typed; the
// rest of the file round-trips through Raw so an edit by hand is never silently dropped.
type Definition struct {
	Domain  string   `json:"domain"`
	Aliases []string `json:"aliases,omitempty"`
	Home    string   `json:"home"`
	Login   struct {
		URL   string `json:"url"`
		Note  string `json:"note,omitempty"`
		TwoFA bool   `json:"twofa,omitempty"`
	} `json:"login"`
	Logout struct {
		Method   string `json:"method,omitempty"` // url | dom | cookies
		URL      string `json:"url,omitempty"`
		Selector string `json:"selector,omitempty"`
		Note     string `json:"note,omitempty"`
	} `json:"logout"`
	Auth struct {
		ProbeURL string `json:"probe_url,omitempty"`
		ProbeJS  string `json:"probe_js"`
	} `json:"auth"`
	Read struct {
		Verb       string `json:"verb,omitempty"`
		FixtureURL string `json:"fixture_url,omitempty"`
	} `json:"read"`
	Write        []string `json:"write,omitempty"`
	Traps        []string `json:"traps,omitempty"`
	Status       string   `json:"status"`
	Source       string   `json:"source,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	ProbeChecked string   `json:"probe_checked,omitempty"`

	// Where this definition came from — "override" | "installed" | "embedded".
	From string `json:"-"`
	Path string `json:"-"` // empty when embedded
}

// Normalize turns whatever a human typed into a bare host: a URL, a www., a trailing path.
func Normalize(d string) string {
	d = strings.TrimSpace(strings.ToLower(d))
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	d = strings.SplitN(d, "/", 2)[0]
	return strings.TrimPrefix(d, "www.")
}

func dirs() []struct{ path, label string } {
	var out []struct{ path, label string }
	if v := os.Getenv("CHROME_AGENT_SITES"); v != "" {
		out = append(out, struct{ path, label string }{v, "override"})
	}
	home, _ := os.UserHomeDir()
	out = append(out, struct{ path, label string }{filepath.Join(home, ".config", "chrome-agent", "sites"), "installed"})
	return out
}

func parse(b []byte, from, path string) (*Definition, error) {
	var d Definition
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	d.From, d.Path = from, path
	return &d, nil
}

// Load returns the definition that WINS for this domain, or nil if nothing defines it.
func Load(domain string) (*Definition, error) {
	domain = Normalize(domain)
	if domain == "" {
		return nil, fmt.Errorf("no domain given")
	}
	for _, d := range dirs() {
		p := filepath.Join(d.path, domain+".json")
		if b, err := os.ReadFile(p); err == nil {
			return parse(b, d.label, p)
		}
	}
	if b, err := embedded.ReadFile(embedRoot + "/" + domain + ".json"); err == nil {
		return parse(b, "embedded", "")
	}
	// An alias is a second name for the same file — a human types "twitter" and "hn" far more often
	// than the canonical host.
	for _, def := range All() {
		for _, a := range def.Aliases {
			if strings.ToLower(a) == domain {
				return def, nil
			}
		}
	}
	return nil, nil
}

// All returns every known definition, each from wherever it wins.
func All() []*Definition {
	seen := map[string]*Definition{}
	for _, d := range dirs() {
		entries, err := os.ReadDir(d.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			key := strings.TrimSuffix(name, ".json")
			if _, taken := seen[key]; taken {
				continue
			}
			b, err := os.ReadFile(filepath.Join(d.path, name))
			if err != nil {
				continue
			}
			if def, err := parse(b, d.label, filepath.Join(d.path, name)); err == nil {
				seen[key] = def
			}
		}
	}
	_ = fs.WalkDir(embedded, embedRoot, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		key := strings.TrimSuffix(filepath.Base(p), ".json")
		if _, taken := seen[key]; taken {
			return nil
		}
		if b, err := embedded.ReadFile(p); err == nil {
			if def, err := parse(b, "embedded", ""); err == nil {
				seen[key] = def
			}
		}
		return nil
	})
	out := make([]*Definition, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

// Problems reports what makes a definition unusable. Empty means usable.
//
// The probe rules are load-bearing, not style: a probe runs against a live logged-in session, so one
// that navigates or clicks is not a probe, it is an action. And `document.cookie` raises
// SecurityError on an opaque origin, so an unguarded read throws and the verdict is lost.
func Problems(d *Definition) []string {
	var out []string
	if d.Domain == "" {
		out = append(out, "missing required field: domain")
	}
	if d.Home == "" {
		out = append(out, "missing required field: home")
	}
	if d.Status != "verified" && d.Status != "unverified" {
		out = append(out, fmt.Sprintf("status must be verified|unverified (got %q)", d.Status))
	}
	probe := d.Auth.ProbeJS
	switch {
	case strings.TrimSpace(probe) == "":
		out = append(out, "auth.probe_js is empty — a site with no probe can never report better than unknown")
	default:
		if !strings.Contains(probe, "return") {
			out = append(out, "auth.probe_js never returns — it must return {signed_in, as?}")
		}
		for bad, why := range map[string]string{
			"location.href =":  "probe must not navigate",
			"location.assign":  "probe must not navigate",
			"location.replace": "probe must not navigate",
			".click(":          "probe must not click",
			"document.write":   "probe must not write to the page",
		} {
			if strings.Contains(probe, bad) {
				out = append(out, fmt.Sprintf("auth.probe_js: %s (found %q)", why, bad))
			}
		}
		if strings.Contains(probe, "document.cookie") && !strings.Contains(probe, "try") {
			out = append(out, "auth.probe_js reads document.cookie without try/catch — it raises SecurityError on an opaque origin and the probe would throw")
		}
	}
	switch d.Logout.Method {
	case "", "url", "dom", "cookies":
	default:
		out = append(out, fmt.Sprintf("logout.method must be url|dom|cookies (got %q)", d.Logout.Method))
	}
	if d.Logout.Method == "url" && d.Logout.URL == "" {
		out = append(out, "logout.method=url needs logout.url")
	}
	if d.Logout.Method == "dom" && d.Logout.Selector == "" {
		out = append(out, "logout.method=dom needs logout.selector")
	}
	return out
}

type SyncResult struct {
	InstalledDir string   `json:"installed_dir"`
	Installed    []string `json:"installed"`
	Replaced     []string `json:"replaced"`
	Kept         []string `json:"kept"`
	Unchanged    []string `json:"unchanged"`
	Force        bool     `json:"force"`
	DryRun       bool     `json:"dry_run"`
}

// Sync writes the embedded definitions into the installed dir. A locally edited file is KEPT unless
// force says otherwise, and the result names what it left alone.
func Sync(force, dry bool) (*SyncResult, error) {
	dst := filepath.Join(paths.ConfigDir(), "sites")
	res := &SyncResult{InstalledDir: dst, Force: force, DryRun: dry,
		Installed: []string{}, Replaced: []string{}, Kept: []string{}, Unchanged: []string{}}
	if !dry {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return nil, err
		}
	}
	entries, err := embedded.ReadDir(embedRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		want, err := embedded.ReadFile(embedRoot + "/" + e.Name())
		if err != nil {
			continue
		}
		target := filepath.Join(dst, e.Name())
		cur, err := os.ReadFile(target)
		switch {
		case err != nil:
			if !dry {
				if err := os.WriteFile(target, want, 0o644); err != nil {
					return nil, err
				}
			}
			res.Installed = append(res.Installed, e.Name())
		case string(cur) == string(want):
			res.Unchanged = append(res.Unchanged, e.Name())
		case force:
			if !dry {
				if err := os.WriteFile(target, want, 0o644); err != nil {
					return nil, err
				}
			}
			res.Replaced = append(res.Replaced, e.Name())
		default:
			res.Kept = append(res.Kept, e.Name())
		}
	}
	sort.Strings(res.Installed)
	sort.Strings(res.Replaced)
	sort.Strings(res.Kept)
	sort.Strings(res.Unchanged)
	return res, nil
}

// Shadowed returns definitions that exist but are OUTRANKED by a higher-priority copy. They are not
// what runs today — and they are what runs after a `sync --force`, so they are worth validating.
func Shadowed() []*Definition {
	winners := map[string]bool{}
	for _, d := range All() {
		winners[d.Domain] = true
	}
	var out []*Definition
	seen := map[string]bool{}
	for _, dir := range dirs() {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			key := dir.label + "/" + e.Name()
			if seen[key] {
				continue
			}
			seen[key] = true
			b, err := os.ReadFile(filepath.Join(dir.path, e.Name()))
			if err != nil {
				continue
			}
			def, err := parse(b, dir.label, filepath.Join(dir.path, e.Name()))
			if err != nil {
				continue
			}
			// A definition is shadowed when the winner for its domain came from somewhere else.
			if w, _ := Load(def.Domain); w != nil && w.Path != def.Path {
				out = append(out, def)
			}
		}
	}
	entries, _ := embedded.ReadDir(embedRoot)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := embedded.ReadFile(embedRoot + "/" + e.Name())
		if err != nil {
			continue
		}
		def, err := parse(b, "embedded", "")
		if err != nil {
			continue
		}
		if w, _ := Load(def.Domain); w != nil && w.From != "embedded" {
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}
