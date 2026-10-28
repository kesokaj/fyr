package search

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// newRepo makes a git checkout whose .gitignore ignores .agents/scratch/*,
// as /init-project writes it, with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@example.invalid")
	gitT(t, root, "config", "user.name", "test")
	gitT(t, root, "config", "commit.gpgsign", "false")
	writeFile(t, root, ".gitignore", ".agents/scratch/*\n")
	writeFile(t, root, "README.md", "# demo\n\nA small demo project.\n")
	commitAll(t, root, "init")
	return root
}

// markRegistered writes root's registration marker, as `fyr index
// --register` does: without it a checkout never joins the global index
// (ADR-068).
func markRegistered(t *testing.T, home, root string) {
	t.Helper()
	if _, err := trail.Register(filepath.Join(home, harness.StateDir), root, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-q", "-m", msg)
}

func runSearch(t *testing.T, o Options) (string, error) {
	t.Helper()
	if o.Home == "" {
		o.Home = t.TempDir()
	}
	var out bytes.Buffer
	err := Search(o, &out)
	return out.String(), err
}

func mustSearch(t *testing.T, o Options) string {
	t.Helper()
	out, err := runSearch(t, o)
	if err != nil {
		t.Fatalf("search %+v: %v\n%s", o, err, out)
	}
	return out
}

func openDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+projectDB(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
