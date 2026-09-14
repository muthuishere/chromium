package recipes

import (
	"errors"
	"strings"
	"testing"

	"github.com/deemwarhq/chrome-agent/internal/sites"
)

// Class is what a pacer keys on. A react mislabelled as a read goes out unpaced; a mutate
// mislabelled as a react gets a toggle's lighter pacing for a post.
func TestClassAssignment(t *testing.T) {
	embeddedOnly(t)
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	reacts := map[string]bool{"linkedin:like": true, "x:like": true, "x:repost": true, "reddit:upvote": true}
	counts := map[string]int{}
	for _, r := range all {
		counts[r.Class]++
		want := ClassRead
		switch {
		case reacts[r.Key]:
			want = ClassReact
		case r.Write:
			want = ClassMutate
		}
		if r.Class != want {
			t.Errorf("%s (source %s, write %v): class %q, want %q", r.Key, r.Source, r.Write, r.Class, want)
		}
		// Resolve builds its own Recipe for a builtin; it must agree with List.
		if got, err := Resolve(r.Key); err != nil || got.Class != r.Class {
			t.Errorf("%s: Resolve class %v (err %v) disagrees with List class %q", r.Key, got, err, r.Class)
		}
	}
	// 48 verbs: 4 reacts, 16 registry writes, 28 reads — the 20 writes of the golden list split 4/16.
	if counts[ClassReact] != 4 || counts[ClassMutate] != 16 || counts[ClassRead] != 28 {
		t.Errorf("class counts = %v, want react:4 mutate:16 read:28", counts)
	}
	for k := range reacts {
		if _, ok := IsReact(k); !ok {
			t.Errorf("%s is a react builtin with no React implementation", k)
		}
	}
	if len(reactSpecs) != len(reacts) {
		t.Errorf("%d react specs, %d react builtins — a spec with no listed verb is unreachable", len(reactSpecs), len(reacts))
	}
}

func TestUserRecipeClass(t *testing.T) {
	if classFor(true) != ClassMutate || classFor(false) != ClassRead {
		t.Error("a registry/user write must be mutate and a non-write read")
	}
}

// The toggle trap, as a table. The one row that matters most: confirmed + already on must NEVER
// click, because that click is an unlike.
func TestDecide(t *testing.T) {
	cases := []struct {
		before  string
		confirm bool
		want    decision
	}{
		{stateOff, false, decideStaged},
		{stateOn, false, decideStaged},
		{stateOff, true, decideClick},
		{stateOn, true, decideAlready}, // clicking here would UNLIKE
		{stateNone, false, decideMissing},
		{stateNone, true, decideMissing},
		{"", true, decideMissing},
		{"liked", true, decideMissing}, // a drifted probe inventing a state is not "off"
	}
	for _, c := range cases {
		if got := decide(c.before, c.confirm); got != c.want {
			t.Errorf("decide(%q, confirm=%v) = %d, want %d", c.before, c.confirm, got, c.want)
		}
	}
}

// A click whose state did not flip must not be ok.
func TestVerifiedOnlyOnTheTargetState(t *testing.T) {
	if !verified(stateOn) {
		t.Error("on must verify")
	}
	for _, s := range []string{stateOff, stateNone, ""} {
		if verified(s) {
			t.Errorf("after=%q must not verify", s)
		}
	}
}

func TestReactTarget(t *testing.T) {
	li, x, rd := reactSpecs["linkedin:like"], reactSpecs["x:like"], reactSpecs["reddit:upvote"]
	if u, err := reactTarget(li, ""); err != nil || u != "https://www.linkedin.com/feed/" {
		t.Errorf("linkedin:like with no url must act on the feed, got %q %v", u, err)
	}
	for _, s := range []reactSpec{x, reactSpecs["x:repost"], rd} {
		if _, err := reactTarget(s, ""); !errors.Is(err, ErrUsage) {
			t.Errorf("%s with no url must be a usage error, got %v", s.key, err)
		}
	}
	good := []struct {
		s reactSpec
		u string
	}{
		{li, "https://www.linkedin.com/feed/update/urn:li:activity:1/"},
		{x, "https://x.com/someone/status/123"},
		{x, "https://twitter.com/someone/status/123"},
		{rd, "https://old.reddit.com/r/golang/comments/abc/t/"},
	}
	for _, g := range good {
		if u, err := reactTarget(g.s, g.u); err != nil || u != g.u {
			t.Errorf("%s %s: got %q %v", g.s.key, g.u, u, err)
		}
	}
	bad := []struct {
		s reactSpec
		u string
	}{
		{x, "https://www.reddit.com/r/x/status/1"},   // wrong site
		{rd, "https://notreddit.com/r/golang/"},      // suffix without the dot
		{li, "linkedin.com/feed"},                    // no scheme
		{li, "javascript:alert(1)//linkedin.com"},    // not http(s)
		{x, "https://x.com.evil.example/status/123"}, // host prefix, not the site
	}
	for _, b := range bad {
		if _, err := reactTarget(b.s, b.u); !errors.Is(err, ErrUsage) {
			t.Errorf("%s must refuse %q, got %v", b.s.key, b.u, err)
		}
	}
}

// The selectors are the bash ones, byte for byte — a port is not the place to also change what a
// click lands on.
func TestReactSelectorsMatchTheBash(t *testing.T) {
	for key, sels := range map[string][]string{
		"linkedin:like": {`button[aria-label^="Reaction button state"]`},
		"x:like":        {`[data-testid="like"]`, `[data-testid="unlike"]`},
		"x:repost":      {`[data-testid="retweet"]`, `[data-testid="unretweet"]`, `[data-testid="retweetConfirm"]`},
		"reddit:upvote": {`button[aria-label*="upvote" i],button[upvote]`, `aria-pressed`},
	} {
		s := reactSpecs[key]
		src := s.find + s.confirmJS
		for _, sel := range sels {
			if !strings.Contains(src, sel) {
				t.Errorf("%s: selector %s missing", key, sel)
			}
		}
		if strings.Contains(s.find, ".click(") {
			t.Errorf("%s: the finder must never click — it runs in the staged read too", key)
		}
	}
}

// A react verb that is not declared as a write on its site is invisible to every consumer that
// reads the site file to learn what can be done there.
func TestReactVerbsAreDeclaredSiteWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CHROME_AGENT_SITES", t.TempDir())
	for key, s := range reactSpecs {
		def, err := sites.Load(s.domain)
		if err != nil || def == nil {
			t.Fatalf("%s: no site definition for %s: %v", key, s.domain, err)
		}
		found := false
		for _, w := range def.Write {
			if w == key {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not in %s's write list %v", key, s.domain, def.Write)
		}
	}
}
