// Package pacing spaces out what an identity does on a site, the way a person would.
//
// Every action is one of three classes, and each class has its own gap and daily cap:
//
//	read    look at something       short gap, no cap
//	react   like / repost / upvote  longer gap, capped per day
//	mutate  post / comment / delete longest gap, tightly capped
//
// The unit is (profile, domain): two sessions driving the SAME profile share one budget — that is
// the whole point, because the site sees one account, not two lanes — while two profiles are two
// people and never slow each other down. State lives on disk under a file lock, so parallel CLI
// invocations serialize instead of both slipping through the same gap.
//
// The jitter is drawn ONCE, when an action is recorded, and stored as the next allowed time. A
// caller that asks twice gets the same answer, and a wait is a real wait rather than a re-roll.
//
// Short waits are served (the call blocks); a wait longer than MaxWait, or a spent daily cap, is
// REFUSED with a retry_after. Refusal is never retried here: a write that is re-sent blindly is how
// a double post happens.
package pacing

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Read   = "read"
	React  = "react"
	Mutate = "mutate"
)

// Policy is one class's pacing. Seconds, because a site file is edited by hand.
type Policy struct {
	MinSeconds float64 `json:"min_seconds"`
	MaxSeconds float64 `json:"max_seconds"`
	DailyCap   int     `json:"daily_cap"` // 0 = no cap
}

// Defaults are deliberately conservative for the classes a site can ban an account over.
func Defaults() map[string]Policy {
	return map[string]Policy{
		Read:   {MinSeconds: 3, MaxSeconds: 8},
		React:  {MinSeconds: 20, MaxSeconds: 60, DailyCap: 30},
		Mutate: {MinSeconds: 120, MaxSeconds: 300, DailyCap: 10},
	}
}

// ValidClass reports whether c is one of the three classes.
func ValidClass(c string) bool { return c == Read || c == React || c == Mutate }

// Config is ~/.config/chrome-agent/pacing.json. It outranks the site definitions, so an operator
// can slow a site down (or loosen it) on one machine without editing a shipped file.
type Config struct {
	Default map[string]Policy            `json:"default,omitempty"`
	Sites   map[string]map[string]Policy `json:"sites,omitempty"`
	// MaxWaitSeconds is the longest wait served inline before refusing. Default 120.
	MaxWaitSeconds float64 `json:"max_wait_seconds,omitempty"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return &c, nil
}

// Resolve picks the policy for (domain, class). Precedence, most specific first:
// pacing.json sites[domain] > the site definition's pacing > pacing.json default > Defaults().
func Resolve(cfg *Config, siteDef map[string]Policy, domain, class string) Policy {
	if cfg != nil {
		if p, ok := cfg.Sites[domain][class]; ok {
			return p
		}
	}
	if p, ok := siteDef[class]; ok {
		return p
	}
	if cfg != nil {
		if p, ok := cfg.Default[class]; ok {
			return p
		}
	}
	return Defaults()[class]
}

func (c *Config) MaxWait() time.Duration {
	if v := os.Getenv("CHROME_AGENT_MAX_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if c != nil && c.MaxWaitSeconds > 0 {
		return time.Duration(c.MaxWaitSeconds * float64(time.Second))
	}
	return 120 * time.Second
}

// State is one (profile, domain)'s ledger of pacing, per class.
type State struct {
	Day   string               `json:"day"` // local calendar day the counts belong to
	Count map[string]int       `json:"count"`
	Next  map[string]time.Time `json:"next"` // earliest time the next action of that class may run
	Last  map[string]time.Time `json:"last"`
}

// Decision is what the gate concluded, and is printed on refusal.
type Decision struct {
	Domain     string    `json:"domain"`
	Class      string    `json:"class"`
	Allowed    bool      `json:"allowed"`
	Wait       float64   `json:"waited_seconds,omitempty"`
	RetryAfter float64   `json:"retry_after_seconds,omitempty"`
	RetryAt    time.Time `json:"retry_at,omitempty"`
	CountToday int       `json:"count_today"`
	DailyCap   int       `json:"daily_cap,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Policy     Policy    `json:"policy"`
}

