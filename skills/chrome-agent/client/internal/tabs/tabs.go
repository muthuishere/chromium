// Package tabs finds and reaps per-session tabs that nobody is using any more.
//
// Every session gets its own tab (ADR 0001), remembered in ~/.config/chrome-agent/tabids/<agent>.
// Nothing ever closed them. On 2026-09-12 the browser was carrying 38 tabs, 32 of them leaked by
// test runs, and SCREENSHOT started timing out; after closing them it acked normally again. A
// resource leak that shows up as a capability failure is the worst kind to debug.
//
// The safety rule: ONLY tabs registered to a session are ever candidates. A tab with no tabid file
// was opened by a human, and this package never touches it.
package tabs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Tab struct {
	TabID string `json:"tabId"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

// Registration is one session's claim on a tab.
type Registration struct {
	Agent    string    `json:"agent"`
	TabID    string    `json:"tabId"`
	LastUsed time.Time `json:"last_used"` // file mtime; the client touches it on every reuse
	File     string    `json:"-"`
}

type Candidate struct {
	Registration
	URL    string `json:"url,omitempty"`
	Reason string `json:"reason"`
}

type Plan struct {
	Close     []Candidate    `json:"close"`   // registered, live in the browser, idle past the threshold
	Forget    []Registration `json:"forget"`  // registered, but the tab is already gone
	Kept      int            `json:"kept"`    // registered and recently used
	Unowned   int            `json:"unowned"` // tabs with no registration: a human's, never touched
	Threshold string         `json:"threshold"`
}

func Dir(configDir string) string { return filepath.Join(configDir, "tabids") }

// Registrations reads every session's remembered tab.
func Registrations(dir string) []Registration {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Registration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(b))
		if id == "" {
			continue
		}
		out = append(out, Registration{Agent: e.Name(), TabID: id, LastUsed: info.ModTime(), File: p})
	}
	return out
}

// Decide is the pure selection rule, kept free of any browser so it can be tested exhaustively.
//
// A live session can be named explicitly (keep) — e.g. the caller's own agent id — so a reap never
// closes the tab of the session running it.
func Decide(live []Tab, regs []Registration, idle time.Duration, now time.Time, keep map[string]bool) Plan {
	byID := map[string]Tab{}
	for _, t := range live {
		byID[t.TabID] = t
	}
	registered := map[string]bool{}
	p := Plan{Threshold: idle.String(), Close: []Candidate{}, Forget: []Registration{}}
	for _, r := range regs {
		registered[r.TabID] = true
		t, alive := byID[r.TabID]
		switch {
		case !alive:
			p.Forget = append(p.Forget, r)
		case keep[r.Agent]:
			p.Kept++
		case now.Sub(r.LastUsed) >= idle:
			p.Close = append(p.Close, Candidate{Registration: r, URL: t.URL,
				Reason: "idle " + now.Sub(r.LastUsed).Round(time.Minute).String()})
		default:
			p.Kept++
		}
	}
	for _, t := range live {
		if !registered[t.TabID] {
			p.Unowned++
		}
	}
	sort.Slice(p.Close, func(i, j int) bool { return p.Close[i].LastUsed.Before(p.Close[j].LastUsed) })
	return p
}
