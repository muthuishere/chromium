package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBrowser plays the role the fork plays: watch the spool, consume published .txt files, and
// answer results/<id>.json. It lets us test the protocol without a browser — which matters, because
// the real one is a shared singleton and a test must never steer the owner's tabs.
func fakeBrowser(t *testing.T, dir string, reply func(line string) map[string]any) func() {
	t.Helper()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".txt") {
					continue
				}
				p := filepath.Join(dir, e.Name())
				b, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				os.Remove(p)
				for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					if line == "" {
						continue
					}
					res := reply(line)
					if res == nil {
						continue
					}
					id := idOf(line)
					if id == "" {
						continue
					}
					os.MkdirAll(filepath.Join(dir, "results"), 0o755)
					j, _ := json.Marshal(res)
					os.WriteFile(filepath.Join(dir, "results", id+".json"), j, 0o644)
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return func() { close(stop) }
}

// idOf pulls the id out of "[TAB:x|]VERB:<id>|payload" or "VERB:<id>".
func idOf(line string) string {
	if i := strings.Index(line, "TAB:"); i == 0 {
		if j := strings.Index(line, "|"); j >= 0 {
			line = line[j+1:]
		}
	}
	colon := strings.Index(line, ":")
	if colon < 0 {
		return ""
	}
	rest := line[colon+1:]
	if bar := strings.Index(rest, "|"); bar >= 0 {
		return rest[:bar]
	}
	return rest
}

func TestEvalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	stop := fakeBrowser(t, dir, func(line string) map[string]any {
		if !strings.HasPrefix(line, "EVAL:") {
			return nil
		}
		return map[string]any{"ok": true, "value": "pong"}
	})
	defer stop()

	res, err := New(dir).Eval("1", 3*time.Second)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if res["value"] != "pong" {
		t.Fatalf("unexpected result: %v", res)
	}
	// The reader owns cleanup: the browser never deletes result files, and 151 orphans once piled up.
	if entries, _ := os.ReadDir(filepath.Join(dir, "results")); len(entries) != 0 {
		t.Fatalf("result file was not deleted: %d left", len(entries))
	}
}

func TestTabPrefixPinsTheCommand(t *testing.T) {
	dir := t.TempDir()
	var seen string
	stop := fakeBrowser(t, dir, func(line string) map[string]any {
		seen = line
		return map[string]any{"ok": true}
	})
	defer stop()

	if _, err := New(dir).WithTab("abc-123").Eval("1", 3*time.Second); err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !strings.HasPrefix(seen, "TAB:abc-123|EVAL:") {
		t.Fatalf("command was not pinned to the tab: %q", seen)
	}
}

func TestNoAnswerIsATimeoutNotASuccess(t *testing.T) {
	dir := t.TempDir() // nobody is servicing this spool
	_, err := New(dir).Eval("1", 300*time.Millisecond)
	if err == nil {
		t.Fatal("a spool nobody services must not look like success")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAliveIsFalseWithoutABrowser(t *testing.T) {
	if New(t.TempDir()).Alive(200 * time.Millisecond) {
		t.Fatal("Alive() said yes with no browser")
	}
}

func TestSweepQuarantinesStaleCommandsInsteadOfReplayingThem(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "1111-1-old.txt")
	os.WriteFile(old, []byte("GOTO:https://example.com\n"), 0o644)
	// Backdate it: a queued command from yesterday must not fire when the watcher next starts.
	past := time.Now().Add(-10 * time.Minute)
	os.Chtimes(old, past, past)

	fresh := filepath.Join(dir, "2222-2-fresh.txt")
	os.WriteFile(fresh, []byte("GOTO:https://example.org\n"), 0o644)

	n, err := New(dir).Sweep(5 * time.Minute)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 quarantined, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "quarantine", "1111-1-old.txt")); err != nil {
		t.Fatal("stale command was not quarantined")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("sweep ate a fresh command")
	}
}
