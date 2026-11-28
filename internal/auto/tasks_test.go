package auto

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

// All dreaming classes render valid Kickoff templates.
func TestTasksKickoffTemplates(t *testing.T) {
	t.Parallel()

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
		{ClassRetroLite, nil, "", 3, "Files:\n", "3 redacted conversation(s) using ONLY the redacted learning input and SessionDigest"},
		{ClassMemoryCandidates, nil, "", 3, "Files:\n", "3 redacted conversation(s) using ONLY the redacted learning input and SessionDigest"},
		{ClassDistill, []string{".agents/skills/auto-distilled/"}, "", 4, "Files: .agents/skills/auto-distilled/\n", "4 sessions"},
		{ClassMissingSkill, nil, "PROPOSAL-2026-11-01-1000-missing-skill", 1, "Files:\n", "skills.sh"},
		{ClassHarnessEdit, []string{"skills/foo/"}, "PROPOSAL-2026-11-01-1000-harness", 1, "Files: skills/foo/\n", "Apply the queued harness proposal"},
	}

	for _, tc := range cases {
		task, err := prepareTask(tc.class, tc.entries, tc.id, tc.count)
		if err != nil {
			t.Fatalf("prepareTask(%s): %v", tc.class, err)
		}
		k, err := buildKickoff(now, task)
		if err != nil {
			t.Fatalf("buildKickoff(%s): %v", tc.class, err)
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
	t.Parallel()

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
		r := &run{o: Options{Home: home, Now: func() time.Time { return now }}, cfg: Config{}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassMemoryConsolidation || task.Count != 10 {
			t.Fatalf("got task %+v, want memory-consolidation count 10", task)
		}
	})

	t.Run("retro-lite-then-memory-candidates", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		convDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "session-1", ".system_generated", "logs")
		if err := os.MkdirAll(convDir, 0o700); err != nil {
			t.Fatal(err)
		}
		convFile := filepath.Join(convDir, "transcript.jsonl")
		if err := os.WriteFile(convFile, []byte(`{"role":"user","content":"working in `+root+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{}}
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
		// Without a matching conversation in window, memory-candidates does not trigger.
		if err := os.Chtimes(convFile, now, now); err != nil {
			t.Fatal(err)
		}
		p.LastMemoryCandidates = now.Add(time.Hour)
		if task3 := r.pickProjectTask(root, &p, nil); task3 != nil {
			t.Fatalf("empty conversations still triggered memory-candidates: %+v", task3)
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
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{}}
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
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{}}
		var p Project
		task := r.pickProjectTask(root, &p, nil)
		if task == nil || task.Class != ClassDistill || task.Count != 3 || len(task.Kept) != 1 || !strings.HasPrefix(task.Kept[0], ".agents/skills/auto-distilled-") {
			t.Fatalf("got %+v, want distill count 3", task)
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
	t.Parallel()

	home := t.TempDir()
	root := initGitRepo(t)
	writeTree(t, root, ".gitignore", ".agents/scratch/\n")
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	tok := "ghp_" + strings.Repeat("Z", 36)
	bo := brain.Options{Home: home, Dir: root, Now: func() time.Time { return now }}
	if _, err := brain.MemoryAdd(bo, brain.MemoryEntry{
		Type: "project", Name: "has-secret", Description: "note with secret",
	}, strings.NewReader("never leak "+tok+" to the model")); err != nil {
		t.Fatal(err)
	}
	g := brain.Options{Home: home, Global: true, Now: func() time.Time { return now }}
	if _, err := brain.MemoryAdd(g, brain.MemoryEntry{
		Type: "feedback", Name: "global-rule", Description: "always vet",
	}, strings.NewReader("run go vet before commit")); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Add(g, brain.AddInput{
		Kind: "proposal", Title: "Proposal with secret", Project: root,
		Body: strings.NewReader("proposal token " + tok),
	}); err != nil {
		t.Fatal(err)
	}
	writeFakeMetricsDB(t, home, root, []string{"search,record"}, []int{1})
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c1", ".system_generated", "logs")
	if err := os.MkdirAll(convDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "transcript.jsonl"), []byte(`{"project":"`+root+`","text":"token `+tok+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{}}
	var p Project
	task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 1}
	if _, err := r.writeLearningInputWithCutoff(root, &p, task, r.sweepConversationCutoff()); err != nil {
		t.Fatalf("writeLearningInputWithCutoff: %v", err)
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
	for _, want := range []string{"## Metric numbers", "## Trajectory digest", "## Global memory records", "MEMORY-global-rule", "kind ", "validity "} {
		if !strings.Contains(string(data), want) {
			t.Errorf("expected %q in learning input:\n%s", want, string(data))
		}
	}
	cleanLearningInput(root)
	if _, err := os.Stat(inPath); !os.IsNotExist(err) {
		t.Errorf("cleanLearningInput left %s", inPath)
	}
	k, err := writeKickoff(home, now, task)
	if err != nil {
		t.Fatalf("writeKickoff: %v", err)
	}
	cutoff, err := r.prepareLearningWorker(root, &p, task, k.Path)
	if err != nil {
		t.Fatalf("prepareLearningWorker: %v", err)
	}
	kData, err := os.ReadFile(k.Path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", k.Path, err)
	}
	if !strings.Contains(string(kData), "## Redacted learning input") || !strings.Contains(string(kData), "## Conversation c1") || !strings.Contains(string(kData), "=====") || strings.Contains(string(kData), tok) {
		t.Errorf("Kickoff missing fenced redacted conversation or leaked value:\n%s", string(kData))
	}
	// After retro-lite finishes and advances p.LastRetroLite, memory-candidates
	// on the next sweep still receives the conversation via p.LastMemoryCandidates.
	r.recordLearningProgress(&p, task, cutoff)
	mcTask := Task{Class: ClassMemoryCandidates, Outcome: OutcomeDrafts, DraftsOnly: true, Count: p.PendingMemoryCandidates}
	mcScrubbed, err := r.writeLearningInputWithCutoff(root, &p, mcTask, r.sweepConversationCutoff())
	if err != nil {
		t.Fatalf("writeLearningInputWithCutoff(memory-candidates): %v", err)
	}
	if !strings.Contains(mcScrubbed, "## Conversation c1") {
		t.Errorf("memory-candidates after retro-lite lost conversation:\n%s", mcScrubbed)
	}
	r.recordLearningProgress(&p, mcTask, cutoff)
	if p.PendingMemoryCandidates != 0 || p.LastMemoryCandidates.IsZero() {
		t.Errorf("after memory-candidates progress: %+v", p)
	}
	cleanLearningInput(root)
}

func TestDreamingOnlyClassSet(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		ClassMemoryConsolidation: true,
		ClassRetroLite:           true,
		ClassMemoryCandidates:    true,
		ClassDistill:             true,
		ClassMissingSkill:        true,
		ClassHarnessEdit:         true,
	}
	if len(classOrder) != len(want) {
		t.Fatalf("classOrder has %d classes, want %d: %+v", len(classOrder), len(want), classOrder)
	}
	for _, c := range classOrder {
		if !want[c.Name] {
			t.Errorf("unexpected class %q in classOrder", c.Name)
		}
		if c.Outcome == "applied" || c.Outcome == "eval" {
			t.Errorf("class %q has non-dreaming outcome %q", c.Name, c.Outcome)
		}
	}
	for _, removed := range []string{
		"spec-drift", "doc-drift", "onboarding",
		"context-adequacy", "scaffold-drift", "eval-rerun", "mini-audit",
	} {
		if _, ok := lookupClass(removed); ok {
			t.Errorf("deleted class %q still present in classOrder", removed)
		}
	}
}

