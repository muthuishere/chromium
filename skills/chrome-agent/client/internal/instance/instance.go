// Package instance is the browser-instance registry (ADR 0009).
//
// "Find the browser" used to be arithmetic on the profile path — hash it, derive the spool. That is
// fine for a private tool and it already produced two bugs (a trailing slash forked one profile into
// two spools; a basename collision merged two profiles into one). As a public verb it needs a
// registry: the engine records what it started, and staleness is decided by asking the OS whether
// the pid is still alive.
//
// The engine does not write these files yet — that is a fork change. Until it does, Discover()
// probes the spools we can derive and reports what actually answers, so the verb is useful today and
// keeps its shape when the engine starts registering itself.
package instance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/spool"
)

type Instance struct {
	ID        string `json:"id"`
	PID       int    `json:"pid,omitempty"`
	Profile   string `json:"profile"`
	Spool     string `json:"spool"`
	WSPort    int    `json:"ws_port,omitempty"`
	Headless  bool   `json:"headless,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	Protocol  int    `json:"protocol,omitempty"`

	// Filled in by us, never by the engine.
	Source    string `json:"source"`             // "registry" | "probe"
	Responds  *bool  `json:"responds,omitempty"` // nil = not probed
	StaleLock bool   `json:"stale_lock,omitempty"`
}

// Alive asks the OS. A registry entry whose process is gone is a corpse, and a corpse that still
// looks like an instance is how a caller ends up waiting forever on a spool nobody services.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// FromRegistry reads what the engine recorded, dropping entries whose pid is dead.
func FromRegistry() (live []Instance, stale []Instance) {
	dir := paths.InstancesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var in Instance
		if json.Unmarshal(b, &in) != nil {
			continue
		}
		in.Source = "registry"
		if in.ID == "" {
			in.ID = strings.TrimSuffix(e.Name(), ".json")
		}
		if Alive(in.PID) {
			live = append(live, in)
		} else {
			stale = append(stale, in)
		}
	}
	return live, stale
}

// Discover probes every spool we can name and reports which ones answer.
//
// It also reports a profile whose SingletonLock is a corpse: chromium leaves the lock behind when it
// is killed, the next launch says "Opening in existing browser session." and exits 0, and nothing
// services the spool. Silence was the failure mode; naming it is the fix.
func Discover(timeout time.Duration) []Instance {
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var out []Instance

	candidates := []string{paths.Profile(), paths.DefaultProfile()}
	if matches, _ := filepath.Glob(filepath.Join(home, "chrome-agent-profile*")); matches != nil {
		candidates = append(candidates, matches...)
	}
	for _, prof := range candidates {
		prof = strings.TrimRight(prof, "/")
		if prof == "" || seen[prof] {
			continue
		}
		fi, err := os.Stat(prof)
		if err != nil || !fi.IsDir() {
			continue
		}
		seen[prof] = true
		sp := paths.SpoolFor(prof)
		responds := spool.New(sp).Alive(timeout)
		out = append(out, Instance{
			ID:        filepath.Base(prof),
			Profile:   prof,
			Spool:     sp,
			Source:    "probe",
			Responds:  &responds,
			StaleLock: staleLock(prof),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Profile < out[j].Profile })
	return out
}

// staleLock: SingletonLock is a SYMLINK to "<host>-<pid>", a target that never exists as a file — so
// a plain existence check (which follows the link) reports "free" while a browser is running. Test
// the link itself, then ask whether the pid is alive.
func staleLock(profile string) bool {
	link := filepath.Join(profile, "SingletonLock")
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	pid := 0
	if i := strings.LastIndex(target, "-"); i >= 0 {
		for _, c := range target[i+1:] {
			if c < '0' || c > '9' {
				return false
			}
		}
		_, _ = fmtSscan(target[i+1:], &pid)
	}
	return pid > 0 && !Alive(pid)
}

// fmtSscan is a tiny shim so this file needs no fmt import solely for one parse.
func fmtSscan(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	*out = n
	return 1, nil
}
