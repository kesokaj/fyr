package dream

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/styrman-harness/internal/harness"
	"github.com/kesokaj/styrman-harness/internal/search"
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
	write(t, filepath.Join(f.home, ".config", "sh-agent", "feedback.jsonl"),
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

func TestCollectInputs(t *testing.T) {
	f := newFixture(t)
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Changed {
		t.Fatal("changed = false with a fresh bug row, feedback and transcript")
	}
	text := c.Prompt
	for _, want := range []string{
		"We deploy on Fridays.",           // memory, sent whole
		"fresh bug row",                   // trail after the watermark
		"fresh feedback for this project", // feedback for this root
		"Go expert",                       // installed skill
		"please remember the deploy day",  // transcript, at the root
		"noted from a subdirectory",       // transcript, under the root
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt lacks %q\ninputs:\n%s", want, labels(c))
		}
	}
	for _, not := range []string{"an old change entry", "old feedback", "another project", "an old line before", "TOOL OUTPUT"} {
		if strings.Contains(text, not) {
			t.Errorf("prompt carries %q", not)
		}
	}
}

func TestCollectUnchanged(t *testing.T) {
	f := newFixture(t)
	later := now.Add(time.Hour)
	c, err := collect(f.home, project(f), later, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Changed {
		t.Errorf("changed = true with nothing after the watermark:\n%s", labels(c))
	}
}

// Input 4 is global: a skill updated after the watermark never makes a
// project changed on its own (D-row in the plan's Interfaces).
func TestCollectSkillAloneIsNoChange(t *testing.T) {
	f := newFixture(t)
	later := now.Add(time.Hour)
	write(t, filepath.Join(f.home, harness.AgyRoot, "skills", "golang", "SKILL.md"), "---\nname: golang\ndescription: \"Go expert, updated\"\n---\n", now.Add(2*time.Hour))
	c, err := collect(f.home, project(f), later, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if c.Changed {
		t.Error("a skill update alone made the project changed")
	}
}

func TestCollectRedactsAndHidesHome(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, ".agents", "memory", "leak.md"), "token = FAKEVALUE\nstored under "+f.home+"/x\n", fresh)
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "FAKEVALUE") {
		t.Error("a secret reached the prompt")
	}
	if strings.Contains(c.Prompt, f.home) || !strings.Contains(c.Prompt, "~/x") {
		t.Error("the home path was not rewritten to ~")
	}
}

