package auto

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/redact"
)

func TestAllInvalidDraftsFailAndPreserveWatermark(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)
	cutoff := now.Add(-15 * time.Minute)
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, validConfig)
	st := State{Date: now.Format("2006-01-02"), Projects: map[string]Project{root: {}}}
	if err := saveState(home, st); err != nil {
		t.Fatal(err)
	}
	cfgBefore, cfgErr := readBounded(ConfigPath(home), configLimit)
	stateHash, err := fileSHA256(filepath.Join(dir(home), "runs.json"))
	if err != nil {
		t.Fatal(err)
	}

	r := &run{
		ctx: context.Background(),
		o:   Options{Home: home, Now: func() time.Time { return now }},
		cfg: Config{Enabled: true, ConversationIdle: "1h"},
		st:  st,
	}
	task := Task{Class: ClassRetroLite, Outcome: OutcomeDrafts, DraftsOnly: true, Count: 5}
	k, err := buildKickoff(now, task)
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: 100% invalid drafts (2/2 rejected) must fail and preserve LastRetroLite watermark.
	draftsDir1 := filepath.Join(isolate.Root(home, "sandbox"), "drafts", "run-1")
	if err := os.MkdirAll(draftsDir1, 0o700); err != nil {
		t.Fatal(err)
	}
	badJSONL := strings.Join([]string{
		`{"type":"proposal","title":"Bad","body":"wrong key"}`,
		`{"class":"memory-candidates","id":"x","body":"wrong key"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(draftsDir1, "auto-drafts.jsonl"), []byte(badJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	var p Project
	res1 := isolate.WorkerResult{Name: k.Name, Verdict: "PASS", Drafts: 1, DraftsDir: draftsDir1}
	v1, fs1 := r.finishWorker(context.Background(), root, &p, task, k, isolate.RunnerIsolate, "isolate", "", cfgBefore, cfgErr, stateHash, checkoutSnapshot{}, nil, res1, nil, cutoff)
	if !r.failed || !strings.Contains(v1, "FAIL (all 2 drafts invalid)") {
		t.Errorf("all-invalid verdict = %q (failed=%v), want FAIL (all 2 drafts invalid)", v1, r.failed)
	}
	if !p.LastRetroLite.IsZero() || p.PendingMemoryCandidates != 0 {
		t.Errorf("all-invalid run advanced learning watermark: LastRetroLite=%v Pending=%d", p.LastRetroLite, p.PendingMemoryCandidates)
	}
	invalidCount := 0
	for _, f := range fs1 {
		if f.Check == "draft" && f.Rule == "invalid" {
			invalidCount++
		}
	}
	if invalidCount != 2 {
		t.Errorf("invalid findings = %d, want 2 (%+v)", invalidCount, fs1)
	}

	// Case 2: partial success (1 valid + 1 invalid) passes and advances watermark.
	r.failed = false
	draftsDir2 := filepath.Join(isolate.Root(home, "sandbox"), "drafts", "run-2")
	if err := os.MkdirAll(draftsDir2, 0o700); err != nil {
		t.Fatal(err)
	}
	mixedJSONL := strings.Join([]string{
		`{"kind":"finding","body":"valid finding"}`,
		`{"type":"proposal","title":"Bad","body":"wrong key"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(draftsDir2, "auto-drafts.jsonl"), []byte(mixedJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	res2 := isolate.WorkerResult{Name: k.Name, Verdict: "PASS", Drafts: 1, DraftsDir: draftsDir2}
	v2, _ := r.finishWorker(context.Background(), root, &p, task, k, isolate.RunnerIsolate, "isolate", "", cfgBefore, cfgErr, stateHash, checkoutSnapshot{}, nil, res2, nil, cutoff)
	if r.failed || !strings.HasPrefix(v2, "PASS;") {
		t.Errorf("mixed verdict = %q (failed=%v), want PASS", v2, r.failed)
	}
	if !p.LastRetroLite.Equal(cutoff) || p.PendingMemoryCandidates != 5 {
		t.Errorf("mixed run did not advance watermark: LastRetroLite=%v Pending=%d", p.LastRetroLite, p.PendingMemoryCandidates)
	}
}

func TestCollectAutoDraftsScrubsSecretsBeforeArchiving(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)
	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}

	draftsDir := filepath.Join(isolate.Root(home, "sandbox"), "drafts", "scrub-1")
	if err := os.MkdirAll(draftsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rawTok := "gh" + "p_" + strings.Repeat("A", 36)
	escapedTok := `\u0067hp_` + strings.Repeat("B", 36)
	spoofEscapedTok := `\u0067hp_` + strings.Repeat("C", 36)
	authHdr := "Auth" + "orization: " + "Bear" + "er " + strings.Repeat("x", 24)
	pemOpen := "-----" + "begin private" + " key-----"
	pemBody := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ"
	pemClose := "-----" + "end private" + " key-----"
	escPemOpen := `\u002d----\u0062egin private key-----`
	escPemBody := "MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQ"
	jsonl := strings.Join([]string{
		`{"kind":"finding","body":"clean note"}`,
		`{"kind":"finding","body":"leaked ` + rawTok + `"}`,
		`{"kind":"finding","body":"escaped ` + escapedTok + `"}`,
		`[redacted by fyr] "` + spoofEscapedTok + `"`,
		`{"kind":"finding","body":"hdr ` + authHdr + `"}`,
		pemOpen,
		pemBody,
		pemClose,
		`{"kind":"finding","body":"` + escPemOpen + `"}`,
		escPemBody,
		pemClose,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(draftsDir, "auto-drafts.jsonl"), []byte(jsonl), 0o600); err != nil {
		t.Fatal(err)
	}

	res := isolate.WorkerResult{Name: "scrub-run", Verdict: "PASS", Drafts: 1, DraftsDir: draftsDir}
	_, _, stats, err := r.collectAutoDrafts(root, "scrub-run", ClassRetroLite, res)
	if err != nil {
		t.Fatalf("collectAutoDrafts: %v", err)
	}
	if stats.valid != 1 || stats.invalid != 10 {
		t.Fatalf("stats = %+v, want valid=1 invalid=10", stats)
	}
	archived := readFile(t, filepath.Join(dir(home), "drafts", "scrub-run.jsonl"))
	if !strings.Contains(archived, `{"kind":"finding","body":"clean note"}`) {
		t.Errorf("archive dropped clean line:\n%s", archived)
	}
	for _, secret := range []string{rawTok, strings.Repeat("B", 20), strings.Repeat("C", 20), authHdr, pemOpen, pemBody, escPemOpen, escPemBody, pemClose} {
		if strings.Contains(archived, secret) {
			t.Errorf("archive leaked secret %q:\n%s", secret, archived)
		}
	}
	if strings.Count(archived, "[redacted by fyr: 3 lines]") != 2 || strings.Count(archived, "[redacted by fyr") != 6 {
		t.Errorf("expected 4 single-line redactions + 2 3-line PEM block redactions, got:\n%s", archived)
	}

	// If archiving fails (e.g. auto/drafts is a regular file), collectAutoDrafts
	// must fail before processDraftLinesStats mutates brain.db or OpenAsks.
	home2 := t.TempDir()
	if err := ensureDir(home2); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir(home2), "drafts"), []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	draftsDir2 := filepath.Join(isolate.Root(home2, "sandbox"), "drafts", "scrub-2")
	if err := os.MkdirAll(draftsDir2, 0o700); err != nil {
		t.Fatal(err)
	}
	propLine := `{"kind":"proposal","target":"project","title":"Should not store","body":"evidence"}` + "\n"
	if err := os.WriteFile(filepath.Join(draftsDir2, "auto-drafts.jsonl"), []byte(propLine), 0o600); err != nil {
		t.Fatal(err)
	}
	r2 := &run{o: Options{Home: home2, Now: func() time.Time { return now }}}
	res2 := isolate.WorkerResult{Name: "scrub-fail", Verdict: "PASS", Drafts: 1, DraftsDir: draftsDir2}
	if _, _, _, err := r2.collectAutoDrafts(root, "scrub-fail", ClassRetroLite, res2); err == nil {
		t.Fatal("expected collectAutoDrafts error when drafts path is blocked by file")
	}
	if r2.st.OpenAsks != 0 {
		t.Errorf("OpenAsks = %d after archive failure, want 0", r2.st.OpenAsks)
	}
	props, err := brain.List(brain.Options{Home: home2, Global: true, Now: r2.o.Now}, brain.ListFilter{Kind: "proposal"})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("brain.List after archive failure: %v", err)
	}
	if len(props) != 0 {
		t.Errorf("proposals mutated after archive failure: %+v", props)
	}
}

func TestDraftsPerClassAllowlistEnforced(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	root := initGitRepo(t)
	now := time.Date(2026, 11, 26, 4, 0, 0, 0, time.UTC)
	bo := brain.Options{Home: home, Dir: root, Now: func() time.Time { return now }}
	if _, err := brain.MemoryDraftWithOptions(bo, "add", "seed-mem", "seeded derived", brain.MemoryDraftOptions{Type: "semantic"}); err != nil {
		t.Fatal(err)
	}
	r := &run{o: Options{Home: home, Now: func() time.Time { return now }}}

	disallowed := []struct {
		class string
		line  string
	}{
		{ClassMissingSkill, `{"kind":"memory","action":"add","id":"injected","body":"poison","scope":"global"}`},
		{ClassMissingSkill, `{"kind":"memory","action":"retire","id":"MEMORY-seed-mem","body":"delete"}`},
		{ClassMissingSkill, `{"kind":"feedback","body":"forged correction"}`},
		{ClassRetroLite, `{"kind":"memory","action":"add","id":"sneak","body":"sneak"}`},
		{ClassRetroLite, `{"kind":"memory","action":"retire","id":"MEMORY-seed-mem","body":"delete"}`},
		{ClassMemoryCandidates, `{"kind":"memory","action":"retire","id":"MEMORY-seed-mem","body":"delete"}`},
		{ClassMemoryCandidates, `{"kind":"memory","action":"merge","id":"MEMORY-seed-mem","body":"overwrite"}`},
		{ClassMemoryCandidates, `{"kind":"proposal","target":"harness","title":"Sneak","body":"sneak"}`},
		{ClassMemoryCandidates, `{"kind":"feedback","body":"sneak"}`},
		{ClassMemoryConsolidation, `{"kind":"feedback","body":"sneak"}`},
		{ClassHarnessEdit, `{"kind":"memory","action":"add","id":"sneak","body":"sneak"}`},
		{ClassDistill, `{"kind":"feedback","body":"sneak"}`},
	}
	for _, tc := range disallowed {
		fs, _, stats := r.processDraftLinesStats(root, tc.class, []byte(tc.line+"\n"))
		if stats.valid != 0 || stats.invalid != 1 || len(fs) != 1 || fs[0].Rule != "invalid" {
			t.Errorf("%s accepted out-of-contract line %s: stats=%+v fs=%+v", tc.class, tc.line, stats, fs)
		}
	}
	// Seed memory remained active and unmodified.
	recs, _, err := brain.Get(bo, []string{"MEMORY-seed-mem"})
	if err != nil || len(recs) != 1 || recs[0].Status != "closed" || !strings.Contains(recs[0].Body, "seeded derived") {
		t.Errorf("seed memory mutated by disallowed drafts: %+v (%v)", recs, err)
	}
}

func TestWorkerFailureSurfacesCredentialPathsAndRedactsEdges(t *testing.T) {
	t.Parallel()

	r := &run{}
	tok := "gh" + "p_" + strings.Repeat("Z", 36)
	res := isolate.WorkerResult{
		Verdict: "FAIL",
		Reasons: []string{isolate.ReasonCredentials},
		Notes: []string{
			// Normal multi-kind entry + token-bearing filename.
			"auto: run1: credentials left in the sandbox (BUG-160): tmp/a.txt (aws-key, github-token), tmp/leak-" + tok + " (aws-key, github-token)",
			// Quoted secret containing ", " must fail closed.
			`auto: run2: credentials left in the sandbox (BUG-160): tmp/tok` + `en="abc, def" (name)`,
			// Newline-split secret must be collapsed before redaction.
			"auto: run3: credentials left in the sandbox (BUG-160): tmp/pass" + "word:\n" + "secret123456 (name)",
			// Long multi-byte UTF-8 path must trim on valid rune boundaries.
			"auto: run4: credentials left in the sandbox (BUG-160): tmp/" + strings.Repeat("é", 220) + " (name)",
			// Unrelated note merely mentioning ReasonCredentials without (BUG-160): marker must be ignored.
			"auto: run5: unrelated note mentioning credentials left in the sandbox without marker",
		},
	}
	_, fs := r.handleWorkerFailure(ClassHarnessEdit, "work/auto-harness-edit-202611260405", "isolate", res, nil)
	var credFindings []Finding
	for _, f := range fs {
		if f.Rule == "credentials" {
			credFindings = append(credFindings, f)
		}
	}
	if len(credFindings) != 4 {
		t.Fatalf("credFindings = %d, want 4: %+v", len(credFindings), credFindings)
	}
	if !strings.Contains(credFindings[0].Text, "tmp/a.txt (aws-key, github-token)") {
		t.Errorf("multi-kind clean entry lost or mangled: %q", credFindings[0].Text)
	}
	if strings.Contains(credFindings[0].Text, tok) || strings.Contains(credFindings[0].Text, "[redacted by fyr], github-token)") {
		t.Errorf("token entry leaked or split multi-kind suffix: %q", credFindings[0].Text)
	}
	if strings.Contains(credFindings[1].Text, "abc, def") {
		t.Errorf("quoted comma-space secret leaked: %q", credFindings[1].Text)
	}
	if strings.Contains(credFindings[2].Text, "secret123456") {
		t.Errorf("newline-split secret leaked: %q", credFindings[2].Text)
	}
	if !utf8.ValidString(credFindings[3].Text) || !strings.HasSuffix(credFindings[3].Text, "...") {
		t.Errorf("UTF-8 truncation invalid: valid=%v text=%q", utf8.ValidString(credFindings[3].Text), credFindings[3].Text)
	}
}

func TestWorkerDraftsTestFileHasNoSecretPatterns(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("worker_drafts_test.go")
	if err != nil {
		t.Fatalf("read worker_drafts_test.go: %v", err)
	}
	text := string(data)
	if redact.Redact(text) == text {
		return
	}
	lineHits := 0
	for i, line := range strings.Split(text, "\n") {
		if scrubbed := redact.Redact(line); scrubbed != line {
			lineHits++
			t.Errorf("worker_drafts_test.go:%d matched secret pattern (%s)", i+1, scrubbed)
		}
	}
	if lineHits == 0 {
		t.Errorf("worker_drafts_test.go matched a multi-line secret pattern")
	}
}
