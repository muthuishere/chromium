// Package recipes runs browser-research's verified recipes through the fork, with no node.
//
// # What this replaces
//
// recipe-run.mjs imported the ESM registry, called `fn.toString()` on the resolved recipe, and
// printed an async-function BODY for the bash CLI to hand to the fork's EVALASYNC verb. That one
// `node` invocation was the last runtime dependency on the read path (ADR 0010). This package emits
// a byte-identical payload from Go.
//
// # Approach: parse/emit at RUNTIME, in Go (option (b)), not a build-time bundle
//
// A build-time generator that flattened the registry into one embedded JS blob would have been less
// code. It was rejected for two reasons, both of which are the same reason ADR 0004 exists:
//
//  1. The INSTALLED copy must keep winning. ~/.config/chrome-agent/recipes is where a re-skinned
//     site gets fixed at 2am on a server — one .js edit, no rebuild, no node, no repo. A generated
//     bundle can only ever carry what was in the tree at build time, so the edit would be inert and
//     the operator would be debugging a file nobody reads. `recipes vendor --force` and hand edits
//     both keep working here because the .js is the artifact, not an input to one.
//  2. A generated artifact ages silently. Two copies of the same registry — the .js and the bundle —
//     drift the moment one is regenerated and the other is not, and the failure mode is a recipe
//     that runs OLD code while its source looks current.
//
// The cost is a JS scanner. It is bounded: the registry's shape is a contract (an exported object
// literal of {world, match, describe, write, fn} entries, each fn a self-contained top-level
// function in the same file, because the extension serializes it through Function.prototype
// .toString() too). recipes_test.go pins the extraction against all 48 known verbs, so a registry
// that grows a shape this scanner cannot read fails a test rather than a live post.
//
// # Worlds
//
// The registry's world is "MAIN" (the real page window — credentialed same-origin fetch riding page
// cookies) or "ISOLATED" (the extension's content-script world: shares the DOM, ignores the page
// CSP). The fork has no extension and no isolated world — sendkeys_watcher.cc injects with
// content::ISOLATED_WORLD_ID_GLOBAL, which that header documents as value 0, THE MAIN WORLD, on
// both eval paths. So:
//
//   - MAIN is satisfied by either path; its requirement is "the page's own window", which both give.
//   - ISOLATED's real requirement here is CSP immunity, and the fork's equivalent is the b64 path
//     (EvalCSP): the source is interpolated into the injected script, never eval()'d, so a page
//     whose script-src lacks 'unsafe-eval' cannot block it. EvalPlain's eval(atob(...)) fails
//     SILENTLY there — no error, no result.
//
// EvalCSP is therefore correct for both and is the default. EvalPlain stays reachable through
// Options.Plain for byte-parity with the bash default when a caller wants it.
package recipes

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/assets"
	"github.com/deemwarhq/chrome-agent/internal/browser"
	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/sites"
)

const embedRoot = "recipes"

// Recipe is one runnable verb. The JSON tags are the wire contract that `recipes --json` serves and
// that apl's playbook generator consumes — it used to regex-parse someone else's registry.js to
// learn this, inferring "is this a write?" from the FILE a key lived in.
type Recipe struct {
	Key      string `json:"key"`
	Site     string `json:"site"`
	Verb     string `json:"verb"`
	Describe string `json:"describe"`
	Write    bool   `json:"write"`
	// Class is the pacing/confirmation class: "read" | "react" | "mutate". A react (like, repost,
	// upvote) is a reversible toggle on someone else's content; a mutate creates or deletes content.
	// Write stays for the consumers that already match on it; Class is what a pacer keys on.
	Class  string `json:"class"`
	World  string `json:"world"`
	Source string `json:"source"` // registry | chrome-agent | user
	CLI    string `json:"cli"`

	Match string `json:"-"`
	Fn    string `json:"-"` // the recipe function's source; empty for a chrome-agent builtin
	From  string `json:"-"` // override | installed | embedded | builtin
	File  string `json:"-"`
}

// builtin is a verb chrome-agent implements ITSELF. None of these are in the registry, and a
// generator that read only the registry concluded that reacting was impossible and wrote that into
// a playbook. Both sources, one list, each entry naming its own.
type builtin struct {
	key, describe, cli string
	write              bool
	class              string // "read" | "react" — a react builtin runs through React(), not Run()
	js                 string // page body for a read builtin; empty for a react (orchestrated in Go)
}

// Recipe classes.
const (
	ClassRead   = "read"
	ClassReact  = "react"
	ClassMutate = "mutate"
)

// classFor is the class of a registry or user recipe: they cannot express "react", so a write is a
// mutate and everything else is a read.
func classFor(write bool) string {
	if write {
		return ClassMutate
	}
	return ClassRead
}

var builtins = []builtin{
	{key: "linkedin:like", write: true, class: ClassReact,
		describe: "like the post at <url> (or the first feed post) — verified by the reaction button flipping state",
		cli:      "chrome-agent linkedin like <url> --confirm"},
	{key: "x:like", write: true, class: ClassReact,
		describe: "like the tweet at <url> — verified by data-testid flipping like -> unlike",
		cli:      "chrome-agent x like <url> --confirm"},
	{key: "x:repost", write: true, class: ClassReact,
		describe: "repost the tweet at <url> — verified by data-testid flipping retweet -> unretweet",
		cli:      "chrome-agent x repost <url> --confirm"},
	{key: "reddit:upvote", write: true, class: ClassReact,
		describe: "upvote the post at <url> — verified by aria-pressed becoming true",
		cli:      "chrome-agent reddit upvote <url> --confirm"},
	{key: "hackernews:top", write: false, class: ClassRead,
		describe: "read the HN front page: title, points, comments, item link",
		cli:      "chrome-agent hackernews top [n]", js: hnTopJS},
	{key: "hackernews:item", write: false, class: ClassRead,
		describe: "read a thread back by url or id — comments with depth, and the [flagged]/[dead] a 200 hides",
		cli:      "chrome-agent hackernews item <url-or-id>", js: hnItemJS},
}

