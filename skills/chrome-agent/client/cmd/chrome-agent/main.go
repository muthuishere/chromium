// chrome-agent — the client for the undetectable chromium fork.
//
// Slice 1 of the Go port (ADR 0010): the protocol, the instance registry, doctor, and the exit-code
// contract. The bash CLI remains the reference implementation until slice 3 lands; this binary must
// agree with it verb for verb, which is what scripts/selftest.sh checks.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/browser"
	"github.com/deemwarhq/chrome-agent/internal/cookies"
	"github.com/deemwarhq/chrome-agent/internal/doctor"
	"github.com/deemwarhq/chrome-agent/internal/exit"
	"github.com/deemwarhq/chrome-agent/internal/identity"
	"github.com/deemwarhq/chrome-agent/internal/install"
	"github.com/deemwarhq/chrome-agent/internal/instance"
	"github.com/deemwarhq/chrome-agent/internal/learned"
	"github.com/deemwarhq/chrome-agent/internal/ledger"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/profile"
	"github.com/deemwarhq/chrome-agent/internal/recipes"
	"github.com/deemwarhq/chrome-agent/internal/sites"
	"github.com/deemwarhq/chrome-agent/internal/spool"
	"github.com/deemwarhq/chrome-agent/internal/tabs"
)

// Protocol is what this client speaks. ADR 0009: a versioned handshake from the first public byte,
// because a client newer than its engine otherwise discovers that as a command that hangs forever.
const Protocol = 1

var version = "dev" // set with -ldflags "-X main.version=…"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		return
	}
	verb, rest := args[0], args[1:]

	// Verbs that must NOT touch the browser, the fork, or the filesystem. A pure query that creates
	// a directory as a side effect turns a probe into a mutation.
	switch verb {
	case "exit-codes", "exitcodes":
		exitCodes(rest)
		return
	case "version", "hello":
		hello()
		return
	case "profile":
		if len(rest) == 0 {
			fmt.Println(paths.Profile())
			return
		}
		profileCmd(rest)
		return
	case "spool":
		fmt.Println(paths.Spool())
		return
	case "sites":
		sitesCmd(rest)
		return
	case "recipes":
		recipesCmd(rest)
		return
	case "ledger":
		ledgerCmd(rest)
		return
	case "note":
		noteCmd(rest)
		return
	case "promote":
		promoteCmd(rest)
		return
	case "install":
		installCmd(rest)
		return
	case "help", "-h", "--help":
		usage()
		return
	}

	// Everything below drives the browser, so the fork has to exist. Checking HERE rather than deep
	// in a helper is deliberate: a failure inside a subprocess or a pipeline gets its exit code and
	// its JSON swallowed, which is exactly how a missing fork used to present as silence.
	if err := paths.RequireFork(); err != nil {
		exit.Die(exit.Browser, "fork-missing", err.Error())
	}

	switch verb {
	case "doctor":
		runDoctor()
	case "instances":
		instances(rest)
	case "status":
		status()
	case "auth":
		authCmd(rest)
	case "login":
		loginCmd(rest)
	case "logout":
		logoutCmd(rest)
	case "goto":
		gotoCmd(rest)
	case "eval":
		evalCmd(rest, false)
	case "evalcsp":
		evalCmd(rest, true)
	case "cookies", "cookie":
		cookiesCmd(rest)
	case "tabs":
		tabsCmd(rest)
	case "recipe":
		recipeCmd(rest)
	case "read":
		readCmd(rest)
	case "verify":
		verifyCmd(rest)
	default:
		exit.Die(exit.Usage, "unknown-verb", fmt.Sprintf("unknown verb %q — run `chrome-agent help`", verb))
	}
}

