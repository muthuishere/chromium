// Package browser is the session-level layer over the spool: this session's own tab, navigation,
// and the two eval paths.
package browser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/instance"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/spool"
)

type Browser struct {
	Profile string
	Spool   string
	tabID   string
}

func New() *Browser {
	return &Browser{Profile: paths.Profile(), Spool: paths.Spool()}
}

func (b *Browser) client() *spool.Client {
	c := spool.New(b.Spool)
	if b.tabID != "" {
		return c.WithTab(b.tabID)
	}
	return c
}

// Spool returns an unpinned spool client, for browser-wide commands such as listing or closing tabs.
func (b *Browser) SpoolClient() *spool.Client { return spool.New(b.Spool) }

func (b *Browser) Alive() bool { return spool.New(b.Spool).Alive(8 * time.Second) }

// AgentID keys this session's remembered tab. tmux session > env > "unowned".
func AgentID() string {
	if v := os.Getenv("CHROME_AGENT_ID"); v != "" {
		return v
	}
	if v := os.Getenv("DEEMWAR_TAB_ID"); v != "" {
		return v
	}
	if os.Getenv("TMUX") != "" {
		if out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	return "unowned"
}

func tabFile() string {
	return filepath.Join(paths.ConfigDir(), "tabids", AgentID())
}

// EnsureTab resolves THIS session's dedicated tab, creating it on first use.
//
// Per-session tabs are what stopped lanes stealing each other's browser: every page-targeting
// command rides TAB:<tabId>, so a background lane can no longer steer the tab a human is reading.
func (b *Browser) EnsureTab() (string, error) {
	if b.tabID != "" {
		return b.tabID, nil
	}
	f := tabFile()
	if data, err := os.ReadFile(f); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" && b.tabExists(id) {
			// Touch on reuse: the reaper reads this mtime as "last used". Without it, a long-lived
			// lane looks idle from the moment its tab was created and gets reaped mid-work.
			now := time.Now()
			_ = os.Chtimes(f, now, now)
			b.tabID = id
			return id, nil
		}
	}
	res, err := spool.New(b.Spool).NewTab("about:blank", 15*time.Second)
	if err != nil {
		return "", err
	}
	id, _ := res["tabId"].(string)
	if id == "" {
		return "", fmt.Errorf("the engine did not return a tabId for the new tab")
	}
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	_ = os.WriteFile(f, []byte(id), 0o644)
	b.tabID = id
	return id, nil
}

func (b *Browser) tabExists(id string) bool {
	res, err := spool.New(b.Spool).ListTabs(8 * time.Second)
	if err != nil {
		return false
	}
	tabs, _ := res["value"].([]any)
	for _, t := range tabs {
		if m, ok := t.(map[string]any); ok && m["tabId"] == id {
			return true
		}
	}
	return false
}

// Goto navigates THIS session's tab and waits for the page to settle.
//
// The settle time is part of the contract, not politeness: a heavy SPA keeps rendering after load,
// and a scrape that starts too early returns an empty result that is indistinguishable from "the
// recipe is broken".
func (b *Browser) Goto(url string, settle time.Duration) error {
	if _, err := b.EnsureTab(); err != nil {
		return err
	}
	if err := b.client().Goto(url); err != nil {
		return err
	}
	time.Sleep(settle)
	return nil
}

// EvalCSP runs a function body on a STRICT-CSP page.
//
// The fork decodes the b64: marker and interpolates the source straight into its injected script —
// no runtime eval() — so a page whose script-src lacks 'unsafe-eval' (LinkedIn, X and most modern
// SPAs) cannot throw EvalError. A plain eval there fails SILENTLY: no error, no result.
func (b *Browser) EvalCSP(js string, timeout time.Duration) (any, error) {
	if _, err := b.EnsureTab(); err != nil {
		return nil, err
	}
	body := "b64:" + base64.StdEncoding.EncodeToString([]byte(js))
	res, err := b.client().EvalAsync(body, timeout)
	if err != nil {
		return nil, err
	}
	return unwrap(res)
}

