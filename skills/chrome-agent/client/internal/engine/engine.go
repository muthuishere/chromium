// Package engine installs the published browser build, so a machine needs this binary and nothing
// else — no fork checkout, no node, no build tree.
//
// This is the fix for the one line that kept `browser:` identities from shipping to anyone: the
// client used to find its browser only inside the owner's chromium checkout. The release now lives
// in a public bucket (see dist/ in the fork), and `chrome-agent engine install` puts it where
// paths.Binary() looks first.
//
// Three rules, each one a way a download goes wrong:
//
//   - VERIFY BEFORE UNPACK. The sha256 comes from SHA256SUMS, the same file install.sh trusts, and
//     must also agree with manifest.json. An unverified browser is not something to run.
//   - SYMLINKS ARE CONTENT. The macOS framework is Versions/Current -> <version> and friends; an
//     extractor that skips or flattens links produces an .app that fails its code signature.
//   - SWAP, DON'T OVERWRITE. The new tree is unpacked beside the old one and renamed into place, so
//     a failed or interrupted install leaves the previous engine working.
package engine

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/paths"
)

// DefaultReleaseURL is where dist/install.sh publishes. CHROME_AGENT_RELEASE_URL overrides it.
const DefaultReleaseURL = "https://hel1.your-objectstorage.com/publicassets/chrome-agent"

// MarkerFile records what is installed, so `engine status` never has to guess from file sizes.
const MarkerFile = ".chrome-agent-engine.json"

func ReleaseURL() string {
	if v := os.Getenv("CHROME_AGENT_RELEASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultReleaseURL
}

// Manifest is the subset of dist/manifest.json this client reads. Linux is the top-level artifact
// (it shipped first); every later platform is a named section.
type Manifest struct {
	Artifact        string            `json:"artifact"`
	SHA256          string            `json:"sha256"`
	ChromiumVersion string            `json:"chromium_version"`
	MacOS           *PlatformArtifact `json:"macos,omitempty"`
}

type PlatformArtifact struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
}

// Pick returns the artifact for goos/goarch, or an error that says which platforms exist.
func (m *Manifest) Pick(goos, goarch string) (PlatformArtifact, error) {
	switch {
	case goos == "linux" && goarch == "amd64":
		if m.Artifact != "" {
			return PlatformArtifact{Artifact: m.Artifact, SHA256: m.SHA256}, nil
		}
	case goos == "darwin" && goarch == "arm64":
		if m.MacOS != nil && m.MacOS.Artifact != "" {
			return *m.MacOS, nil
		}
	}
	return PlatformArtifact{}, fmt.Errorf("no engine is published for %s/%s — releases exist for linux/amd64 and darwin/arm64", goos, goarch)
}

// Installed is the marker written after a verified install.
type Installed struct {
	Artifact    string `json:"artifact"`
	SHA256      string `json:"sha256"`
	ReleaseURL  string `json:"release_url"`
	InstalledAt string `json:"installed_at"`
}

type Status struct {
	Dir        string     `json:"dir"`
	Binary     string     `json:"binary"`
	Present    bool       `json:"present"`
	Installed  *Installed `json:"installed,omitempty"`
	Resolved   string     `json:"resolved_binary"`
	ResolvedBy string     `json:"resolved_by"`
	Latest     string     `json:"latest,omitempty"`
	UpToDate   *bool      `json:"up_to_date,omitempty"`
	Note       string     `json:"note,omitempty"`
}

// StatusOf reports what is installed. checkLatest asks the bucket for the current manifest.
func StatusOf(checkLatest bool) Status {
	dir := paths.EngineDir()
	bin := paths.BinaryIn(dir)
	s := Status{Dir: dir, Binary: bin, Present: executable(bin)}
	s.Resolved, s.ResolvedBy = paths.Binary()
	if b, err := os.ReadFile(filepath.Join(dir, MarkerFile)); err == nil {
		var in Installed
		if json.Unmarshal(b, &in) == nil {
			s.Installed = &in
		}
	}
	if checkLatest {
		if m, err := FetchManifest(ReleaseURL()); err == nil {
			if pa, err := m.Pick(runtime.GOOS, runtime.GOARCH); err == nil {
				s.Latest = pa.Artifact
				up := s.Installed != nil && s.Installed.SHA256 == pa.SHA256
				s.UpToDate = &up
			}
		} else {
			s.Note = "could not read the release manifest: " + err.Error()
		}
	}
	return s
}

