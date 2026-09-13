package ledger

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox puts the ledger in a temp dir. The real one is the operator's audit trail; a test must
// never append to it, and must certainly never rotate it.
func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CHROME_AGENT_PROFILE", filepath.Join(home, "chrome-agent-profile"))
	p := filepath.Join(home, ".config", "chrome-agent", "actions.ndjson")
	t.Setenv("CHROME_AGENT_LEDGER", p)
	t.Setenv("CHROME_AGENT_LEDGER_MAX", "")
	t.Setenv("CHROME_AGENT_LEDGER_KEEP", "")
	return p
}

func read(t *testing.T, p string) []Entry {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var out []Entry
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unparseable ledger line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestAppendCarriesTheIdentity(t *testing.T) {
	p := sandbox(t)
	if _, err := Append("sess-7", "li:post", "https://linkedin.com/feed", "ok"); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	// Without the profile column, an audit trail cannot say WHICH identity acted — and more than one
	// profile is in use.
	if e.Profile == "" || !strings.HasSuffix(e.Profile, "chrome-agent-profile") {
		t.Errorf("profile not recorded: %+v", e)
	}
	if e.Agent != "sess-7" || e.Action != "li:post" || e.Target != "https://linkedin.com/feed" || e.Result != "ok" {
		t.Errorf("entry lost a field: %+v", e)
	}
	if len(e.TS) != 20 || !strings.HasSuffix(e.TS, "Z") {
		t.Errorf("ts is not a UTC stamp: %q", e.TS)
	}
}

// The bash version interpolated action/target into a printf format and escaped only `result`, so a
// quote or a newline produced a line no parser could read. In an audit log, an unparseable line is a
// missing line.
func TestAppendEscapesEveryField(t *testing.T) {
	p := sandbox(t)
	nasty := "he said \"post\"\nand a \\backslash\t"
	if _, err := Append(nasty, nasty, nasty, nasty); err != nil {
		t.Fatal(err)
	}
	got := read(t, p) // read() fails the test on any line that does not parse
	if len(got) != 1 {
		t.Fatalf("a multi-line field produced %d lines", len(got))
	}
	if got[0].Target != nasty || got[0].Result != nasty || got[0].Action != nasty {
		t.Errorf("round-trip changed the value: %q", got[0].Target)
	}
}

func gunzip(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s is not gzip: %v", p, err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRotationOnSizeGzipsTheRollAndKeepsTheLiveFile(t *testing.T) {
	p := sandbox(t)
	t.Setenv("CHROME_AGENT_LEDGER_MAX", "400")
	t.Setenv("CHROME_AGENT_LEDGER_KEEP", "50") // pruning is TestRotationKeepsOnlyNRolls's job

	for i := 0; i < 12; i++ {
		if _, err := Append("sess", "read", "https://example.com/page", strings.Repeat("x", 64)); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal("the LIVE ledger is gone — rotation must never delete it")
	}
	if fi.Size() > 400 {
		t.Errorf("live ledger is %d bytes, over the %d threshold — it did not roll", fi.Size(), 400)
	}
	rolls, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "actions-*.ndjson.gz"))
	if len(rolls) == 0 {
		t.Fatal("no gzipped roll was produced")
	}
	// The gzip must be finished by the time we return — bash backgrounded it and raced its own prune.
	var lines int
	for _, r := range rolls {
		lines += strings.Count(strings.TrimRight(gunzip(t, r), "\n"), "\n") + 1
	}
	if live := len(read(t, p)); lines+live != 12 {
		t.Errorf("lost lines across the roll: %d archived + %d live != 12", lines, live)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "actions-*.ndjson")); len(leftovers) > 0 {
		t.Errorf("uncompressed roll left behind: %v", leftovers)
	}
}

func TestRotationKeepsOnlyNRolls(t *testing.T) {
	p := sandbox(t)
	t.Setenv("CHROME_AGENT_LEDGER_KEEP", "2")
	dir := filepath.Dir(p)

	for i := 0; i < 5; i++ {
		if _, err := Append("sess", "read", "t", "r"); err != nil {
			t.Fatal(err)
		}
		res, err := Rotate("manual")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Rotated || !strings.HasSuffix(res.Roll, ".gz") {
			t.Fatalf("rotate %d: %+v", i, res)
		}
	}
	rolls, _ := filepath.Glob(filepath.Join(dir, "actions-*.ndjson.gz"))
	if len(rolls) != 2 {
		t.Fatalf("keep=2 left %d rolls: %v", len(rolls), rolls)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("the live ledger was deleted by rotation")
	}
}

func TestRotateIsSafeOnAnAbsentLedger(t *testing.T) {
	sandbox(t)
	res, err := Rotate("manual")
	if err != nil {
		t.Fatalf("rotate with no ledger: %v", err)
	}
	if res.Rotated {
		t.Errorf("claimed to rotate a ledger that does not exist: %+v", res)
	}
}

func TestStatusReportsSizeEntriesAndRolls(t *testing.T) {
	p := sandbox(t)
	t.Setenv("CHROME_AGENT_LEDGER_MAX", "1000000")
	t.Setenv("CHROME_AGENT_LEDGER_KEEP", "3")
	for i := 0; i < 3; i++ {
		Append("sess", "a", "t", "r")
	}
	if _, err := Rotate("manual"); err != nil {
		t.Fatal(err)
	}
	Append("sess", "a", "t", "r")

	st := StatusOf()
	if st.Ledger != p {
		t.Errorf("ledger = %q, want %q", st.Ledger, p)
	}
	if st.Entries != 1 {
		t.Errorf("entries = %d, want 1 (three were rolled away)", st.Entries)
	}
	if st.Bytes <= 0 {
		t.Errorf("bytes = %d", st.Bytes)
	}
	if st.MaxBytes != 1000000 || st.Keep != 3 {
		t.Errorf("env overrides ignored: max=%d keep=%d", st.MaxBytes, st.Keep)
	}
	if len(st.Rolls) != 1 || !strings.HasSuffix(st.Rolls[0].File, ".ndjson.gz") || st.Rolls[0].Bytes <= 0 {
		t.Errorf("rolls not reported: %+v", st.Rolls)
	}
}

func TestDefaultsAreEightMegabytesAndFiveRolls(t *testing.T) {
	sandbox(t)
	if MaxBytes() != 8<<20 {
		t.Errorf("default max = %d, want 8MB", MaxBytes())
	}
	if Keep() != 5 {
		t.Errorf("default keep = %d, want 5", Keep())
	}
	t.Setenv("CHROME_AGENT_LEDGER_MAX", "not-a-number")
	if MaxBytes() != 8<<20 {
		t.Errorf("a junk override must fall back to the default, got %d", MaxBytes())
	}
}

func TestTailSkipsUnparseableLegacyLines(t *testing.T) {
	p := sandbox(t)
	Append("sess", "a", "one", "r")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{\"ts\":\"x\",\"target\":\"broken\n")
	f.Close()
	Append("sess", "a", "two", "r")

	got := Tail(5)
	if len(got) != 2 || got[0].Target != "one" || got[1].Target != "two" {
		t.Errorf("tail = %+v", got)
	}
}