// EvalPlain is the original path: the page runs the body through eval(atob(...)). Fine on ordinary
// pages; use EvalCSP on anything strict.
func (b *Browser) EvalPlain(js string, timeout time.Duration) (any, error) {
	if _, err := b.EnsureTab(); err != nil {
		return nil, err
	}
	wrapped := fmt.Sprintf("(async()=>{ %s })()", js)
	b64 := base64.StdEncoding.EncodeToString([]byte(wrapped))
	res, err := b.client().EvalAsync("return eval(atob('"+b64+"'))", timeout)
	if err != nil {
		return nil, err
	}
	return unwrap(res)
}

func unwrap(res map[string]any) (any, error) {
	if ok, _ := res["ok"].(bool); ok {
		return res["value"], nil
	}
	if e, has := res["error"]; has {
		return nil, fmt.Errorf("%v", e)
	}
	return nil, fmt.Errorf("evalAsync: no result")
}

// EvalJSON runs a probe and decodes its object result.
func (b *Browser) EvalJSON(js string, timeout time.Duration, out any) error {
	v, err := b.EvalCSP(js, timeout)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Up launches the browser if nothing is servicing the spool.
//
// It execs the browser DIRECTLY — the node launcher (chromium-agent-launch.cjs) is gone from the
// runtime path, so a machine needs this binary and an installed engine, nothing else. The flags are
// that launcher's, unchanged; each one fixed something (see LaunchArgs).
func (b *Browser) Up(url string, headless bool) error {
	if b.Alive() {
		return nil
	}
	if err := paths.RequireBinary(); err != nil {
		return err
	}
	bin, _ := paths.Binary()
	if err := os.MkdirAll(b.Spool, 0o755); err != nil {
		return err
	}
	if _, err := spool.New(b.Spool).Sweep(5 * time.Minute); err != nil && !os.IsNotExist(err) {
		// A sweep failure is not fatal; a replayed stale command would be.
		fmt.Fprintf(os.Stderr, "spool sweep: %v\n", err)
	}
	if err := clearStaleLock(b.Profile); err != nil {
		return err
	}
	logPath := paths.LaunchLogFor(b.Profile)
	cmd := exec.Command(bin, LaunchArgs(b.Profile, url, headless)...)
	// The watcher refuses to start without its spool dir, and reads it from this variable only.
	cmd.Env = append(os.Environ(), "CHROMIUM_SENDKEYS_DIR="+b.Spool)
	if log, err := os.Create(logPath); err == nil {
		cmd.Stdout, cmd.Stderr = log, log
		defer log.Close()
	}
	detach(cmd) // the browser must outlive this CLI invocation
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start %s: %w", bin, err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if b.Alive() {
			return nil
		}
		select {
		case err := <-exited:
			// Chromium exits 0 when it hands the URL to an ALREADY-RUNNING instance of this profile
			// ("Opening in existing browser session."). That instance was not started with this spool,
			// so nothing will ever answer — say it, instead of waiting out the deadline.
			return fmt.Errorf("the browser (pid %d) exited immediately (%v) — if another browser already has %s open without this spool, close it first; see %s", pid, err, b.Profile, logPath)
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("the browser did not come up within 45s — see %s", logPath)
}

// LaunchArgs is the flag set chromium-agent-launch.cjs used, carried over verbatim.
func LaunchArgs(profile, url string, headless bool) []string {
	args := []string{
		"--user-data-dir=" + profile,
		// One predictable window/tab: no welcome tab, no default-browser prompt, no crash-restore
		// bubble. Without these the first-run tab becomes the tab the watcher targets.
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",
		// Auto-grant mic/camera (no dialog) so the per-tab audio/video verbs can select devices.
		"--use-fake-ui-for-media-stream",
	}
	if headless {
		// Modern headless: the full browser, no window.
		args = append(args, "--headless=new")
	}
	if url != "" {
		args = append(args, url)
	}
	return args
}

// clearStaleLock removes the Singleton* files a KILLED browser leaves behind. With them present the
// next launch says "Opening in existing browser session.", exits 0, and nothing services the spool —
// the silent no-op from INPROGRESS P3. A lock whose pid is ALIVE is a real browser and is left alone.
func clearStaleLock(profile string) error {
	if !instance.StaleLock(profile) {
		return nil
	}
	for _, f := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		if err := os.Remove(filepath.Join(profile, f)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stale %s in %s could not be removed: %w", f, profile, err)
		}
	}
	fmt.Fprintf(os.Stderr, "chrome-agent: cleared a stale SingletonLock in %s (a killed browser left it)\n", profile)
	return nil
}