func TestCollectCapDropsTranscriptThenTrail(t *testing.T) {
	f := newFixture(t)
	full, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	// Room for everything but the transcript.
	small := len(full.Prompt) - 40
	c, err := collect(f.home, project(f), since, small)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "please remember the deploy day") || !strings.Contains(c.Prompt, "fresh bug row") {
		t.Errorf("the transcript should go first and the trail stay:\n%s", labels(c))
	}
	if len(c.Dropped) == 0 || !strings.Contains(strings.Join(c.Dropped, " "), "transcript") {
		t.Errorf("dropped = %v, want the transcript named", c.Dropped)
	}
	// Room for less than memory alone: the project fails.
	if _, err := collect(f.home, project(f), since, 10); err == nil || !strings.Contains(err.Error(), "memory over cap") {
		t.Errorf("err = %v, want memory over cap", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now },
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	before := tree(t, f.home)
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if after := tree(t, f.home); after != before {
		t.Errorf("dry run wrote:\nbefore %s\nafter  %s", before, after)
	}
	if !strings.Contains(out.String(), f.root) || !strings.Contains(out.String(), "agy --input-format stream-json") {
		t.Errorf("dry run output lacks the project or the command:\n%s", out.String())
	}
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

func TestTargets(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(f.root, "sub")
	os.MkdirAll(sub, 0o755)
	listed := []search.Project{{Root: "/p/one"}, {Root: "/p/two"}}
	o := Options{Home: f.home, Projects: func(string) ([]search.Project, error) { return listed, nil }}

	o.Project = sub
	got, err := o.targets()
	if err != nil || len(got) != 1 || !sameDir(got[0].Root, f.root) || got[0].DocsRoot != ".agents/docs" {
		t.Errorf("--project from a subdirectory: %+v, %v", got, err)
	}
	// Both and neither are refused by Run before targets (TestRunUsage).
	o.Project, o.All = "", true
	if got, err := o.targets(); err != nil || len(got) != 2 {
		t.Errorf("--all: %+v, %v", got, err)
	}
	o.Project = t.TempDir() // not a checkout
	if _, err := o.targets(); err == nil {
		t.Error("--project outside a checkout was accepted")
	}
}

func sameDir(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestSkipReason(t *testing.T) {
	f := newFixture(t)
	if why := skipReason(project(f)); why != "no .agents/docs" && why != "" {
		t.Fatalf("fixture: %q", why)
	}
	os.MkdirAll(filepath.Join(f.root, ".agents", "docs"), 0o755)
	if why := skipReason(project(f)); why != "" {
		t.Errorf("a scaffolded checkout skipped: %q", why)
	}
	if why := skipReason(search.Project{Root: filepath.Join(f.root, "gone")}); why != "gone" {
		t.Errorf("missing root: %q", why)
	}
	plain := t.TempDir()
	if why := skipReason(search.Project{Root: plain}); why != "not a git checkout" {
		t.Errorf("plain directory: %q", why)
	}
	bare := t.TempDir()
	run(t, bare, "git", "init", "-q")
	if why := skipReason(search.Project{Root: bare, DocsRoot: "docs"}); why != "no docs" {
		t.Errorf("no docs root: %q", why)
	}
}

// TestDryRunLines drives every line the dry run prints: a skipped root, an
// unchanged project, a project over the cap, and --project's prompt.
func TestDryRunLines(t *testing.T) {
	f := newFixture(t)
	calm := newFixture(t)
	later := now.Add(time.Hour)
	write(t, statePath(f.home), `{"projects":{"`+calm.root+`":"`+later.Format(time.RFC3339)+`"}}`, old)
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now },
		Projects: func(string) ([]search.Project, error) {
			return []search.Project{project(f), project(calm), {Root: filepath.Join(f.root, "gone")}}, nil
		}}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{f.root + ": memory", "would run: agy --input-format stream-json", calm.root + ": unchanged since", "gone: skip (gone)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	o.All, o.Project = false, f.root
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[dry-run] prompt:") || !strings.Contains(out.String(), "We deploy on Fridays.") {
		t.Errorf("--project dry run did not print the prompt:\n%s", out.String())
	}
}

func TestDryRunOverCapAndDropped(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("a memory line that fills the store. ", 8000) // ~290 KB
	write(t, filepath.Join(f.root, ".agents", "memory", "huge.md"), big, fresh)
	var out bytes.Buffer
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now },
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would fail (memory over cap)") {
		t.Errorf("over-cap memory not reported:\n%s", out.String())
	}

	// A store that refuses to be read is left out and named, and the run
	// goes on with the rest.
	g := newFixture(t)
	store := filepath.Join(g.root, ".agents", "memory")
	os.RemoveAll(store)
	if err := os.Symlink(t.TempDir(), store); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	o.Projects = func(string) ([]search.Project, error) { return []search.Project{project(g)}, nil }
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "left out: memory:") || !strings.Contains(out.String(), "symlink") {
		t.Errorf("a refused store is not named:\n%s", out.String())
	}
}

