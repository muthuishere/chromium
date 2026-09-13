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
	"time"

	"github.com/deemwarhq/chrome-agent/internal/doctor"
	"github.com/deemwarhq/chrome-agent/internal/exit"
	"github.com/deemwarhq/chrome-agent/internal/instance"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/spool"
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
		fmt.Println(paths.Profile())
		return
	case "spool":
		fmt.Println(paths.Spool())
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

func usage() {
	fmt.Print(`chrome-agent — client for the undetectable chromium fork (Go; ADR 0010 slice 1)

  doctor                     can this machine run anything at all?
  status                     webdriver + current url, through the live engine
  instances [--probe]        which browsers exist, and which answer
  hello | version            client version + protocol
  exit-codes [--json]        0 ok · 1 usage · 2 not signed in · 3 browser down · 4 site refused
  profile | spool            which profile/spool this invocation resolves to

Fork: $CHROME_AGENT_FORK (default ~/muthu/gitworkspace/chromium)
Slice 1 of the Go port. auth/login/logout/read/sites still live in the bash CLI.
`)
}
