package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

func TestMigrationTenToEleven(t *testing.T) {
	if len(migrations) < 11 {
		t.Fatalf("len(migrations) = %d, want >= 11", len(migrations))
	}
	p := filepath.Join(t.TempDir(), "metrics.db")
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = saved[:10]

	s10 := openT(t, p)
	for _, q := range []string{
		`INSERT INTO sessions(id, vendor, project_root) VALUES ('s1', 'agy', '/repo/a')`,
		`INSERT INTO judge_verdicts(ts, project_root, target, model, criterion_hash, verdict) VALUES (1000, '/repo/a', 'feat', 'claude-opus-5-5-high', 'h1', 'PASS')`,
		`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, created_at) VALUES ('/repo/a', 's1', 1, 'TASK', 'PROCEED', 90, 1000)`,
		`INSERT INTO session_ratings(session_id, project_root, evaluated_at, outcome_score, token_efficiency, plan_alignment, skill_action) VALUES ('s1', '/repo/a', 1000, 4, 4, 'matched', 'none')`,
	} {
		if _, err := s10.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s10.Close()

	migrations = saved
	s11 := openT(t, p)
	if got := s11.meta(t, "schema"); got != "13" {
		t.Fatalf("schema = %q, want 13", got)
	}
	assertV11ColumnsAndIndex(t, s11)
}

func assertV11ColumnsAndIndex(t *testing.T, s *Store) {
	t.Helper()
	var idxCnt int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='sessions_repo_id'`).Scan(&idxCnt); err != nil || idxCnt != 1 {
		t.Fatalf("sessions_repo_id index cnt=%d err=%v, want 1", idxCnt, err)
	}
	var sessRepo string
	if err := s.db.QueryRow(`SELECT repo_id FROM sessions WHERE id = 's1'`).Scan(&sessRepo); err != nil || sessRepo != "" {
		t.Fatalf("sessions.repo_id = %q, err=%v; want empty default", sessRepo, err)
	}
	var jRepo, wMod, wSess string
	if err := s.db.QueryRow(`SELECT repo_id, worker_model, worker_session_id FROM judge_verdicts WHERE target = 'feat'`).Scan(&jRepo, &wMod, &wSess); err != nil || jRepo != "" || wMod != "" || wSess != "" {
		t.Fatalf("judge_verdicts v11 defaults = (%q, %q, %q), err=%v", jRepo, wMod, wSess, err)
	}
	var tvRepo, dir string
	var sub, dur, to, inj, pivU, skL int
	if err := s.db.QueryRow(`SELECT repo_id, directive, subtask_count, duration_ms, timed_out, injected_bytes, pivots_used_count, skills_loaded_count FROM turn_vets WHERE conversation_id = 's1'`).Scan(
		&tvRepo, &dir, &sub, &dur, &to, &inj, &pivU, &skL,
	); err != nil || tvRepo != "" || dir != "" || sub+dur+to+inj+pivU+skL != 0 {
		t.Fatalf("turn_vets v11 defaults mismatch: err=%v", err)
	}
	var rRepo string
	if err := s.db.QueryRow(`SELECT repo_id FROM session_ratings WHERE session_id = 's1'`).Scan(&rRepo); err != nil || rRepo != "" {
		t.Fatalf("session_ratings.repo_id = %q, err=%v; want empty default", rRepo, err)
	}
}

func TestNormalizeAndResolveRepoID(t *testing.T) {
	const want = "git:github.com/myorg/myrepo"
	secretTok := strings.Join([]string{"gh", "p_", "SUPERSECRETTOKEN987654321"}, "")
	schemeHTTPS := strings.Join([]string{"https", "://"}, "")
	urls := []string{
		"git@github.com:MyOrg/MyRepo.git",
		"git@GitHub.COM:MyOrg/MyRepo",
		"ssh://git@github.com/MyOrg/MyRepo.git",
		"ssh://git@github.com:22/MyOrg/MyRepo.git/",
		"https://user@github.com/MyOrg/MyRepo.git",
		schemeHTTPS + "oauth2" + ":" + secretTok + "@" + "github.com/MyOrg/MyRepo.git/",
		"oauth2:" + secretTok + "@github.com:MyOrg/MyRepo.git",
		"oauth2:" + secretTok + "@github.com/MyOrg/MyRepo.git?foo=1#bar",
		"http://github.com/MyOrg/MyRepo/",
	}
	for _, u := range urls {
		got := NormalizeRepoID(u, "/fallback/ignored")
		if got != want {
			t.Errorf("NormalizeRepoID(%q) = %q, want %q", u, got, want)
		}
		if strings.Contains(got, secretTok) || strings.Contains(got, "oauth2") {
			t.Fatalf("NormalizeRepoID leaked userinfo in %q", got)
		}
	}

	absRoot := "/work/local-only/project/"
	cleanSum := sha256.Sum256([]byte("/work/local-only/project"))
	wantPathID := "path:" + hex.EncodeToString(cleanSum[:])[:16]
	if got := NormalizeRepoID("", absRoot); got != wantPathID {
		t.Errorf("NormalizeRepoID empty origin = %q, want %q", got, wantPathID)
	}
	// Malformed URL with credentials and no repo path must fall back to absRoot without leaking secret.
	badCredURL := schemeHTTPS + "user" + ":" + secretTok + "@" + "github.com/"
	if got := NormalizeRepoID(badCredURL, absRoot); got != wantPathID || strings.Contains(got, secretTok) {
		t.Errorf("malformed URL fallback = %q, want %q", got, wantPathID)
	}
	if got := ResolveRepoID(""); got != "" {
		t.Errorf("ResolveRepoID empty = %q, want empty", got)
	}
	if got := ResolveRepoID(search.GlobalWorkspace); got != "" {
		t.Errorf("ResolveRepoID global = %q, want empty", got)
	}

	repo := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteWithCred := schemeHTTPS + "deploy" + ":" + secretTok + "@" + "GitLab.Example.com/Core/Engine.git"
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remoteWithCred}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	sub := filepath.Join(repo, "internal", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveRepoID(sub); got != "git:gitlab.example.com/core/engine" {
		t.Errorf("ResolveRepoID(subdir) = %q, want git:gitlab.example.com/core/engine", got)
	}

	// Local-only repo (no origin): linked worktree and subdir must match main worktree path:<16-hex>.
	localRepo := filepath.Join(t.TempDir(), "local-only")
	if err := os.MkdirAll(localRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitLocal := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitLocal(localRepo, "init", "-q")
	gitLocal(localRepo, "commit", "--allow-empty", "-qm", "init")
	localWT := filepath.Join(t.TempDir(), "local-wt")
	gitLocal(localRepo, "worktree", "add", "-q", "-b", "wt-local", localWT, "HEAD")
	localSub := filepath.Join(localRepo, "pkg", "nested")
	if err := os.MkdirAll(localSub, 0o755); err != nil {
		t.Fatal(err)
	}
	wantLocal := ResolveRepoID(localRepo)
	if !strings.HasPrefix(wantLocal, "path:") {
		t.Fatalf("ResolveRepoID(localRepo) = %q, want path:<16-hex>", wantLocal)
	}
	for _, p := range []string{localWT, localSub} {
		if got := ResolveRepoID(p); got != wantLocal {
			t.Errorf("ResolveRepoID(%q) = %q, want %q", p, got, wantLocal)
		}
	}
}

func TestRepoIDWiredAcrossAllTables(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "wired")
	if err := os.MkdirAll(filepath.Join(repo, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:Example/Wired.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	const wantRepo = "git:github.com/example/wired"

	home := t.TempDir()
	s := openT(t, Path(home))
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	w := writer{tx: tx, salt: "salt"}
	w.agySession(agyConv{id: "c-wired", root: repo, modified: 1000})
	if w.err != nil {
		t.Fatal(w.err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := s.RecordJudgeVerdict(JudgeVerdictRow{
		TS: 2000, ProjectRoot: repo, Target: "work/p1", Model: "claude-opus-5-5-high",
		WorkerModel: "gemini-4-neon-high", WorkerSessionID: "c-wired", CriterionHash: "abc", Verdict: "PASS",
	}); err != nil {
		t.Fatalf("RecordJudgeVerdict: %v", err)
	}
	if err := s.UpsertSessionRatings([]SessionRating{{
		SessionID: "c-wired", ProjectRoot: repo, EvaluatedAt: 3000,
		OutcomeScore: 5, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none",
	}}); err != nil {
		t.Fatalf("UpsertSessionRatings: %v", err)
	}
	sqliteT(t, filepath.Join(repo, ".agents", "scratch", "search.db"),
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('c-wired', 1, 'TASK', 'PROCEED', 95, 'go', '', '', 4000)`,
	)
	ingestT(t, s, Options{Home: home})

	for _, tbl := range []string{"sessions", "judge_verdicts", "session_ratings", "turn_vets"} {
		var got string
		if err := s.db.QueryRow(`SELECT repo_id FROM ` + tbl + ` LIMIT 1`).Scan(&got); err != nil || got != wantRepo {
			t.Errorf("%s.repo_id = %q, err=%v; want %q", tbl, got, err, wantRepo)
		}
	}
}

