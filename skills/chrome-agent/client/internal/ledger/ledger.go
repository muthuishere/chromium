// Package ledger is the audit trail: which identity did what, when, and what came back.
//
// Two properties are load-bearing.
//
// IDENTITY. A line carries the PROFILE and the AGENT, not just the action. The ledger is ONE global
// file shared by every profile, so without a profile column an audit trail cannot say which identity
// posted — and more than one profile is in use. `agent` is the session key that owns the tab.
//
// BOUNDEDNESS. Every full-page read result is stored verbatim, so the ledger grows in megabytes, not
// lines; the real one reached 14.7 MB before rotation existed. An unbounded audit log is one nobody
// opens and eventually one that fills a server's disk. So: roll on size, gzip the roll, keep a
// bounded history — and NEVER delete the live file, because the live file is the thing being
// audited.
package ledger

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deemwarhq/chrome-agent/internal/paths"
)

const (
	DefaultMaxBytes int64 = 8 << 20 // 8 MB
	DefaultKeep           = 5
)

// Path is the live ledger. CHROME_AGENT_LEDGER overrides it — tests and CI must never append to the
// operator's real audit trail.
func Path() string {
	if v := os.Getenv("CHROME_AGENT_LEDGER"); v != "" {
		return v
	}
	return filepath.Join(paths.ConfigDir(), "actions.ndjson")
}

