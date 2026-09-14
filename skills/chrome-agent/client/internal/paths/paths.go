// Package paths resolves everything this CLI needs to find: the fork, the skill, the profile, and
// the spool that profile maps to.
//
// Every rule here was paid for. Read the comments before changing a line of it.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Fork is a chromium checkout with a build in it. It is the DEV fallback only: a machine that ran
// `chrome-agent engine install` never needs one.
//
// The path used to be hardcoded AND required, which was the entire reason a `browser:` identity
// could not ship to anyone else. CHROME_AGENT_FORK overrides it; the owner's checkout stays the
// default so a dev build on that machine keeps working.
func Fork() string {
	if v := os.Getenv("CHROME_AGENT_FORK"); v != "" {
		return strings.TrimRight(v, "/")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "muthu/gitworkspace/chromium")
}

// EngineDir is where `chrome-agent engine install` (and dist/install.sh) unpack the published build.
func EngineDir() string {
	if v := os.Getenv("CHROME_AGENT_ENGINE_DIR"); v != "" {
		return strings.TrimRight(v, "/")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "chrome-agent", "engine")
}

// BinaryIn is the browser inside an out dir or an unpacked engine. darwin ships an .app bundle;
// linux ships a bare executable. Same layout the build tree and the release tarballs both use.
func BinaryIn(dir string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(dir, "Chromium.app", "Contents", "MacOS", "Chromium")
	}
	return filepath.Join(dir, "chrome")
}

// Binary resolves the browser to launch, and says which rule found it:
//
//	env    CHROMIUM_SENDKEYS_OUT — an explicit out dir always wins
//	engine the installed release (chrome-agent engine install)
//	fork   <fork>/out/Default — a dev build
//
// When nothing exists it returns the ENGINE path, because that is the one a new machine should get.
func Binary() (path, source string) {
	if out := os.Getenv("CHROMIUM_SENDKEYS_OUT"); out != "" {
		return BinaryIn(strings.TrimRight(out, "/")), "env"
	}
	if b := BinaryIn(EngineDir()); isExecutable(b) {
		return b, "engine"
	}
	if b := BinaryIn(filepath.Join(Fork(), "out", "Default")); isExecutable(b) {
		return b, "fork"
	}
	return BinaryIn(EngineDir()), "none"
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

var ErrNotBuilt = errors.New("not built")

// RequireBinary fails with the fix NAMED, not a stack trace from three frames down.
func RequireBinary() error {
	b, src := Binary()
	if isExecutable(b) {
		return nil
	}
	if src == "env" {
		return fmt.Errorf("CHROMIUM_SENDKEYS_OUT points at %s, which has no browser — unset it or build there", b)
	}
	return fmt.Errorf("no browser installed (looked in %s and %s/out/Default) — run: chrome-agent engine install", EngineDir(), Fork())
}

// Profile is the browser profile this invocation acts as. The trailing slash is stripped HERE, once,
// because everything downstream compares against it.
func Profile() string {
	home, _ := os.UserHomeDir()
	p := os.Getenv("CHROME_AGENT_PROFILE")
	if p == "" {
		p = filepath.Join(home, "chrome-agent-profile")
	}
	return strings.TrimRight(p, "/")
}

// DefaultProfile keeps the historic paths so every existing script, session and running browser that
// references them keeps working.
func DefaultProfile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "chrome-agent-profile")
}

// spoolKey hashes the FULL profile path, not its basename.
//
// TRAP 1: the trailing slash must be stripped BEFORE the default comparison. Omitting that once made
// ~/chrome-agent-profile/ fall through to the hashed branch, forking one profile into two spools and
// two Chromium instances contending for the same user-data-dir.
//
// TRAP 2: hash the full path. ~/work/profile and ~/personal/profile share a basename, and keying on
// the basename collapsed two identities onto one spool — i.e. posting as the wrong person.
func spoolKey(profile string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(profile, "/")))
	return hex.EncodeToString(sum[:])[:12]
}

// SpoolFor is the ONE derivation. `profile create` must report the spool the next `up` will really
// use, and two copies of this rule is how both traps above got in.
func SpoolFor(profile string) string {
	home, _ := os.UserHomeDir()
	p := strings.TrimRight(profile, "/")
	if p == DefaultProfile() {
		return filepath.Join(home, "chrome-agent-sendkeys")
	}
	return filepath.Join(home, "chrome-agent-sendkeys-"+spoolKey(p))
}

func LaunchLogFor(profile string) string {
	p := strings.TrimRight(profile, "/")
	if p == DefaultProfile() {
		return "/tmp/chrome-agent.log"
	}
	return "/tmp/chrome-agent-" + spoolKey(p) + ".log"
}

func Spool() string { return SpoolFor(Profile()) }

// ConfigDir holds the editable half: site definitions, vendored recipes, learned notes, the ledger.
func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "chrome-agent")
}

func SitesDir() string {
	if v := os.Getenv("CHROME_AGENT_SITES"); v != "" {
		return v
	}
	return filepath.Join(ConfigDir(), "sites")
}

func InstancesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "chromium-agent", "instances")
}