func out(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func exitCodes(args []string) {
	if len(args) > 0 && args[0] == "--json" {
		out(exit.Table())
		return
	}
	for _, c := range exit.Table() {
		fmt.Printf("  %d  %-20s %s\n", c.Code, c.Name, c.Means)
	}
}

func hello() {
	out(map[string]any{
		"client":   "chrome-agent",
		"version":  version,
		"protocol": Protocol,
		"fork":     paths.Fork(),
		"profile":  paths.Profile(),
		"spool":    paths.Spool(),
	})
}

func runDoctor() {
	r := doctor.Run()
	out(r)
	if !r.Ready {
		os.Exit(exit.Browser)
	}
}

func instances(args []string) {
	probe := false
	for _, a := range args {
		if a == "--probe" {
			probe = true
		}
	}
	live, stale := instance.FromRegistry()
	res := map[string]any{
		"registry":       live,
		"stale_registry": stale,
		"registry_dir":   paths.InstancesDir(),
	}
	if probe {
		res["probed"] = instance.Discover(3 * time.Second)
	} else {
		res["note"] = "pass --probe to ask each derivable spool whether anything answers"
	}
	if len(live) == 0 && len(stale) == 0 {
		res["registry_note"] = "the engine does not register itself yet (ADR 0009) — until it does, use --probe"
	}
	out(res)
}

func status() {
	c := spool.New(paths.Spool())
	if !c.Alive(8 * time.Second) {
		exit.Die(exit.Browser, "browser-down", "no browser is servicing "+paths.Spool()+" — run: chrome-agent up")
	}
	res, err := c.Eval("({webdriver:navigator.webdriver,url:location.href})", 10*time.Second)
	if err != nil {
		exit.Die(exit.Browser, "eval-failed", err.Error())
	}
	out(res)
}

// --- slice 2: sites + identity ------------------------------------------------------------------

func sitesCmd(args []string) {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		all := sites.All()
		if contains(args, "--json") {
			out(all)
			return
		}
		for _, d := range all {
			verb := d.Read.Verb
			if verb == "" {
				verb = "-"
			}
			fmt.Printf("  %-24s %-11s %-10s read=%-28s writes=%d\n", d.Domain, d.Status, d.From, verb, len(d.Write))
		}
	case "show", "resolve":
		d := mustSite(args)
		out(d)
	case "path":
		d := mustSite(args)
		if d.Path == "" {
			fmt.Println("(embedded in the binary)")
			return
		}
		fmt.Println(d.Path)
	case "validate":
		// Default: the EFFECTIVE set — the definition that actually wins for each domain, which is
		// what the browser will really use. `--all` additionally checks shadowed copies, which
		// matters before a `sync --force` promotes one of them into service.
		all := sites.All()
		bad := 0
		for _, d := range all {
			errs := sites.Problems(d)
			if len(errs) == 0 {
				fmt.Printf("ok   %-28s %-11s %s\n", d.Domain, d.Status, d.From)
				continue
			}
			bad++
			fmt.Printf("FAIL %s (%s)\n", d.Domain, d.From)
			for _, e := range errs {
				fmt.Printf("     - %s\n", e)
			}
		}
		shadowed := 0
		if contains(args, "--all") {
			shadowed, bad = validateShadowed(bad)
		}
		fmt.Printf("\n%d domain(s) checked", len(all))
		if shadowed > 0 {
			fmt.Printf(" + %d shadowed file(s)", shadowed)
		}
		fmt.Printf(", %d bad\n", bad)
		if bad > 0 {
			os.Exit(exit.Usage)
		}
	case "sync":
		res, err := sites.Sync(contains(args, "--force"), contains(args, "--dry-run"))
		if err != nil {
			exit.Die(exit.Usage, "sync-failed", err.Error())
		}
		out(res)
		if len(res.Kept) > 0 {
			fmt.Fprintf(os.Stderr, "sites sync: %d locally-edited file(s) left alone — --force replaces them\n", len(res.Kept))
		}
	default:
		exit.Die(exit.Usage, "usage", "sites list|show <domain>|path <domain>|validate|sync [--force]")
	}
}

// validateShadowed checks the copies that are currently OUTRANKED. A broken shipped file is
// invisible until someone runs `sync --force` and promotes it into service.
func validateShadowed(bad int) (int, int) {
	n := 0
	for _, d := range sites.Shadowed() {
		n++
		errs := sites.Problems(d)
		if len(errs) == 0 {
			fmt.Printf("ok   %-28s %-11s %s (shadowed)\n", d.Domain, d.Status, d.From)
			continue
		}
		bad++
		fmt.Printf("FAIL %s (%s, shadowed)\n", d.Domain, d.From)
		for _, e := range errs {
			fmt.Printf("     - %s\n", e)
		}
	}
	return n, bad
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func mustSite(args []string) *sites.Definition {
	var name string
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "--") {
			name = a
			break
		}
	}
	if name == "" {
		exit.Die(exit.Usage, "usage", "needs a domain, e.g. chrome-agent sites show linkedin.com")
	}
	d, err := sites.Load(name)
	if err != nil || d == nil {
		exit.Die(exit.Usage, "no-definition", "no site definition for "+name)
	}
	return d
}

func domainArg(args []string, verb string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return a
		}
	}
	exit.Die(exit.Usage, "usage", verb+" <domain> — e.g. chrome-agent "+verb+" linkedin.com")
	return ""
}

