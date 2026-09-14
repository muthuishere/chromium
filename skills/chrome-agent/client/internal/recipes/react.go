package recipes

// React verbs: linkedin:like, x:like, x:repost, reddit:upvote — ported from the bash CLI's li_like,
// x_like, x_repost and reddit_upvote.
//
// These are DOM actions, not API replays, and every one of them is a TOGGLE. That is the whole
// danger: the button that likes a post also unlikes it, so "click the like button" run twice is a
// no-op that reports success twice, and run once on an already-liked post it silently takes the like
// away. The flow is therefore always read -> decide -> (click -> re-read), and the decision is a pure
// function (decide) so the rules are pinned by tests rather than by a live account.
//
// Every eval goes through EvalCSP: all four sites ship a script-src without 'unsafe-eval', and a
// plain eval there fails SILENTLY — no error, no result, which a caller reads as "control not found".

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/browser"
)

// ErrUsage marks a React failure where nothing was attempted (bad key, missing or foreign url).
var ErrUsage = errors.New("usage")

// Control states a probe reports.
const (
	stateOn   = "on"   // already in the target state (liked / reposted / upvoted)
	stateOff  = "off"  // control present, not yet in the target state
	stateNone = "none" // control not found: signed out, not rendered, or selector drift
)

type reactSpec struct {
	key, site, action string
	domain            string // bare host the url must be on; also the pacing domain
	defaultURL        string // "" = a url is required
	settle            time.Duration
	// find is JS defining `function __find()` returning {el, state, label?, why?}. It may read
	// `__url` (the page the caller asked for). It must never click.
	find string
	// confirmJS, when set, is a second click after the first (x:repost's menu item).
	confirmJS string
}

var reactSpecs = map[string]reactSpec{
	"linkedin:like": {
		key: "linkedin:like", site: "linkedin", action: "like", domain: "linkedin.com",
		// As bash: no url means the first post on the feed.
		defaultURL: "https://www.linkedin.com/feed/", settle: 4 * time.Second,
		// Selector identical to bash li_like.
		// TRAP (the bash had it): li_like clicked without reading whether a reaction was already set,
		// so on an already-liked post it UNLIKED and then its after-check ("label contains like")
		// passed on the stale label. Here "on" is decided from the label AND aria-pressed, and any
		// label we do not recognise as "no reaction" counts as on — an unknown state must never be
		// clicked, because the wrong guess removes a reaction.
		// TRAP: a Celebrate/Support/etc. reaction is also "on". Clicking the button then removes
		// THAT reaction; it does not convert it to a like.
		find: `function __find(){
  const b=document.querySelector('button[aria-label^="Reaction button state"]');
  if(!b) return {state:'none', why:'reaction button not found (signed out or selector drift)'};
  const l=b.getAttribute('aria-label')||'';
  const off=/no reaction/i.test(l) && b.getAttribute('aria-pressed')!=='true';
  return {el:b, state:off?'off':'on', label:l};
}`,
	},
	"x:like": {
		key: "x:like", site: "x", action: "like", domain: "x.com", settle: 5 * time.Second,
		find: xFind(`like`, `unlike`),
	},
	"x:repost": {
		key: "x:repost", site: "x", action: "repost", domain: "x.com", settle: 5 * time.Second,
		find: xFind(`retweet`, `unretweet`),
		// TRAP: the retweet button only opens a menu (Repost / Quote). Nothing is reposted until
		// retweetConfirm is clicked; a flow that stops after the first click leaves a dangling menu
		// and an unchanged state. If the menu item never appears, close the menu (Escape) rather
		// than leave it open over the next lane's work. Selector identical to bash x_repost.
		confirmJS: `const c=document.querySelector('[data-testid="retweetConfirm"]');
if(!c){ try{document.body.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));}catch(e){}
  return {clicked:false, why:'retweetConfirm menu item did not appear'}; }
c.click(); return {clicked:true};`,
	},
	"reddit:upvote": {
		key: "reddit:upvote", site: "reddit", action: "upvote", domain: "reddit.com", settle: 5 * time.Second,
		// Selector identical to bash reddit_upvote.
		// TRAP: new Reddit (shreddit) renders the vote buttons INSIDE shreddit-post's shadow root, where
		// document.querySelector cannot see them — the bash reported "not found" for a perfectly
		// visible button. Same selector, tried on the document first and then inside the first
		// shreddit-post's shadowRoot (the post itself, never a comment: comment vote rows live in
		// their own nested shadow roots and are not reached by this lookup).
		// TRAP: a pressed upvote is aria-pressed="true"; clicking it again REMOVES the upvote.
		find: `function __find(){
  const sel='button[aria-label*="upvote" i],button[upvote]';
  let b=document.querySelector(sel);
  if(!b){ const p=document.querySelector('shreddit-post'); if(p&&p.shadowRoot) b=p.shadowRoot.querySelector(sel); }
  if(!b) return {state:'none', why:'upvote button not found (shadow DOM, signed out or drift)'};
  return {el:b, state:b.getAttribute('aria-pressed')==='true'?'on':'off', label:b.getAttribute('aria-label')||''};
}`,
	},
}