type Result struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
	Dir      string `json:"dir"`
	Binary   string `json:"binary"`
	Skipped  bool   `json:"skipped,omitempty"`
	Replaced bool   `json:"replaced,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Next     string `json:"next"`
}

// Install downloads, verifies and unpacks the engine for this machine. force reinstalls even when
// the marker says the same sha256 is already there.
func Install(force bool, progress io.Writer) (*Result, error) {
	base := ReleaseURL()
	m, err := FetchManifest(base)
	if err != nil {
		return nil, err
	}
	pa, err := m.Pick(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	sums, err := fetchSums(base)
	if err != nil {
		return nil, err
	}
	want, ok := sums[pa.Artifact]
	if !ok {
		return nil, fmt.Errorf("%s is in manifest.json but not in SHA256SUMS — refusing an artifact nothing vouches for", pa.Artifact)
	}
	if pa.SHA256 != "" && !strings.EqualFold(pa.SHA256, want) {
		return nil, fmt.Errorf("manifest.json and SHA256SUMS disagree about %s (%s vs %s) — the release is inconsistent; refusing", pa.Artifact, pa.SHA256, want)
	}

	dir := paths.EngineDir()
	res := &Result{Artifact: pa.Artifact, SHA256: want, Dir: dir, Binary: paths.BinaryIn(dir),
		Next: "chrome-agent up"}
	if !force {
		if b, err := os.ReadFile(filepath.Join(dir, MarkerFile)); err == nil {
			var in Installed
			if json.Unmarshal(b, &in) == nil && strings.EqualFold(in.SHA256, want) && executable(res.Binary) {
				res.Skipped = true
				return res, nil
			}
		}
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	tmpTar, err := os.CreateTemp(parent, ".engine-*.tar.gz")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpTar.Name())
	n, got, err := download(base+"/"+pa.Artifact, tmpTar, progress)
	tmpTar.Close()
	if err != nil {
		return nil, err
	}
	res.Bytes = n
	if !strings.EqualFold(got, want) {
		return nil, fmt.Errorf("CHECKSUM FAILED for %s: got %s, SHA256SUMS says %s — refusing to install", pa.Artifact, got, want)
	}

	staging, err := os.MkdirTemp(parent, ".engine-staging-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := Extract(tmpTar.Name(), staging, 1); err != nil {
		return nil, fmt.Errorf("unpack %s: %w", pa.Artifact, err)
	}
	if !executable(paths.BinaryIn(staging)) {
		return nil, fmt.Errorf("%s unpacked, but has no browser at %s — the artifact does not match this platform's layout", pa.Artifact, paths.BinaryIn(staging))
	}
	marker, _ := json.MarshalIndent(Installed{Artifact: pa.Artifact, SHA256: want, ReleaseURL: base,
		InstalledAt: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, MarkerFile), marker, 0o644); err != nil {
		return nil, err
	}

	// Swap. The old tree moves aside first so a failed rename can put it back.
	old := ""
	if _, err := os.Lstat(dir); err == nil {
		old = dir + ".old-" + time.Now().Format("20060102150405")
		if err := os.Rename(dir, old); err != nil {
			return nil, fmt.Errorf("could not move the previous engine aside (is a browser from it still running?): %w", err)
		}
		res.Replaced = true
	}
	if err := os.Rename(staging, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir)
		}
		return nil, err
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return res, nil
}

func FetchManifest(base string) (*Manifest, error) {
	body, err := get(base + "/manifest.json")
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var m Manifest
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest.json is not valid JSON: %w", err)
	}
	return &m, nil
}

func fetchSums(base string) (map[string]string, error) {
	body, err := get(base + "/SHA256SUMS")
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return ParseSums(body), nil
}

// ParseSums reads `<hex>  <name>` lines (sha256sum format; a leading * marks binary mode).
func ParseSums(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

var httpClient = &http.Client{Timeout: 30 * time.Minute}

func get(url string) (io.ReadCloser, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

func download(url string, dst io.Writer, progress io.Writer) (int64, string, error) {
	body, err := get(url)
	if err != nil {
		return 0, "", err
	}
	defer body.Close()
	h := sha256.New()
	var w io.Writer = io.MultiWriter(dst, h)
	if progress != nil {
		fmt.Fprintf(progress, "downloading %s\n", url)
	}
	n, err := io.Copy(w, body)
	if err != nil {
		return n, "", fmt.Errorf("download %s: %w", url, err)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// Extract unpacks a .tar.gz into dst, dropping the first strip path components. It refuses any
// entry — or symlink target — that would land outside dst.
func Extract(tarGz, dst string, strip int) error {
	f, err := os.Open(tarGz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	root, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := stripComponents(hdr.Name, strip)
		if name == "" {
			continue
		}
		target := filepath.Join(root, name)
		if !within(root, target) {
			return fmt.Errorf("entry %q escapes the install dir", hdr.Name)
		}
		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			resolved := hdr.Linkname
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(target), resolved)
			}
			if !within(root, resolved) {
				return fmt.Errorf("symlink %q -> %q escapes the install dir", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			src := filepath.Join(root, stripComponents(hdr.Linkname, strip))
			if !within(root, src) {
				return fmt.Errorf("hardlink %q escapes the install dir", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Link(src, target); err != nil {
				return err
			}
		}
	}
}

func stripComponents(name string, n int) string {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	parts := strings.Split(strings.Trim(name, "/"), "/")
	if len(parts) <= n {
		return ""
	}
	return filepath.FromSlash(strings.Join(parts[n:], "/"))
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}