func liveBrowser() *browser.Browser {
	b := browser.New()
	if !b.Alive() {
		exit.Die(exit.Browser, "browser-down", "no browser is servicing "+b.Spool+" — run: chrome-agent up")
	}
	return b
}

func authCmd(args []string) {
	d := domainArg(args, "auth")
	v, err := identity.Auth(liveBrowser(), d)
	if err != nil {
		exit.Die(exit.Usage, "no-probe", err.Error())
	}
	out(v)
	if !v.SignedIn {
		os.Exit(exit.Auth)
	}
}

func loginCmd(args []string) {
	d := domainArg(args, "login")
	headless := os.Getenv("CHROMIUM_AGENT_HEADLESS") == "1" || os.Getenv("CHROME_AGENT_HEADLESS") == "1" || contains(args, "--headless")
	if err := paths.RequireBinary(); err != nil {
		exit.Die(exit.Browser, "fork-not-built", err.Error())
	}
	info, err := identity.Login(browser.New(), d, headless)
	if err != nil {
		exit.Die(exit.Browser, "login-failed", err.Error())
	}
	out(info)
}

func logoutCmd(args []string) {
	d := domainArg(args, "logout")
	res, err := identity.Logout(liveBrowser(), d)
	if err != nil {
		exit.Die(exit.Usage, "bad-definition", err.Error())
	}
	out(res)
	// Idempotent by intent: "be signed out" is satisfied whether or not we had to do anything.
	if !res.SignedOut {
		os.Exit(exit.Site)
	}
}

func gotoCmd(args []string) {
	if len(args) == 0 {
		exit.Die(exit.Usage, "usage", "goto <url> [settle-seconds]")
	}
	settle := 4 * time.Second
	if len(args) > 1 {
		if n, err := time.ParseDuration(args[1] + "s"); err == nil {
			settle = n
		}
	}
	b := liveBrowser()
	if err := b.Goto(args[0], settle); err != nil {
		exit.Die(exit.Browser, "goto-failed", err.Error())
	}
	out(map[string]any{"ok": true, "url": args[0]})
}

// evalCmd runs JS in this session's tab. `evalcsp` is the CSP-safe path: on a page whose script-src
// lacks 'unsafe-eval' (LinkedIn, X, most modern SPAs) a plain eval fails SILENTLY — no error, no
// result — which is indistinguishable from a page that had nothing to say.
func evalCmd(args []string, csp bool) {
	if len(args) == 0 {
		exit.Die(exit.Usage, "usage", "eval '<js that returns>' [timeout-seconds]")
	}
	timeout := 20 * time.Second
	if len(args) > 1 {
		if n, err := time.ParseDuration(args[1] + "s"); err == nil {
			timeout = n
		}
	}
	b := liveBrowser()
	var v any
	var err error
	if csp {
		v, err = b.EvalCSP(args[0], timeout)
	} else {
		v, err = b.EvalPlain(args[0], timeout)
	}
	if err != nil {
		exit.Die(exit.Site, "eval-failed", err.Error())
	}
	out(v)
}

// --- slice 3: recipes, reads, and the operational verbs ------------------------------------------

func recipesCmd(args []string) {
	if len(args) > 0 && args[0] == "vendor" {
		res, err := recipes.Sync(contains(args, "--force"), contains(args, "--dry-run"))
		if err != nil {
			exit.Die(exit.Usage, "vendor-failed", err.Error())
		}
		out(res)
		return
	}
	list, err := recipes.List()
	if err != nil {
		exit.Die(exit.Usage, "no-registry", err.Error())
	}
	if contains(args, "--json") {
		out(list)
		return
	}
	for _, r := range list {
		suffix := ""
		if r.Source == "chrome-agent" {
			suffix = "  [chrome-agent verb]"
		}
		fmt.Printf("  %s — %s%s\n", r.Key, r.Describe, suffix)
	}
}

func recipeCmd(args []string) {
	if len(args) == 0 {
		exit.Die(exit.Usage, "usage", "recipe <site:name> [opts-json]")
	}
	optsJSON := ""
	if len(args) > 1 {
		optsJSON = args[1]
	}
	v, err := recipes.Run(liveBrowser(), args[0], optsJSON, recipes.Options{})
	if err != nil {
		exit.Die(exit.Site, "recipe-failed", err.Error())
	}
	logQuietly("recipe:"+args[0], args[0], v)
	out(v)
}

func readCmd(args []string) {
	if len(args) == 0 {
		exit.Die(exit.Usage, "usage", "read <domain> [generic:recipe]")
	}
	generic := ""
	if len(args) > 1 {
		generic = args[1]
	}
	res, err := recipes.Read(liveBrowser(), args[0], generic)
	if err != nil {
		exit.Die(exit.Site, "read-failed", err.Error())
	}
	out(res)
}