func TestDistillRouting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	t.Run("harness-checkout-eval-gate", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		writeFakeMetricsDB(t, home, root, []string{"search,record", "search,record", "search,record"}, []int{0, 0, 0})
		r := &run{o: Options{Home: home, RealHome: home, Repo: root, Now: func() time.Time { return now }}, cfg: Config{}}
		var p Project
		task := r.triggerProjectClass(root, &p, ClassDistill, nil)
		if task == nil || task.Class != ClassDistill || task.Outcome != OutcomeEvalGate || len(task.Kept) != 1 || !strings.HasPrefix(task.Kept[0], "skills/auto-distilled-") {
			t.Fatalf("harness distill = %+v, want OutcomeEvalGate and skills/auto-distilled-*", task)
		}
	})

	t.Run("consumer-project-proposal", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		other := initGitRepo(t)
		writeFakeMetricsDB(t, home, root, []string{"search,record", "search,record", "search,record"}, []int{0, 0, 0})
		r := &run{o: Options{Home: home, RealHome: home, Repo: other, Now: func() time.Time { return now }}, cfg: Config{}}
		var p Project
		task := r.triggerProjectClass(root, &p, ClassDistill, nil)
		if task == nil || task.Class != ClassDistill || task.Outcome != OutcomeProposal || len(task.Kept) != 1 || !strings.HasPrefix(task.Kept[0], ".agents/skills/auto-distilled-") {
			t.Fatalf("consumer distill = %+v, want OutcomeProposal and .agents/skills/auto-distilled-*", task)
		}
	})
}