// xFind builds the X finder for a testid pair. Selectors identical to bash x_like / x_repost.
//
// TRAP (the bash had it): on a status page, document.querySelector('[data-testid="like"]') matches
// the FIRST unliked button on the page. If the focal tweet is already liked, that is a REPLY's like
// button — so the bash liked a stranger's reply and reported the focal tweet as liked. The lookup
// is scoped to the article whose timestamp permalink is exactly /status/<id> from the url, and a
// url whose tweet is not rendered reports "none" rather than falling back to the whole page.
func xFind(offID, onID string) string {
	return `function __find(){
  const id=((__url||'').match(/\/status\/(\d+)/)||[])[1];
  let scope=document;
  if(id){
    const t=document.querySelector('article a[href$="/status/'+id+'"] time');
    const a=t&&t.closest('article');
    if(!a) return {state:'none', why:'tweet '+id+' is not rendered on this page (signed out, deleted, or still loading)'};
    scope=a;
  }
  const on=scope.querySelector('[data-testid="` + onID + `"]');
  if(on) return {el:on, state:'on'};
  const off=scope.querySelector('[data-testid="` + offID + `"]');
  if(off) return {el:off, state:'off'};
  return {state:'none', why:'` + offID + ` button not found (signed out or drift)'};
}`
}

// IsReact reports whether key is a react verb, and the domain it acts on.
func IsReact(key string) (domain string, ok bool) {
	s, ok := reactSpecs[key]
	return s.domain, ok
}

// decision is what React does with a before-state.
type decision int

const (
	decideMissing decision = iota // no control: fail, never click
	decideStaged                  // not confirmed: report, never click
	decideAlready                 // confirmed but already in the target state: never click (would toggle OFF)
	decideClick                   // confirmed and off: click
)

// decide is React's whole rule table, separated from the browser.
func decide(before string, confirm bool) decision {
	switch before {
	case stateOn, stateOff:
	default:
		return decideMissing // includes "" and anything a drifted probe invents
	}
	if !confirm {
		return decideStaged
	}
	if before == stateOn {
		return decideAlready
	}
	return decideClick
}

// verified is the only success condition after a click: the control is now in the target state.
func verified(after string) bool { return after == stateOn }

// reactTarget picks the page to open and refuses a url on another site — a like must never land on
// whatever the tab (or a typo) points at.
func reactTarget(s reactSpec, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if s.defaultURL == "" {
			return "", fmt.Errorf("%w: %s needs a url — chrome-agent %s %s <url> [--confirm]", ErrUsage, s.key, s.site, s.action)
		}
		return s.defaultURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("%w: %s: not an http(s) url: %q", ErrUsage, s.key, raw)
	}
	h := strings.ToLower(u.Hostname())
	ok := h == s.domain || strings.HasSuffix(h, "."+s.domain)
	if s.domain == "x.com" && (h == "twitter.com" || strings.HasSuffix(h, ".twitter.com")) {
		ok = true
	}
	if !ok {
		return "", fmt.Errorf("%w: %s acts on %s, got a url on %s", ErrUsage, s.key, s.domain, h)
	}
	return raw, nil
}

