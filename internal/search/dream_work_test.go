package search

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// A registered checkout at $HOME holds the config directory, so the dream's
// own conversations are under its root; they are never its own (visible
// dream plan D5), and are named as the dream's. The home is passed as
// written and through a symlink while the root is the resolved checkout
// (D15): the stored workspace is the resolved form either way.
func TestDreamWorkNotAProjectsConversation(t *testing.T) {
	home := newRepo(t)
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	work := harness.DreamWork(home)
	type conv = struct {
		workspace string
		modified  string
		payloads  [][]byte
	}
	const at = "2026-10-28 10:00:00.000000000+00:00"
	agyFixture(t, home, map[string]conv{
		"project":   {home, at, [][]byte{blob("a session in the home checkout itself")}},
		"dream":     {work, at, [][]byte{blob("dreaming: home, the earlier dream's text")}},
		"dream-sub": {filepath.Join(work, "sub"), at, [][]byte{blob("dreaming: home, from below its directory")}},
	})
	since := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	for _, h := range []string{home, link} {
		got, leftOut, err := AgyConversationText(h, home, since)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != "project" {
			t.Errorf("home %s: conversations %v, want the project's only", h, got)
		}
		slices.Sort(leftOut)
		if want := []string{"dream (" + whyDream + ")", "dream-sub (" + whyDream + ")"}; !slices.Equal(leftOut, want) {
			t.Errorf("home %s: left out %q, want %q", h, leftOut, want)
		}
	}
}
