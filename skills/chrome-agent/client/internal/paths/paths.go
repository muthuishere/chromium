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

// Fork is the chromium fork this CLI drives.
//
// The path used to be hardcoded, which was the entire reason a `browser:` identity could not ship
// to anyone else. CHROME_AGENT_FORK overrides it; the owner's checkout stays the default so nothing
// on that machine changes.
func Fork() string {
	if v := os.Getenv("CHROME_AGENT_FORK"); v != "" {
		return strings.TrimRight(v, "/")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "muthu/gitworkspace/chromium")
}

func SpoolClient() string { return filepath.Join(Fork(), "chromesendkeys.cjs") }
func Launcher() string    { return filepath.Join(Fork(), "chromium-agent-launch.cjs") }

// ForkBinary resolves the built browser exactly the way chromium-agent-launch.cjs does.
// darwin ships an .app bundle; linux and friends ship a bare executable in the out dir.
func ForkBinary() string {
	out := os.Getenv("CHROMIUM_SENDKEYS_OUT")
	if out == "" {
		out = filepath.Join(Fork(), "out", "Default")
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(out, "Chromium.app", "Contents", "MacOS", "Chromium")
	}
	return filepath.Join(out, "chrome")
}

// RequireFork fails with the MISSING FILE NAMED, not a stack trace from three frames down.
func RequireFork() error {
	var missing []string
	for _, p := range []string{SpoolClient(), Launcher()} {
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("chromium fork not found at %s (missing: %s) — set CHROME_AGENT_FORK to your checkout of the fork, or clone and build it",
		Fork(), strings.Join(missing, ", "))
}

var ErrNotBuilt = errors.New("not built")

func RequireBinary() error {
	b := ForkBinary()
	fi, err := os.Stat(b)
	if err != nil || fi.Mode()&0o111 == 0 {
		return fmt.Errorf("the fork at %s has no built binary at %s — build it: autoninja -C out/Default chrome", Fork(), b)
	}
	return nil
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
