package dream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// TestTrailDateFromGit: "changed since the watermark" was the file's mtime,
// so a clone or checkout marked the whole trail new and the size cap then
// evicted the transcripts to keep old records (audit of 2026-09-30, SD4). A
// tracked, unmodified trail file is dated by its newest commit; an untracked
// or modified one by its mtime, as before.
func TestTrailDateFromGit(t *testing.T) {
	f := newFixture(t)
	rel := filepath.Join(".agents", "docs", "bugs", "BUG-001-old.md")
	write(t, filepath.Join(f.root, rel), "an old bug record\n", old)
	run(t, f.root, "git", "-c", "user.email=t@t", "-c", "user.name=t", "add", rel)
	cmd := exec.Command("git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "old")
	cmd.Dir = f.root
	stamp := old.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	// A checkout or clone gives the file a fresh mtime.
	if err := os.Chtimes(filepath.Join(f.root, rel), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "an old bug record") {
		t.Errorf("a trail file committed before the watermark was sent because its mtime is fresh:\n%s", labels(c))
	}
	// Modified after its commit: back to the mtime, so it is sent (the
	// raw-blob comparison in trail.Dates must see the edit; BUG-165 replaced
	// the `git status` read, whose misplaced global option once made it fail
	// silently — security review round 2, 2026-10-01).
	write(t, filepath.Join(f.root, rel), "an old bug record, edited today\n", fresh)
	c, err = collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Prompt, "edited today") {
		t.Errorf("a modified tracked record was not sent:\n%s", labels(c))
	}
}

// TestTrailDateFromGitInPartialClone: trail.Log refused every partial
// clone, so trail.Dates returned no dates and collect fell back to the mtime,
// the SD4 case, on a clone the search index reads (audit 2026-10-25, SD17).
// On a git that honours GIT_NO_LAZY_FETCH a partial clone is dated by git
// like any checkout.
func TestTrailDateFromGitInPartialClone(t *testing.T) {
	if !trail.LazyFetchSafe() {
		t.Skip("this git ignores GIT_NO_LAZY_FETCH: a partial clone is not read, and keeps its mtime")
	}
	f := newFixture(t)
	rel := filepath.Join(".agents", "docs", "bugs", "BUG-001-old.md")
	write(t, filepath.Join(f.root, rel), "an old bug record\n", old)
	run(t, f.root, "git", "-c", "user.email=t@t", "-c", "user.name=t", "add", rel)
	cmd := exec.Command("git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "old")
	cmd.Dir = f.root
	stamp := old.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	run(t, f.root, "git", "config", "remote.origin.promisor", "true")
	if !trail.PartialClone(f.root) {
		t.Fatal("the fixture is not a partial clone, so it proves nothing")
	}
	if err := os.Chtimes(filepath.Join(f.root, rel), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "an old bug record") {
		t.Errorf("a partial clone's trail file committed before the watermark was sent by its fresh mtime:\n%s", labels(c))
	}
}