// ---------------------------------------------------------------------------------------------
// Where the registry lives. Same shape as the site definitions (ADR 0004), same reason: the path
// used to be hardcoded to the owner's checkout, so a server reported 6 verbs instead of 48.
// ---------------------------------------------------------------------------------------------

type tree struct {
	label string
	dir   string // empty => the embedded FS
}

func (t tree) read(name string) ([]byte, error) {
	if t.dir == "" {
		return assets.Recipes.ReadFile(embedRoot + "/" + name)
	}
	return os.ReadFile(filepath.Join(t.dir, filepath.FromSlash(name)))
}

// asRegistryDir accepts a directory OR a path to registry.js, exactly like recipes-path.mjs.
func asRegistryDir(p string) string {
	if p == "" {
		return ""
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	if fi.IsDir() {
		if _, err := os.Stat(filepath.Join(p, "registry.js")); err == nil {
			return p
		}
		return ""
	}
	return filepath.Dir(p)
}

// Trees is the resolution order, highest first. Exported so `doctor` can say which one wins.
func Trees() []tree {
	var out []tree
	for _, env := range []string{"CHROME_AGENT_RECIPES", "BR_REGISTRY"} {
		if d := asRegistryDir(os.Getenv(env)); d != "" {
			out = append(out, tree{"override", d})
			break
		}
	}
	if d := asRegistryDir(filepath.Join(paths.ConfigDir(), "recipes")); d != "" {
		out = append(out, tree{"installed", d})
	}
	return append(out, tree{"embedded", ""})
}

// ---------------------------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------------------------

var (
	reExportConst = regexp.MustCompile(`(?m)^export const ([A-Za-z_$][\w$]*)\s*=\s*\{`)
	reImport      = regexp.MustCompile(`(?m)^import\s+(?:\{\s*([A-Za-z_$][\w$]*)\s*\}|([A-Za-z_$][\w$]*))\s+from\s+["']\./([^"']+)["']`)
	reSpread      = regexp.MustCompile(`\.\.\.([A-Za-z_$][\w$]*)`)
	reFuncDecl    = regexp.MustCompile(`(?m)^(export default\s+)?(async\s+)?function\s*([A-Za-z_$][\w$]*)?\s*\(`)
	reTopConst    = regexp.MustCompile(`(?m)^(?:export )?const ([A-Za-z_$][\w$]*)\s*=\s*`)
)

// registryFromTree parses one recipe tree into the built-in catalog, in registry.js's spread order.
// It mirrors `mod.RECIPES` — NOT getRecipes() — because that is what the verb list has always been.
func registryFromTree(t tree) (map[string]*Recipe, error) {
	src, err := t.read("registry.js")
	if err != nil {
		return nil, err
	}
	s := string(src)

	imports := map[string]string{} // ident -> file
	for _, m := range reImport.FindAllStringSubmatch(s, -1) {
		ident := m[1]
		if ident == "" {
			ident = m[2]
		}
		imports[ident] = m[3]
	}

	// The spread order inside `export const RECIPES = { ...a, ...b }` is the shadowing order: a
	// later file wins a duplicate key. research.js re-uses the generic: prefix, so this matters.
	loc := reExportConst.FindStringSubmatchIndex(s)
	for loc != nil && s[loc[2]:loc[3]] != "RECIPES" {
		next := reExportConst.FindStringSubmatchIndex(s[loc[1]:])
		if next == nil {
			loc = nil
			break
		}
		for i := range next {
			if next[i] >= 0 {
				next[i] += loc[1]
			}
		}
		loc = next
	}
	if loc == nil {
		return nil, fmt.Errorf("registry.js has no `export const RECIPES = {`")
	}
	open := loc[1] - 1
	end := matchBrace(s, open)
	if end < 0 {
		return nil, fmt.Errorf("registry.js: unbalanced RECIPES object")
	}

	out := map[string]*Recipe{}
	for _, m := range reSpread.FindAllStringSubmatch(s[open:end], -1) {
		file, ok := imports[m[1]]
		if !ok {
			continue
		}
		body, err := t.read(file)
		if err != nil {
			continue
		}
		if err := parseRecipeFile(string(body), file, t.label, out); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("registry.js resolved to zero recipes")
	}
	return out, nil
}

// parseRecipeFile pulls every {world, match, describe, write, fn} entry out of one recipe module.
func parseRecipeFile(s, file, label string, out map[string]*Recipe) error {
	funcs := topLevelFunctions(s)
	consts := topLevelStringConsts(s)

	for _, loc := range reExportConst.FindAllStringSubmatchIndex(s, -1) {
		name := s[loc[2]:loc[3]]
		if name == "RECIPES" {
			continue
		}
		open := loc[1] - 1
		end := matchBrace(s, open)
		if end < 0 {
			return fmt.Errorf("unbalanced `export const %s`", name)
		}
		for _, e := range objectEntries(s, open, end) {
			if e.key == "" || !strings.HasPrefix(e.val, "{") {
				continue
			}
			ve := matchBrace(s, e.valStart)
			if ve < 0 {
				continue
			}
			r := &Recipe{Key: e.key, Source: "registry", From: label, File: file,
				CLI: "chrome-agent recipe " + e.key}
			for _, f := range objectEntries(s, e.valStart, ve) {
				switch f.key {
				case "world":
					r.World, _ = jsString(f.val)
				case "describe":
					r.Describe, _ = jsString(f.val)
				case "match":
					if v, ok := jsString(f.val); ok {
						r.Match = v
					} else {
						r.Match = consts[f.val]
					}
				case "write":
					r.Write = f.val == "true"
				case "fn":
					if src, ok := funcs[f.val]; ok {
						r.Fn = src // `fn: someTopLevelFunction` — the registry's only shape today
					} else {
						r.Fn = f.val // an inline function/arrow expression
					}
				}
			}
			r.Site, r.Verb = splitKey(r.Key)
			r.Class = classFor(r.Write)
			out[r.Key] = r
		}
	}
	return nil
}

// userRecipes folds in the drop-in userscripts from <tree>/user/registry.generated.js.
//
// They are RUNNABLE but deliberately absent from List(): recipe-run.mjs resolved them (via
// getRecipes()) while recipes-list.mjs did not (it read RECIPES), so a user recipe has always been
// invisible to `recipes --json` and runnable by `recipe <key>`. That asymmetry is preserved here so
// the verb list stays byte-identical; see WIRING.md.
func userRecipes(t tree) map[string]*Recipe {
	out := map[string]*Recipe{}
	if t.dir == "" {
		return out // per-machine, git-ignored drop-ins are never embedded
	}
	b, err := os.ReadFile(filepath.Join(t.dir, "user", "registry.generated.js"))
	if err != nil {
		return out
	}
	s := string(b)
	imports := map[string]string{}
	for _, m := range reImport.FindAllStringSubmatch(s, -1) {
		ident := m[1]
		if ident == "" {
			ident = m[2]
		}
		imports[ident] = m[3]
	}
	for _, loc := range reExportConst.FindAllStringSubmatchIndex(s, -1) {
		open := loc[1] - 1
		end := matchBrace(s, open)
		if end < 0 {
			continue
		}
		for _, e := range objectEntries(s, open, end) {
			if e.key == "" || !strings.HasPrefix(e.val, "{") {
				continue
			}
			ve := matchBrace(s, e.valStart)
			if ve < 0 {
				continue
			}
			r := &Recipe{Key: e.key, Source: "user", From: t.label,
				CLI: "chrome-agent recipe " + e.key}
			for _, f := range objectEntries(s, e.valStart, ve) {
				switch f.key {
				case "world":
					r.World, _ = jsString(f.val)
				case "describe":
					r.Describe, _ = jsString(f.val)
				case "match":
					r.Match, _ = jsString(f.val)
				case "write":
					r.Write = f.val == "true"
				case "fn":
					file, ok := imports[f.val]
					if !ok {
						continue
					}
					body, err := os.ReadFile(filepath.Join(t.dir, "user", filepath.FromSlash(file)))
					if err != nil {
						continue
					}
					if src, ok := topLevelFunctions(string(body))["default"]; ok {
						r.Fn, r.File = src, "user/"+file
					}
				}
			}
			if r.Fn == "" {
				continue
			}
			r.Site, r.Verb = splitKey(r.Key)
			r.Class = classFor(r.Write)
			out[r.Key] = r
		}
	}
	return out
}

// Registry returns the built-in catalog from whichever tree wins, and the label of that tree.
func Registry() (map[string]*Recipe, string, error) {
	var firstErr error
	for _, t := range Trees() {
		m, err := registryFromTree(t)
		if err == nil {
			return m, t.label, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, "", fmt.Errorf("no recipe registry found: %w", firstErr)
}

// List is every verb: the registry PLUS chrome-agent's own, each entry naming its own source.
// It is the one source of truth for "what verbs exist", for humans and for generators.
func List() ([]Recipe, error) {
	reg, _, err := Registry()
	if err != nil {
		return nil, err
	}
	out := make([]Recipe, 0, len(reg)+len(builtins))
	for _, r := range reg {
		out = append(out, *r)
	}
	for _, b := range builtins {
		site, verb := splitKey(b.key)
		// world "main" in lower case, because that is what this list has always said for a
		// chrome-agent DOM verb and a consumer may be matching on the string.
		out = append(out, Recipe{Key: b.key, Site: site, Verb: verb, Describe: b.describe,
			Write: b.write, Class: b.class, World: "main", Source: "chrome-agent", CLI: b.cli, From: "builtin"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Resolve finds one verb by key: registry, then the machine's user drop-ins, then the builtins.
func Resolve(key string) (*Recipe, error) {
	reg, _, err := Registry()
	if err == nil {
		if r, ok := reg[key]; ok {
			return r, nil
		}
	}
	for _, t := range Trees() {
		if r, ok := userRecipes(t)[key]; ok {
			return r, nil
		}
	}
	for _, b := range builtins {
		if b.key == key {
			site, verb := splitKey(key)
			return &Recipe{Key: key, Site: site, Verb: verb, Describe: b.describe, Write: b.write,
				Class: b.class, World: "main", Source: "chrome-agent", CLI: b.cli, From: "builtin", Fn: b.js}, nil
		}
	}
	known := []string{}
	if reg != nil {
		for k := range reg {
			known = append(known, k)
		}
	}
	for _, b := range builtins {
		known = append(known, b.key)
	}
	sort.Strings(known)
	return nil, fmt.Errorf("no recipe %q. known: %s", key, strings.Join(known, ", "))
}

func splitKey(key string) (site, verb string) {
	if i := strings.Index(key, ":"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

// ---------------------------------------------------------------------------------------------
// Running
// ---------------------------------------------------------------------------------------------

// Payload is the async-function BODY the fork's EVALASYNC verb runs — byte-identical to what
// recipe-run.mjs printed. `await` covers a sync or async fn; a throw propagates and the fork acks
// it as {ok:false,error}, so there is no stash-on-a-global-and-poll dance on this side.
func Payload(r *Recipe, optsJSON string) string {
	opts := strings.TrimSpace(optsJSON)
	if opts == "" {
		opts = "undefined"
	}
	if r.Source == "chrome-agent" {
		// A builtin is already a body, not a function: it reads opts off __opts.
		return "const __opts=(" + opts + ")||{};\n" + r.Fn
	}
	return "const __fn=(" + r.Fn + ");\nconst __r=await __fn(" + opts + ");\nreturn (__r===undefined?{ok:true}:__r);"
}

type Options struct {
	// Plain runs the payload through eval(atob(...)) instead of the CSP-safe b64 path. Parity with
	// the bash default; wrong on any strict-CSP page, where eval fails SILENTLY.
	Plain bool
	// Timeout for the page-side eval. 20s is the bash default and a heavy SPA uses most of it.
	Timeout time.Duration
	// NoOrigin skips EnsureOrigin. Only for a caller that has already placed the tab.
	NoOrigin bool
}

// Run resolves a verb and executes it IN THE PAGE.
func Run(b *browser.Browser, key, optsJSON string, opt Options) (any, error) {
	r, err := Resolve(key)
	if err != nil {
		return nil, err
	}
	if r.Class == ClassReact {
		// A react is not one page body: it navigates, reads state, clicks, and re-reads (React()).
		// Running it through `recipe` would also skip the site verb's pacing and confirm gate.
		return nil, fmt.Errorf("%s is a react verb — it does not run through `recipe`; use: %s", key, r.CLI)
	}
	if r.Source == "chrome-agent" && r.Fn == "" {
		return nil, fmt.Errorf("%s is a chrome-agent verb with no page body — it is listed, not runnable here: %s", key, r.CLI)
	}
	if !opt.NoOrigin {
		if err := EnsureOrigin(b, key); err != nil {
			return nil, err
		}
	}
	// hackernews:item acts on a caller-chosen item, so it places the tab itself.
	if key == "hackernews:item" {
		u, err := hnItemURL(optsJSON)
		if err != nil {
			return nil, err
		}
		if err := b.Goto(u, 4*time.Second); err != nil {
			return nil, err
		}
	}
	timeout := opt.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	js := Payload(r, optsJSON)
	if opt.Plain {
		return b.EvalPlain(js, timeout)
	}
	return b.EvalCSP(js, timeout)
}

// EnsureOrigin puts the tab where the recipe can actually read cookies and DOM.
//
// WHY (2026-07-21): a site recipe reads that site's cookies from the ACTIVE TAB, and cookies are
// origin-scoped. Run linkedin:feed while the tab sits on openrouter.ai and it returns
// {"error":"no JSESSIONID - not logged in?"} for a PERFECTLY VALID session. That false logged-out
// silently cost 44 hours of LinkedIn posting while X kept publishing, and the owner spotted it
// before any monitor did. The tab is shared mutable state — it is wherever the last lane left it —
// so the failure is intermittent and ordering-dependent. Fixing it in the caller's head does not
// scale; it is fixed here, once, for every caller.
//
// CAREFUL: navigating is WRONG for some recipes, so this is deliberately conservative.
//   - generic:* / capture:* / compose:*  operate on WHATEVER page you are on -> never navigate.
//   - recipes documented "open the post's permalink first" (linkedin:comment, comment-delete,
//     reddit:comment, hackernews:comment) act on a page the CALLER chose -> never navigate.
//   - the rest are credentialed API calls that need only the right ORIGIN -> navigate only when the
//     HOST differs, never when we are already somewhere valid on that site.
func EnsureOrigin(b *browser.Browser, key string) error {
	o := originFor(key)
	if !o.navigate {
		return nil
	}
	cur := ""
	if v, err := b.EvalCSP("return location.href", 8*time.Second); err == nil {
		cur, _ = v.(string)
	}
	if OriginSatisfied(cur, o) {
		return nil // right site AND right page
	}
	// 8s, not 3s: LinkedIn recent-activity/feed are heavy SPAs that keep rendering after load. With
	// 3s, linkedin:my-posts navigated correctly and then TIMED OUT scraping a half-built DOM — which
	// is indistinguishable from "the recipe is broken". Settle time is part of the contract.
	_ = b.Goto(o.url, 8*time.Second)
	return nil
}

// origin is where a verb must be run from. navigate=false means "wherever the caller left the tab".
type origin struct {
	navigate bool
	host     string
	path     string
	url      string
}

// originFor is EnsureOrigin's whole decision, separated from the browser so the rules it encodes are
// testable without steering a live tab.
func originFor(key string) origin {
	site, _ := splitKey(key)
	switch site {
	case "generic", "capture", "compose":
		return origin{} // operate on the current page
	}
	switch key {
	case "linkedin:comment", "linkedin:comment-delete", "reddit:comment", "hackernews:comment":
		return origin{} // caller-chosen page
	}
	o := origin{navigate: true}
	switch site {
	case "linkedin":
		o.host, o.url = "linkedin.com", "https://www.linkedin.com/feed/"
	case "x", "twitter":
		o.host, o.url = "x.com", "https://x.com/home"
	case "reddit":
		o.host, o.url = "reddit.com", "https://www.reddit.com/"
	case "hackernews":
		o.host, o.url = "news.ycombinator.com", "https://news.ycombinator.com/"
	default:
		return origin{}
	}
	// linkedin:my-posts is a DOM scrape of the recent-activity page. Same origin is NOT enough: run
	// from /feed/ it returns postCount:0, which is indistinguishable from "we have no posts".
	if key == "linkedin:my-posts" {
		o.url, o.path = "https://www.linkedin.com/in/me/recent-activity/all/", "recent-activity"
	} else if site == "linkedin" {
		// ORIGIN IS NOT ENOUGH for the LinkedIn voyager recipes — they need /feed/ specifically.
		// Proven 2026-07-21: with the tab on linkedin.com/in/me/recent-activity (host matches!),
		// linkedin:feed TIMED OUT; the same call from /feed/ returned 20 posts.
		// linkedin:notifications documents the same requirement in its own describe ("Run it from
		// /feed/ — the /notifications/ page itself wedges every recipe eval"). So match the PATH we
		// need, not just the host.
		o.path = "/feed"
	}
	return o
}

// OriginSatisfied reports whether the tab at `cur` is already somewhere this verb can run.
func OriginSatisfied(cur string, o origin) bool {
	if !o.navigate {
		return true
	}
	if !strings.Contains(cur, o.host) {
		return false
	}
	return o.path == "" || strings.Contains(cur, o.path)
}

// ---------------------------------------------------------------------------------------------
// read <domain> / verify <domain>
// ---------------------------------------------------------------------------------------------

// Plan is what to run and where: a verb, and the PAGE that verb needs.
type Plan struct {
	Key    string // "" = no read recipe registered for this domain
	CLI    string // non-empty for a chrome-agent builtin — not a registry recipe
	URL    string
	Status string
}

// fixture: a read verb needs the right PAGE, not just the right origin. Every remaining "0 items"
// was this — youtube:channel-videos scrapes THE CURRENT PAGE, so the homepage yields nothing and the
// site looked broken; instagram:post wants an open post, not a profile root.
func fixture(domain string) (key, url string) {
	switch domain {
	case "youtube.com":
		return "youtube:channel-videos", "https://www.youtube.com/results?search_query=chromium"
	case "instagram.com":
		return "instagram:profile", "https://www.instagram.com/instagram/"
	case "news.ycombinator.com":
		return "hackernews:top", "https://news.ycombinator.com/"
	}
	return "", ""
}

// siteOf maps a recipe's site prefix to the domain it reads.
var siteOf = map[string]string{
	"linkedin": "linkedin.com", "x": "x.com", "facebook": "facebook.com",
	"instagram": "instagram.com", "reddit": "reddit.com", "youtube": "youtube.com",
	"hackernews": "news.ycombinator.com",
}

var rePlaceholder = regexp.MustCompile(`^[\[<].*[\]>]$`)

// PlanFor decides what `read`/`verify` should run for a domain, and on what page.
func PlanFor(domain string) Plan {
	domain = sites.Normalize(domain)
	p := Plan{}
	// The site definition owns this (ADR 0004); the fixture table is the fallback for a domain with
	// no file. "eval" means the site has no read recipe yet — there is nothing to run, so verify
	// falls through to the auth probe, which is the honest answer for those sites.
	if def, _ := sites.Load(domain); def != nil {
		p.Status = def.Status
		p.Key, p.URL = def.Read.Verb, def.Read.FixtureURL
		if p.Key == "eval" {
			p.Key = ""
		}
		if p.Key == "" && p.URL == "" {
			if k, u := fixture(domain); k != "" {
				p.Key, p.URL = k, u
			}
		}
		if p.URL == "" {
			p.URL = def.Home
		}
	} else if k, u := fixture(domain); k != "" {
		p.Key, p.URL = k, u
	}
	if p.Status == "" {
		p.Status = "none"
	}
	if p.URL == "" {
		p.URL = "https://" + domain + "/"
	}

	all, err := List()
	if err != nil {
		return p
	}
	var cands []Recipe
	for _, r := range all {
		if !r.Write && siteOf[r.Site] == domain {
			cands = append(cands, r)
		}
	}
	var pick *Recipe
	if p.Key != "" {
		for i := range cands {
			if cands[i].Key == p.Key {
				pick = &cands[i]
			}
		}
	}
	if pick == nil {
		sort.SliceStable(cands, func(i, j int) bool {
			return cands[i].Source == "registry" && cands[j].Source != "registry"
		})
		if len(cands) > 0 {
			pick = &cands[0]
		}
	}
	if pick == nil {
		p.Key, p.CLI = "", ""
		return p
	}
	p.Key = pick.Key
	if pick.Source != "registry" {
		// The cli string is a usage template: drop its placeholders ([n], <url>) so what is left is
		// a runnable command. A literal "[n]" once reached the page as JS and threw
		// ReferenceError: n.
		var words []string
		for _, w := range strings.Fields(pick.CLI) {
			if !rePlaceholder.MatchString(w) {
				words = append(words, w)
			}
		}
		p.CLI = strings.Join(words, " ")
	}
	return p
}

// ReadResult is what `read <domain>` answers with.
type ReadResult struct {
	Domain     string `json:"domain"`
	ReadWith   string `json:"read_with"`
	Page       string `json:"page"`
	Definition string `json:"definition"`
	Generic    bool   `json:"generic"`
	Result     any    `json:"result"`
	Note       string `json:"note,omitempty"`
}

const genericNote = "this site has no read recipe — you are getting the page as text, not its API. " +
	"Promote it: capture the real call (chrome-agent capture arm|dump) and add a recipe."

// Read answers for EVERY known site, whether or not it has a read recipe.
//
// 12 of the 19 site definitions have no read recipe yet (read.verb = "eval"), and "we know this site
// but cannot read it" is a useless kind of knowing. The site file already names the page worth
// reading and the registry already has generic readers, so: a site with a recipe runs it, and a site
// without one gets the generic reader pointed at its fixture page. The output ALWAYS says which
// happened, because a generic page-text read is a weaker thing than a site's own API replay and must
// never be mistaken for one.
func Read(b *browser.Browser, domain, generic string) (*ReadResult, error) {
	domain = sites.Normalize(domain)
	if domain == "" {
		return nil, fmt.Errorf("usage: read <domain> [generic-recipe] — e.g. chrome-agent read linkedin.com")
	}
	if generic == "" {
		generic = "generic:page-text"
	}
	p := PlanFor(domain)
	if err := b.Goto(p.URL, 6*time.Second); err != nil {
		return nil, err
	}
	out := &ReadResult{Domain: domain, Page: p.URL, Definition: p.Status}
	key := p.Key
	if key == "" {
		key = generic
	}
	out.ReadWith = key
	out.Generic = strings.HasPrefix(key, "generic:")
	if out.Generic {
		out.Note = genericNote
	}
	// The tab is already on the fixture page; re-deciding the origin would only move it off.
	res, err := Run(b, key, "", Options{NoOrigin: true})
	if err != nil {
		out.Result = map[string]any{"error": err.Error()}
		return out, nil
	}
	out.Result = res
	return out, nil
}

// VerifyResult is what `verify <domain>` answers with. Verified==false is an exit-code 4 condition.
type VerifyResult struct {
	Domain       string `json:"domain"`
	Verified     bool   `json:"verified"`
	How          string `json:"how"`
	LastVerified string `json:"last_verified,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// Verify makes `last_verified` mean something.
//
// The playbooks' last_verified was stamped at GENERATION time, which proves only that someone ran a
// generator. This runs the site's real read recipe and succeeds only if the read is real. Three ways
// it fails, and ALL THREE used to read as success:
//
//	an error;
//	a result whose own `url` is not this domain — `verify instagram.com` once ran instagram:post
//	against whatever page the tab happened to be on, scraped REDDIT, found images, and stamped
//	instagram as verified (2026-09-12). A probe that can pass while pointed at the wrong site proves
//	nothing at all;
//	an EMPTY read — a logged-out facebook feed returns postCount:0 with no error, and "the verb ran"
//	is not "the verb works".
//
// A domain with no read recipe falls back to the caller's auth probe (probe != nil): "can we still
// read this site as ourselves" is the question either way.
func Verify(b *browser.Browser, domain string, probe func(string) error) (*VerifyResult, error) {
	domain = sites.Normalize(domain)
	if domain == "" {
		return nil, fmt.Errorf("usage: verify <domain> — e.g. chrome-agent verify linkedin.com")
	}
	p := PlanFor(domain)
	out := &VerifyResult{Domain: domain}
	if p.Key == "" {
		out.How = "auth"
		if probe == nil {
			out.Detail = "no read recipe for this domain and no auth probe was supplied"
			return out, nil
		}
		if err := probe(domain); err != nil {
			out.Detail = truncate(err.Error(), 200)
			return out, nil
		}
		out.Verified = true
		out.LastVerified = time.Now().UTC().Format("2006-01-02")
		return out, nil
	}
	out.How = "recipe:" + p.Key
	// PUT THE TAB ON THE PAGE FIRST. EnsureOrigin only navigates for the four sites it knows.
	if err := b.Goto(p.URL, 6*time.Second); err != nil {
		return nil, err
	}
	res, err := Run(b, p.Key, "", Options{NoOrigin: true})
	if err != nil {
		out.Detail = truncate(err.Error(), 200)
		return out, nil
	}
	if why := rejectRead(res, domain); why != "" {
		out.Detail = why
		return out, nil
	}
	out.Verified = true
	out.LastVerified = time.Now().UTC().Format("2006-01-02")
	return out, nil
}

// rejectRead returns "" when the read is real, or why it is not.
func rejectRead(res any, domain string) string {
	m, ok := res.(map[string]any)
	if !ok {
		b, _ := json.Marshal(res)
		return "the read returned no object: " + truncate(string(b), 200)
	}
	if e, has := m["error"]; has && e != nil && e != "" {
		b, _ := json.Marshal(e)
		return truncate(string(b), 200)
	}
	if u, _ := m["url"].(string); u != "" {
		h := ""
		if parsed, err := url.Parse(u); err == nil {
			h = strings.ToLower(parsed.Host)
			if i := strings.Index(h, ":"); i >= 0 {
				h = h[:i]
			}
		}
		if h != domain && !strings.HasSuffix(h, "."+domain) {
			return "the read ran on the wrong site: " + u
		}
	}
	maxLen, sawList := 0, false
	for _, v := range m {
		if l, ok := v.([]any); ok {
			sawList = true
			if len(l) > maxLen {
				maxLen = len(l)
			}
		}
	}
	if sawList && maxLen == 0 {
		return "the read returned 0 items — signed out, or the recipe drifted"
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------------------------------------------------------------------------------------------
// Sync — the embedded copy out to disk, never clobbering an operator's edit (mirrors sites.Sync)
// ---------------------------------------------------------------------------------------------

type SyncResult struct {
	InstalledDir string   `json:"installed_dir"`
	Installed    []string `json:"installed"`
	Replaced     []string `json:"replaced"`
	Kept         []string `json:"kept"`
	Unchanged    []string `json:"unchanged"`
	Force        bool     `json:"force"`
	DryRun       bool     `json:"dry_run"`
}

// Sync writes the embedded registry into ~/.config/chrome-agent/recipes. A locally edited file is
// KEPT unless force says otherwise: silently reverting an operator's 2am hotfix is the same class of
// failure as silently ageing a last_verified stamp.
func Sync(force, dry bool) (*SyncResult, error) {
	dst := filepath.Join(paths.ConfigDir(), "recipes")
	res := &SyncResult{InstalledDir: dst, Force: force, DryRun: dry,
		Installed: []string{}, Replaced: []string{}, Kept: []string{}, Unchanged: []string{}}
	if !dry {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return nil, err
		}
	}
	err := fs.WalkDir(assets.Recipes, embedRoot, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		name := strings.TrimPrefix(p, embedRoot+"/")
		want, err := assets.Recipes.ReadFile(p)
		if err != nil {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		cur, err := os.ReadFile(target)
		switch {
		case err != nil:
			if !dry {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(target, want, 0o644); err != nil {
					return err
				}
			}
			res.Installed = append(res.Installed, name)
		case string(cur) == string(want):
			res.Unchanged = append(res.Unchanged, name)
		case force:
			if !dry {
				if err := os.WriteFile(target, want, 0o644); err != nil {
					return err
				}
			}
			res.Replaced = append(res.Replaced, name)
		default:
			res.Kept = append(res.Kept, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(res.Installed)
	sort.Strings(res.Replaced)
	sort.Strings(res.Kept)
	sort.Strings(res.Unchanged)
	return res, nil
}

// ---------------------------------------------------------------------------------------------
// The JS scanner
//
// Enough of a JavaScript lexer to walk an object literal without being fooled by what looks like a
// brace: strings, template literals (with ${} nesting), line and block comments, and regex literals.
// It is NOT a parser and does not need to be — it never evaluates anything, it only finds balanced
// spans and copies source text out verbatim, which is exactly what Function.prototype.toString did
// on the node side.
//
// Regex-vs-divide is the one genuinely ambiguous case in JS lexing. It is resolved the way every
// hand-written scanner resolves it: a `/` starts a regex only where an expression may start — after
// an operator, an opening bracket, or a keyword like `return`. The recipes are full of
// /\s+/ and /^https?:/ inside strings and conditions, so getting this wrong truncates a function.
// ---------------------------------------------------------------------------------------------

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func regexAllowed(prev byte, prevWord string) bool {
	if prevWord != "" {
		switch prevWord {
		case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "case",
			"do", "else", "yield", "await":
			return true
		}
		return false
	}
	switch prev {
	case 0, '(', ',', '=', ':', '[', '!', '&', '|', '?', '{', '}', ';', '+', '-', '*', '%', '~', '^', '<', '>':
		return true
	}
	return false
}

// step advances past exactly one token starting at i, updating the "what came before" state that
// regexAllowed needs. It never returns i unchanged.
func step(s string, i int, prev *byte, prevWord *string) int {
	c := s[i]
	switch {
	case c == '/' && i+1 < len(s) && s[i+1] == '/':
		if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
			return i + j
		}
		return len(s)
	case c == '/' && i+1 < len(s) && s[i+1] == '*':
		if j := strings.Index(s[i+2:], "*/"); j >= 0 {
			return i + 2 + j + 2
		}
		return len(s)
	case c == '"' || c == '\'':
		j := i + 1
		for j < len(s) {
			if s[j] == '\\' {
				j += 2
				continue
			}
			if s[j] == c {
				j++
				break
			}
			j++
		}
		*prev, *prevWord = c, ""
		return j
	case c == '`':
		j := i + 1
		for j < len(s) {
			if s[j] == '\\' {
				j += 2
				continue
			}
			if s[j] == '`' {
				j++
				break
			}
			if s[j] == '$' && j+1 < len(s) && s[j+1] == '{' {
				if e := matchBrace(s, j+1); e >= 0 {
					j = e + 1
					continue
				}
			}
			j++
		}
		*prev, *prevWord = '`', ""
		return j
	case c == '/' && regexAllowed(*prev, *prevWord):
		j, inClass := i+1, false
		for j < len(s) {
			if s[j] == '\\' {
				j += 2
				continue
			}
			if s[j] == '\n' {
				break // an unterminated "regex" was a divide after all; fall back conservatively
			}
			if s[j] == '[' {
				inClass = true
			} else if s[j] == ']' {
				inClass = false
			} else if s[j] == '/' && !inClass {
				j++
				for j < len(s) && isIdentChar(s[j]) {
					j++ // flags
				}
				break
			}
			j++
		}
		*prev, *prevWord = '/', ""
		return j
	}
	if isIdentChar(c) {
		j := i
		for j < len(s) && isIdentChar(s[j]) {
			j++
		}
		*prev, *prevWord = c, s[i:j]
		return j
	}
	if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
		*prev, *prevWord = c, ""
	}
	return i + 1
}

func isOpen(c byte) bool  { return c == '{' || c == '[' || c == '(' }
func isClose(c byte) bool { return c == '}' || c == ']' || c == ')' }

// matchBrace returns the index of the closer matching the opener at `open`, or -1.
func matchBrace(s string, open int) int {
	if open < 0 || open >= len(s) || !isOpen(s[open]) {
		return -1
	}
	depth := 0
	var prev byte
	var pw string
	for i := open; i < len(s); {
		c := s[i]
		switch {
		case isOpen(c):
			depth++
			prev, pw = c, ""
			i++
		case isClose(c):
			depth--
			if depth == 0 {
				return i
			}
			prev, pw = c, ""
			i++
		default:
			i = step(s, i, &prev, &pw)
		}
	}
	return -1
}

// scanValue returns the end of the value starting at i, stopping at a top-level `,` or `}` (or `;`
// when stmt is set, for a top-level declaration).
func scanValue(s string, i int, stmt bool) int {
	depth := 0
	var prev byte
	var pw string
	for i < len(s) {
		c := s[i]
		if depth == 0 && (c == ',' || c == '}' || (stmt && c == ';')) {
			return i
		}
		switch {
		case isOpen(c):
			depth++
			prev, pw = c, ""
			i++
		case isClose(c):
			depth--
			prev, pw = c, ""
			i++
		default:
			i = step(s, i, &prev, &pw)
		}
	}
	return len(s)
}

func skipWS(s string, i int) int {
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if c == '/' && i+1 < len(s) && (s[i+1] == '/' || s[i+1] == '*') {
			var prev byte
			var pw string
			i = step(s, i, &prev, &pw)
			continue
		}
		return i
	}
	return i
}

type entry struct {
	key      string
	val      string // trimmed source of the value
	valStart int    // index of the value's first char in s
}

// objectEntries walks the TOP-LEVEL members of the object literal spanning [open, close].
func objectEntries(s string, open, close int) []entry {
	var out []entry
	i := open + 1
	for i < close {
		i = skipWS(s, i)
		if i >= close {
			break
		}
		if strings.HasPrefix(s[i:], "...") { // a spread is handled by the caller, not here
			i = scanValue(s, i, false)
			if i < close && s[i] == ',' {
				i++
			}
			continue
		}
		var key string
		if s[i] == '"' || s[i] == '\'' {
			var prev byte
			var pw string
			j := step(s, i, &prev, &pw)
			key, _ = jsString(s[i:j])
			i = j
		} else {
			j := i
			for j < close && isIdentChar(s[j]) {
				j++
			}
			if j == i {
				i++ // something unexpected; don't spin
				continue
			}
			key, i = s[i:j], j
		}
		i = skipWS(s, i)
		if i < close && s[i] == ':' {
			i++
		}
		i = skipWS(s, i)
		end := scanValue(s, i, false)
		out = append(out, entry{key: key, val: strings.TrimSpace(s[i:end]), valStart: i})
		i = end
		if i < close && s[i] == ',' {
			i++
		}
	}
	return out
}

// topLevelFunctions maps a declared name to its VERBATIM source — the string node's
// Function.prototype.toString() would have produced. An anonymous `export default function` is
// keyed "default" (the shape every user drop-in uses).
func topLevelFunctions(s string) map[string]string {
	out := map[string]string{}
	for _, loc := range reFuncDecl.FindAllStringSubmatchIndex(s, -1) {
		start := loc[0]
		name := "default"
		if loc[6] >= 0 {
			name = s[loc[6]:loc[7]]
		}
		paren := loc[1] - 1
		closeParen := matchBrace(s, paren)
		if closeParen < 0 {
			continue
		}
		body := skipWS(s, closeParen+1)
		if body >= len(s) || s[body] != '{' {
			continue
		}
		end := matchBrace(s, body)
		if end < 0 {
			continue
		}
		// `export default function …` — the declaration keyword is not part of the function source.
		if loc[2] >= 0 {
			start = loc[3]
			start = skipWS(s, start)
		}
		out[name] = s[start : end+1]
	}
	return out
}

// topLevelStringConsts resolves `const CAP_MATCH = "*://*/*";` so a recipe whose `match` is an
// identifier still reports a match pattern.
func topLevelStringConsts(s string) map[string]string {
	out := map[string]string{}
	for _, loc := range reTopConst.FindAllStringSubmatchIndex(s, -1) {
		end := scanValue(s, loc[1], true)
		if v, ok := jsString(strings.TrimSpace(s[loc[1]:end])); ok {
			out[s[loc[2]:loc[3]]] = v
		}
	}
	return out
}

// jsString decodes a single-quoted, double-quoted or substitution-free template literal. It reports
// false for anything that is not a string literal, which is how a caller tells `describe: "x"` from
// `match: SOME_CONST`.
func jsString(lit string) (string, bool) {
	if len(lit) < 2 {
		return "", false
	}
	q := lit[0]
	if (q != '"' && q != '\'' && q != '`') || lit[len(lit)-1] != q {
		return "", false
	}
	body := lit[1 : len(lit)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			if c == q {
				return "", false // an unescaped quote means this was not one literal
			}
			b.WriteByte(c)
			continue
		}
		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case 'u', 'x':
			n := 4
			if body[i] == 'x' {
				n = 2
			}
			if body[i] == 'u' && i+1 < len(body) && body[i+1] == '{' {
				j := strings.IndexByte(body[i:], '}')
				if j < 0 {
					return "", false
				}
				var r rune
				fmt.Sscanf(body[i+2:i+j], "%x", &r)
				b.WriteRune(r)
				i += j
				continue
			}
			if i+n >= len(body) {
				return "", false
			}
			var r rune
			fmt.Sscanf(body[i+1:i+1+n], "%x", &r)
			b.WriteRune(r)
			i += n
		case '\n':
			// a line continuation contributes nothing
		default:
			b.WriteByte(body[i])
		}
	}
	return b.String(), true
}

// ---------------------------------------------------------------------------------------------
// chrome-agent's own read verbs, as page JS
//
// HN could post and comment and could not read a single thing back — while its own traps file says
// "HN serves HTTP 200 on a dead or flagged post, open the item and read it back". These two are
// DOM reads (HN has no API worth replaying and no CSP to fight) and they report the two states a
// 200 hides: [flagged] and [dead].
// ---------------------------------------------------------------------------------------------

// hnItemURL turns {"id":123} or {"url":"..."} into the item page to open.
func hnItemURL(optsJSON string) (string, error) {
	var o struct {
		URL string `json:"url"`
		ID  any    `json:"id"`
	}
	if strings.TrimSpace(optsJSON) != "" {
		if err := json.Unmarshal([]byte(optsJSON), &o); err != nil {
			return "", fmt.Errorf("hackernews:item opts must be JSON {id|url}: %w", err)
		}
	}
	if strings.HasPrefix(o.URL, "http") {
		return o.URL, nil
	}
	id := ""
	switch v := o.ID.(type) {
	case string:
		id = v
	case float64:
		id = fmt.Sprintf("%.0f", v)
	}
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return "", fmt.Errorf("hackernews:item needs {\"id\":<digits>} or {\"url\":\"https://…\"} — got %q", optsJSON)
	}
	return "https://news.ycombinator.com/item?id=" + id, nil
}

const hnTopJS = `
const n = Math.max(1, Math.min(500, parseInt(__opts.n || __opts.count || 30, 10) || 30));
const rows=[...document.querySelectorAll('tr.athing.submission')].slice(0,n).map(tr=>{
  const a=tr.querySelector('.titleline a');
  const sub=tr.nextElementSibling;
  const txt=sub?sub.innerText:'';
  const pts=txt.match(/(\d+)\s+points?/), cmt=txt.match(/(\d+)\s+comments?/);
  return {id:tr.id,title:a?a.textContent.trim():null,url:a?a.href:null,
          by:sub&&sub.querySelector('.hnuser')?sub.querySelector('.hnuser').textContent.trim():null,
          points:pts?parseInt(pts[1],10):null,comments:cmt?parseInt(cmt[1],10):0,
          item:'https://news.ycombinator.com/item?id='+tr.id};
});
return {site:'hackernews',recipe:'top',url:location.href,capturedAt:new Date().toISOString(),
        postCount:rows.length,posts:rows};`

const hnItemJS = `
const q=(s,r)=>(r||document).querySelector(s);
const title=q(".titleline a")?q(".titleline a").textContent.trim():(q(".title")?q(".title").textContent.trim():null);
const sub=q(".subtext")?q(".subtext").textContent.trim():"";
const head=document.body.innerText.slice(0,400);
// A dead or flagged item still answers 200; the only evidence is in the page text.
const flagged=/\[flagged\]/i.test(head), dead=/\[dead\]/i.test(head);
const comments=[...document.querySelectorAll("tr.comtr")].map(tr=>{
  const ind=tr.querySelector("td.ind img");
  const body=tr.querySelector(".commtext");
  return {
    depth: ind?Math.round(parseInt(ind.getAttribute("width")||"0",10)/40):0,
    author: tr.querySelector(".hnuser")?tr.querySelector(".hnuser").textContent.trim():null,
    age: tr.querySelector(".age")?tr.querySelector(".age").textContent.trim():null,
    dead: !!tr.querySelector(".comhead .dead") || (body?/^\s*\[(dead|flagged)\]/i.test(body.innerText):false),
    text: body?body.innerText.trim().slice(0,2000):null,
  };
});
const pts=sub.match(/(\d+)\s+points?/);
return {site:"hackernews",recipe:"item",url:location.href,capturedAt:new Date().toISOString(),
        title,points:pts?parseInt(pts[1],10):null,flagged,dead,
        commentCount:comments.length,comments};`
