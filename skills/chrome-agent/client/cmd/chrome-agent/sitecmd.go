package main

import (
	"errors"
	"strings"

	"github.com/deemwarhq/chrome-agent/internal/exit"
	"github.com/deemwarhq/chrome-agent/internal/recipes"
)

// siteVerb handles the react verbs spelled as a site and an action:
//
//	chrome-agent linkedin like [url] [--confirm]
//	chrome-agent x like <url> [--confirm]
//	chrome-agent x repost <url> [--confirm]
//	chrome-agent reddit upvote <url> [--confirm]
//
// It returns false, touching nothing, for anything else — so the dispatcher can try it first.
// Without --confirm nothing is clicked: the verb reads the control and reports what it WOULD do.
func siteVerb(verb string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	key := verb + ":" + args[0]
	domain, ok := recipes.IsReact(key)
	if !ok {
		return false
	}
	confirm, url := false, ""
	for _, a := range args[1:] {
		switch {
		case a == "--confirm":
			confirm = true
		case strings.HasPrefix(a, "--"):
			exit.Die(exit.Usage, "usage", "unknown flag "+a+" — "+verb+" "+args[0]+" [url] [--confirm]")
		case url == "":
			url = a
		default:
			exit.Die(exit.Usage, "usage", "one url only — "+verb+" "+args[0]+" [url] [--confirm]")
		}
	}
	b := liveBrowser()
	beforeAction(domain, recipes.ClassReact, confirm)
	res, err := recipes.React(b, key, url, confirm)
	if err != nil {
		if errors.Is(err, recipes.ErrUsage) {
			exit.Die(exit.Usage, "usage", err.Error())
		}
		exit.Die(exit.Site, "react-failed", err.Error())
	}
	if confirm {
		// Logged whether or not it verified: a click that did not flip is still a click on a real
		// account, and the audit trail is where that shows up.
		logQuietly("react:"+key, url, res)
	}
	if okv, has := res["ok"]; has && okv != true {
		out(res)
		detail, _ := res["error"].(string)
		exit.Die(exit.Site, "react-unverified", key+": "+detail)
	}
	out(res)
	return true
}
