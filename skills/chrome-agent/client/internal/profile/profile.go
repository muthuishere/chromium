// Package profile is the profile half of the lifecycle in ADR 0005: create, list, delete.
//
// A browser profile IS the credential — not a token, a directory. So every verb here is explicit and
// auditable, and `Delete` is a REVOCATION, not an `rm`. Each guard below exists because its absence
// already shipped:
//
//   - the liveness guard uses Lstat, because SingletonLock is a SYMLINK whose target never exists.
//     A plain existence check follows the link, finds nothing, and reports "free" while a browser is
//     running — which is why that guard had never once fired.
//   - the spool a profile maps to is asked of paths.SpoolFor and never re-derived here. Two copies
//     of that rule is how BOTH spool bugs got in (a trailing slash forked one profile into two
//     spools; a basename collision merged two profiles onto one).
package profile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/deemwarhq/chrome-agent/internal/exit"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/sites"
)

// Error carries the exit-code contract out of this package so the dispatcher never has to guess
// which failure it is looking at. ADR 0005: an undocumented code is indistinguishable from the
// wrong one.
type Error struct {
	Code   int    `json:"exit"`
	Slug   string `json:"error"`
	Detail string `json:"detail"`
}

func (e *Error) Error() string { return e.Detail }

func usageErr(slug, format string, a ...any) *Error {
	return &Error{Code: exit.Usage, Slug: slug, Detail: fmt.Sprintf(format, a...)}
}

// ---- lock state -------------------------------------------------------------------------------

// Lock states, as reported by List.
const (
	LockFree  = "free"
	LockStale = "STALE-LOCK" // a killed browser left it behind; the next launch silently no-ops
)

// LockState reports "free", "running:<pid>" or "STALE-LOCK" for a profile directory.
//
// Lstat, NOT Stat: SingletonLock points at "<host>-<pid>", a target that never exists as a file.
func LockState(dir string) (state string, pid int) {
	link := filepath.Join(strings.TrimRight(dir, "/"), "SingletonLock")
	fi, err := os.Lstat(link)
	if err != nil {
		return LockFree, 0
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		// A regular file where a symlink belongs: something is there, but we cannot name an owner.
		return LockStale, 0
	}
	target, err := os.Readlink(link)
	if err != nil {
		return LockStale, 0
	}
	pid = pidFromLockTarget(target)
	if pid > 0 && alive(pid) {
		return "running:" + strconv.Itoa(pid), pid
	}
	return LockStale, pid
}

// Running answers the one question Delete must not get wrong.
func Running(dir string) (bool, int) {
	state, pid := LockState(dir)
	return strings.HasPrefix(state, "running:"), pid
}

func pidFromLockTarget(target string) int {
	i := strings.LastIndex(target, "-")
	if i < 0 || i+1 >= len(target) {
		return 0
	}
	n, err := strconv.Atoi(target[i+1:])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// alive asks the OS. Signal 0 is the portable "does this pid exist and may I touch it".
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// ---- create -----------------------------------------------------------------------------------

type CreateResult struct {
	Profile string `json:"profile"`
	Existed bool   `json:"existed"`
	Spool   string `json:"spool"`
	Launch  string `json:"launch"`
	Note    string `json:"note"`
}

// Create makes the directory and does NOT launch anything. A created profile is signed out of
// everything; the only way in is a human at `login`.
func Create(dir string) (*CreateResult, error) {
	dir = strings.TrimRight(strings.TrimSpace(dir), "/")
	if dir == "" {
		return nil, usageErr("usage", "profile create <dir>")
	}
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err == nil {
			dir = abs
		}
	}
	existed := false
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return nil, usageErr("not-a-directory", "%s exists and is not a directory", dir)
		}
		existed = true
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, usageErr("mkdir-failed", "could not create %s: %v", dir, err)
	}
	return &CreateResult{
		Profile: dir,
		Existed: existed,
		Spool:   paths.SpoolFor(dir),
		Launch:  "CHROME_AGENT_PROFILE=" + dir + " chrome-agent up",
		Note:    "created empty and signed out of everything. Sign in per site: CHROME_AGENT_PROFILE=" + dir + " chrome-agent login <domain>",
	}, nil
}

// ---- list -------------------------------------------------------------------------------------

type Info struct {
	Profile   string `json:"profile"`
	Spool     string `json:"spool"`
	Size      string `json:"size"`
	SizeBytes int64  `json:"size_bytes"`
	Lock      string `json:"lock"`
	PID       int    `json:"pid,omitempty"`
	Default   bool   `json:"default,omitempty"`
	Current   bool   `json:"current,omitempty"`
}

type ListResult struct {
	Count    int    `json:"count"`
	Profiles []Info `json:"profiles"`
}

// Candidates is where profiles are looked for. It is deliberately the same set the bash CLI used, so
// `profile list` does not suddenly stop seeing a directory someone has been using for months.
func Candidates() []string {
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimRight(p, "/")
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	matches, _ := filepath.Glob(filepath.Join(home, "chrome-agent-profile*"))
	for _, m := range matches {
		add(m)
	}
	nested, _ := filepath.Glob(filepath.Join(home, ".config", "chrome-agent", "profiles", "*"))
	for _, m := range nested {
		add(m)
	}
	add(paths.DefaultProfile())
	if v := os.Getenv("CHROME_AGENT_PROFILE"); v != "" {
		add(v)
	}
	sort.Strings(out)
	return out
}

