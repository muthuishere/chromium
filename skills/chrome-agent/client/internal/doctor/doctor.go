// Package doctor answers every question whose wrong answer is a hang (ADR 0006, ADR 0009).
//
// The spool protocol has no VERSION verb — that is a fork change. Until it has one, the honest
// substitute is to ask the engine what it can actually DO, each question under its own timeout. A
// missing capability then shows up as a named line in a report instead of a command that never
// returns.
//
// Fatal vs degraded is deliberate. Without evalasync or tabId nothing works and lanes collide, so
// the machine is not ready. A screenshot that never acks costs `shot` and `learn` and nothing else;
// calling that "not ready" would train an operator to ignore the word.
package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/instance"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/spool"
)

type Report struct {
	Browser BrowserInfo       `json:"browser"`
	Profile ProfileInfo       `json:"profile"`
	Spool   SpoolInfo         `json:"spool"`
	Tools   map[string]bool   `json:"tools"`
	Sites   map[string]string `json:"sites"`
	Engine  EngineInfo        `json:"engine"`
	Ready   bool              `json:"ready"`
	Missing []string          `json:"missing"`
	Notes   []string          `json:"notes,omitempty"`
}

// BrowserInfo is WHICH browser this machine would launch, and which rule found it. There is no fork
// requirement any more: an installed engine is the normal case, a fork build the dev one.
type BrowserInfo struct {
	Binary     string `json:"binary"`
	ResolvedBy string `json:"resolved_by"` // env | engine | fork | none
	Present    bool   `json:"present"`
	EngineDir  string `json:"engine_dir"`
	Fork       string `json:"fork"`
}

type ProfileInfo struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Locked    bool   `json:"locked"`
	StaleLock bool   `json:"stale_lock"`
}

type SpoolInfo struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type EngineInfo struct {
	Running       bool     `json:"running"`
	Protocol      int      `json:"protocol,omitempty"`
	EngineVersion string   `json:"engine_version,omitempty"`
	Eval          bool     `json:"eval"`
	EvalAsync     bool     `json:"evalasync"`
	TabID         bool     `json:"tabid"`
	ScreenshotAck bool     `json:"screenshot_ack"`
	TooOldFor     []string `json:"too_old_for,omitempty"`
	Degraded      []string `json:"degraded,omitempty"`
	Note          string   `json:"note,omitempty"`
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func Run() Report {
	r := Report{Tools: map[string]bool{}, Sites: map[string]string{}}

	bin, by := paths.Binary()
	r.Browser = BrowserInfo{
		Binary:     bin,
		ResolvedBy: by,
		Present:    paths.RequireBinary() == nil,
		EngineDir:  paths.EngineDir(),
		Fork:       paths.Fork(),
	}

	prof := paths.Profile()
	lockFI, lockErr := os.Lstat(filepath.Join(prof, "SingletonLock"))
	r.Profile = ProfileInfo{
		Path:      prof,
		Exists:    exists(prof),
		Locked:    lockErr == nil && lockFI.Mode()&os.ModeSymlink != 0,
		StaleLock: false,
	}
	for _, in := range instance.Discover(0) { // 0 = do not probe, just look at the locks
		if in.Profile == prof {
			r.Profile.StaleLock = in.StaleLock
		}
	}

	sp := paths.Spool()
	r.Spool = SpoolInfo{Path: sp, Exists: exists(sp)}
	r.Sites["dir"] = paths.SitesDir()

	for _, t := range []string{"cloudflared", "Xvfb", "x11vnc"} {
		_, err := exec.LookPath(t)
		r.Tools[t] = err == nil
	}

	r.Engine = probeEngine(sp)

	// A browser that is already RUNNING is enough to work; a missing install only blocks `up`.
	if !r.Browser.Present && !r.Engine.Running {
		r.Missing = append(r.Missing, "browser (run: chrome-agent engine install)")
	}
	if len(r.Engine.TooOldFor) > 0 {
		r.Missing = append(r.Missing, "engine capabilities (fork predates this client)")
	}
	if !r.Tools["cloudflared"] {
		r.Notes = append(r.Notes, "cloudflared absent — a remote login grant cannot be tunnelled")
	}
	if r.Profile.StaleLock {
		r.Notes = append(r.Notes, "stale SingletonLock: a killed browser left it behind, and the next launch will silently no-op")
	}
	r.Ready = len(r.Missing) == 0
	if r.Missing == nil {
		r.Missing = []string{}
	}
	return r
}

func probeEngine(dir string) EngineInfo {
	c := spool.New(dir)
	e := EngineInfo{}
	if !c.Alive(8 * time.Second) {
		e.Note = "no browser is servicing the spool — run: chrome-agent up (capabilities unknown, not absent)"
		return e
	}
	e.Running, e.Eval = true, true

	// VERSION first. A live engine that does not ack it within a short window predates protocol 1
	// (ADR 0009 §6) — which is worth SAYING, because every other capability probe below would then
	// also be answering an engine that cannot speak the handshake.
	if v, err := c.Version(6 * time.Second); err == nil {
		if p, ok := v["protocol"].(float64); ok {
			e.Protocol = int(p)
		}
		if ev, ok := v["engine_version"].(string); ok {
			e.EngineVersion = ev
		}
	} else {
		e.Note = "engine did not answer VERSION — it predates protocol 1; the capability probe below reflects an older engine"
	}

	if _, err := c.EvalAsync("return 1", 10*time.Second); err == nil {
		e.EvalAsync = true
	}
	if res, err := c.ListTabs(8 * time.Second); err == nil {
		if tabs, ok := res["value"].([]any); ok {
			for _, t := range tabs {
				if m, ok := t.(map[string]any); ok {
					if _, has := m["tabId"]; has {
						e.TabID = true
						break
					}
				}
			}
		}
	}
	tmp := filepath.Join(os.TempDir(), "chrome-agent-doctor.png")
	if res, err := c.SendAndAwait(func(id string) string { return "SCREENSHOT:" + id + "|" + tmp }, 12*time.Second); err == nil {
		if _, has := res["bytes"]; has {
			e.ScreenshotAck = true
		}
	}
	os.Remove(tmp)

	if !e.EvalAsync {
		e.TooOldFor = append(e.TooOldFor, "evalasync -> every recipe and every probe (the client would hang or time out)")
	}
	if !e.TabID {
		e.TooOldFor = append(e.TooOldFor, "tabId in listtabs -> per-session tab isolation; lanes would steer each other")
	}
	if !e.ScreenshotAck {
		e.Degraded = append(e.Degraded, "screenshot did not ack -> `shot` and `learn` cannot write a PNG; everything else works")
	}
	return e
}
