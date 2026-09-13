package tabs

import (
	"testing"
	"time"
)

func TestDecideNeverTouchesAHumansTab(t *testing.T) {
	now := time.Now()
	live := []Tab{{TabID: "human-1"}, {TabID: "human-2"}, {TabID: "old-lane"}}
	regs := []Registration{{Agent: "test-123", TabID: "old-lane", LastUsed: now.Add(-48 * time.Hour)}}

	p := Decide(live, regs, 24*time.Hour, now, nil)
	if len(p.Close) != 1 || p.Close[0].TabID != "old-lane" {
		t.Fatalf("expected only the idle registered tab to close, got %+v", p.Close)
	}
	if p.Unowned != 2 {
		t.Fatalf("unregistered tabs must be counted and left alone, got %d", p.Unowned)
	}
}

func TestDecideKeepsRecentlyUsedAndTheCallersOwnTab(t *testing.T) {
	now := time.Now()
	live := []Tab{{TabID: "busy"}, {TabID: "mine"}}
	regs := []Registration{
		{Agent: "busy-lane", TabID: "busy", LastUsed: now.Add(-time.Minute)},
		{Agent: "me", TabID: "mine", LastUsed: now.Add(-72 * time.Hour)},
	}
	p := Decide(live, regs, 24*time.Hour, now, map[string]bool{"me": true})
	if len(p.Close) != 0 {
		t.Fatalf("closed a recent tab or the caller's own: %+v", p.Close)
	}
	if p.Kept != 2 {
		t.Fatalf("expected both kept, got %d", p.Kept)
	}
}

func TestDecideForgetsRegistrationsWhoseTabIsGone(t *testing.T) {
	now := time.Now()
	regs := []Registration{{Agent: "ghost", TabID: "closed-long-ago", LastUsed: now}}
	p := Decide(nil, regs, time.Hour, now, nil)
	if len(p.Forget) != 1 || len(p.Close) != 0 {
		t.Fatalf("a registration pointing at a closed tab should be forgotten, not closed: %+v", p)
	}
}