func TestLearningInputHardeningAndCutoff(t *testing.T) {
	t.Parallel()

	t.Run("non-gitignored-skips-context-and-symlink-fails-closed", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}, cfg: Config{}}
		task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 1}
		if _, err := r.writeLearningInputWithCutoff(root, &Project{}, task, r.sweepConversationCutoff()); err != nil {
			t.Fatalf("writeLearningInputWithCutoff(non-ignored): %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, ".agents", "scratch", "auto-input", "context.md")); !os.IsNotExist(err) {
			t.Errorf("non-gitignored repo wrote context.md: err=%v", err)
		}
		writeTree(t, root, ".gitignore", ".agents/scratch/\n")
		if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(root, ".agents", "scratch")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.writeLearningInputWithCutoff(root, &Project{}, task, r.sweepConversationCutoff()); err == nil {
			t.Errorf("expected symlinked .agents/scratch to fail closed")
		}
	})

	t.Run("redact-jsonl-utf8-longest-prefix-and-conv-reserve", func(t *testing.T) {
		tok := "gh" + "p_" + strings.Repeat("Q", 36)
		escaped := marshalRedactedJSON(func() (any, error) {
			return map[string]string{"v": "x\u003d\"y\" " + tok}, nil
		})
		if strings.Contains(escaped, tok) {
			t.Errorf("marshalRedactedJSON leaked secret: %s", escaped)
		}
		realHome := "/home/op"
		scratchHome := "/home/op/scratch"
		scrubbed := scrubHomeAndRedact("path "+scratchHome+"/a and "+realHome+"/b "+strings.Repeat("é", 25000), realHome, scratchHome)
		if strings.Contains(scrubbed, "/scratch") || strings.ContainsRune(scrubbed, '\uFFFD') {
			t.Errorf("longest-prefix or UTF-8 truncation failed: containsScratch=%v", strings.Contains(scrubbed, "/scratch"))
		}
		home := t.TempDir()
		root := initGitRepo(t)
		writeTree(t, root, ".gitignore", ".agents/scratch/\n")
		bo := brain.Options{Home: home, Dir: root, Now: func() time.Time { return runAt }}
		for i := 0; i < 6; i++ {
			if _, err := brain.MemoryAdd(bo, brain.MemoryEntry{
				Type: "project", Name: fmt.Sprintf("huge-%d", i), Description: "fill non-conv budget",
			}, strings.NewReader(strings.Repeat("M", 8192))); err != nil {
				t.Fatal(err)
			}
		}
		convDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-keep", ".system_generated", "logs")
		if err := os.MkdirAll(convDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(convDir, "transcript.jsonl"), []byte(`{"cwd":"`+root+`","msg":"KEEP_CONVERSATION_MARKER"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}, cfg: Config{}}
		task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 1}
		got, err := r.writeLearningInputWithCutoff(root, &Project{}, task, r.sweepConversationCutoff())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "KEEP_CONVERSATION_MARKER") {
			t.Errorf("conversations starved despite 16 KiB reserve")
		}
		k, err := writeKickoff(home, runAt, task)
		if err != nil {
			t.Fatal(err)
		}
		if err := appendKickoffInput(k.Path, strings.Repeat("é", 40000)); err != nil {
			t.Fatal(err)
		}
		kBytes, err := os.ReadFile(k.Path)
		if err != nil {
			t.Fatal(err)
		}
		kStr := string(kBytes)
		if len(kBytes) > 60<<10 {
			t.Errorf("appendKickoffInput wrote %d bytes, want <= %d", len(kBytes), 60<<10)
		}
		if strings.ContainsRune(kStr, '\uFFFD') || strings.Count(kStr, "=====") < 2 {
			t.Errorf("appendKickoffInput broke UTF-8 or fence markers (count=%d)", strings.Count(kStr, "====="))
		}
	})

	t.Run("sibling-prefix-rejected-and-cutoff-captured-at-start", func(t *testing.T) {
		home := t.TempDir()
		root := initGitRepo(t)
		sibling := root + "-sibling"
		tStart := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		now := tStart
		conv1Dir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c-old", ".system_generated", "logs")
		conv2Dir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c-new", ".system_generated", "logs")
		sibDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c-sib", ".system_generated", "logs")
		for _, d := range []string{conv1Dir, conv2Dir, sibDir} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		f1 := filepath.Join(conv1Dir, "transcript.jsonl")
		f2 := filepath.Join(conv2Dir, "transcript.jsonl")
		fSib := filepath.Join(sibDir, "transcript.jsonl")
		_ = os.WriteFile(f1, []byte(`{"cwd":"`+root+`","n":1}`+"\n"), 0o600)
		_ = os.WriteFile(f2, []byte(`{"cwd":"`+root+`","n":2}`+"\n"), 0o600)
		_ = os.WriteFile(fSib, []byte(`{"cwd":"`+sibling+`"}`+"\n"), 0o600)
		_ = os.Chtimes(f1, tStart.Add(-90*time.Minute), tStart.Add(-90*time.Minute))
		// Modified at T_start - 50m: not yet idle (1h) when worker starts at T_start.
		_ = os.Chtimes(f2, tStart.Add(-50*time.Minute), tStart.Add(-50*time.Minute))
		_ = os.Chtimes(fSib, tStart.Add(-90*time.Minute), tStart.Add(-90*time.Minute))

		r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{ConversationIdle: "1h"}}
		var p Project
		task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 1}
		cutoff, err := r.prepareLearningWorker(root, &p, task, "")
		if err != nil {
			t.Fatal(err)
		}
		// Advance clock 20 minutes during worker execution, then record progress.
		now = tStart.Add(20 * time.Minute)
		r.recordLearningProgress(&p, task, cutoff)
		convs := r.conversationsSince(root, p.LastRetroLite)
		if len(convs) != 1 || convs[0].label != "c-new" {
			t.Errorf("conversations after worker advance = %+v, want [c-new] only (no drop, no sibling)", convs)
		}
	})
}

func TestProjectPassCacheSharesAndCleans(t *testing.T) {
	t.Parallel()

	home, root := t.TempDir(), initGitRepo(t)
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	g := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}
	if _, err := brain.Add(g, brain.AddInput{Kind: "proposal", Status: "queued", Project: root, Title: "p1", Body: strings.NewReader("b")}); err != nil {
		t.Fatal(err)
	}
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c-pass", ".system_generated", "logs")
	_ = os.MkdirAll(convDir, 0o700)
	trPath := filepath.Join(convDir, "transcript.jsonl")
	_ = os.WriteFile(trPath, []byte(`{"cwd":"`+root+`"}`+"\n"), 0o600)
	_ = os.Chtimes(trPath, runAt.Add(-2*time.Hour), runAt.Add(-2*time.Hour))
	r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return runAt }}, cfg: Config{ConversationIdle: "1h"}}
	endPass := r.beginProjectPass(root)
	defer endPass()
	s1, c1 := r.sweepMetricsSnapshot()
	s2, c2 := r.sweepMetricsSnapshot()
	defer c1()
	defer c2()
	if tmps, _ := filepath.Glob(filepath.Join(filepath.Dir(metrics.Path(home)), "auto-metrics-*")); s1 == nil || s1 != s2 || len(tmps) != 1 {
		t.Fatalf("metrics snapshot s1=%v s2=%v tmps=%v", s1, s2, tmps)
	}
	convs1 := r.conversationsSince(root, time.Time{})
	_ = os.Remove(trPath)
	if convs2 := r.conversationsSince(root, time.Time{}); len(convs1) != 1 || len(convs2) != 1 {
		t.Fatalf("cached convs = %d / %d, want 1 / 1", len(convs1), len(convs2))
	}
	p1, err1 := r.sweepOpenProposals()
	_, _ = brain.Add(g, brain.AddInput{Kind: "proposal", Status: "queued", Project: root, Title: "p2", Body: strings.NewReader("b")})
	if p2, err2 := r.sweepOpenProposals(); err1 != nil || err2 != nil || len(p1) != 1 || len(p2) != 1 {
		t.Fatalf("cached proposals = (%d,%v) / (%d,%v)", len(p1), err1, len(p2), err2)
	}
	r.finishPassMetrics()
	if tmps, _ := filepath.Glob(filepath.Join(filepath.Dir(metrics.Path(home)), "auto-metrics-*")); len(tmps) != 0 {
		t.Fatalf("tempdirs left = %v", tmps)
	}
}

func TestTasksSelfReferentialLogFilterAndGlobalMemories(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	realHome := t.TempDir()
	root := t.TempDir()
	brainDir := filepath.Join(realHome, ".gemini", "antigravity-cli", "brain")
	writeConv := func(id, body string) {
		p := filepath.Join(brainDir, id, ".system_generated", "logs", "transcript.jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := runAt.Add(-2 * time.Hour)
		_ = os.Chtimes(p, ts, ts)
	}
	writeConv("conv-keep", `{"content":"`+root+` normal session conversation"}`)
	writeConv("conv-dream", `{"content":"`+root+` dreaming: skip me"}`)
	writeConv("conv-auto", `{"content":"`+root+` This is a Kickoff written by fyr auto."}`)
	writeConv("conv-capsule", `{"content":"`+root+` [FYR_WORKER_CAPSULE]"}`)
	writeConv("conv-dreamer", `{"content":"`+root+` You are the dreamer of a coding-agent harness"}`)

	g := brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt }}
	if _, err := brain.Add(g, brain.AddInput{Kind: "memory", Status: "closed", Title: "global mem", Body: strings.NewReader("fact")}); err != nil {
		t.Fatal(err)
	}
	r := &run{o: Options{Home: home, RealHome: realHome, Now: func() time.Time { return runAt }}}
	convs := r.conversationsSince(root, time.Time{})
	if len(convs) != 3 {
		t.Fatalf("conversationsSince got %+v, want 3 retained conversations (conv-keep, conv-dream, conv-capsule)", convs)
	}
	gotLabels := map[string]bool{}
	for _, c := range convs {
		gotLabels[c.label] = true
	}
	for _, want := range []string{"conv-keep", "conv-dream", "conv-capsule"} {
		if !gotLabels[want] {
			t.Errorf("missing expected conversation %s in %+v", want, convs)
		}
	}
	for _, drop := range []string{"conv-auto", "conv-dreamer"} {
		if gotLabels[drop] {
			t.Errorf("self-referential conversation %s should have been dropped: %+v", drop, convs)
		}
	}
	if n := r.countMemoriesSince("__global__", time.Time{}); n != 1 {
		t.Fatalf("countMemoriesSince(__global__) = %d, want 1", n)
	}
}
