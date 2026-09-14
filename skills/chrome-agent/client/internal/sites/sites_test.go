package sites

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedDefinitionsAreShippedAndValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // test what SHIPS, not what this laptop has installed
	t.Setenv("CHROME_AGENT_SITES", t.TempDir())
	all := All()
	if len(all) < 19 {
		t.Fatalf("expected the shipped site set, got %d", len(all))
	}
	for _, d := range all {
		if errs := Problems(d); len(errs) > 0 {
			t.Errorf("%s is not usable: %v", d.Domain, errs)
		}
	}
}

// The resolution order IS the design (ADR 0004): a file in the installed dir must beat the one
// compiled into the binary, or a hotfix on a server does nothing.
func TestInstalledCopyBeatsEmbedded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHROME_AGENT_SITES", dir)
	os.WriteFile(filepath.Join(dir, "linkedin.com.json"), []byte(`{
      "domain":"linkedin.com","home":"https://example.invalid/",
      "login":{"url":"https://example.invalid/"},
      "auth":{"probe_js":"return {signed_in:false, why:'OVERRIDE'};"},
      "status":"unverified"}`), 0o644)

	d, err := Load("linkedin.com")
	if err != nil || d == nil {
		t.Fatalf("load: %v", err)
	}
	if d.From != "override" || !strings.Contains(d.Auth.ProbeJS, "OVERRIDE") {
		t.Fatalf("embedded copy won over the override: from=%s", d.From)
	}
	// And with BOTH the override and the installed dir empty, the embedded one must come back.
	// HOME has to move too: this machine has a real installed copy, and a test that reads it is
	// testing the developer's laptop rather than the resolver.
	t.Setenv("CHROME_AGENT_SITES", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	d, _ = Load("linkedin.com")
	if d == nil || d.From != "embedded" {
		t.Fatalf("embedded definition did not come back: from=%v", d.From)
	}
}

func TestNormalizeAcceptsWhatHumansType(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.linkedin.com/feed/": "linkedin.com",
		"WWW.X.COM":                      "x.com",
		"reddit.com/r/golang":            "reddit.com",
		" news.ycombinator.com ":         "news.ycombinator.com",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every rule in Problems() exists because a probe that breaks it lies about a live session.
func TestProblemsCatchesTheProbeRules(t *testing.T) {
	base := func(js string) *Definition {
		d := &Definition{Domain: "example.com", Home: "https://example.com/", Status: "unverified"}
		d.Auth.ProbeJS = js
		return d
	}
	cases := map[string]string{
		"":                                 "empty",
		"const x = 1;":                     "never returns",
		"location.href = '/x'; return {};": "must not navigate",
		"document.querySelector('a').click(); return {};": "must not click",
		"return document.cookie.length > 0;":              "document.cookie without try/catch",
	}
	for js, why := range cases {
		if errs := Problems(base(js)); len(errs) == 0 {
			t.Errorf("probe %q should have been rejected (%s)", js, why)
		}
	}
	good := base("const c = (() => { try { return document.cookie; } catch (e) { return ''; } })(); return {signed_in: c.includes('x')};")
	if errs := Problems(good); len(errs) != 0 {
		t.Errorf("a well-formed probe was rejected: %v", errs)
	}
}

func TestSyncKeepsALocallyEditedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Sync(false, false); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	target := filepath.Join(home, ".config", "chrome-agent", "sites", "x.com.json")
	if err := os.WriteFile(target, []byte(`{"domain":"x.com","home":"h","auth":{"probe_js":"return {}"},"status":"unverified"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(false, false)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(res.Kept) != 1 || res.Kept[0] != "x.com.json" {
		t.Fatalf("a locally edited file was not kept: %+v", res.Kept)
	}
	res, _ = Sync(true, false)
	if len(res.Replaced) != 1 {
		t.Fatalf("--force did not replace the edited file: %+v", res.Replaced)
	}
}

// An old shipped copy nobody edited must be UPGRADED, not kept. Without the record, a fixed probe in
// a new binary never reached any machine that had synced before (the YouTube "Avatar image" bug).
func TestSyncUpgradesAnUneditedOldCopyButKeepsAnEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Sync(false, false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "chrome-agent", "sites")
	old := []byte(`{"domain":"x.com","home":"old","auth":{"probe_js":"return {}"},"status":"unverified"}`)
	os.WriteFile(filepath.Join(dir, "x.com.json"), old, 0o644)
	// As if an OLDER binary's sync had written that content.
	rec := map[string]string{}
	b, _ := os.ReadFile(filepath.Join(dir, ShippedRecord))
	json.Unmarshal(b, &rec)
	rec["x.com.json"] = sha(old)
	b, _ = json.Marshal(rec)
	os.WriteFile(filepath.Join(dir, ShippedRecord), b, 0o644)
	// And an operator edit on another file, which the record does not vouch for.
	os.WriteFile(filepath.Join(dir, "reddit.com.json"), []byte(`{"edited":true}`), 0o644)

	res, err := Sync(false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Upgraded) != 1 || res.Upgraded[0] != "x.com.json" {
		t.Fatalf("unedited old copy not upgraded: %+v", res)
	}
	if len(res.Kept) != 1 || res.Kept[0] != "reddit.com.json" {
		t.Fatalf("operator edit not kept: %+v", res)
	}
}
