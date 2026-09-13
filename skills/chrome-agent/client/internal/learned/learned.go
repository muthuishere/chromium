// Package learned turns what an agent learns about a site into canon, as a REVIEW.
//
// apl ADR-0008 says promotion from learned to canon is deliberate and reviewed, and shipped no
// tooling for it. Without tooling, everything an agent learns piles up in a log nobody opens while
// canon ages into fiction two directories away. So the review is a diff:
//
//	chrome-agent note <domain> "what you learned"   # capture it the moment you learn it
//	chrome-agent promote [<domain>]                 # what is known but not written down
//	chrome-agent promote <domain> --apply           # append it into the playbook's kept block
//
// Three rules are not negotiable:
//
//   - canon is never rewritten. Text is APPENDED below traps.md's keep-marker, the one block the
//     generator will not touch, so a promoted trap survives the next regeneration.
//   - promoting twice is a no-op. Items are deduped against the existing traps.md on words alone,
//     so re-wrapping or re-punctuating a line does not create a second copy.
//   - DRIFT IS OFFERED, NOT PROMOTED. A drift line is a failure report ("`x:post` drifted:
//     post-url required"), which is usually a usage mistake, not something the site lies about.
//     Writing failure reports into a traps file is how a traps file stops being read.
package learned

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/sites"
)

// KeepMarker is the contract with the playbook generator. Everything below it is hand-written (or
// promoted) and survives regeneration; everything above it is generated and will be replaced.
const KeepMarker = "<!-- keep: hand-written below — the generator never touches this -->"

// Source selects which kind of knowledge a review considers.
type Source string

const (
	SourceNote  Source = "note"
	SourceDrift Source = "drift"
	SourceAll   Source = "all"
)

// recipeSite maps a recipe key's prefix to the domain whose playbook owns it.
var recipeSite = map[string]string{
	"linkedin":   "linkedin.com",
	"x":          "x.com",
	"facebook":   "facebook.com",
	"instagram":  "instagram.com",
	"reddit":     "reddit.com",
	"youtube":    "youtube.com",
	"hackernews": "news.ycombinator.com",
}

// Dir holds one ndjson per domain: ~/.config/chrome-agent/learned/<domain>.ndjson.
func Dir() string { return filepath.Join(paths.ConfigDir(), "learned") }

// DriftPath is where the verification loop records recipes that stopped working.
func DriftPath() string { return filepath.Join(paths.ConfigDir(), "drift.ndjson") }

// PlaybooksDir resolves the playbook tree without hardcoding one checkout — the assumption that
// broke the bash CLI on Ubuntu was exactly this kind of path.
func PlaybooksDir() string {
	if v := os.Getenv("CHROME_AGENT_PLAYBOOKS"); v != "" {
		return v
	}
	if v := os.Getenv("CHROME_AGENT_SKILL"); v != "" {
		return filepath.Join(v, "playbooks")
	}
	return filepath.Join(paths.Fork(), "skills", "chrome-agent", "playbooks")
}

// ---- note -------------------------------------------------------------------------------------

// Record is one learned line.
type Record struct {
	TS       string `json:"ts"`
	Domain   string `json:"domain"`
	Text     string `json:"text"`
	Promoted bool   `json:"promoted"`
}

type NoteResult struct {
	Noted       string `json:"noted"`
	Text        string `json:"text"`
	File        string `json:"file"`
	PromoteWith string `json:"promote_with"`
}

// Note appends what you just learned about a domain. It is deliberately cheap: a capture that costs
// a review is a capture that never happens.
func Note(domain, text string) (*NoteResult, error) {
	domain = sites.Normalize(domain)
	text = strings.TrimSpace(text)
	if domain == "" || text == "" {
		return nil, fmt.Errorf(`note <domain> "<what you learned>"`)
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return nil, err
	}
	rec := Record{
		TS:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Domain: domain,
		Text:   text,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	p := NotePath(domain)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &NoteResult{Noted: domain, Text: text, File: p, PromoteWith: "chrome-agent promote " + domain}, nil
}

func NotePath(domain string) string {
	return filepath.Join(Dir(), sites.Normalize(domain)+".ndjson")
}

// ---- pending ----------------------------------------------------------------------------------

// Item is one thing that is known and not written down.
type Item struct {
	Source Source `json:"source"`
	Line   int    `json:"line"` // index into the notes file; -1 for drift
	TS     string `json:"ts,omitempty"`
	Text   string `json:"text"`
}

var nonWord = regexp.MustCompile(`[^a-z0-9 ]+`)

// norm compares on WORDS only: punctuation and line-wrapping must not manufacture a duplicate.
func norm(t string) []string {
	s := nonWord.ReplaceAllString(strings.ToLower(t), " ")
	if len(s) > 400 {
		s = s[:400]
	}
	return strings.Fields(s)
}

// alreadySays is the dedupe. The first dozen words are the fingerprint — enough to recognise the
// same trap reworded, short enough that an appended clause does not read as new knowledge.
func alreadySays(traps, text string) bool {
	want := norm(text)
	if len(want) == 0 {
		return true
	}
	if len(want) > 12 {
		want = want[:12]
	}
	return strings.Contains(strings.Join(norm(traps), " "), strings.Join(want, " "))
}

func notesFor(domain string) []Record {
	b, err := os.ReadFile(NotePath(domain))
	if err != nil {
		return nil
	}
	var out []Record
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, Record{}) // keep line indices aligned with the file
			continue
		}
		var r Record
		if json.Unmarshal([]byte(line), &r) != nil {
			out = append(out, Record{})
			continue
		}
		out = append(out, r)
	}
	return out
}

type driftRec struct {
	TS  string `json:"ts"`
	Key string `json:"key"`
	Why string `json:"why"`
}

