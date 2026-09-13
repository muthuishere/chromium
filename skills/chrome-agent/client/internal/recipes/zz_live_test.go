package recipes

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/deemwarhq/chrome-agent/internal/browser"
	"github.com/deemwarhq/chrome-agent/internal/spool"
)

func TestLive(t *testing.T) {
	if os.Getenv("CHROME_AGENT_LIVE") != "1" {
		t.Skip("live")
	}
	b := browser.New()
	if !b.Alive() {
		t.Fatal("browser down")
	}
	defer func() {
		if id, err := b.EnsureTab(); err == nil {
			_ = spool.New(b.Spool).Send("CLOSETAB:" + id)
			_ = os.Remove(os.Getenv("HOME") + "/.config/chrome-agent/tabids/" + browser.AgentID())
		}
	}()
	for _, key := range []string{"linkedin:feed", "hackernews:top", "reddit:listing"} {
		res, err := Run(b, key, "", Options{})
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		m, _ := res.(map[string]any)
		counts := map[string]int{}
		for k, v := range m {
			if l, ok := v.([]any); ok {
				counts[k] = len(l)
			}
		}
		u, _ := m["url"].(string)
		c, _ := json.Marshal(counts)
		t.Logf("LIVE %s url=%s lists=%s", key, u, c)
	}
}
