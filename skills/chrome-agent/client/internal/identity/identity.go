// Package identity is login, auth and logout — DOMAIN-scoped, never profile-scoped (ADR 0005).
//
// A browser profile is not signed into a thing; it is signed into many sites. A profile-level
// "signed in" boolean becomes a lie the moment one site's cookie expires while another holds — a
// green light nobody verified, which is the failure this whole design exists to prevent.
package identity

import (
	"fmt"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/browser"
	"github.com/deemwarhq/chrome-agent/internal/sites"
)

type Verdict struct {
	Domain       string `json:"domain"`
	Profile      string `json:"profile"`
	SignedIn     bool   `json:"signed_in"`
	As           string `json:"as,omitempty"`
	Why          string `json:"why,omitempty"`
	Inconclusive bool   `json:"inconclusive,omitempty"`
	Definition   string `json:"definition,omitempty"`
	Note         string `json:"note,omitempty"`
}

// probeResult is exactly what a site definition's probe_js must return.
type probeResult struct {
	SignedIn     bool   `json:"signed_in"`
	As           string `json:"as"`
	Why          string `json:"why"`
	Inconclusive bool   `json:"inconclusive"`
}

// Auth runs the site's own probe and reports {signed_in, as?}.
//
// It navigates THIS session's tab to the site first, because a cookie is origin-scoped and half
// these probes ask the site's own API: run from the wrong origin, a perfectly valid session reports
// logged out. That false negative once cost 44 hours of LinkedIn posting while X kept publishing.
func Auth(b *browser.Browser, domain string) (*Verdict, error) {
	def, err := sites.Load(domain)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, fmt.Errorf("no auth probe for %s — a guess here would report a green light nobody verified", sites.Normalize(domain))
	}
	url := def.Auth.ProbeURL
	if url == "" {
		url = def.Home
	}
	if url == "" {
		url = "https://" + def.Domain + "/"
	}
	if err := b.Goto(url, 5*time.Second); err != nil {
		return nil, err
	}
	var pr probeResult
	if err := b.EvalJSON(def.Auth.ProbeJS, 20*time.Second, &pr); err != nil {
		return nil, fmt.Errorf("probe returned no verdict: %w", err)
	}
	v := &Verdict{
		Domain: def.Domain, Profile: b.Profile,
		SignedIn: pr.SignedIn, As: pr.As, Why: pr.Why, Inconclusive: pr.Inconclusive,
	}
	// An UNVERIFIED definition is a hypothesis written from documentation. Saying so in the same
	// breath as the verdict is what keeps a guess from reading like proof.
	if def.Status == "unverified" {
		v.Definition = "unverified"
		v.Note = "this site definition has never been proven live — treat the verdict as provisional"
	}
	return v, nil
}

type LoginInfo struct {
	Status  string `json:"status"`
	Domain  string `json:"domain"`
	Profile string `json:"profile"`
	Opened  string `json:"opened,omitempty"`
	Expect  string `json:"expect,omitempty"`
	TwoFA   bool   `json:"twofa,omitempty"`
	Note    string `json:"note"`
	Then    string `json:"then"`
	Do      string `json:"do,omitempty"`
	Why     string `json:"why,omitempty"`
}

// Login opens the window at the site and GETS OUT OF THE WAY.
//
// It types NOTHING. Never a credential, never a 2FA code, never a "helpful" click on a provider
// button. A password an agent can type is a password inside an agent's context, and automated
// sign-in is the fastest route to a restricted account.
func Login(b *browser.Browser, domain string, headless bool) (*LoginInfo, error) {
	def, err := sites.Load(domain)
	if err != nil {
		return nil, err
	}
	d := sites.Normalize(domain)
	url := "https://" + d + "/"
	info := &LoginInfo{Domain: d, Profile: b.Profile,
		Note: "the browser window is open at this site in this profile. Sign in BY HAND — this command types nothing and never will.",
		Then: "chrome-agent auth " + d}
	if def != nil {
		if def.Login.URL != "" {
			url = def.Login.URL
		}
		info.Domain, info.Expect, info.TwoFA = def.Domain, def.Login.Note, def.Login.TwoFA
		info.Then = "chrome-agent auth " + def.Domain
	}
	// A human cannot sign in to a window that is not drawn. On a server the window lives behind a
	// per-tab grant (ADR 0009); say so instead of opening something nobody can see and then waiting.
	if headless {
		info.Status = "needs-a-screen"
		info.Why = "this profile is running headless, and a human cannot sign in to a window that is not drawn"
		info.Do = "on a server: grant this tab to a human (ADR 0009), or run the profile headful"
		return info, nil
	}
	if err := b.Up(url, false); err != nil {
		return nil, err
	}
	if err := b.Goto(url, 3*time.Second); err != nil {
		return nil, err
	}
	info.Status, info.Opened = "awaiting-human", url
	return info, nil
}

