package engine

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTarGz(t *testing.T, entries []tar.Header, bodies map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		body := bodies[h.Name]
		h.Size = int64(len(body))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

// The macOS framework is held together by symlinks (Versions/Current -> <version>). An extractor
// that drops them produces an .app that fails its code signature — this is the regression test.
func TestExtractKeepsSymlinksAndStripsTheTopDir(t *testing.T) {
	p := writeTarGz(t, []tar.Header{
		{Name: "top/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "top/Versions/1.0/bin", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "top/Versions/Current", Typeflag: tar.TypeSymlink, Linkname: "1.0"},
	}, map[string]string{"top/Versions/1.0/bin": "#!/bin/sh\n"})
	dst := t.TempDir()
	if err := Extract(p, dst, 1); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(dst, "Versions", "Current"))
	if err != nil || link != "1.0" {
		t.Fatalf("symlink lost: %q %v", link, err)
	}
	fi, err := os.Stat(filepath.Join(dst, "Versions", "Current", "bin"))
	if err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("executable bit lost through the link: %v", err)
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	for name, h := range map[string]tar.Header{
		"dotdot":  {Name: "top/../../evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"symlink": {Name: "top/link", Typeflag: tar.TypeSymlink, Linkname: "../../../etc"},
	} {
		p := writeTarGz(t, []tar.Header{h}, nil)
		if err := Extract(p, t.TempDir(), 1); err == nil {
			t.Fatalf("%s: an entry escaping the install dir was accepted", name)
		}
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("a", 64)
	got := ParseSums(strings.NewReader(h + "  one.tar.gz\n" + strings.ToUpper(strings.Repeat("b", 64)) + " *two.tar.gz\ngarbage\n"))
	if got["one.tar.gz"] != h || got["two.tar.gz"] != strings.Repeat("b", 64) || len(got) != 2 {
		t.Fatalf("bad parse: %v", got)
	}
}

func TestPickNamesThePublishedPlatforms(t *testing.T) {
	m := &Manifest{Artifact: "linux.tar.gz", SHA256: "x", MacOS: &PlatformArtifact{Artifact: "mac.tar.gz", SHA256: "y"}}
	if a, _ := m.Pick("linux", "amd64"); a.Artifact != "linux.tar.gz" {
		t.Fatal("linux")
	}
	if a, _ := m.Pick("darwin", "arm64"); a.Artifact != "mac.tar.gz" {
		t.Fatal("mac")
	}
	if _, err := m.Pick("darwin", "amd64"); err == nil || !strings.Contains(err.Error(), "darwin/arm64") {
		t.Fatalf("intel mac must be refused with the list of what exists: %v", err)
	}
}
