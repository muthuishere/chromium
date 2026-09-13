// Package install makes a machine ready to run this CLI: the config tree, the site definitions, and
// the binary on PATH.
//
// Two rules here are the whole point.
//
// A SYMLINK, NOT A COPY. A copy is a second version of the CLI that ages silently — you upgrade the
// one in the repo and keep running the one on PATH, and the difference surfaces as a verb that
// behaves like last month. If something that is NOT a symlink already sits on PATH, we refuse to
// touch it and say so; clobbering a file we did not put there is not an install step.
//
// SYNC WITHOUT FORCE. Site definitions are written out, and a file an operator has edited is KEPT
// (sites.Sync's rule, ADR 0004). Reverting a 2am hotfix during an install is the same class of
// failure as silently ageing a last_verified stamp — so install never forces, and the report names
// exactly where to edit when a page changes.
package install

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/deemwarhq/chrome-agent/internal/paths"
	"github.com/deemwarhq/chrome-agent/internal/sites"
)

type Result struct {
	CLI          string   `json:"cli"`     // the binary this install points at
	OnPath       string   `json:"on_path"` // where the symlink was placed
	Linked       bool     `json:"linked"`
	LinkNote     string   `json:"link_note,omitempty"`
	BinDirOnPath bool     `json:"bindir_on_path"`
	SitesDir     string   `json:"sites_dir"`
	LearnedDir   string   `json:"learned_dir"`
	ShareDir     string   `json:"share_dir"`
	SitesNew     []string `json:"sites_installed"`
	SitesKept    []string `json:"sites_kept_local_edits"`
	SitesSame    []string `json:"sites_unchanged"`
	EditHere     string   `json:"edit_here"`
	Next         []string `json:"next"`
	Warnings     []string `json:"warnings,omitempty"`
}

// DefaultBinDir is where the CLI goes when the caller names nowhere.
func DefaultBinDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// Install creates the config tree, syncs the embedded site definitions without forcing, and puts a
// symlink to this binary on PATH. It is idempotent: running it twice changes nothing.
func Install(binDir string) (*Result, error) {
	if strings.TrimSpace(binDir) == "" {
		binDir = DefaultBinDir()
	}
	binDir = strings.TrimRight(binDir, "/")

	cfg := paths.ConfigDir()
	sitesDir := filepath.Join(cfg, "sites")
	learnedDir := filepath.Join(cfg, "learned")
	shareDir := filepath.Join(cfg, "share")
	for _, d := range []string{sitesDir, learnedDir, shareDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	res := &Result{
		SitesDir:   sitesDir,
		LearnedDir: learnedDir,
		ShareDir:   shareDir,
		OnPath:     filepath.Join(binDir, "chrome-agent"),
		EditHere:   "a page changed? edit " + sitesDir + "/<domain>.json — it wins over the shipped copy, no redeploy",
		Next:       []string{"chrome-agent doctor", "chrome-agent sites list", "chrome-agent auth <domain>"},
	}

	sync, err := sites.Sync(false, false)
	if err != nil {
		return nil, err
	}
	res.SitesNew, res.SitesKept, res.SitesSame = sync.Installed, sync.Kept, sync.Unchanged
	if len(sync.Kept) > 0 {
		res.Warnings = append(res.Warnings,
			"kept your locally edited site definitions ("+strings.Join(sync.Kept, ", ")+") — install never reverts an operator's fix; `sites sync --force` does, deliberately")
	}

	self, err := os.Executable()
	if err == nil {
		if resolved, e := filepath.EvalSymlinks(self); e == nil {
			self = resolved
		}
	}
	res.CLI = self
	linked, note := link(self, res.OnPath)
	res.Linked, res.LinkNote = linked, note
	if !linked && note != "" {
		res.Warnings = append(res.Warnings, note)
	}

	res.BinDirOnPath = onPath(binDir)
	if !res.BinDirOnPath {
		res.Warnings = append(res.Warnings, binDir+" is not on your PATH — add it, or the install is invisible to every caller")
	}
	return res, nil
}

// link places the symlink. It replaces a symlink (that is an upgrade) and refuses a regular file
// (that is someone else's binary).
func link(self, dst string) (bool, string) {
	if self == "" {
		return false, "could not determine this binary's own path; no symlink created"
	}
	fi, err := os.Lstat(dst)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		if cur, e := os.Readlink(dst); e == nil && cur == self {
			return true, "already linked"
		}
		if e := os.Remove(dst); e != nil {
			return false, "could not replace the existing symlink at " + dst + ": " + e.Error()
		}
	case err == nil:
		return false, dst + " exists and is NOT a symlink — refusing to overwrite it; a copy is a second version of the CLI that ages silently. Remove it, or install elsewhere."
	}
	if e := os.Symlink(self, dst); e != nil {
		return false, "could not link " + dst + ": " + e.Error()
	}
	return true, ""
}

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimRight(p, "/") == strings.TrimRight(dir, "/") {
			return true
		}
	}
	return false
}