// verifyCmd hands recipes.Verify an auth probe as its fallback: a site with no read recipe (HN had
// none for months) is still verifiable by asking whether we are still someone there.
func verifyCmd(args []string) {
	if len(args) == 0 {
		exit.Die(exit.Usage, "usage", "verify <domain>")
	}
	b := liveBrowser()
	res, err := recipes.Verify(b, args[0], func(domain string) error {
		v, err := identity.Auth(b, domain)
		if err != nil {
			return err
		}
		if !v.SignedIn {
			return fmt.Errorf("not signed in: %s", v.Why)
		}
		return nil
	})
	if err != nil {
		exit.Die(exit.Site, "verify-failed", err.Error())
	}
	out(res)
	if !res.Verified {
		os.Exit(exit.Site)
	}
}

func profileCmd(args []string) {
	switch args[0] {
	case "create":
		if len(args) < 2 {
			exit.Die(exit.Usage, "usage", "profile create <dir>")
		}
		res, err := profile.Create(args[1])
		if err != nil {
			exit.Die(exit.Usage, "create-failed", err.Error())
		}
		out(res)
	case "list":
		out(profile.List())
	case "delete", "rm":
		if len(args) < 2 {
			exit.Die(exit.Usage, "usage", "profile delete <dir> [--yes] [--force]")
		}
		res, err := profile.Delete(args[1], profile.DeleteOpts{
			Yes:   contains(args, "--yes"),
			Force: contains(args, "--force"),
		})
		if err != nil {
			exit.Die(exit.Usage, "delete-refused", err.Error())
		}
		out(res)
	case "show", "":
		fmt.Println(paths.Profile())
	default:
		exit.Die(exit.Usage, "usage", "profile [create <dir> | list | delete <dir> [--yes]]")
	}
}

func ledgerCmd(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "status":
		out(ledger.StatusOf())
	case "rotate":
		if _, err := ledger.Rotate("manual"); err != nil {
			exit.Die(exit.Usage, "rotate-failed", err.Error())
		}
		out(ledger.StatusOf())
	default:
		exit.Die(exit.Usage, "usage", "ledger status|rotate")
	}
}

func noteCmd(args []string) {
	if len(args) < 2 {
		exit.Die(exit.Usage, "usage", `note <domain> "<what you learned>"`)
	}
	res, err := learned.Note(args[0], args[1])
	if err != nil {
		exit.Die(exit.Usage, "note-failed", err.Error())
	}
	out(res)
}

func promoteCmd(args []string) {
	o := learned.Opts{Apply: contains(args, "--apply"), IncludeDrift: contains(args, "--include-drift")}
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			o.Domain = a
			break
		}
	}
	rep, err := learned.Promote(o)
	if err != nil {
		exit.Die(exit.Usage, "promote-failed", err.Error())
	}
	out(rep)
}

func installCmd(args []string) {
	bin := install.DefaultBinDir()
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			bin = a
			break
		}
	}
	res, err := install.Install(bin)
	if err != nil {
		exit.Die(exit.Usage, "install-failed", err.Error())
	}
	out(res)
}

// logQuietly records an action in the audit trail. A failure to log must never fail the action —
// but it must not be invisible either.
func logQuietly(action, target string, result any) {
	b, _ := json.Marshal(result)
	if _, err := ledger.Append(browser.AgentID(), action, target, string(b)); err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
	}
}

// tabsCmd lists and reaps per-session tabs. Reaping is STAGED like every other destructive verb
// here: without --yes it only reports what it would close.
// cookiesCmd is the client half of ADR 0011: the engine reads/writes the store (HttpOnly cookies
// are invisible to a page), the client owns encryption, the ledger, and refusing to look like
// success when cookies were rejected.
func cookiesCmd(args []string) {
	if len(args) == 0 {
		exit.Die(exit.Usage, "usage", "cookies export <domain> --out FILE [--pass P | --i-know-this-is-a-credential] | cookies import --in FILE [--pass P]")
	}
	flag := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch args[0] {
	case "export":
		var domain string
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "--") {
				domain = a
				break
			}
		}
		outPath := flag("--out")
		if domain == "" || outPath == "" {
			exit.Die(exit.Usage, "usage", "cookies export <domain> --out FILE [--pass P | --i-know-this-is-a-credential]")
		}
		res, err := cookies.Export(liveBrowser(), domain, outPath, flag("--pass"), contains(args, "--i-know-this-is-a-credential"))
		if err != nil {
			exit.Die(exit.Usage, "export-failed", err.Error())
		}
		out(res)
	case "import":
		inPath := flag("--in")
		if inPath == "" {
			exit.Die(exit.Usage, "usage", "cookies import --in FILE [--pass P]")
		}
		res, err := cookies.Import(liveBrowser(), inPath, flag("--pass"))
		if err != nil {
			exit.Die(exit.Usage, "import-failed", err.Error())
		}
		out(res)
		// A partial import is not a clean import. Exit non-zero so a script cannot treat it as one.
		if res.RejectedCount > 0 {
			os.Exit(exit.Site)
		}
	default:
		exit.Die(exit.Usage, "usage", "cookies export ... | cookies import ...")
	}
}