type LogoutResult struct {
	Domain    string   `json:"domain"`
	Profile   string   `json:"profile"`
	Method    string   `json:"method"`
	Did       string   `json:"did"`
	SignedOut bool     `json:"signed_out"`
	AuthAfter *Verdict `json:"auth_after,omitempty"`
	Warning   string   `json:"warning,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// clearCookiesJS is the LAST RESORT path. It ends the LOCAL session only: the site is never told,
// so its session stays alive until it expires on its own. The output has to say so.
const clearCookiesJS = `
const names = (() => { try { return document.cookie.split(";").map(c => c.trim().split("=")[0]).filter(Boolean); } catch (e) { return []; } })();
const host = location.hostname.replace(/^www\./, "");
const paths = ["/", location.pathname];
const domains = ["", host, "." + host, "." + host.split(".").slice(-2).join(".")];
for (const n of names) for (const p of paths) for (const d of domains) {
  document.cookie = n + "=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=" + p + (d ? "; domain=" + d : "");
}
try { localStorage.clear(); sessionStorage.clear(); } catch (e) {}
return {cleared: names.length};`

// Logout ends THIS site's session and leaves every other site in the profile alone.
//
// It VERIFIES afterwards. Every other verb here proves its effect; a logout that says "done"
// without checking is the same lie as a 200 on a dead post.
func Logout(b *browser.Browser, domain string) (*LogoutResult, error) {
	def, err := sites.Load(domain)
	if err != nil {
		return nil, err
	}
	d := sites.Normalize(domain)
	method, home := "cookies", "https://"+d+"/"
	var lurl, selector, note string
	if def != nil {
		d = def.Domain
		if def.Home != "" {
			home = def.Home
		}
		if def.Logout.Method != "" {
			method = def.Logout.Method
		}
		lurl, selector, note = def.Logout.URL, def.Logout.Selector, def.Logout.Note
	}

	res := &LogoutResult{Domain: d, Profile: b.Profile, Method: method, Note: note}
	switch method {
	case "url":
		if lurl == "" {
			return nil, fmt.Errorf("logout.method=url but the site file has no logout.url for %s", d)
		}
		if err := b.Goto(lurl, 6*time.Second); err != nil {
			return nil, err
		}
		res.Did = "navigated the site's own logout route"
	case "dom":
		if selector == "" {
			return nil, fmt.Errorf("logout.method=dom but the site file has no logout.selector for %s", d)
		}
		if err := b.Goto(home, 5*time.Second); err != nil {
			return nil, err
		}
		js := fmt.Sprintf("const b = document.querySelector(%q); if (!b) return 'no-control'; b.click(); return 'clicked';", selector)
		if _, err := b.EvalCSP(js, 10*time.Second); err != nil {
			return nil, err
		}
		time.Sleep(4 * time.Second)
		res.Did = "clicked the site's own logout control"
	case "cookies":
		if err := b.Goto(home, 4*time.Second); err != nil {
			return nil, err
		}
		if _, err := b.EvalCSP(clearCookiesJS, 12*time.Second); err != nil {
			return nil, err
		}
		time.Sleep(2 * time.Second)
		res.Did = "cleared this origin's readable cookies and web storage"
		res.Warning = "local only — the site was never told, so its session persists until it expires"
	default:
		return nil, fmt.Errorf("logout.method must be url|dom|cookies (got %q)", method)
	}

	after, err := Auth(b, d)
	if err == nil {
		res.AuthAfter = after
		res.SignedOut = !after.SignedIn
	}
	return res, nil
}