func TestDynamicVetHistoryIngestSchemas(t *testing.T) {
	home := t.TempDir()
	st := openT(t, Path(home))

	makeProj := func(name string, stmts ...string) string {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(root, ".agents", "scratch"), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		sqliteT(t, filepath.Join(root, ".agents", "scratch", "search.db"), stmts...)
		return root
	}

	// 1. Pre-v10 9-column vet_history + read_ledger + steps.skill fallback calculation.
	pOld := makeProj("old",
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT)`,
		`CREATE TABLE read_ledger(conversation_id TEXT, resolved_path TEXT)`,
		`INSERT INTO vet_history VALUES ('C-OLD', 1, 'TASK', 'PROCEED', 85, 'fix', 'golang,docker', 'pkg/a.go,pkg/b.go', 1000)`,
	)
	sqliteT(t, filepath.Join(pOld, ".agents", "scratch", "search.db"),
		`INSERT INTO read_ledger VALUES ('C-OLD', '`+filepath.Join(pOld, "pkg/a.go")+`')`,
	)
	if _, err := st.db.Exec(`INSERT INTO sessions(id, vendor, project_root) VALUES ('C-OLD', 'agy', ?)`, pOld); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, is_error) VALUES ('C-OLD', 'e1', 1001, 'tool', 'Read', 'golang', 0)`); err != nil {
		t.Fatal(err)
	}

	// 2. v10 vet_history with directive and subtask_count.
	pV10 := makeProj("v10",
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, subtask_count INT, directive TEXT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('C-V10', 1, 'FEATURE', 'VET_FIRST', 60, 'plan', 'fyr-feature', 'cmd/main.go', 3, 'DELEGATE_SWARM', 2000)`,
	)
	if _, err := st.db.Exec(`INSERT INTO sessions(id, vendor, project_root) VALUES ('C-V10', 'agy', ?)`, pV10); err != nil {
		t.Fatal(err)
	}

	// 3. v11 vet_history with duration_ms, timed_out, injected_bytes, pivots_used_count, skills_loaded_count.
	pV11 := makeProj("v11",
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, subtask_count INT, directive TEXT, duration_ms INT, timed_out INT, injected_bytes INT, pivots_used_count INT, skills_loaded_count INT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('C-V11', 2, 'BUG', 'PROCEED', 92, '!fast', 'fyr-bug', 'x.go,y.go', 2, 'PASS_THROUGH', 145, 1, 4096, 2, 1, 3000)`,
	)
	if _, err := st.db.Exec(`INSERT INTO sessions(id, vendor, project_root) VALUES ('C-V11', 'agy', ?)`, pV11); err != nil {
		t.Fatal(err)
	}

	ingestT(t, st, Options{Home: home})

	var dir string
	var sub, dur, to, inj, pivU, skL int
	if err := st.db.QueryRow(`SELECT directive, subtask_count, duration_ms, timed_out, injected_bytes, pivots_used_count, skills_loaded_count FROM turn_vets WHERE conversation_id = 'C-OLD'`).Scan(
		&dir, &sub, &dur, &to, &inj, &pivU, &skL,
	); err != nil || dir != "" || sub != 0 || dur != 0 || to != 0 || inj != 0 || pivU != 1 || skL != 1 {
		t.Errorf("C-OLD got (%q,%d,%d,%d,%d,%d,%d) err=%v, want ('',0,0,0,0,1,1)", dir, sub, dur, to, inj, pivU, skL, err)
	}
	if err := st.db.QueryRow(`SELECT directive, subtask_count, duration_ms, timed_out, injected_bytes FROM turn_vets WHERE conversation_id = 'C-V10'`).Scan(
		&dir, &sub, &dur, &to, &inj,
	); err != nil || dir != "DELEGATE_SWARM" || sub != 3 || dur != 0 || to != 0 || inj != 0 {
		t.Errorf("C-V10 got (%q,%d,%d,%d,%d) err=%v, want ('DELEGATE_SWARM',3,0,0,0)", dir, sub, dur, to, inj, err)
	}
	if err := st.db.QueryRow(`SELECT directive, subtask_count, duration_ms, timed_out, injected_bytes, pivots_used_count, skills_loaded_count FROM turn_vets WHERE conversation_id = 'C-V11'`).Scan(
		&dir, &sub, &dur, &to, &inj, &pivU, &skL,
	); err != nil || dir != "PASS_THROUGH" || sub != 2 || dur != 145 || to != 1 || inj != 4096 || pivU != 2 || skL != 1 {
		t.Errorf("C-V11 got (%q,%d,%d,%d,%d,%d,%d) err=%v", dir, sub, dur, to, inj, pivU, skL, err)
	}
}

