package dream

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

var (
	now   = time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)
	since = now.Add(-24 * time.Hour)
	old   = now.Add(-72 * time.Hour)
	fresh = now.Add(-1 * time.Hour)
)

// fixture is a scaffolded project and a home, with every input kind present
// once as changed and once as unchanged.
type fixture struct{ home, root string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{home: t.TempDir(), root: t.TempDir()}
	run(t, f.root, "git", "init", "-q")
	write(t, filepath.Join(f.root, ".gitignore"), ".agents/memory\n.agents/scratch/*\n", old)
	write(t, filepath.Join(f.root, ".agents", "memory", "MEMORY.md"), "- [deploy-day](deploy-day.md) — fridays\n", old)
	write(t, filepath.Join(f.root, ".agents", "memory", "deploy-day.md"), "---\nname: deploy-day\n---\n\nWe deploy on Fridays.\n", old)
	write(t, filepath.Join(f.root, ".agents", "docs", "bugs", "INDEX.md"), "| BUG-001 | fresh bug row |\n", fresh)
	write(t, filepath.Join(f.root, ".agents", "docs", "changes", "2026-09-01-0000-old.md"), "an old change entry\n", old)
	write(t, filepath.Join(f.home, harness.AgyRoot, "skills", "golang", "SKILL.md"), "---\nname: golang\ndescription: \"Go expert\"\n---\n", old)
	fb := func(ts time.Time, project, text string) string {
		b, _ := json.Marshal(map[string]string{"ts": ts.Format(time.RFC3339), "project": project, "original": text})
		return string(b) + "\n"
	}
	write(t, filepath.Join(f.home, harness.StateDir, "feedback.jsonl"),
		fb(fresh, f.root, "fresh feedback for this project")+fb(old, f.root, "old feedback")+fb(fresh, "/elsewhere", "another project's feedback"), fresh)
	// The root's conversation is older than the subdirectory's, so the cap
	// drops it first, and long enough that dropping it frees more than the
	// line naming it costs (TestCollectCapDropsTranscriptThenTrail).
	agyConversations(t, f.home, map[string]agyConv{
		"at-root":   {f.root, fresh.Add(-10 * time.Minute), []agyStep{{typ: 14, text: "please remember the deploy day"}, {typ: 15, text: "noted: the deploy day is Friday, and every release waits for it"}, {typ: 132, text: "TOOL OUTPUT that is not prose at all"}}},
		"under":     {filepath.Join(f.root, "sub"), fresh, []agyStep{{typ: 15, text: "noted from a subdirectory"}}},
		"old-one":   {f.root, old, []agyStep{{typ: 14, text: "an old line before the watermark"}}},
		"elsewhere": {"/elsewhere", fresh, []agyStep{{typ: 14, text: "another project's line of text"}}},
	})
	return f
}

// agyConv is one Antigravity conversation: its workspace, when it was last
// modified, and its steps.
type agyConv struct {
	workspace string
	modified  time.Time
	steps     []agyStep
}

// agyStep is one step: 14 the user's input, 15 the model's reply, 132 a
// tool's output (the D8 probe's step types). A tool step may name its tool
// and arguments in the metadata, where the metrics reader finds them.
type agyStep struct {
	typ        int
	text       string
	tool, args string
}

// agyConversations writes the two databases Antigravity keeps, in the shapes
// internal/search and internal/metrics read: summaries map conversations to
// workspaces, and each conversation's steps hold a protobuf blob with the text
// as one field. A second call adds to the summaries.
func agyConversations(t *testing.T, home string, convs map[string]agyConv) {
	t.Helper()
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	exec := func(path string, stmts ...func(*sql.DB) error) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, f := range stmts {
			if err := f(db); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	exec(filepath.Join(dir, "conversation_summaries.db"), func(db *sql.DB) error {
		_, err := db.Exec("CREATE TABLE IF NOT EXISTS conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')")
		return err
	}, func(db *sql.DB) error {
		for id, c := range convs {
			if _, err := db.Exec(`INSERT INTO conversation_summaries(conversation_id, last_modified_time, workspace_uris) VALUES(?,?,?)`, id, c.modified.UTC().Format("2006-01-02 15:04:05.000000000+00:00"), `["file://`+c.workspace+`"]`); err != nil {
				return err
			}
		}
		return nil
	})
	for id, c := range convs {
		exec(filepath.Join(dir, "conversations", id+".db"), func(db *sql.DB) error {
			if _, err := db.Exec("CREATE TABLE steps (`idx` integer, `step_type` integer, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `step_payload` blob)"); err != nil {
				return err
			}
			for i, st := range c.steps {
				var meta []byte
				if st.tool != "" {
					meta = pbField(4, append(pbField(2, []byte(st.tool)), pbField(3, []byte(st.args))...))
				}
				if _, err := db.Exec(`INSERT INTO steps(idx, step_type, metadata, step_payload) VALUES(?,?,?,?)`, i, st.typ, meta, pbField(1, []byte(st.text))); err != nil {
					return err
				}
			}
			return nil
		})
	}
}

// pbField is protobuf wire bytes: a length-delimited field n holding b.
func pbField(n int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(n<<3|2))
	return append(binary.AppendUvarint(out, uint64(len(b))), b...)
}

func write(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func project(f fixture) search.Project {
	return search.Project{Root: f.root, Name: filepath.Base(f.root), DocsRoot: ".agents/docs"}
}

func labels(c Collected) string {
	var b strings.Builder
	for _, in := range c.Inputs {
		b.WriteString(in.Kind + ":" + in.Label + "\n")
	}
	return b.String()
}

func tree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			b.WriteString(p + " ")
		}
		return nil
	})
	return b.String()
}

func sameDir(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func noScopes(string) ([]string, error) { return nil, nil }
