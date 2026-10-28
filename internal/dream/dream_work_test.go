package dream

import (
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// A registered checkout at $HOME is not in use because the dream's own
// conversation sits in its config directory (visible dream plan D5); a
// session in the checkout itself still is (the positive control).
func TestDreamWorkNotInUse(t *testing.T) {
	home := t.TempDir()
	run(t, home, "git", "init", "-q")
	now := time.Now()
	p := search.Project{Root: home, Name: "home"}
	agyConversations(t, home, map[string]agyConv{
		"dream": {harness.DreamWork(home), now.Add(-time.Minute), []agyStep{{typ: 14, text: "dreaming: home and the rest of it"}}},
	})
	if why := activityIn(home, home, p, now); why != "" {
		t.Errorf("in use by the dream's own conversation: %q", why)
	}
	agyConversations(t, home, map[string]agyConv{
		"session": {home, now.Add(-time.Minute), []agyStep{{typ: 14, text: "a person working in the home checkout"}}},
	})
	if why := activityIn(home, home, p, now); !strings.HasPrefix(why, "an Antigravity session at ") {
		t.Errorf("a session in the checkout: %q, want in use", why)
	}
}
