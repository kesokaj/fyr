package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// markRegistered is `fyr index --register` in root under home (ADR-068).
func markRegistered(t *testing.T, home, root string) {
	t.Helper()
	if _, err := trail.Register(filepath.Join(home, harness.StateDir), root, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// An ingest over a checkout that holds .agents/trail.jsonl but was never
// registered stores its sessions and leaves no scope of it in brain.db, so
// neither the dream nor --global reads its trail; registered, its trail
// joins the metrics events in memory without writing brain.db (audit-choices
// SD49, § Interfaces "The metrics join", ADR-091).
func TestIngestJoinsRegisteredTrailOnly(t *testing.T) {
	home, root := agyFixture(t)
	os.RemoveAll(filepath.Join(root, ".agents", "docs")) // a migrated checkout
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	other := t.TempDir() // another home's add+export writes the checkout's trail.jsonl
	o := brain.Options{Home: other, Dir: root, Now: func() time.Time { return time.Unix(1791367205, 0) }}
	if _, err := brain.Add(o, brain.AddInput{Kind: "handoff", Title: "a stranger's handoff", Body: strings.NewReader("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Export(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents", "trail.jsonl")); err != nil {
		t.Fatal(err)
	}
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	if r := ingestT(t, s, Options{Home: home}); r.Sessions != 2 {
		t.Fatalf("unregistered: %+v, want its 2 sessions", r)
	}
	if got := events(t, s); len(got) != 0 {
		t.Errorf("unregistered trail.jsonl joined events: %v", got)
	}
	if scopes, err := brain.Scopes(home); err != nil || len(scopes) != 0 {
		t.Errorf("an unregistered trail.jsonl became a brain scope: %q %v", scopes, err)
	}
	markRegistered(t, home, root)
	ingestT(t, s, Options{Home: home})
	if got := events(t, s); len(got) != 1 || !strings.HasPrefix(got[0], "C1 handoff ") {
		t.Errorf("a registered trail.jsonl did not join events: %v", got)
	}
	if scopes, err := brain.Scopes(home); err != nil || len(scopes) != 0 {
		t.Errorf("a read-only metrics join wrote a brain scope: %q %v", scopes, err)
	}
}
