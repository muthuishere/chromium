package learned

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox gives the test its own HOME and its own playbook tree. The real playbooks are canon; a
// test that appends to them writes fiction into the thing this package exists to keep honest.
func sandbox(t *testing.T) (cfg, playbooks string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CHROME_AGENT_PROFILE", "")
	playbooks = filepath.Join(t.TempDir(), "playbooks")
	t.Setenv("CHROME_AGENT_PLAYBOOKS", playbooks)
	return filepath.Join(home, ".config", "chrome-agent"), playbooks
}

func writeTraps(t *testing.T, playbooks, domain, body string) string {
	t.Helper()
	dir := filepath.Join(playbooks, domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "traps.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeDrift(t *testing.T, cfg string, lines ...string) {
	t.Helper()
	os.MkdirAll(cfg, 0o755)
	if err := os.WriteFile(filepath.Join(cfg, "drift.ndjson"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoteAppendsToTheDomainsFile(t *testing.T) {
	cfg, _ := sandbox(t)

	res, err := Note("https://www.LinkedIn.com/feed/", "  the comment API returns 500 and creates the comment anyway  ")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfg, "learned", "linkedin.com.ndjson")
	if res.File != want {
		t.Errorf("file = %q, want %q", res.File, want)
	}
	if res.Noted != "linkedin.com" {
		t.Errorf("domain not normalized: %q", res.Noted)
	}
	if _, err := Note("linkedin.com", "HN serves 200 on a flagged post"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 appended lines, got %d", len(lines))
	}
	var r Record
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatalf("note line does not parse: %v", err)
	}
	if r.Domain != "linkedin.com" || r.Promoted || !strings.HasPrefix(r.Text, "the comment API") || strings.HasSuffix(r.Text, " ") {
		t.Errorf("record = %+v", r)
	}
}

func TestNoteRejectsEmptyInput(t *testing.T) {
	sandbox(t)
	if _, err := Note("", "x"); err == nil {
		t.Error("empty domain accepted")
	}
	if _, err := Note("x.com", "   "); err == nil {
		t.Error("empty text accepted")
	}
}

func TestPendingSkipsWhatTrapsAlreadySays(t *testing.T) {
	_, pb := sandbox(t)
	writeTraps(t, pb, "linkedin.com", "# traps\n\n- The comment API returns HTTP 500 while creating the comment anyway; read the thread back.\n")

	// Same knowledge, different punctuation and wrapping — must NOT be offered again.
	Note("linkedin.com", "the comment API returns HTTP 500, while creating the comment anyway!")
	// Genuinely new.
	Note("linkedin.com", "strict CSP kills evalAsync on the feed; use evalwithcsp")

	items := Pending("linkedin.com", SourceAll)
	if len(items) != 1 {
		t.Fatalf("want 1 pending item, got %d: %+v", len(items), items)
	}
	if !strings.Contains(items[0].Text, "evalwithcsp") {
		t.Errorf("wrong item pending: %+v", items[0])
	}
	if items[0].Line != 1 {
		t.Errorf("line index = %d, want 1 (the second line of the file)", items[0].Line)
	}
}

func TestPromoteAppendsBelowTheKeepMarkerAndIsIdempotent(t *testing.T) {
	_, pb := sandbox(t)
	generated := "# traps — linkedin.com\n\n- generated: never trust a 200.\n\n" + KeepMarker + "\n- an older hand-written trap\n"
	tp := writeTraps(t, pb, "linkedin.com", generated)
	Note("linkedin.com", "strict CSP kills evalAsync; use evalwithcsp")

	rep, err := Promote(Opts{Domain: "linkedin.com", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pending != 1 || len(rep.Domains["linkedin.com"].Applied) != 1 {
		t.Fatalf("report = %+v", rep.Domains["linkedin.com"])
	}
	got, _ := os.ReadFile(tp)
	head, tail, found := strings.Cut(string(got), KeepMarker)
	if !found {
		t.Fatal("the keep-marker is gone")
	}
	if head != strings.SplitN(generated, KeepMarker, 2)[0] {
		t.Errorf("promotion rewrote canon ABOVE the marker:\n%q", head)
	}
	if !strings.Contains(tail, "an older hand-written trap") {
		t.Error("promotion destroyed an existing hand-written line")
	}
	if !strings.Contains(tail, "evalwithcsp") || !strings.Contains(tail, "from note") {
		t.Errorf("the note was not appended below the marker: %q", tail)
	}

	// Promoting twice is a no-op: the note is marked promoted AND the text is now in traps.md.
	before, _ := os.ReadFile(tp)
	rep2, err := Promote(Opts{Domain: "linkedin.com", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Pending != 0 {
		t.Errorf("second promote still offers %d item(s): %+v", rep2.Pending, rep2.Domains)
	}
	after, _ := os.ReadFile(tp)
	if string(before) != string(after) {
		t.Errorf("second promote changed the file:\n%q", string(after))
	}
	if strings.Count(string(after), "evalwithcsp") != 1 {
		t.Error("the trap was appended twice")
	}

	// ...and the note is flagged, not just deduped by text.
	b, _ := os.ReadFile(NotePath("linkedin.com"))
	var r Record
	json.Unmarshal([]byte(strings.TrimSpace(string(b))), &r)
	if !r.Promoted {
		t.Error("the promoted note was not marked promoted")
	}
}

func TestPromoteCreatesTheKeepMarkerWhenTheFileHasNone(t *testing.T) {
	_, pb := sandbox(t)
	tp := writeTraps(t, pb, "x.com", "# traps — x.com\n\n- generated line\n")
	Note("x.com", "a repost needs the tweet url")

	if _, err := Promote(Opts{Domain: "x.com", Apply: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(tp)
	if !strings.Contains(string(got), KeepMarker) {
		t.Fatal("no keep-marker was created")
	}
	_, tail, _ := strings.Cut(string(got), KeepMarker)
	if !strings.Contains(tail, "tweet url") {
		t.Errorf("the note did not land below the new marker: %q", tail)
	}
}

// A drift line is a FAILURE REPORT, not a trap. It must be visible to the review and must not be
// written into canon unless someone asks for it by name.
func TestDriftIsOfferedButNotPromoted(t *testing.T) {
	cfg, pb := sandbox(t)
	tp := writeTraps(t, pb, "x.com", "# traps\n\n"+KeepMarker+"\n")
	writeDrift(t, cfg,
		`{"ts":"2026-09-10T00:00:00Z","key":"x:post","why":"post-url required"}`,
		`{"ts":"2026-09-10T00:01:00Z","key":"x:post","why":"post-url required"}`, // a retry: one entry, not two
		`{"ts":"2026-09-10T00:02:00Z","key":"linkedin:feed","why":"selector gone"}`)
	Note("x.com", "a repost needs the tweet url")

	items := Pending("x.com", SourceAll)
	if len(items) != 2 {
		t.Fatalf("want note + one deduped drift, got %+v", items)
	}
	if only := Pending("x.com", SourceDrift); len(only) != 1 || only[0].Source != SourceDrift {
		t.Fatalf("drift for the wrong domain leaked in: %+v", only)
	}

	rep, err := Promote(Opts{Domain: "x.com", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	dr := rep.Domains["x.com"]
	if len(dr.Applied) != 1 || dr.Applied[0].Source != SourceNote {
		t.Errorf("applied = %+v, want the note only", dr.Applied)
	}
	if len(dr.Offered) != 1 || dr.Offered[0].Source != SourceDrift {
		t.Errorf("drift was not offered to the review: %+v", dr.Offered)
	}
	got, _ := os.ReadFile(tp)
	if strings.Contains(string(got), "drifted") {
		t.Errorf("a drift line was written into canon:\n%s", got)
	}

	// Asked for by name, it goes in.
	if _, err := Promote(Opts{Domain: "x.com", Apply: true, IncludeDrift: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(tp)
	if !strings.Contains(string(got), "`x:post` drifted") {
		t.Errorf("--include-drift did not promote it:\n%s", got)
	}
}

func TestPromoteWithoutApplyChangesNothing(t *testing.T) {
	_, pb := sandbox(t)
	tp := writeTraps(t, pb, "reddit.com", "# traps\n\n"+KeepMarker+"\n")
	Note("reddit.com", "a subreddit ban shows as a 200 with an empty listing")
	before, _ := os.ReadFile(tp)

	rep, err := Promote(Opts{Domain: "reddit.com"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pending != 1 || rep.Applied {
		t.Errorf("report = %+v", rep)
	}
	after, _ := os.ReadFile(tp)
	if string(before) != string(after) {
		t.Error("a dry review edited canon")
	}
}

func TestPromoteAcrossEveryPlaybookWhenGivenNoDomain(t *testing.T) {
	_, pb := sandbox(t)
	writeTraps(t, pb, "x.com", "# x\n")
	writeTraps(t, pb, "reddit.com", "# reddit\n")
	Note("x.com", "x specific thing")
	Note("reddit.com", "reddit specific thing")
	Note("news.ycombinator.com", "a domain with no playbook is not reviewed")

	rep, err := Promote(Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pending != 2 || len(rep.Domains) != 2 {
		t.Fatalf("want both playbook domains, got %+v", rep.Domains)
	}
	if _, ok := rep.Domains["news.ycombinator.com"]; ok {
		t.Error("reviewed a domain that has no playbook")
	}
}

func TestApplyReportsAMissingPlaybookInsteadOfCreatingOne(t *testing.T) {
	_, pb := sandbox(t)
	writeTraps(t, pb, "x.com", "# x\n")
	os.Remove(filepath.Join(pb, "x.com", "traps.md"))
	Note("x.com", "something learned")

	rep, err := Promote(Opts{Domain: "x.com", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Domains["x.com"].Error == "" {
		t.Error("a missing traps.md was not reported")
	}
	if _, err := os.Stat(filepath.Join(pb, "x.com", "traps.md")); err == nil {
		t.Error("promote invented a playbook file")
	}
}
