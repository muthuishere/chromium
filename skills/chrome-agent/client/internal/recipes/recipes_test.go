package recipes

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The verb list is a CONTRACT, not a convenience. apl's playbook generator consumes it, and before
// it existed that generator regex-parsed browser-research's registry.js across a repo boundary and
// inferred "is this a write?" from the FILE a key lived in. This table is the exact output of
// `node recipes-list.mjs --json` on the day the Go port landed — 48 verbs, 20 of them writes. If the
// Go path ever drops a verb (a registry file it cannot parse) or mislabels a write, that is a live
// post going out under a read's expectations, so it fails HERE instead.
var goldenList = []struct {
	key, world, source string
	write              bool
}{
	{"capture:arm", "MAIN", "registry", false},
	{"capture:clear", "MAIN", "registry", false},
	{"capture:dump", "MAIN", "registry", false},
	{"chatgpt:conversation", "ISOLATED", "registry", false},
	{"chatgpt:list", "ISOLATED", "registry", false},
	{"compose:fill", "MAIN", "registry", true},
	{"facebook:feed", "ISOLATED", "registry", false},
	{"generic:article", "ISOLATED", "registry", false},
	{"generic:detect-captcha", "ISOLATED", "registry", false},
	{"generic:images", "ISOLATED", "registry", false},
	{"generic:links", "ISOLATED", "registry", false},
	{"generic:meta", "ISOLATED", "registry", false},
	{"generic:page-text", "ISOLATED", "registry", false},
	{"gsc:current-urls", "ISOLATED", "registry", false},
	{"gsc:drilldown", "ISOLATED", "registry", false},
	{"hackernews:comment", "MAIN", "registry", true},
	{"hackernews:item", "main", "chrome-agent", false},
	{"hackernews:post", "MAIN", "registry", true},
	{"hackernews:top", "main", "chrome-agent", false},
	{"instagram:post", "ISOLATED", "registry", false},
	{"instagram:profile", "ISOLATED", "registry", false},
	{"linkedin:comment", "MAIN", "registry", true},
	{"linkedin:comment-delete", "MAIN", "registry", true},
	{"linkedin:delete-post", "MAIN", "registry", true},
	{"linkedin:feed", "MAIN", "registry", false},
	{"linkedin:like", "main", "chrome-agent", true},
	{"linkedin:my-posts", "ISOLATED", "registry", false},
	{"linkedin:notifications", "MAIN", "registry", false},
	{"linkedin:post", "MAIN", "registry", true},
	{"linkedin:post-image", "MAIN", "registry", true},
	{"reddit:comment", "MAIN", "registry", true},
	{"reddit:delete-post", "MAIN", "registry", true},
	{"reddit:listing", "MAIN", "registry", false},
	{"reddit:post", "MAIN", "registry", true},
	{"reddit:post-oauth", "MAIN", "registry", true},
	{"reddit:submit", "MAIN", "registry", true},
	{"reddit:upvote", "main", "chrome-agent", true},
	{"search:bing", "ISOLATED", "registry", false},
	{"search:duckduckgo", "ISOLATED", "registry", false},
	{"search:google", "ISOLATED", "registry", false},
	{"x:delete-post", "MAIN", "registry", true},
	{"x:like", "main", "chrome-agent", true},
	{"x:post", "MAIN", "registry", true},
	{"x:reply", "MAIN", "registry", true},
	{"x:repost", "main", "chrome-agent", true},
	{"x:timeline", "ISOLATED", "registry", false},
	{"x:timeline-twitter", "ISOLATED", "registry", false},
	{"youtube:channel-videos", "ISOLATED", "registry", false},
}

// embeddedOnly forces resolution down to the compiled-in copy: no override env, and a HOME with no
// ~/.config/chrome-agent/recipes. This is the "fresh machine, one binary, no repo" case from ADR
// 0010's acceptance list, and it is the one that used to report 6 verbs instead of 48.
func embeddedOnly(t *testing.T) {
	t.Helper()
	t.Setenv("CHROME_AGENT_RECIPES", "")
	t.Setenv("BR_REGISTRY", "")
	t.Setenv("HOME", t.TempDir())
}