type probe struct {
	State string `json:"state"`
	Label string `json:"label,omitempty"`
	Why   string `json:"why,omitempty"`
}

func (s reactSpec) prelude(target string) string {
	u, _ := json.Marshal(target)
	return "const __url=" + string(u) + ";\n" + s.find + "\n"
}

func readState(b *browser.Browser, s reactSpec, target string) (probe, error) {
	var p probe
	js := s.prelude(target) + "const f=__find(); return {state:f.state, label:f.label||'', why:f.why||''};"
	if err := b.EvalJSON(js, 8*time.Second, &p); err != nil {
		return p, fmt.Errorf("%s: reading the control failed: %w", s.key, err)
	}
	return p, nil
}

// React runs one react verb on this session's tab. Without confirm it reads and reports; with
// confirm it clicks only a control that is present and not already in the target state, then
// reports ok only if the state actually flipped.
func React(b *browser.Browser, key, rawURL string, confirm bool) (map[string]any, error) {
	s, ok := reactSpecs[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a react verb", ErrUsage, key)
	}
	target, err := reactTarget(s, rawURL)
	if err != nil {
		return nil, err
	}
	if err := b.Goto(target, s.settle); err != nil {
		return nil, err
	}
	before, err := readState(b, s, target)
	if err != nil {
		return nil, err
	}
	res := map[string]any{"site": s.site, "action": s.action, "url": target, "before": before.State}
	if before.Label != "" {
		res["label"] = before.Label
	}
	switch decide(before.State, confirm) {
	case decideMissing:
		why := before.Why
		if why == "" {
			why = "probe returned state " + fmt.Sprintf("%q", before.State)
		}
		return nil, fmt.Errorf("%s: %s", key, why)
	case decideStaged:
		res["staged"] = true
		res["note"] = "pass --confirm to " + s.action
		if before.State == stateOn {
			res["already"] = true // --confirm would be a no-op, never a toggle
		}
		return res, nil
	case decideAlready:
		res["already"] = true
		return res, nil
	}

	// Click — and re-check the state IN THE PAGE in the same eval, so a state that changed between
	// the read and the click (another lane, a late render) is never toggled off.
	var c struct {
		Clicked bool   `json:"clicked"`
		State   string `json:"state"`
		Why     string `json:"why"`
	}
	clickJS := s.prelude(target) + "const f=__find(); if(f.state!=='off') return {clicked:false, state:f.state, why:f.why||''}; f.el.click(); return {clicked:true};"
	if err := b.EvalJSON(clickJS, 8*time.Second, &c); err != nil {
		return nil, fmt.Errorf("%s: click failed: %w", key, err)
	}
	if !c.Clicked {
		if c.State == stateOn {
			res["already"] = true
			return res, nil
		}
		return nil, fmt.Errorf("%s: control vanished before the click: %s", key, c.Why)
	}
	res["clicked"] = true
	if s.confirmJS != "" {
		time.Sleep(time.Second) // the menu animates in
		var cc struct {
			Clicked bool   `json:"clicked"`
			Why     string `json:"why"`
		}
		if err := b.EvalJSON(s.confirmJS, 8*time.Second, &cc); err != nil {
			return nil, fmt.Errorf("%s: confirm click failed: %w", key, err)
		}
		if !cc.Clicked {
			res["ok"], res["verified"], res["error"] = false, false, cc.Why
			return res, nil
		}
	}

	// Poll for the flip rather than one fixed sleep: a slow render must not read as a failed click,
	// and re-reading never clicks again.
	after := probe{}
	for i := 0; i < 4; i++ {
		time.Sleep(time.Duration(2+i) * time.Second / 2)
		if after, err = readState(b, s, target); err == nil && verified(after.State) {
			break
		}
	}
	res["after"] = after.State
	res["verified"] = verified(after.State)
	res["ok"] = verified(after.State)
	if !verified(after.State) {
		res["error"] = fmt.Sprintf("clicked, but the %s control did not flip to its target state (got %q)", s.action, after.State)
	}
	return res, nil
}