// Decide is the pure core: given state and policy at time now, may the action run, and after how
// long? It does not mutate state.
func Decide(st *State, p Policy, class string, now time.Time, maxWait time.Duration) Decision {
	d := Decision{Class: class, Policy: p, DailyCap: p.DailyCap}
	st.rollDay(now)
	d.CountToday = st.Count[class]
	if p.DailyCap > 0 && d.CountToday >= p.DailyCap {
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		d.RetryAt = midnight
		d.RetryAfter = midnight.Sub(now).Seconds()
		d.Reason = fmt.Sprintf("daily cap reached: %d/%d %s actions today", d.CountToday, p.DailyCap, class)
		return d
	}
	wait := st.Next[class].Sub(now)
	if wait <= 0 {
		d.Allowed = true
		return d
	}
	if wait > maxWait {
		d.RetryAt = st.Next[class]
		d.RetryAfter = wait.Seconds()
		d.Reason = fmt.Sprintf("next %s allowed in %.0fs, longer than the %gs this call will wait", class, wait.Seconds(), maxWait.Seconds())
		return d
	}
	d.Allowed = true
	d.Wait = wait.Seconds()
	return d
}

// Record books an action at time at: bump today's count and draw the next gap.
func (st *State) Record(p Policy, class string, at time.Time, rng *rand.Rand) {
	st.rollDay(at)
	st.Count[class]++
	st.Last[class] = at
	gap := p.MinSeconds
	if p.MaxSeconds > p.MinSeconds {
		gap += rng.Float64() * (p.MaxSeconds - p.MinSeconds)
	}
	st.Next[class] = at.Add(time.Duration(gap * float64(time.Second)))
}

func (st *State) rollDay(now time.Time) {
	day := now.Format("2006-01-02")
	if st.Count == nil {
		st.Count = map[string]int{}
	}
	if st.Next == nil {
		st.Next = map[string]time.Time{}
	}
	if st.Last == nil {
		st.Last = map[string]time.Time{}
	}
	if st.Day != day {
		st.Day, st.Count = day, map[string]int{}
	}
}

// Gate is the on-disk, locked version of Decide + Record.
type Gate struct {
	Dir     string // state root, one subdir per profile key
	Profile string // profile key (already hashed/derived by the caller)
	Config  *Config
	Now     func() time.Time
	Sleep   func(time.Duration)
	Rand    *rand.Rand
}

func (g *Gate) statePath(domain string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", "..", "_").Replace(domain)
	return filepath.Join(g.Dir, g.Profile, safe+".json")
}

// Acquire blocks for a short gap, refuses a long one, and books the action when allowed. The lock
// is held across the wait, so a second invocation queues behind the first instead of sharing its gap.
func (g *Gate) Acquire(domain, class string, siteDef map[string]Policy) (Decision, error) {
	if !ValidClass(class) {
		return Decision{}, fmt.Errorf("unknown pacing class %q", class)
	}
	now, sleep, rng := g.Now, g.Sleep, g.Rand
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	path := g.statePath(domain)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Decision{}, err
	}
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		return Decision{}, err
	}
	defer unlock()

	st := &State{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, st)
	}
	p := Resolve(g.Config, siteDef, domain, class)
	d := Decide(st, p, class, now(), g.Config.MaxWait())
	d.Domain = domain
	if !d.Allowed {
		return d, nil
	}
	if d.Wait > 0 {
		sleep(time.Duration(d.Wait * float64(time.Second)))
	}
	st.Record(p, class, now(), rng)
	d.CountToday = st.Count[class]
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return d, err
	}
	return d, os.Rename(tmp, path)
}

// Peek reports the state of every class for domain without booking anything.
func (g *Gate) Peek(domain string, siteDef map[string]Policy) map[string]Decision {
	st := &State{}
	if b, err := os.ReadFile(g.statePath(domain)); err == nil {
		_ = json.Unmarshal(b, st)
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	out := map[string]Decision{}
	for _, c := range []string{Read, React, Mutate} {
		d := Decide(st, Resolve(g.Config, siteDef, domain, c), c, now(), 0)
		d.Domain = domain
		if d.Allowed {
			d.Reason = "ready"
		} else if d.Reason == "" {
			d.Reason = "waiting"
		}
		out[c] = d
	}
	return out
}
