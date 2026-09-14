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

func TestForkOverride(t *testing.T) {
	t.Setenv("CHROME_AGENT_FORK", "/tmp/fork/")
	if Fork() != "/tmp/fork" {
		t.Fatalf("CHROME_AGENT_FORK ignored or kept its trailing slash: %q", Fork())
	}
}

// A fresh machine has no fork. The error must name the ONE command that fixes it, not a build step
// for a checkout the user does not have.
func TestMissingBrowserNamesEngineInstall(t *testing.T) {
	t.Setenv("CHROMIUM_SENDKEYS_OUT", "")
	t.Setenv("CHROME_AGENT_FORK", t.TempDir())
	t.Setenv("CHROME_AGENT_ENGINE_DIR", t.TempDir())
	err := RequireBinary()
	if err == nil || !strings.Contains(err.Error(), "chrome-agent engine install") {
		t.Fatalf("missing browser must point at engine install: %v", err)
	}
	if _, src := Binary(); src != "none" {
		t.Fatalf("nothing installed must resolve as none, got %q", src)
	}
}

// The installed engine outranks a dev build, and an explicit out dir outranks both.
func TestBinaryResolutionOrder(t *testing.T) {
	fork, eng := t.TempDir(), t.TempDir()
	t.Setenv("CHROME_AGENT_FORK", fork)
	t.Setenv("CHROME_AGENT_ENGINE_DIR", eng)
	t.Setenv("CHROMIUM_SENDKEYS_OUT", "")
	mk := func(p string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
	}
	mk(BinaryIn(filepath.Join(fork, "out", "Default")))
	if _, src := Binary(); src != "fork" {
		t.Fatalf("dev build only: want fork, got %s", src)
	}
	mk(BinaryIn(eng))
	if b, src := Binary(); src != "engine" || b != BinaryIn(eng) {
		t.Fatalf("installed engine must win over the fork: %s %s", b, src)
	}
	t.Setenv("CHROMIUM_SENDKEYS_OUT", "/somewhere")
	if _, src := Binary(); src != "env" {
		t.Fatalf("explicit out dir must win: %s", src)
	}
}