func TestListMatchesTheNodeBaseline(t *testing.T) {
	embeddedOnly(t)
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(goldenList) {
		var keys []string
		for _, r := range got {
			keys = append(keys, r.Key)
		}
		t.Fatalf("List() returned %d verbs, want %d: %s", len(got), len(goldenList), strings.Join(keys, " "))
	}
	writes := 0
	for i, want := range goldenList {
		g := got[i]
		if g.Key != want.key || g.World != want.world || g.Source != want.source || g.Write != want.write {
			t.Errorf("verb %d: got {%s %s %s %v}, want {%s %s %s %v}",
				i, g.Key, g.World, g.Source, g.Write, want.key, want.world, want.source, want.write)
		}
		if g.Write {
			writes++
		}
		if g.Describe == "" {
			t.Errorf("%s has no describe — the list is what a generator reads to write a playbook", g.Key)
		}
		if g.Site == "" || g.Verb == "" || g.CLI == "" {
			t.Errorf("%s: site/verb/cli must all be populated, got %+v", g.Key, g)
		}
	}
	if writes != 20 {
		t.Errorf("%d writes, want 20 — a write mislabelled as a read is a post going out unstaged", writes)
	}
}

func TestListIsSortedAndUnique(t *testing.T) {
	embeddedOnly(t)
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i, r := range got {
		if seen[r.Key] {
			t.Errorf("duplicate key %s", r.Key)
		}
		seen[r.Key] = true
		if i > 0 && got[i-1].Key >= r.Key {
			t.Errorf("not sorted at %d: %s then %s", i, got[i-1].Key, r.Key)
		}
	}
}

// Every registry recipe must yield a function SOURCE, because that source is the whole payload.
// An entry that parses into metadata with an empty fn is the silent failure this port could have:
// `recipes` looks healthy, `recipe <key>` evaluates "const __fn=();" and the page throws.
func TestEveryRegistryRecipeHasRunnableSource(t *testing.T) {
	embeddedOnly(t)
	reg, _, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg) != 42 {
		t.Fatalf("registry has %d recipes, want 42", len(reg))
	}
	for key, r := range reg {
		if r.Fn == "" {
			t.Errorf("%s: no fn source extracted", key)
			continue
		}
		if !strings.HasSuffix(strings.TrimSpace(r.Fn), "}") {
			t.Errorf("%s: fn source does not end at a closing brace — it was truncated:\n…%s",
				key, tail(r.Fn, 120))
		}
		if !strings.Contains(r.Fn, "function") && !strings.Contains(r.Fn, "=>") {
			t.Errorf("%s: fn source is not a function: %q", key, head(r.Fn, 80))
		}
		if matchBrace(r.Fn, strings.IndexByte(r.Fn, '{')) != len(r.Fn)-1 {
			t.Errorf("%s: fn source is not brace-balanced end to end", key)
		}
	}
}

// The extraction must be VERBATIM. node produced the payload with Function.prototype.toString(),
// so anything this scanner normalises (a re-indent, a dropped comment) is a behaviour change hiding
// in a formatting change.
func TestFnSourceIsVerbatim(t *testing.T) {
	embeddedOnly(t)
	reg, _, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	src, err := readEmbedded("linkedin.js")
	if err != nil {
		t.Fatal(err)
	}
	fn := reg["linkedin:feed"].Fn
	if !strings.HasPrefix(fn, "async function fetchHomeFeed(") {
		t.Fatalf("linkedin:feed fn starts %q", head(fn, 60))
	}
	if !strings.Contains(src, fn) {
		t.Error("the extracted source is not a verbatim substring of linkedin.js")
	}
}