func TestReadState(t *testing.T) {
	home := t.TempDir()
	if st, err := readState(home); err != nil || st.Projects == nil || len(st.Projects) != 0 {
		t.Errorf("missing file: %+v, %v", st, err)
	}
	write(t, statePath(home), "{not json", old)
	if _, err := readState(home); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("corrupt file: %v, want an error, never a silent reset", err)
	}
	write(t, statePath(home), `{"projects":{"/p":"2026-09-29T00:00:00Z"}}`, old)
	o := Options{Now: func() time.Time { return now }}
	st, err := readState(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.since(st, "/p"); !got.Equal(time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("saved watermark: %v", got)
	}
	if got := o.since(st, "/new"); !got.Equal(now.Add(-firstLook)) {
		t.Errorf("first look: %v", got)
	}
}

// TestCollectDoesNotFollowLinkedTrail: a committed symlink under the docs
// root is named and not read (security review 2026-09-30).
func TestCollectDoesNotFollowLinkedTrail(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "private.md"), "private notes elsewhere\n", fresh)
	if err := os.Symlink(elsewhere, filepath.Join(f.root, ".agents", "docs", "reviews")); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "private notes elsewhere") {
		t.Error("a linked record directory was read")
	}
	if !strings.Contains(strings.Join(c.Dropped, " "), "reviews: a symlink") {
		t.Errorf("dropped = %v", c.Dropped)
	}
}

func TestCommand(t *testing.T) {
	agy := strings.Join(command(), " ")
	if !strings.HasPrefix(agy, "agy --input-format stream-json --output-format stream-json --mode plan -p") {
		t.Errorf("command: %s", agy)
	}
}

// TestCollectDoesNotFollowALinkedAgentsDir: .agents itself as a symlink.
func TestCollectDoesNotFollowALinkedAgentsDir(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "docs", "changes", "x.md"), "outside the repository\n", fresh)
	os.RemoveAll(filepath.Join(f.root, ".agents"))
	if err := os.Symlink(elsewhere, filepath.Join(f.root, ".agents")); err != nil {
		t.Fatal(err)
	}
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "outside the repository") {
		t.Error("a linked .agents was followed")
	}
}

func TestRunUsage(t *testing.T) {
	home := t.TempDir()
	var ee *ExitError
	for _, o := range []Options{{Home: home, All: true, Project: "/x"}, {Home: home}} {
		if err := Run(o); !errors.As(err, &ee) || ee.Code != 2 {
			t.Errorf("%+v: %v, want exit 2", o, err)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a usage error wrote %v", entries)
	}
}

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

// A secret hidden behind a JSON escape in feedback.jsonl is redacted on the
// decoded line (v2.0.0 gate, security code slice).
func TestFeedbackRedactsEscapedSecret(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.home, ".config", "sh-agent", "feedback.jsonl")
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// json.Marshal writes & as \u0026, so the raw line reads "\u0026sig=…" and the
	// query-string needle matches only the decoded line (the gate's measurement).
	fmt.Fprintf(fh, `{"ts":%q,"project":%q,"original":"see https://h.example/p?x=1\u0026sig=0123456789abcdef0123456789abcdef"}`+"\n", fresh.Format(time.RFC3339), f.root)
	fh.Close()
	c, err := collect(f.home, project(f), since, maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Prompt, "0123456789abcdef0123456789abcdef") {
		t.Fatalf("the escaped key reached the prompt:\n%s", labels(c))
	}
	if !strings.Contains(c.Prompt, "fresh feedback for this project") {
		t.Error("the clean feedback line was lost")
	}
}

// A skill whose description is a block scalar (`description: >`) reached the
// dreamer as the literal `>` while dream read the key line itself; it reads
// through the installer's frontmatter parser now (project review 2026-10-09,
// row 16).
func TestSkillsReadsBlockScalarDescription(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, harness.AgyRoot, "skills", "folded")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: folded\ndescription: >\n  one line\n  and two\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := skills(home)
	if len(got) != 1 || got[0].name != "folded" {
		t.Fatalf("skills = %+v, want the one folded skill", got)
	}
	if d := got[0].description; strings.Contains(d, ">") || !strings.Contains(d, "one line") || !strings.Contains(d, "and two") {
		t.Errorf("description = %q, want the folded text", d)
	}
}
