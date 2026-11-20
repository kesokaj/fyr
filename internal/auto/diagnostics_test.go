package auto

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

func TestAutoDiagnosticTriggerAndInput(t *testing.T) {
	home := t.TempDir()
	root := idleProject(t)
	now := runAt
	seedAutoKneeMetrics(t, home, root, now)

	r := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{}}
	var p Project
	task := r.pickProjectTask(root, &p, nil)
	if task == nil || task.Class != ClassRetroLite || task.Count == 0 {
		t.Fatalf("pickProjectTask = %+v, want ClassRetroLite from diagnostic signal", task)
	}
	sigID := assertAutoDiagnosticLearningInput(t, r, root, &p, *task, home)
	assertAutoDiagnosticDedupAndBgRun(t, home, root, now, sigID)
}

func seedAutoKneeMetrics(t *testing.T, home, root string, now time.Time) {
	t.Helper()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := now.Add(-2 * time.Hour).UnixMilli()
	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("ak-%d", i)
		if _, err := db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`, sid, root, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES (?, '1', ?, 'model', 210000)`, sid, ts+int64(i)*1000+10); err != nil {
			t.Fatal(err)
		}
	}
}

func assertAutoDiagnosticLearningInput(t *testing.T, r *run, root string, p *Project, task Task, home string) string {
	t.Helper()
	scrubbed, err := r.writeLearningInput(root, p, task)
	if err != nil {
		t.Fatalf("writeLearningInput: %v", err)
	}
	if !strings.Contains(scrubbed, "## Diagnostic signals") || !strings.Contains(scrubbed, "[high]") || !strings.Contains(scrubbed, "context_knee_rate") {
		t.Fatalf("learning input missing Diagnostic signals section:\n%s", scrubbed)
	}
	if strings.Contains(scrubbed, home) {
		t.Fatalf("learning input leaked home %q:\n%s", home, scrubbed)
	}
	idx := strings.Index(scrubbed, "sig:proj-")
	if idx < 0 {
		t.Fatalf("learning input missing sig:proj-:\n%s", scrubbed)
	}
	end := strings.IndexByte(scrubbed[idx:], ' ')
	return scrubbed[idx : idx+end]
}

func assertAutoDiagnosticDedupAndBgRun(t *testing.T, home, root string, now time.Time, sigID string) {
	t.Helper()
	bo := brain.Options{Home: home, Global: true, Now: func() time.Time { return now }}
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "proposal", Title: "Dream, project — knee fix", Project: root, Status: "queued",
		Body: strings.NewReader("Signal: " + sigID + "\n"),
	}); err != nil {
		t.Fatal(err)
	}
	r2 := &run{o: Options{Home: home, RealHome: home, Now: func() time.Time { return now }}, cfg: Config{}}
	var p Project
	if got := r2.pickProjectTask(root, &p, nil); got != nil {
		t.Fatalf("expected queued signal_id %s to dedup RetroLite trigger, got %+v", sigID, got)
	}

	writeConfig(t, home, validConfig)
	var bgRows []metrics.BackgroundRunRow
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	o.RecordBackgroundRun = func(row metrics.BackgroundRunRow) error {
		bgRows = append(bgRows, row)
		return nil
	}
	if err := Run(o, true); err != nil || len(bgRows) != 0 {
		t.Fatalf("dry Run err=%v bgRows=%+v", err, bgRows)
	}
	if err := Run(o, false); err != nil || len(bgRows) != 1 || bgRows[0].Kind != "auto" || bgRows[0].Outcome != "pass" || bgRows[0].ProjectsChecked != 1 {
		t.Fatalf("real Run err=%v bgRows=%+v\n%s", err, bgRows, out.String())
	}
}
