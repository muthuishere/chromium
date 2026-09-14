package pacing

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 14, 10, 0, 0, 0, time.Local)

func TestFirstActionRunsImmediately(t *testing.T) {
	d := Decide(&State{}, Defaults()[Mutate], Mutate, t0, 2*time.Minute)
	if !d.Allowed || d.Wait != 0 {
		t.Fatalf("a fresh identity must not wait: %+v", d)
	}
}

func TestJitterIsDrawnOnceAndServedAsAWait(t *testing.T) {
	st := &State{}
	p := Policy{MinSeconds: 20, MaxSeconds: 60}
	st.Record(p, React, t0, rand.New(rand.NewSource(1)))
	gap := st.Next[React].Sub(t0)
	if gap < 20*time.Second || gap > 60*time.Second {
		t.Fatalf("gap outside policy: %v", gap)
	}
	a := Decide(st, p, React, t0.Add(5*time.Second), 2*time.Minute)
	b := Decide(st, p, React, t0.Add(5*time.Second), 2*time.Minute)
	if !a.Allowed || a.Wait != b.Wait || a.Wait <= 0 {
		t.Fatalf("asking twice must give the same wait: %+v %+v", a, b)
	}
}

func TestLongWaitIsRefusedWithRetryAfter(t *testing.T) {
	st := &State{}
	p := Policy{MinSeconds: 300, MaxSeconds: 300}
	st.Record(p, Mutate, t0, rand.New(rand.NewSource(1)))
	d := Decide(st, p, Mutate, t0.Add(10*time.Second), 2*time.Minute)
	if d.Allowed || d.RetryAfter < 289 || d.RetryAfter > 291 {
		t.Fatalf("a 290s wait must be refused with retry_after≈290: %+v", d)
	}
}

func TestClassesDoNotShareAGap(t *testing.T) {
	st := &State{}
	st.Record(Defaults()[Mutate], Mutate, t0, rand.New(rand.NewSource(1)))
	if d := Decide(st, Defaults()[Read], Read, t0.Add(time.Second), 2*time.Minute); !d.Allowed || d.Wait != 0 {
		t.Fatalf("a post must not make the next READ wait minutes: %+v", d)
	}
}

func TestDailyCapRefusesUntilMidnightAndResetsNextDay(t *testing.T) {
	st := &State{}
	p := Policy{DailyCap: 2}
	r := rand.New(rand.NewSource(1))
	st.Record(p, React, t0, r)
	st.Record(p, React, t0, r)
	d := Decide(st, p, React, t0.Add(time.Minute), 2*time.Minute)
	if d.Allowed || d.RetryAt.Day() != 15 {
		t.Fatalf("cap must refuse until tomorrow: %+v", d)
	}
	if d := Decide(st, p, React, t0.Add(24*time.Hour), 2*time.Minute); !d.Allowed {
		t.Fatalf("cap must reset on a new day: %+v", d)
	}
}

func TestResolvePrecedence(t *testing.T) {
	site := map[string]Policy{React: {DailyCap: 5}}
	cfg := &Config{Default: map[string]Policy{React: {DailyCap: 7}, Read: {DailyCap: 9}},
		Sites: map[string]map[string]Policy{"x.com": {React: {DailyCap: 1}}}}
	if Resolve(cfg, site, "x.com", React).DailyCap != 1 {
		t.Fatal("machine config for the site must win")
	}
	if Resolve(cfg, site, "linkedin.com", React).DailyCap != 5 {
		t.Fatal("site definition must beat the machine default")
	}
	if Resolve(cfg, nil, "linkedin.com", Read).DailyCap != 9 {
		t.Fatal("machine default must beat builtin defaults")
	}
	if Resolve(nil, nil, "linkedin.com", Mutate).DailyCap != 10 {
		t.Fatal("builtin default")
	}
}

// Two lanes on the same profile must serialize: the second waits out the gap the first booked,
// instead of both reading "ready" at once.
func TestGateSerializesConcurrentCallers(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	clock := t0
	slept := []time.Duration{}
	mk := func() *Gate {
		return &Gate{Dir: dir, Profile: "p", Config: &Config{},
			Now:   func() time.Time { mu.Lock(); defer mu.Unlock(); return clock },
			Sleep: func(d time.Duration) { mu.Lock(); slept = append(slept, d); clock = clock.Add(d); mu.Unlock() },
			Rand:  rand.New(rand.NewSource(1))}
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, err := mk().Acquire("x.com", Read, nil); err != nil || !d.Allowed {
				t.Errorf("acquire: %+v %v", d, err)
			}
		}()
	}
	wg.Wait()
	if len(slept) != 1 || slept[0] < 3*time.Second {
		t.Fatalf("exactly one caller must have waited out the read gap, slept=%v", slept)
	}
	peek := mk().Peek("x.com", nil)
	if peek[Read].CountToday != 2 {
		t.Fatalf("both reads must be booked: %+v", peek[Read])
	}
}