func readEmbedded(name string) (string, error) {
	b, err := (tree{"embedded", ""}).read(name)
	return string(b), err
}

// Payload is the wire format recipe-run.mjs printed. Keep it byte-for-byte: the fork runs it as an
// async function BODY, so it must use await and return, and an absent opts must be the literal
// `undefined` and never an empty argument list.
func TestPayloadShape(t *testing.T) {
	embeddedOnly(t)
	r, err := Resolve("reddit:listing")
	if err != nil {
		t.Fatal(err)
	}
	p := Payload(r, "")
	if !strings.HasPrefix(p, "const __fn=(") {
		t.Errorf("payload starts %q", head(p, 40))
	}
	if !strings.Contains(p, "const __r=await __fn(undefined);") {
		t.Error("an omitted opts must become the literal `undefined`")
	}
	if !strings.HasSuffix(p, "return (__r===undefined?{ok:true}:__r);") {
		t.Errorf("payload ends %q", tail(p, 60))
	}
	if p2 := Payload(r, `{"sub":"golang"}`); !strings.Contains(p2, `await __fn({"sub":"golang"})`) {
		t.Error("opts JSON must be interpolated as the call argument")
	}
}

// A chrome-agent builtin with no Go implementation must REFUSE, loudly, naming the bash verb. The
// alternative — evaluating an empty body — returns {ok:true} and reads as a successful like.
func TestUnimplementedBuiltinRefuses(t *testing.T) {
	embeddedOnly(t)
	r, err := Resolve("linkedin:like")
	if err != nil {
		t.Fatal(err)
	}
	if r.Fn != "" {
		t.Fatal("linkedin:like has a Go implementation now — delete this test and add a real one")
	}
	if _, err := Run(nil, "linkedin:like", "", Options{NoOrigin: true}); err == nil {
		t.Fatal("Run must refuse a builtin with no Go implementation")
	} else if !strings.Contains(err.Error(), "chrome-agent linkedin like") {
		t.Errorf("the refusal must name the bash verb to use, got: %v", err)
	}
}

// EnsureOrigin encodes a 44-hour incident and three separate "never navigate" rules. This is the
// table it must obey.
func TestOriginRules(t *testing.T) {
	cases := []struct {
		key      string
		navigate bool
		url      string
		path     string
	}{
		// The tab is shared mutable state; a credentialed recipe needs the right origin.
		{"linkedin:feed", true, "https://www.linkedin.com/feed/", "/feed"},
		{"linkedin:notifications", true, "https://www.linkedin.com/feed/", "/feed"},
		// Origin is NOT enough: from /feed/ this scrape returns postCount:0.
		{"linkedin:my-posts", true, "https://www.linkedin.com/in/me/recent-activity/all/", "recent-activity"},
		{"x:timeline", true, "https://x.com/home", ""},
		{"reddit:listing", true, "https://www.reddit.com/", ""},
		{"hackernews:top", true, "https://news.ycombinator.com/", ""},
		// Operate on WHATEVER page you are on.
		{"generic:page-text", false, "", ""},
		{"capture:dump", false, "", ""},
		{"compose:fill", false, "", ""},
		// "Open the post's permalink first" — the CALLER chose the page.
		{"linkedin:comment", false, "", ""},
		{"linkedin:comment-delete", false, "", ""},
		{"reddit:comment", false, "", ""},
		{"hackernews:comment", false, "", ""},
		// Nothing is known about this site: never move the tab on a guess.
		{"chatgpt:list", false, "", ""},
		{"youtube:channel-videos", false, "", ""},
	}
	for _, c := range cases {
		got := originFor(c.key)
		if got.navigate != c.navigate || got.url != c.url || got.path != c.path {
			t.Errorf("%s: got %+v, want navigate=%v url=%q path=%q", c.key, got, c.navigate, c.url, c.path)
		}
	}
}

