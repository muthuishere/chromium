// Package spool speaks the fork's file-drop control protocol (ADR 0001, ADR 0009).
//
// Wire format, matching chromesendkeys.cjs byte for byte:
//
//	append lines to <spool>/.staging-<ts>-<pid>-<rand>
//	rename that file to <spool>/<ts>-<pid>-<rand>.txt          (atomic publish)
//	for verbs that answer: poll <spool>/results/<id>.json, read it, DELETE it
//
// A line is "[TAB:<tabId>|]VERB:<id>|<payload>" — the id is embedded per-verb, because its position
// differs (EVAL:<id>|<js> vs WAITFOR:<ms>|<id>|<js>), which is why callers pass a builder.
//
// Three properties here are not stylistic. Each one is a bug that shipped:
//
//   - PER-PROCESS staging name. A shared name made concurrent publish a TOCTOU race: two producers
//     appended to one file, the winner's rename carried BOTH lines (both executed), the loser's
//     rename threw ENOENT — a crash-after-execute that callers retried into duplicate writes.
//   - NEVER die between publish and result-read. If publish fails, the command may already be in
//     flight; the only honest move is to go read the result anyway and let the browser answer.
//   - A crash with no output must NOT look like "nothing happened". It returns an explicit
//     cli-crash result, because an empty string was once indistinguishable from a no-op and a
//     caller retried a write that had already executed.
package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	Dir string // the spool directory: one per profile
	Tab string // optional tabId; pins every command to this session's own tab
}

func New(dir string) *Client { return &Client{Dir: dir} }

func (c *Client) WithTab(tab string) *Client {
	return &Client{Dir: c.Dir, Tab: tab}
}

func randomID() string {
	return fmt.Sprintf("%d-%d-%s", time.Now().UnixMilli(), os.Getpid(), randSuffix())
}

func randSuffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 9)
	for i := range b {
		b[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return string(b)
}

func (c *Client) stagingPath(token string) string {
	return filepath.Join(c.Dir, ".staging-"+token)
}

// Send publishes a line that expects no answer (GOTO, CLOSETAB, NEWWINDOW…).
func (c *Client) Send(line string) error {
	token := randomID()
	if err := c.append(token, line); err != nil {
		return err
	}
	return c.publish(token)
}

// SendAndAwait publishes a line and waits for results/<id>.json.
//
// build receives the id because its position in the line is verb-specific.
func (c *Client) SendAndAwait(build func(id string) string, timeout time.Duration) (map[string]any, error) {
	id := randomID()
	token := randomID()
	if err := c.append(token, build(id)); err != nil {
		return nil, err
	}
	// IDEMPOTENCY GUARD: a failed publish does NOT mean the command did not run. Record the error,
	// then go ask the browser. Dying here is what turned executed writes into "failures" that
	// callers retried into duplicates.
	pubErr := c.publish(token)
	res, err := c.waitForResult(id, timeout)
	if err != nil && pubErr != nil {
		return nil, fmt.Errorf("publish failed (%v) and no result appeared: %w", pubErr, err)
	}
	return res, err
}

func (c *Client) append(token, line string) error {
	if c.Tab != "" {
		line = "TAB:" + c.Tab + "|" + line
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(c.stagingPath(token), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func (c *Client) publish(token string) error {
	dest := filepath.Join(c.Dir, fmt.Sprintf("%d-%d-%s.txt", time.Now().UnixMilli(), os.Getpid(), randSuffix()))
	err := os.Rename(c.stagingPath(token), dest)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		// Someone else's `push` flushed our staging file; its lines are published either way.
		return nil
	}
	return err
}

var ErrTimeout = errors.New("timed out waiting for result")

// waitForResult polls, reads, and DELETES the result file. The browser never deletes result files,
// so the reader owns cleanup — 151 orphans had piled up before that was true.
func (c *Client) waitForResult(id string, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	path := filepath.Join(c.Dir, "results", id+".json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			os.Remove(path)
			var out map[string]any
			if err := json.Unmarshal(b, &out); err != nil {
				return nil, fmt.Errorf("result %s is not JSON: %w", id, err)
			}
			return out, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("%w: %s", ErrTimeout, path)
}

// --- the verbs slice 1 needs -------------------------------------------------------------------

func (c *Client) Eval(js string, timeout time.Duration) (map[string]any, error) {
	return c.SendAndAwait(func(id string) string { return "EVAL:" + id + "|" + js }, timeout)
}

func (c *Client) EvalAsync(body string, timeout time.Duration) (map[string]any, error) {
	return c.SendAndAwait(func(id string) string { return "EVALASYNC:" + id + "|" + body }, timeout)
}

func (c *Client) ListTabs(timeout time.Duration) (map[string]any, error) {
	return c.SendAndAwait(func(id string) string { return "LISTTABS:" + id }, timeout)
}

func (c *Client) NewTab(url string, timeout time.Duration) (map[string]any, error) {
	return c.SendAndAwait(func(id string) string { return "NEWTAB:" + id + "|" + url }, timeout)
}

func (c *Client) Goto(url string) error { return c.Send("GOTO:" + url) }

// Alive answers the only question every other verb depends on: is anything servicing this spool?
func (c *Client) Alive(timeout time.Duration) bool {
	_, err := c.Eval("1", timeout)
	return err == nil
}

// Sweep quarantines stale queued commands instead of letting them REPLAY when the watcher starts.
// Yesterday's staged post firing unattended on relaunch is a real thing that happened.
func (c *Client) Sweep(olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return 0, err
	}
	q := filepath.Join(c.Dir, "quarantine")
	if err := os.MkdirAll(q, 0o755); err != nil {
		return 0, err
	}
	n := 0
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".txt") && !strings.HasPrefix(name, ".staging-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Rename(filepath.Join(c.Dir, name), filepath.Join(q, name)) == nil {
			n++
		}
	}
	return n, nil
}
