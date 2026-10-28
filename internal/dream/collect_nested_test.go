package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNestedCheckoutTranscriptNotCollected pins ledger item 77: a session in
// an unregistered checkout nested inside the registered root is never sent
// to the model; it is named by its ID among the left-out inputs, without its
// path, while the root's own sessions still go.
func TestNestedCheckoutTranscriptNotCollected(t *testing.T) {
	f := newFixture(t)
	nested := filepath.Join(f.root, "vendor", "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, nested, "git", "init", "-q")
	agyConversations(t, f.home, map[string]agyConv{
		"inner": {nested, fresh.Add(-5 * time.Minute), []agyStep{{typ: 14, text: "NESTED-CANARY text from a checkout nobody registered"}}},
	})
	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "NESTED-CANARY") {
		t.Errorf("the nested checkout's session reached the prompt:\n%s", labels(c))
	}
	if !strings.Contains(c.Prompt, "please remember the deploy day") {
		t.Errorf("the root's own session was lost:\n%s", labels(c))
	}
	want := "transcript antigravity inner (its workspace is a nested checkout, not this project)"
	if !strings.Contains(strings.Join(c.Dropped, "\n"), want) {
		t.Errorf("dropped = %q, want %q", c.Dropped, want)
	}
	if strings.Contains(c.Prompt, "vendor/inner") || strings.Contains(strings.Join(c.Dropped, "\n"), "vendor/inner") {
		t.Errorf("the nested checkout's path was named: %q", c.Dropped)
	}
}