// Matching the HOST alone is what made linkedin:feed time out from /in/me/recent-activity.
func TestOriginSatisfiedNeedsThePathToo(t *testing.T) {
	feed := originFor("linkedin:feed")
	if OriginSatisfied("https://www.linkedin.com/in/me/recent-activity/all/", feed) {
		t.Error("the linkedin host alone must NOT satisfy a voyager recipe — it needs /feed/")
	}
	if !OriginSatisfied("https://www.linkedin.com/feed/", feed) {
		t.Error("/feed/ must satisfy it")
	}
	if OriginSatisfied("https://openrouter.ai/", feed) {
		t.Error("the wrong site must never satisfy it — this is the 44-hour bug")
	}
	mine := originFor("linkedin:my-posts")
	if OriginSatisfied("https://www.linkedin.com/feed/", mine) {
		t.Error("/feed/ must NOT satisfy my-posts — from there it returns postCount:0")
	}
	if !OriginSatisfied("https://www.linkedin.com/in/me/recent-activity/all/", mine) {
		t.Error("recent-activity must satisfy my-posts")
	}
	if !OriginSatisfied("about:blank", originFor("generic:page-text")) {
		t.Error("a never-navigate verb is satisfied anywhere, including about:blank")
	}
}

// Three ways a read fails, and all three used to read as SUCCESS.
func TestVerifyRejectsTheThreeFalsePasses(t *testing.T) {
	obj := func(s string) any {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		name, domain, res, want string
	}{
		{"an error", "linkedin.com", `{"error":"no JSESSIONID - not logged in?"}`, "JSESSIONID"},
		// verify instagram.com once scraped REDDIT, found images, and stamped instagram verified.
		{"the wrong site", "instagram.com",
			`{"url":"https://www.reddit.com/","images":["a","b"]}`, "wrong site"},
		// A logged-out facebook feed returns postCount:0 with no error at all.
		{"an empty read", "facebook.com", `{"url":"https://www.facebook.com/","posts":[]}`, "0 items"},
		{"not an object", "x.com", `"just a string"`, "no object"},
	}
	for _, c := range cases {
		if why := rejectRead(obj(c.res), c.domain); !strings.Contains(why, c.want) {
			t.Errorf("%s: rejectRead = %q, want it to mention %q", c.name, why, c.want)
		}
	}
	ok := obj(`{"url":"https://www.reddit.com/r/golang/","posts":[{"t":"a"}]}`)
	if why := rejectRead(ok, "reddit.com"); why != "" {
		t.Errorf("a real read must pass, got %q", why)
	}
	// A subdomain of the target is still the target: www.reddit.com verifies reddit.com.
	if why := rejectRead(obj(`{"url":"https://old.reddit.com/","posts":[{"t":"a"}]}`), "reddit.com"); why != "" {
		t.Errorf("a subdomain must pass, got %q", why)
	}
	// No lists at all is not an empty read — a meta/article scrape returns scalars only.
	if why := rejectRead(obj(`{"url":"https://x.com/home","title":"t"}`), "x.com"); why != "" {
		t.Errorf("a list-free result must pass, got %q", why)
	}
}

// A read verb needs the right PAGE, not just the right origin. Every remaining "0 items" was this.
func TestPlanForPicksTheFixturePage(t *testing.T) {
	embeddedOnly(t)
	for _, c := range []struct{ domain, key, urlPart string }{
		{"linkedin.com", "linkedin:feed", "linkedin.com"},
		{"news.ycombinator.com", "hackernews:top", "news.ycombinator.com"},
		{"reddit.com", "reddit:listing", "reddit.com"},
	} {
		p := PlanFor(c.domain)
		if p.Key != c.key {
			t.Errorf("%s: plan key %q, want %q", c.domain, p.Key, c.key)
		}
		if !strings.Contains(p.URL, c.urlPart) {
			t.Errorf("%s: plan url %q must be on %s", c.domain, p.URL, c.urlPart)
		}
	}
	// A chrome-agent verb is not a registry recipe, and its cli string is a USAGE TEMPLATE. A
	// literal "[n]" once reached the page as JS and threw ReferenceError: n.
	p := PlanFor("news.ycombinator.com")
	if strings.ContainsAny(p.CLI, "[]<>") {
		t.Errorf("placeholders must be stripped from the cli template, got %q", p.CLI)
	}
	// A domain nobody has a read recipe for must answer "no recipe", not a recipe for another site.
	if p := PlanFor("stackoverflow.com"); p.Key != "" {
		t.Errorf("stackoverflow.com has no read verb; PlanFor picked %q", p.Key)
	}
}

