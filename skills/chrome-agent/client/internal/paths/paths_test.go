package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both of these are REGRESSION tests for bugs that actually shipped. ADR 0010 says every trap the
// bash CLI encodes in a comment must arrive in Go as a test, not as a comment. These are the two
// that cost the most: one forked an identity into two browsers, the other merged two identities
// into one.

func TestSpoolKeying_TrailingSlashIsTheSameProfile(t *testing.T) {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, "chrome-agent-profile")

	a := SpoolFor(def)
	b := SpoolFor(def + "/")
	if a != b {
		t.Fatalf("a trailing slash forked one profile into two spools: %q vs %q", a, b)
	}
	// And the default profile must keep its HISTORIC path, or every running browser and existing
	// script that references ~/chrome-agent-sendkeys stops being reachable.
	if want := filepath.Join(home, "chrome-agent-sendkeys"); a != want {
		t.Fatalf("default profile lost its historic spool: got %q want %q", a, want)
	}
}

func TestSpoolKeying_BasenameCollisionStaysTwoSpools(t *testing.T) {
	home, _ := os.UserHomeDir()
	work := SpoolFor(filepath.Join(home, "work", "profile"))
	personal := SpoolFor(filepath.Join(home, "personal", "profile"))
	if work == personal {
		t.Fatalf("two profiles sharing a basename collapsed onto one spool (%q) — that is posting as the wrong person", work)
	}
	if !strings.HasPrefix(filepath.Base(work), "chrome-agent-sendkeys-") {
		t.Fatalf("non-default profile should be keyed by hash, got %q", work)
	}
}

func TestLaunchLogFollowsTheSameRule(t *testing.T) {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, "chrome-agent-profile")
	if got := LaunchLogFor(def + "/"); got != "/tmp/chrome-agent.log" {
		t.Fatalf("default launch log changed: %q", got)
	}
	if LaunchLogFor(filepath.Join(home, "work", "profile")) == LaunchLogFor(filepath.Join(home, "personal", "profile")) {
		t.Fatal("two profiles share a launch log")
	}
}

func TestForkOverrideAndMissingForkIsNamed(t *testing.T) {
	t.Setenv("CHROME_AGENT_FORK", "/tmp/definitely-not-a-fork")
	if Fork() != "/tmp/definitely-not-a-fork" {
		t.Fatalf("CHROME_AGENT_FORK ignored: %q", Fork())
	}
	err := RequireFork()
	if err == nil {
		t.Fatal("a missing fork must be an error")
	}
	// The error has to NAME the missing file. "file not found" from three frames down is the thing
	// this replaced.
	if !strings.Contains(err.Error(), "chromesendkeys.cjs") {
		t.Fatalf("error does not name the missing file: %v", err)
	}
}

func TestForkTrailingSlashIsStripped(t *testing.T) {
	t.Setenv("CHROME_AGENT_FORK", "/tmp/fork/")
	if got := SpoolClient(); got != "/tmp/fork/chromesendkeys.cjs" {
		t.Fatalf("trailing slash on the fork leaked into a path: %q", got)
	}
}