// List reports every profile with the spool it REALLY maps to (paths.SpoolFor — never re-derived),
// its size, and its lock state. On a server nobody can see these directories; this is the only way
// to notice that a profile's spool is not the one you assumed.
func List() *ListResult {
	cur := paths.Profile()
	def := paths.DefaultProfile()
	res := &ListResult{Profiles: []Info{}}
	for _, d := range Candidates() {
		state, pid := LockState(d)
		bytes := dirSize(d)
		res.Profiles = append(res.Profiles, Info{
			Profile:   d,
			Spool:     paths.SpoolFor(d),
			Size:      humanBytes(bytes),
			SizeBytes: bytes,
			Lock:      state,
			PID:       pid,
			Default:   d == def,
			Current:   d == cur,
		})
	}
	res.Count = len(res.Profiles)
	return res
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
			total += fi.Size()
		}
		return nil
	})
	return total
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTP"[exp])
}

// ---- delete -----------------------------------------------------------------------------------

type DeleteOpts struct {
	Yes   bool // actually destroy it
	Force bool // permit the DEFAULT profile; does NOT imply Yes
}

type DeleteResult struct {
	Staged bool   `json:"staged"`
	Target string `json:"about_to_delete,omitempty"`
	Delete string `json:"deleted,omitempty"`
	Spool  string `json:"spool"`
	Size   string `json:"size"`
	// SignedIn is best-effort: origins with cookies stored in this profile. Names only, never
	// values. Empty means "we could not read the cookie store", NOT "signed into nothing".
	SignedIn        []string `json:"signed_in_domains,omitempty"`
	SignedInIsGuess bool     `json:"signed_in_best_effort"`
	Lock            string   `json:"lock"`
	Warning         string   `json:"warning,omitempty"`
	Note            string   `json:"note,omitempty"`
	SpoolRemoved    bool     `json:"spool_removed"`
}

// Delete destroys a profile, and it is the ONLY revocation of a browser identity this system has.
// It therefore refuses more than it accepts:
//
//  1. a browser running on that profile — checked with Lstat + a live pid, see LockState.
//  2. the default profile without Force — every existing script and session uses it.
//  3. a bare invocation: without Yes it STAGES, naming the profile, its spool, its size and the
//     domains it holds sessions for.
//
// Force permits the default profile. It does NOT imply Yes: permission to name a dangerous target
// is not permission to destroy it. (The bash CLI conflated the two — see WIRING.md.)
func Delete(dir string, opts DeleteOpts) (*DeleteResult, error) {
	dir = strings.TrimRight(strings.TrimSpace(dir), "/")
	if dir == "" {
		return nil, usageErr("usage", "profile delete <dir> [--yes] [--force]")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, usageErr("no-such-profile", "no profile at %s", dir)
	}
	spool := paths.SpoolFor(dir)
	state, _ := LockState(dir)

	if running, pid := Running(dir); running {
		return nil, usageErr("profile-in-use",
			"a browser (pid %d) is running on %s — close it first; deleting a live profile loses every session in it", pid, dir)
	}
	if dir == paths.DefaultProfile() && !opts.Force {
		return nil, usageErr("default-profile",
			"%s is the DEFAULT profile — every existing script and session uses it. Pass --force if you really mean it.", dir)
	}

	domains, guessed := SignedInDomains(dir)
	res := &DeleteResult{
		Spool:           spool,
		Size:            humanBytes(dirSize(dir)),
		SignedIn:        domains,
		SignedInIsGuess: guessed,
		Lock:            state,
	}

	if !opts.Yes {
		res.Staged = true
		res.Target = dir
		res.Warning = "this destroys every site session in this profile — it is a revocation, not a cleanup"
		res.Note = "pass --yes to actually delete"
		return res, nil
	}

	if err := os.RemoveAll(dir); err != nil {
		return nil, usageErr("delete-failed", "could not delete %s: %v", dir, err)
	}
	// The spool is derived state for a profile that no longer exists; leaving it behind leaves a
	// directory that the next `up` on a same-named profile would inherit mid-conversation.
	if err := os.RemoveAll(spool); err == nil {
		res.SpoolRemoved = true
	}
	res.Delete = dir
	return res, nil
}

// SignedInDomains names the origins this profile holds cookies for, so a staged delete can say what
// it is about to revoke (ADR 0005: "including which domains that profile is currently signed into").
//
// It is a byte scan of the cookie store for hosts we have site definitions for — chromium stores
// host_key as plain text, so this is reliable enough to warn with and never good enough to trust as
// an auth verdict (that is `auth`'s job, and it needs a live browser). It reads NAMES only; no
// cookie value is ever read out, compared, or returned.
func SignedInDomains(dir string) (domains []string, bestEffort bool) {
	var blob []byte
	for _, rel := range []string{
		filepath.Join("Default", "Cookies"),
		filepath.Join("Default", "Network", "Cookies"),
		"Cookies",
	} {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil || len(b) == 0 {
			continue
		}
		if len(b) > 64<<20 {
			b = b[:64<<20]
		}
		blob = append(blob, b...)
	}
	if len(blob) == 0 {
		return nil, true
	}
	hay := string(blob)
	seen := map[string]bool{}
	for _, def := range sites.All() {
		d := sites.Normalize(def.Domain)
		if d == "" || seen[d] {
			continue
		}
		if strings.Contains(hay, d) {
			seen[d] = true
			domains = append(domains, d)
		}
	}
	sort.Strings(domains)
	return domains, true
}

// JSON is the one encoder for this package's results, so every verb prints the same shape.
func JSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
