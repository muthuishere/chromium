// Package exit is the CLI's exit-code contract (ADR 0005).
//
// apl maps a child's exit code to a user-facing state. An undocumented code means it cannot tell
// "the site said no" from "the browser was never running" — two failures with completely different
// fixes (a human signs in vs. start the browser), and a caller that cannot tell them apart retries
// the wrong one forever.
package exit

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	OK      = 0 // the command did what it says
	Usage   = 1 // bad or missing argument; nothing was attempted
	Auth    = 2 // no valid session for that site — a human must sign in
	Browser = 3 // no fork, no build, or no browser servicing the spool
	Site    = 4 // the browser worked and the site said no
	Paced   = 5 // pacing refused: too soon, or the daily cap is spent — retry_after says when
)

// Code is the machine-readable contract, served by `chrome-agent exit-codes --json`.
type Code struct {
	Code  int    `json:"code"`
	Name  string `json:"name"`
	Means string `json:"means"`
}

func Table() []Code {
	return []Code{
		{OK, "ok", "the command did what it says"},
		{Usage, "usage", "bad or missing argument; nothing was attempted"},
		{Auth, "not-signed-in", "the profile has no valid session for that site — a human must sign in"},
		{Browser, "browser-unreachable", "no fork, no build, or no browser servicing the spool"},
		{Site, "site-refused", "the browser worked and the site said no"},
		{Paced, "rate-limited", "pacing refused the action (too soon, or today's cap is spent); nothing was sent — retry after retry_after_seconds"},
	}
}

// Die prints the machine-readable failure on stdout and the human one on stderr, then exits.
//
// Both halves matter: a caller parses stdout, a person reads stderr, and printing only one of them
// is how a failure becomes either unreadable or unparseable.
func Die(code int, slug, detail string) {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": slug, "exit": code, "detail": detail})
	fmt.Fprintln(os.Stdout, string(b))
	fmt.Fprintf(os.Stderr, "chrome-agent: %s\n", detail)
	os.Exit(code)
}