func driftFor(domain string) []driftRec {
	b, err := os.ReadFile(DriftPath())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []driftRec
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var d driftRec
		if json.Unmarshal([]byte(line), &d) != nil {
			continue
		}
		if recipeSite[strings.SplitN(d.Key, ":", 2)[0]] != domain {
			continue
		}
		sig := d.Key + "\x00" + d.Why
		if seen[sig] { // the learning loop records every retry; one entry per failure
			continue
		}
		seen[sig] = true
		out = append(out, d)
	}
	return out
}

// TrapsPath is the file promotion appends to.
func TrapsPath(domain string) string {
	return filepath.Join(PlaybooksDir(), domain, "traps.md")
}

func trapsText(domain string) string {
	b, err := os.ReadFile(TrapsPath(domain))
	if err != nil {
		return ""
	}
	return string(b)
}

// Pending reports what this domain knows and has not written down. Anything already said in
// traps.md, and any note already promoted, is left out.
func Pending(domain string, src Source) []Item {
	traps := trapsText(domain)
	var items []Item
	if src == SourceAll || src == SourceNote {
		for i, n := range notesFor(domain) {
			if n.Text == "" || n.Promoted || alreadySays(traps, n.Text) {
				continue
			}
			items = append(items, Item{Source: SourceNote, Line: i, TS: n.TS, Text: n.Text})
		}
	}
	if src == SourceAll || src == SourceDrift {
		for _, d := range driftFor(domain) {
			text := fmt.Sprintf("`%s` drifted: %s", d.Key, d.Why)
			if alreadySays(traps, text) {
				continue
			}
			items = append(items, Item{Source: SourceDrift, Line: -1, TS: d.TS, Text: text})
		}
	}
	return items
}

// Domains is every domain with a playbook — the set `promote` reviews when given no argument.
func Domains() []string {
	entries, err := os.ReadDir(PlaybooksDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ---- promote ----------------------------------------------------------------------------------

type Opts struct {
	Domain string // empty = every domain with a playbook
	Apply  bool   // actually append to traps.md
	// IncludeDrift promotes drift lines too. OFF by default: a drift line is a failure report, not
	// a trap. With Apply off, drift is always listed — the review should see it.
	IncludeDrift bool
}

type DomainReport struct {
	Pending   []Item `json:"pending"`
	Applied   []Item `json:"applied,omitempty"`
	Offered   []Item `json:"offered_not_applied,omitempty"`
	TrapsFile string `json:"traps_file"`
	Error     string `json:"error,omitempty"`
}

type Report struct {
	Applied      bool                     `json:"applied"`
	IncludeDrift bool                     `json:"include_drift"`
	Pending      int                      `json:"pending"`
	Domains      map[string]*DomainReport `json:"domains"`
	Note         string                   `json:"note,omitempty"`
}

// Promote is the review. Without Apply it only reports; with Apply it appends the eligible items
// below the keep-marker and marks the promoted notes so the next review does not show them again.
func Promote(o Opts) (*Report, error) {
	domains := Domains()
	if d := sites.Normalize(o.Domain); d != "" {
		domains = []string{d}
	}
	rep := &Report{Applied: o.Apply, IncludeDrift: o.IncludeDrift, Domains: map[string]*DomainReport{}}
	for _, dom := range domains {
		items := Pending(dom, SourceAll)
		if len(items) == 0 {
			continue
		}
		rep.Pending += len(items)
		dr := &DomainReport{Pending: items, TrapsFile: TrapsPath(dom)}
		rep.Domains[dom] = dr

		var eligible, offered []Item
		for _, it := range items {
			if it.Source == SourceDrift && !o.IncludeDrift {
				offered = append(offered, it)
				continue
			}
			eligible = append(eligible, it)
		}
		dr.Offered = offered
		if !o.Apply || len(eligible) == 0 {
			continue
		}
		if err := apply(dom, eligible); err != nil {
			dr.Error = err.Error()
			continue
		}
		dr.Applied = eligible
	}
	if !o.IncludeDrift {
		rep.Note = "drift lines are listed, not promoted — a drift line is a failure report, not a trap. Pass --include-drift to promote them anyway."
	}
	return rep, nil
}

// apply appends below the keep-marker, creating the marker if the file has none. It never edits a
// single byte above it.
func apply(domain string, items []Item) error {
	f := TrapsPath(domain)
	b, err := os.ReadFile(f)
	if err != nil {
		return fmt.Errorf("no playbook at %s", f)
	}
	t := string(b)
	if !strings.Contains(t, KeepMarker) {
		t = strings.TrimRight(t, "\n") + "\n\n" + KeepMarker + "\n"
	}
	head, tail, _ := strings.Cut(t, KeepMarker)
	var add strings.Builder
	for _, it := range items {
		ts := it.TS
		if len(ts) > 10 {
			ts = ts[:10]
		}
		fmt.Fprintf(&add, "- %s  _(promoted %s, from %s)_\n", it.Text, ts, it.Source)
	}
	out := head + KeepMarker + strings.TrimRight(tail, "\n") + "\n" + add.String()
	if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
		return err
	}
	return markPromoted(domain, items)
}

// markPromoted rewrites the notes file in place, flipping `promoted` on the lines that made it into
// canon. Without this, every future review re-offers knowledge that is already written down.
func markPromoted(domain string, items []Item) error {
	p := NotePath(domain)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil // nothing to mark: the items came from drift
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for _, it := range items {
		if it.Source != SourceNote || it.Line < 0 || it.Line >= len(lines) {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(lines[it.Line]), &r) != nil {
			continue
		}
		r.Promoted = true
		if nb, err := json.Marshal(r); err == nil {
			lines[it.Line] = string(nb)
		}
	}
	return os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
