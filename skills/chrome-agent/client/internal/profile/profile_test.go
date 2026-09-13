package profile

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/deemwarhq/chrome-agent/internal/paths"
)

// sandbox points HOME and the profile env at a temp dir. A test that reads the developer's laptop is
// testing the laptop — and in this package a test that got it wrong would delete a real profile.
func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CHROME_AGENT_PROFILE", "")
	return home
}

// lock writes the SingletonLock exactly as chromium does: a SYMLINK to "<host>-<pid>" whose target
// does not exist.
func lock(t *testing.T, dir string, pid int) {
	t.Helper()
	if err := os.Symlink("somehost-"+strconv.Itoa(pid), filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
}

const deadPID = 99999999 // above every platform's pid_max: guaranteed not to be alive

func TestCreateReportsTheSpoolThatWillReallyBeUsed(t *testing.T) {
	sandbox(t)
	dir := filepath.Join(t.TempDir(), "work-profile")

	res, err := Create(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Existed {
		t.Errorf("existed = true for a fresh dir")
	}
	if res.Spool != paths.SpoolFor(dir) {
		t.Errorf("spool %q != paths.SpoolFor %q — the spool must never be re-derived", res.Spool, paths.SpoolFor(dir))
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("create did not make %s", dir)
	}

	// A trailing slash must not fork one profile into two spools (the shipped bug).
	again, err := Create(dir + "/")
	if err != nil {
		t.Fatalf("create again: %v", err)
	}
	if !again.Existed {
		t.Errorf("existed = false for a dir that already exists")
	}
	if again.Spool != res.Spool {
		t.Errorf("trailing slash forked the spool: %q vs %q", again.Spool, res.Spool)
	}
	if again.Profile != res.Profile {
		t.Errorf("trailing slash changed the profile path: %q vs %q", again.Profile, res.Profile)
	}
}

func TestCreateRejectsEmptyAndNonDirectory(t *testing.T) {
	sandbox(t)
	if _, err := Create("  "); err == nil {
		t.Fatal("empty dir accepted")
	} else if e, ok := err.(*Error); !ok || e.Slug != "usage" {
		t.Errorf("want usage error, got %#v", err)
	}
	f := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := Create(f); err == nil {
		t.Fatal("a regular file was accepted as a profile")
	}
}

func TestListReportsSpoolSizeAndLockState(t *testing.T) {
	home := sandbox(t)
	def := paths.DefaultProfile()
	other := filepath.Join(home, "chrome-agent-profile-work")
	stale := filepath.Join(home, ".config", "chrome-agent", "profiles", "old")
	for _, d := range []string{def, other, stale} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(other, "Cookies"), make([]byte, 4096), 0o644)
	lock(t, other, os.Getpid()) // a live browser
	lock(t, stale, deadPID)     // a corpse

	got := map[string]Info{}
	res := List()
	for _, p := range res.Profiles {
		got[p.Profile] = p
	}
	if res.Count != 3 {
		t.Fatalf("count = %d, want 3 (%v)", res.Count, got)
	}
	for _, d := range []string{def, other, stale} {
		if got[d].Spool != paths.SpoolFor(d) {
			t.Errorf("%s: spool %q != paths.SpoolFor %q", d, got[d].Spool, paths.SpoolFor(d))
		}
	}
	// Three profiles, three DISTINCT spools (ADR 0005's own proof).
	seen := map[string]bool{}
	for _, p := range res.Profiles {
		if seen[p.Spool] {
			t.Fatalf("two profiles share a spool: %s", p.Spool)
		}
		seen[p.Spool] = true
	}
	if want := "running:" + strconv.Itoa(os.Getpid()); got[other].Lock != want {
		t.Errorf("lock for a live profile = %q, want %q", got[other].Lock, want)
	}
	if got[stale].Lock != LockStale {
		t.Errorf("lock for a dead owner = %q, want %q", got[stale].Lock, LockStale)
	}
	if got[def].Lock != LockFree {
		t.Errorf("lock for an unlocked profile = %q, want %q", got[def].Lock, LockFree)
	}
	if got[other].SizeBytes < 4096 || got[other].Size == "" {
		t.Errorf("size not reported: %+v", got[other])
	}
	if !got[def].Default {
		t.Errorf("the default profile was not flagged as default")
	}
}

// This is the regression test for the guard that had never once fired: SingletonLock is a symlink to
// a target that does not exist, so os.Stat (which follows it) reports "nothing there" while a
// browser is running. Only Lstat sees it.
func TestDeleteRefusesWhileABrowserIsRunning(t *testing.T) {
	sandbox(t)
	dir := filepath.Join(t.TempDir(), "live-profile")
	os.MkdirAll(dir, 0o755)
	lock(t, dir, os.Getpid())

	link := filepath.Join(dir, "SingletonLock")
	if _, err := os.Stat(link); err == nil {
		t.Fatal("fixture is wrong: the lock target exists, so this test would pass even with a Stat check")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("fixture is wrong: Lstat cannot see the lock: %v", err)
	}

	_, err := Delete(dir, DeleteOpts{Yes: true})
	if err == nil {
		t.Fatal("deleted a profile with a live browser on it")
	}
	e, ok := err.(*Error)
	if !ok || e.Slug != "profile-in-use" {
		t.Fatalf("want profile-in-use, got %#v", err)
	}
	if !strings.Contains(e.Detail, strconv.Itoa(os.Getpid())) {
		t.Errorf("the refusal does not name the pid holding it: %q", e.Detail)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("the profile was destroyed despite the refusal")
	}
}

func TestDeleteProceedsPastAStaleLock(t *testing.T) {
	sandbox(t)
	dir := filepath.Join(t.TempDir(), "stale-profile")
	os.MkdirAll(dir, 0o755)
	lock(t, dir, deadPID)

	res, err := Delete(dir, DeleteOpts{Yes: true})
	if err != nil {
		t.Fatalf("a stale lock blocked a delete: %v", err)
	}
	if res.Delete != dir {
		t.Errorf("deleted = %q, want %q", res.Delete, dir)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the profile still exists")
	}
}

func TestDeleteRefusesTheDefaultProfileWithoutForce(t *testing.T) {
	sandbox(t)
	def := paths.DefaultProfile()
	os.MkdirAll(def, 0o755)

	_, err := Delete(def, DeleteOpts{Yes: true})
	e, ok := err.(*Error)
	if !ok || e.Slug != "default-profile" {
		t.Fatalf("want default-profile refusal, got %#v", err)
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatal("the default profile was destroyed")
	}

	if _, err := Delete(def, DeleteOpts{Yes: true, Force: true}); err != nil {
		t.Fatalf("--force --yes should delete the default profile: %v", err)
	}
	if _, err := os.Stat(def); err == nil {
		t.Error("--force --yes did not delete")
	}
}

// Force permits a dangerous TARGET; it is not permission to destroy it. (The bash CLI treated
// --force as --yes, so `profile delete <default> --force` destroyed it with no staging step.)
func TestForceDoesNotImplyYes(t *testing.T) {
	sandbox(t)
	def := paths.DefaultProfile()
	os.MkdirAll(def, 0o755)

	res, err := Delete(def, DeleteOpts{Force: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !res.Staged {
		t.Fatal("--force alone deleted without staging")
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatal("--force alone destroyed the default profile")
	}
}

func TestDeleteStagesAndNamesWhatItWouldDestroy(t *testing.T) {
	sandbox(t)
	dir := filepath.Join(t.TempDir(), "named-profile")
	os.MkdirAll(filepath.Join(dir, "Default"), 0o755)
	os.WriteFile(filepath.Join(dir, "Default", "Cookies"),
		[]byte("SQLite format 3\x00....linkedin.com....li_at....news.ycombinator.com..."), 0o644)
	spool := paths.SpoolFor(dir)
	os.MkdirAll(spool, 0o755)

	res, err := Delete(dir, DeleteOpts{})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !res.Staged {
		t.Fatal("a bare delete was not staged")
	}
	if res.Target != dir || res.Spool != spool || res.Size == "" || res.Warning == "" {
		t.Errorf("the staged report does not name what it would destroy: %+v", res)
	}
	if !contains(res.SignedIn, "linkedin.com") || !contains(res.SignedIn, "news.ycombinator.com") {
		t.Errorf("staged delete did not name the sessions it revokes: %v", res.SignedIn)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("a staged delete destroyed the profile")
	}
	if _, err := os.Stat(spool); err != nil {
		t.Fatal("a staged delete destroyed the spool")
	}

	// ...and with --yes, profile AND spool go.
	done, err := Delete(dir, DeleteOpts{Yes: true})
	if err != nil {
		t.Fatalf("delete --yes: %v", err)
	}
	if done.Staged || done.Delete != dir || !done.SpoolRemoved {
		t.Errorf("delete --yes: %+v", done)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("profile survived --yes")
	}
	if _, err := os.Stat(spool); err == nil {
		t.Error("spool survived --yes")
	}
}

func TestDeleteRejectsUnknownTargets(t *testing.T) {
	sandbox(t)
	for _, arg := range []string{"", filepath.Join(t.TempDir(), "nope")} {
		_, err := Delete(arg, DeleteOpts{Yes: true})
		if err == nil {
			t.Fatalf("delete(%q) succeeded", arg)
		}
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
