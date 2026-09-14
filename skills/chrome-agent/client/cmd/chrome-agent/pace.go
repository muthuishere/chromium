package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/exit"
	"github.com/deemwarhq/chrome-agent/internal/pacing"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/recipes"
	"github.com/deemwarhq/chrome-agent/internal/sites"
)

// paced is the budget the current invocation spent, so the ledger line can say which one.
var paced struct{ domain, class string }

func pacingGate() (*pacing.Gate, error) {
	cfg, err := pacing.LoadConfig(filepath.Join(paths.ConfigDir(), "pacing.json"))
	if err != nil {
		return nil, err
	}
	// The spool dir name is already the per-profile key (default profile keeps its historic name,
	// every other profile is hashed on its full path) — one budget per identity, never shared.
	return &pacing.Gate{Dir: filepath.Join(paths.ConfigDir(), "pacing"),
		Profile: filepath.Base(paths.Spool()), Config: cfg}, nil
}

// siteFor maps what a caller typed (a domain, an alias, a URL, a subdomain) to the site definition
// whose budget it spends. old.reddit.com and www.reddit.com are reddit.com's budget, not their own.
func siteFor(target string) (string, map[string]pacing.Policy) {
	host := sites.Normalize(target)
	for h := host; ; {
		if d, err := sites.Load(h); err == nil && d != nil {
			return d.Domain, d.Pacing
		}
		parts := strings.SplitN(h, ".", 2)
		if len(parts) < 2 || !strings.Contains(parts[1], ".") {
			break
		}
		h = parts[1]
	}
	return host, nil
}

// beforeAction is the pacing gate every site-touching verb passes through. A write that is only
// STAGED (no --confirm) sends nothing to the site but still loads its pages, so it is paced as a read.
//
// Short waits are served here, blocking. A refusal prints the machine-readable reason with
// retry_after and exits 5 — before anything reaches the site.
func beforeAction(target, class string, confirm bool) {
	if class != pacing.Read && !confirm {
		class = pacing.Read
	}
	domain, siteDef := siteFor(target)
	paced.domain, paced.class = domain, class
	if os.Getenv("CHROME_AGENT_PACING") == "off" {
		fmt.Fprintf(os.Stderr, "chrome-agent: pacing is OFF (CHROME_AGENT_PACING=off) — %s on %s is not spaced\n", class, domain)
		return
	}
	g, err := pacingGate()
	if err != nil {
		exit.Die(exit.Usage, "pacing-config", err.Error())
	}
	d, err := g.Acquire(domain, class, siteDef)
	if err != nil {
		exit.Die(exit.Usage, "pacing-failed", err.Error())
	}
	if !d.Allowed {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": "rate-limited", "exit": exit.Paced,
			"detail": d.Reason, "pacing": d})
		fmt.Println(string(b))
		fmt.Fprintf(os.Stderr, "chrome-agent: %s — retry after %s (nothing was sent)\n", d.Reason,
			d.RetryAt.Local().Format(time.Kitchen))
		os.Exit(exit.Paced)
	}
	if d.Wait > 0 {
		fmt.Fprintf(os.Stderr, "chrome-agent: pacing — waited %.1fs before this %s on %s\n", d.Wait, class, domain)
	}
}

// recipeClass decides what a registry recipe spends. A write recipe that honours `confirm` and was
// not given confirm:true only stages, so it is a read; one that never mentions confirm always sends.
func recipeClass(key, optsJSON string) (domain, class string, confirm bool) {
	site := key
	if i := strings.Index(key, ":"); i >= 0 {
		site = key[:i]
	}
	class = pacing.Read
	r, err := recipes.Resolve(key)
	if err != nil || !r.Write {
		return site, class, false
	}
	var opts map[string]any
	_ = json.Unmarshal([]byte(optsJSON), &opts)
	confirm, _ = opts["confirm"].(bool)
	if !strings.Contains(r.Fn, "confirm") {
		confirm = true
	}
	return site, pacing.Mutate, confirm
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// pacingCmd shows the budget for one site on this profile, without booking anything.
func pacingCmd(args []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		exit.Die(exit.Usage, "usage", "pacing <domain> — show read/react/mutate budget for this profile")
	}
	domain, siteDef := siteFor(args[0])
	g, err := pacingGate()
	if err != nil {
		exit.Die(exit.Usage, "pacing-config", err.Error())
	}
	out(map[string]any{"domain": domain, "profile": paths.Profile(), "max_wait_seconds": g.Config.MaxWait().Seconds(),
		"classes": g.Peek(domain, siteDef)})
}
