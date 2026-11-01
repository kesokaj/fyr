package auto

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

// All seven learning classes render valid Kickoff templates with bare Files:
// for the six drafts-only classes and .agents/skills/<new>/ for distill.
func TestTasksKickoffTemplates(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		class      string
		entries    []string
		id         string
		count      int
		wantFiles  string
		wantSubstr string
	}{
		{ClassMemoryConsolidation, nil, "", 12, "Files:\n", "12 memory record(s)"},
		{ClassRetroLite, nil, "", 3, "Files:\n", "3 redacted conversation(s) under .agents/scratch/auto-input/"},
		{ClassMemoryCandidates, nil, "", 3, "Files:\n", "3 redacted conversation(s) under .agents/scratch/auto-input/"},
		{ClassDistill, []string{".agents/skills/auto-distilled/"}, "", 4, "Files: .agents/skills/auto-distilled/\n", "4 sessions"},
		{ClassMissingSkill, nil, "PROPOSAL-2026-11-01-1000-missing-skill", 1, "Files:\n", "skills.sh"},
		{ClassContextAdequacy, nil, "", 5, "Files:\n", "5 session(s) above the 30-day median"},
		{ClassScaffoldDrift, nil, "", 2, "Files:\n", "2 unedited scaffold file(s)"},
	}

	for _, tc := range cases {
		task, err := PrepareTask(tc.class, LevelProject, tc.entries, tc.id, tc.count)
		if err != nil {
			t.Fatalf("PrepareTask(%s): %v", tc.class, err)
		}
		k, err := BuildKickoff(now, task)
		if err != nil {
			t.Fatalf("BuildKickoff(%s): %v", tc.class, err)
		}
		if !strings.Contains(k.Text, tc.wantFiles) {
			t.Errorf("%s Kickoff missing %q:\n%s", tc.class, tc.wantFiles, k.Text)
		}
		if !strings.Contains(k.Text, tc.wantSubstr) {
			t.Errorf("%s Kickoff missing %q:\n%s", tc.class, tc.wantSubstr, k.Text)
		}
		if !strings.Contains(k.Text, ".agents/scratch/agents/auto-drafts.jsonl") {
			t.Errorf("%s Kickoff missing drafts path:\n%s", tc.class, k.Text)
		}
	}
}

// Each of the seven learning classes triggers when its condition holds.
func TestTasksTriggers(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	t.Run("memory-consolidation", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		bo := brain.Options{Home: home, Dir: root, Now: func() time.Time { return now }}
		for i := 1; i <= 10; i++ {
			if _, err := brain.MemoryAdd(bo, brain.MemoryEntry{
				Type: "project", Name: fmt.Sprintf("mem-%02d", i), Description: "note",
			}, strings.NewReader("fact")); err != nil {
				t.Fatal(err)
			}
		}
		r := &run{o: Options{Home: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassMemoryConsolidation || task.Count != 10 {
			t.Fatalf("got task %+v, want memory-consolidation count 10", task)
		}
	})

	t.Run("retro-lite-then-memory-candidates", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		convDir := filepath.Join(home, ".gemini", "antigravity", "conversations")
		if err := os.MkdirAll(convDir, 0o700); err != nil {
			t.Fatal(err)
		}
		convFile := filepath.Join(convDir, "session-1.jsonl")
		if err := os.WriteFile(convFile, []byte(`{"role":"user","content":"working in `+root+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassRetroLite || task.Count != 1 {
			t.Fatalf("first run: got %+v, want retro-lite count 1", task)
		}
		// Simulate retro-lite completing: on the next run, memory-candidates triggers.
		p.LastClass = ClassRetroLite
		p.LastRetroLite = now
		p.PendingMemoryCandidates = 1
		task2 := r.pickProjectTask(root, &p, nil)
		if task2 == nil || task2.Class != ClassMemoryCandidates || task2.Count != 1 {
			t.Fatalf("run after retro-lite: got %+v, want memory-candidates count 1", task2)
		}
	})

	t.Run("missing-skill", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		g := brain.Options{Home: home, Global: true, Now: func() time.Time { return now }}
		id, err := brain.Add(g, brain.AddInput{
			Kind:    "proposal",
			Title:   "Missing skill: openapi-lint",
			Project: root,
			Body:    strings.NewReader("missing-skill: openapi-lint\nNeed an OpenAPI linter skill."),
		})
		if err != nil {
			t.Fatal(err)
		}
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassMissingSkill || task.ID != id {
			t.Fatalf("got %+v, want missing-skill with ID %s", task, id)
		}
	})

	t.Run("distill", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		writeFakeMetricsDB(t, home, root, []string{"search,record", "search,record", "search,record"}, []int{0, 0, 0})
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassDistill || task.Count != 3 || len(task.Kept) != 1 || !strings.HasPrefix(task.Kept[0], ".agents/skills/auto-distilled-") {
			t.Fatalf("got %+v, want distill count 3", task)
		}
	})

	t.Run("context-adequacy", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		writeFakeMetricsDB(t, home, root, []string{"search", "search", "search", "search", "search"}, []int{0, 1, 1, 5, 6})
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassContextAdequacy || task.Count != 2 {
			t.Fatalf("got %+v, want context-adequacy count 2", task)
		}
	})

	t.Run("scaffold-drift", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		// Write an unedited placeholder SDD.md from an older scaffold.
		if err := os.WriteFile(filepath.Join(root, "SDD.md"), []byte("# SDD\n\n<!-- TODO: fill in architecture -->\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", root, "add", "SDD.md").CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}
		if out, err := exec.Command("git", "-C", root, "commit", "-q", "-m", "init scaffold").CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassScaffoldDrift || task.Count != 1 {
			t.Fatalf("got %+v, want scaffold-drift count 1", task)
		}
	})
}