func tabsCmd(args []string) {
	b := liveBrowser()
	res, err := b.SpoolClient().ListTabs(10 * time.Second)
	if err != nil {
		exit.Die(exit.Browser, "listtabs-failed", err.Error())
	}
	var live []tabs.Tab
	if raw, ok := res["value"].([]any); ok {
		for _, r := range raw {
			if m, ok := r.(map[string]any); ok {
				id, _ := m["tabId"].(string)
				url, _ := m["url"].(string)
				title, _ := m["title"].(string)
				if id != "" {
					live = append(live, tabs.Tab{TabID: id, URL: url, Title: title})
				}
			}
		}
	}
	regs := tabs.Registrations(tabs.Dir(paths.ConfigDir()))
	idle := 24 * time.Hour
	for i, a := range args {
		if a == "--idle" && i+1 < len(args) {
			if d, err := time.ParseDuration(args[i+1]); err == nil {
				idle = d
			}
		}
	}
	plan := tabs.Decide(live, regs, idle, time.Now(), map[string]bool{browser.AgentID(): true})

	if len(args) == 0 || args[0] == "list" {
		out(map[string]any{"live": len(live), "registered": len(regs), "plan": plan})
		return
	}
	if args[0] != "reap" {
		exit.Die(exit.Usage, "usage", "tabs list | tabs reap [--idle 24h] [--yes]")
	}
	if !contains(args, "--yes") {
		out(map[string]any{"staged": true, "would_close": plan.Close, "would_forget": plan.Forget,
			"unowned_left_alone": plan.Unowned, "note": "pass --yes to close them"})
		return
	}
	closed := 0
	for _, c := range plan.Close {
		if err := b.SpoolClient().Send("CLOSETAB:" + c.TabID); err == nil {
			_ = os.Remove(c.File)
			closed++
		}
	}
	for _, r := range plan.Forget {
		_ = os.Remove(r.File)
	}
	logQuietly("tabs:reap", fmt.Sprintf("closed=%d forgot=%d", closed, len(plan.Forget)), plan)
	out(map[string]any{"closed": closed, "forgot": len(plan.Forget), "unowned_left_alone": plan.Unowned})
}

func usage() {
	fmt.Print(`chrome-agent — client for the undetectable chromium fork (Go; ADR 0010 slice 1)

  doctor                     can this machine run anything at all?
  status                     webdriver + current url, through the live engine
  instances [--probe]        which browsers exist, and which answer
  hello | version            client version + protocol
  exit-codes [--json]        0 ok · 1 usage · 2 not signed in · 3 browser down · 4 site refused
  profile | spool            which profile/spool this invocation resolves to

  auth <domain>              read-only {signed_in, as?} — exit 0 yes, 2 no
  login <domain>             open the page for a HUMAN; types nothing, ever
  logout <domain>            end THIS site's session; verifies with auth after
  sites list|show|path|validate|sync [--force]
  read <domain>              read any known site — its recipe, or the generic reader
  verify <domain>            run the site's real read verb; prove the playbook is not fiction
  recipe <key> [opts-json]   run any registry recipe through the fork
  recipes [--json] | recipes vendor [--force]
  profile [create <dir> | list | delete <dir> [--yes]]
  note <domain> "<learned>" | promote [<domain>] [--apply]
  ledger status|rotate       the audit trail and its rotation
  install [bindir]           assets + CLI onto PATH
  cookies export <domain> --out F [--pass P] | cookies import --in F [--pass P]
  tabs list | tabs reap [--idle 24h] [--yes]   close idle SESSION tabs; never a human's
  goto <url> [settle]        navigate THIS session's tab
  eval | evalcsp '<js>'      run JS in it (evalcsp survives strict script-src)

Fork: $CHROME_AGENT_FORK (default ~/muthu/gitworkspace/chromium)
Slices 1-3 of the Go port: no node, no python3. Streams and cookies are next (ADR 0009/0011).
`)
}