func TestModelEvidenceDecayedSourcesAndWaveSeparation(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -30)
	day := 24 * time.Hour

	// 1. Legacy work_waves: populates WorkRuns/WorkPass/WorkMeanSeconds ONLY, NOT DecayedRuns/DecayedSuccesses.
	if _, err := s.db.Exec(
		`INSERT INTO work_waves(stamp, worker, project_root, state, model, updated_at, setup_s, agy_s, check_s) VALUES ('st1', 'w1', '/repo', 'PASS', 'gemini-4-neon-high', ?, 5, 20, 5)`,
		now.Add(-day).Unix(),
	); err != nil {
		t.Fatal(err)
	}

	// 2. Trials joined to sessions: 1 pass 1s ago (w=w0), 1 fail 14d ago (age=14, w=0.5).
	if err := s.RecordTrialInput(TrialInput{
		Run: "bench/neon/1", Label: "bench=neon", Status: "pass",
		StartedAt: now.Add(-time.Second), Seconds: 10, Model: "gemini-4-neon-high",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrialInput(TrialInput{
		Run: "bench/neon/2", Label: "bench=neon", Status: "fail", Cause: "test_fail",
		StartedAt: now.Add(-14 * day), Seconds: 12, Model: "gemini-4-neon-high",
	}); err != nil {
		t.Fatal(err)
	}

	// 3. Judge verdicts attributed to worker_model (weight 0.9): PASS 7d ago on gemini-4-neon-high; judge's own model claude-opus-5-5-high must NOT receive JudgeRuns!
	if err := s.RecordJudgeVerdict(JudgeVerdictRow{
		TS: now.Add(-7 * day).UnixMilli(), ProjectRoot: "/repo", Target: "work/p1",
		Model: "claude-opus-5-5-high", WorkerModel: "gemini-4-neon-high",
		CriterionHash: "c1", Verdict: "PASS",
	}); err != nil {
		t.Fatal(err)
	}

	// 4. Session ratings joined to sessions (weight 0.6, (score-1)/4): score=4 -> 0.75 at age 14d (decay=0.5).
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, model, started_at) VALUES ('rated-1', 'agy', 'gemini-4-neon-high', ?)`, now.Add(-14*day).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSessionRatings([]SessionRating{{
		SessionID: "rated-1", ProjectRoot: "/repo", EvaluatedAt: now.Add(-14 * day).UnixMilli(),
		OutcomeScore: 4, TokenEfficiency: 5, PlanAlignment: "matched", SkillAction: "none",
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ModelEvidence(since, now)
	if err != nil {
		t.Fatalf("ModelEvidence: %v", err)
	}
	if _, leaked := got["claude-opus-5-5-high"]; leaked {
		t.Errorf("judge model received worker attribution: %+v", got["claude-opus-5-5-high"])
	}
	ev := got["gemini-4-neon-high"]
	if ev.WorkRuns != 1 || ev.WorkPass != 1 || ev.WorkMeanSeconds != 30 {
		t.Errorf("legacy work_waves = %+v, want 1/1 @ 30s", ev)
	}
	if ev.TrialRuns != 2 || ev.TrialPass != 1 || ev.EvalRuns != 2 || ev.EvalPass != 1 {
		t.Errorf("trial counts = %+v, want 2 runs / 1 pass", ev)
	}
	if ev.JudgeRuns != 1 || ev.JudgePass != 1 {
		t.Errorf("judge counts = %+v, want 1/1", ev)
	}
	if ev.RatingRuns != 1 || math.Abs(ev.RatingScoreSum-0.75) > 1e-9 {
		t.Errorf("rating counts = %+v, want 1 run / 0.75 score sum", ev)
	}

	w0 := math.Pow(0.5, 1.0/(86400.0*14.0))
	w7 := math.Pow(0.5, 7.0/14.0)
	wantRuns := 1.0*w0 + 1.0*0.5 + 0.9*w7 + 0.6*0.5
	wantSucc := 1.0*w0 + 1.0*0.5*0.0 + 0.9*w7*1.0 + 0.6*0.5*0.75
	if math.Abs(ev.DecayedRuns-wantRuns) > 1e-9 || math.Abs(ev.DecayedSuccesses-wantSucc) > 1e-9 {
		t.Errorf("decayed = (runs=%v, succ=%v), want (runs=%v, succ=%v)", ev.DecayedRuns, ev.DecayedSuccesses, wantRuns, wantSucc)
	}
}

func TestModelEvidenceUnclaimedEvalSessionsDecayed(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -30)
	day := 24 * time.Hour

	for _, row := range []struct {
		id, outcome, model string
		ts                 int64
	}{
		{"u-pass", "pass", "gemini-4-neon-high", now.Add(-14 * day).UnixMilli()},
		{"u-fail", "fail", "gemini-4-neon-high", now.Add(-time.Second).UnixMilli()},
		{"u-def", "pass", "default", now.Add(-time.Second).UnixMilli()},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO sessions(id, vendor, label, outcome, model, started_at) VALUES (?, 'agy', 'eval=resume', ?, ?, ?)`,
			row.id, row.outcome, row.model, row.ts,
		); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.ModelEvidence(since, now)
	if err != nil {
		t.Fatalf("ModelEvidence: %v", err)
	}
	if _, hasDef := got["default"]; hasDef {
		t.Errorf("default model should be skipped: %+v", got["default"])
	}
	ev := got["gemini-4-neon-high"]
	if ev.EvalRuns != 2 || ev.EvalPass != 1 {
		t.Fatalf("EvalRuns/EvalPass = %+v, want 2/1", ev)
	}
	w0 := math.Pow(0.5, 1.0/(86400.0*14.0))
	wantRuns := 0.5 + w0
	wantSucc := 0.5
	if math.Abs(ev.DecayedRuns-wantRuns) > 1e-9 || math.Abs(ev.DecayedSuccesses-wantSucc) > 1e-9 {
		t.Errorf("decayed = (runs=%v, succ=%v), want (%v, %v)", ev.DecayedRuns, ev.DecayedSuccesses, wantRuns, wantSucc)
	}
}