func writeFakeMetricsDB(t *testing.T, home, root string, verbs []string, frictions []int) {
	t.Helper()
	mp := metrics.Path(home)
	s, err := metrics.Open(mp)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", search.DSN(mp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC).UnixMilli()
	for i := range verbs {
		sid := fmt.Sprintf("sess-%d", i)
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES(?, 'agy', ?, ?, ?)`,
			sid, root, start+int64(i*60000), start+int64((i+1)*60000)); err != nil {
			t.Fatal(err)
		}
		for n, v := range strings.Split(verbs[i], ",") {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			ext := fmt.Sprintf("step-v-%d", n)
			if _, err := db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES(?, ?, ?, 'tool', 'Bash')`,
				sid, ext, start+int64(n*1000)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES(?, ?, ?, ?)`,
				sid, ext, n, v); err != nil {
				t.Fatal(err)
			}
		}
		for f := 0; f < frictions[i]; f++ {
			ext := fmt.Sprintf("step-f-%d", f)
			if _, err := db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, denied) VALUES(?, ?, ?, 'tool', 'Bash', 1)`,
				sid, ext, start+int64(10000+f*1000)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Learning task inputs written to .agents/scratch/auto-input/context.md pass
// through the dream's redactor first (R8 8.3, D19 item 2) and are removed
// after the worker finishes.
func TestTasksRedactedLearningInput(t *testing.T) {
	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	tok := "ghp_" + strings.Repeat("Z", 36)
	bo := brain.Options{Home: home, Dir: root, Now: func() time.Time { return now }}
	if _, err := brain.MemoryAdd(bo, brain.MemoryEntry{
		Type: "project", Name: "has-secret", Description: "note with secret",
	}, strings.NewReader("never leak "+tok+" to the model")); err != nil {
		t.Fatal(err)
	}
	g := brain.Options{Home: home, Global: true, Now: func() time.Time { return now }}
	if _, err := brain.Add(g, brain.AddInput{
		Kind: "proposal", Title: "Proposal with secret", Project: root,
		Body: strings.NewReader("proposal token " + tok),
	}); err != nil {
		t.Fatal(err)
	}
	writeFakeMetricsDB(t, home, root, []string{"search,record"}, []int{1})
	convDir := filepath.Join(home, ".gemini", "antigravity", "conversations")
	if err := os.MkdirAll(convDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "c1.jsonl"), []byte(`{"project":"`+root+`","text":"token `+tok+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{Level: LevelProject}}
	var p Project
	task := Task{Class: ClassRetroLite, Level: LevelProject, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 1}
	if _, err := r.writeLearningInput(root, &p, task); err != nil {
		t.Fatalf("writeLearningInput: %v", err)
	}
	inPath := filepath.Join(root, ".agents", "scratch", "auto-input", "context.md")
	data, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", inPath, err)
	}
	if strings.Contains(string(data), tok) {
		t.Errorf("secret survived redaction in learning input:\n%s", string(data))
	}
	if !strings.Contains(string(data), "[redacted by fyr]") {
		t.Errorf("expected [redacted by fyr] in learning input:\n%s", string(data))
	}
	if !strings.Contains(string(data), "## Metric numbers") {
		t.Errorf("expected ## Metric numbers in learning input:\n%s", string(data))
	}
	cleanLearningInput(root)
	if _, err := os.Stat(inPath); !os.IsNotExist(err) {
		t.Errorf("cleanLearningInput left %s", inPath)
	}
	k, err := WriteKickoff(home, now, task)
	if err != nil {
		t.Fatalf("WriteKickoff: %v", err)
	}
	if err := r.prepareLearningWorker(root, &p, task, k.Path); err != nil {
		t.Fatalf("prepareLearningWorker: %v", err)
	}
	kData, err := os.ReadFile(k.Path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", k.Path, err)
	}
	if !strings.Contains(string(kData), "## Redacted learning input") || !strings.Contains(string(kData), "## Conversation c1.jsonl") || strings.Contains(string(kData), tok) {
		t.Errorf("Kickoff missing redacted conversation or leaked secret:\n%s", string(kData))
	}
	// After retro-lite finishes and advances p.LastRetroLite, memory-candidates
	// on the next sweep still receives the conversation via p.LastMemoryCandidates.
	r.recordLearningProgress(&p, task)
	mcTask := Task{Class: ClassMemoryCandidates, Level: LevelProject, Outcome: OutcomeDrafts, DraftsOnly: true, Count: p.PendingMemoryCandidates}
	mcScrubbed, err := r.writeLearningInput(root, &p, mcTask)
	if err != nil {
		t.Fatalf("writeLearningInput(memory-candidates): %v", err)
	}
	if !strings.Contains(mcScrubbed, "## Conversation c1.jsonl") {
		t.Errorf("memory-candidates after retro-lite lost conversation:\n%s", mcScrubbed)
	}
	r.recordLearningProgress(&p, mcTask)
	if p.PendingMemoryCandidates != 0 || p.LastMemoryCandidates.IsZero() {
		t.Errorf("after memory-candidates progress: %+v", p)
	}
	cleanLearningInput(root)
}