// ---- the scanner itself: these are the inputs that break a naive brace counter ----------------

func TestScannerIsNotFooledByLiterals(t *testing.T) {
	cases := []struct{ name, src string }{
		{"a brace in a string", `{ a: "}", b: 1 }`},
		{"a brace in a comment", "{ a: 1, /* } */ b: 2 }"},
		{"a brace in a line comment", "{ a: 1, // }\n b: 2 }"},
		{"a regex holding a brace", `{ a: /[}]{1,2}/g, b: 2 }`},
		{"a template literal", "{ a: `x ${ {y:1} } }`, b: 2 }"},
		{"an escaped quote", `{ a: "he said \" }", b: 2 }`},
	}
	for _, c := range cases {
		if got := matchBrace(c.src, 0); got != len(c.src)-1 {
			t.Errorf("%s: matchBrace = %d, want %d (src %s)", c.name, got, len(c.src)-1, c.src)
		}
	}
}

func TestObjectEntriesReadsKeysAndValues(t *testing.T) {
	src := `{
  "a:b": { world: "MAIN", write: true, describe: "one, with a comma", fn: someFn },
  c: 2,
}`
	got := objectEntries(src, 0, matchBrace(src, 0))
	if len(got) != 2 || got[0].key != "a:b" || got[1].key != "c" {
		t.Fatalf("entries = %+v", got)
	}
	inner := objectEntries(src, got[0].valStart, matchBrace(src, got[0].valStart))
	want := map[string]string{"world": `"MAIN"`, "write": "true", "describe": `"one, with a comma"`, "fn": "someFn"}
	for _, e := range inner {
		if want[e.key] != e.val {
			t.Errorf("%s = %q, want %q", e.key, e.val, want[e.key])
		}
	}
}

func TestJSStringDecodes(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{`"plain"`, "plain", true},
		{`'single'`, "single", true},
		{"`tpl`", "tpl", true},
		{`"a\nb"`, "a\nb", true},
		{`"quote\" here"`, `quote" here`, true},
		{`"é"`, "é", true},
		{`SOME_CONST`, "", false},
		{`true`, "", false},
	} {
		got, ok := jsString(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("jsString(%s) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The installed copy must WIN over the embedded one — that rule is why a re-skinned site is a
// one-file fix on a server instead of a redeploy (ADR 0004).
func TestOverrideTreeWins(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(dir+"/"+name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("registry.js", "import { only } from \"./only.js\";\nexport const RECIPES = {\n  ...only,\n};\n")
	write("only.js", "async function readIt(opts) { return { ok: 1 }; }\n"+
		"export const only = {\n  \"only:read\": { world: \"ISOLATED\", match: \"*://*/*\", describe: \"d\", fn: readIt },\n};\n")
	t.Setenv("CHROME_AGENT_RECIPES", dir)
	t.Setenv("HOME", t.TempDir())
	reg, label, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if label != "override" {
		t.Errorf("tree label %q, want override", label)
	}
	if len(reg) != 1 || reg["only:read"] == nil {
		t.Fatalf("override tree did not win: %+v", reg)
	}
	if got := reg["only:read"].Fn; got != "async function readIt(opts) { return { ok: 1 }; }" {
		t.Errorf("fn = %q", got)
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