// MaxBytes is the roll threshold (CHROME_AGENT_LEDGER_MAX).
func MaxBytes() int64 {
	if v := os.Getenv("CHROME_AGENT_LEDGER_MAX"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxBytes
}

// Keep is how many gzipped rolls survive (CHROME_AGENT_LEDGER_KEEP).
func Keep() int {
	if v := os.Getenv("CHROME_AGENT_LEDGER_KEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return DefaultKeep
}

// Entry is one audited action. The field order matches the bash format so old and new lines parse
// with the same reader.
type Entry struct {
	TS      string `json:"ts"`
	Profile string `json:"profile"`
	Agent   string `json:"agent"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Result  string `json:"result"`
}

// Append writes one line and rolls the file if it has outgrown MaxBytes.
//
// Every field goes through encoding/json. The bash version interpolated action/target straight into
// a printf format and only escaped `result`, so a quote or a newline in a URL produced a line no
// parser could read — in an audit log, an unparseable line is a missing line.
func Append(agent, action, target, result string) (*Entry, error) {
	e := &Entry{
		TS:      time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Profile: paths.Profile(),
		Agent:   agent,
		Action:  action,
		Target:  target,
		Result:  result,
	}
	if err := AppendEntry(e); err != nil {
		return nil, err
	}
	return e, nil
}

// AppendEntry writes a fully-formed entry (filling ts/profile when empty).
func AppendEntry(e *Entry) error {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	if e.Profile == "" {
		e.Profile = paths.Profile()
	}
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, err = RotateIfBig()
	return err
}

// ---- rotation ---------------------------------------------------------------------------------

type Roll struct {
	File  string `json:"file"`
	Bytes int64  `json:"bytes"`
}

type RotateResult struct {
	Rotated bool     `json:"rotated"`
	Reason  string   `json:"reason"`
	Roll    string   `json:"roll,omitempty"` // the .gz that now holds the old lines
	Bytes   int64    `json:"bytes"`          // size of the live file before the roll
	Pruned  []string `json:"pruned,omitempty"`
}

func Size() int64 {
	fi, err := os.Stat(Path())
	if err != nil {
		return 0
	}
	return fi.Size()
}

// RotateIfBig rolls only when the live file has outgrown MaxBytes.
func RotateIfBig() (*RotateResult, error) {
	sz := Size()
	max := MaxBytes()
	if sz <= max {
		return &RotateResult{Rotated: false, Bytes: sz, Reason: "under threshold"}, nil
	}
	return Rotate(fmt.Sprintf("size %dB > %dB", sz, max))
}

// Rotate moves the live file aside, gzips it, truncates a fresh live file, and prunes to Keep().
//
// The gzip is SYNCHRONOUS. Bash backgrounded it and then immediately listed actions-*.ndjson.gz to
// decide what to prune — a race in which the just-created roll is not yet there to be counted, so
// the prune could keep Keep()+1 or, worse, delete a roll whose .gz was still being written.
//
// The live file is re-created empty, never deleted: a missing ledger and an empty one look the same
// to `ls` and mean very different things to an auditor.
func Rotate(reason string) (*RotateResult, error) {
	p := Path()
	fi, err := os.Stat(p)
	if err != nil {
		return &RotateResult{Rotated: false, Reason: "no ledger to rotate"}, nil
	}
	dir := filepath.Dir(p)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	// Two rolls inside the same second must not overwrite each other — the stamp has one-second
	// resolution and `rotate` is also a manual verb someone can run twice.
	rolled := filepath.Join(dir, "actions-"+stamp+".ndjson")
	for i := 1; exists(rolled) || exists(rolled+".gz"); i++ {
		rolled = filepath.Join(dir, fmt.Sprintf("actions-%s-%d.ndjson", stamp, i))
	}
	if err := os.Rename(p, rolled); err != nil {
		return nil, err
	}
	// Re-create the live file immediately, before anything that can fail.
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err == nil {
		f.Close()
	}
	gz := rolled + ".gz"
	if err := gzipFile(rolled, gz); err != nil {
		// The lines are still on disk, uncompressed, under their roll name. That is worse for disk
		// and fine for the audit trail, which is the property that matters.
		return &RotateResult{Rotated: true, Reason: reason, Roll: rolled, Bytes: fi.Size()}, err
	}
	_ = os.Remove(rolled)
	pruned := prune(dir)
	return &RotateResult{Rotated: true, Reason: reason, Roll: gz, Bytes: fi.Size(), Pruned: pruned}, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// rolls returns the gzipped history, NEWEST FIRST.
//
// Ordered by MODTIME, not by name. The stamp has one-second resolution, so two rolls in the same
// second differ only by a "-N" suffix — and lexically "actions-<stamp>.ndjson.gz" sorts AFTER
// "actions-<stamp>-1.ndjson.gz" even though it is the older of the two. Name order would therefore
// prune the wrong roll. Name is only the tie-break.
func rolls(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "actions-*.ndjson.gz"))
	mod := map[string]time.Time{}
	for _, f := range m {
		if fi, err := os.Stat(f); err == nil {
			mod[f] = fi.ModTime()
		}
	}
	sort.Slice(m, func(i, j int) bool {
		a, b := mod[m[i]], mod[m[j]]
		if a.Equal(b) {
			return m[i] > m[j]
		}
		return a.After(b)
	})
	return m
}

func prune(dir string) []string {
	keep := Keep()
	all := rolls(dir)
	if len(all) <= keep {
		return nil
	}
	var pruned []string
	for _, f := range all[keep:] {
		if os.Remove(f) == nil {
			pruned = append(pruned, filepath.Base(f))
		}
	}
	return pruned
}

// ---- status -----------------------------------------------------------------------------------

type Status struct {
	Ledger   string `json:"ledger"`
	Bytes    int64  `json:"bytes"`
	Entries  int    `json:"entries"`
	MaxBytes int64  `json:"max_bytes"`
	Keep     int    `json:"keep"`
	Rolls    []Roll `json:"rolls"`
}

// StatusOf reports what the audit trail costs and how much history is behind it.
func StatusOf() *Status {
	p := Path()
	st := &Status{Ledger: p, MaxBytes: MaxBytes(), Keep: Keep(), Rolls: []Roll{}}
	if fi, err := os.Stat(p); err == nil {
		st.Bytes = fi.Size()
	}
	st.Entries = countLines(p)
	for _, f := range rolls(filepath.Dir(p)) {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		st.Rolls = append(st.Rolls, Roll{File: filepath.Base(f), Bytes: fi.Size()})
	}
	return st
}

func countLines(p string) int {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// Tail returns the last n entries, skipping lines that do not parse (an old, unescaped bash line
// among them must not take the whole read down).
func Tail(n int) []Entry {
	b, err := os.ReadFile(Path())
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	var out []Entry
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(lines[i]), &e) != nil {
			continue
		}
		out = append([]Entry{e}, out...)
	}
	return out
}
