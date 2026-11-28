package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckAndRecordReadConsumeOnHit(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	var src strings.Builder
	src.WriteString("package service\n\n")
	src.WriteString("// ExecuteTask runs the primary task.\n")
	src.WriteString("func ExecuteTask(id string) error {\n")
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&src, "\t_ = %d\n", i)
	}
	src.WriteString("\treturn HelperFunc(id)\n}\n\n")
	src.WriteString("func HelperFunc(id string) error {\n\treturn nil\n}\n")
	writeFile(t, root, "internal/service/task.go", src.String())
	commitAll(t, root, "add task.go")

	if err := Index(Options{Dir: root, Home: home}, os.Stdout); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// 1st read stamps sha256 and allows the read.
	d1, err := CheckAndRecordRead(root, "conv-alpha", "internal/service/task.go", 1, 45, 1)
	if err != nil {
		t.Fatalf("1st CheckAndRecordRead: %v", err)
	}
	if !d1.Allowed || d1.SHA256 == "" || d1.HitCount != 1 {
		t.Fatalf("1st read = %+v, want Allowed=true, non-empty SHA256, HitCount=1", d1)
	}

	// 2nd unchanged covering read (sub-range 5..25 covered by 1..45) returns [UNCHANGED] + skeleton and consumes stamp.
	d2, err := CheckAndRecordRead(root, "conv-alpha", "internal/service/task.go", 5, 25, 2)
	if err != nil {
		t.Fatalf("2nd CheckAndRecordRead: %v", err)
	}
	if d2.Allowed {
		t.Fatalf("2nd unchanged covering read should be denied, got Allowed=true: %+v", d2)
	}
	if !strings.HasPrefix(d2.Reason, "[UNCHANGED]") {
		t.Errorf("2nd read Reason = %q, want prefix [UNCHANGED]", d2.Reason)
	}
	if !strings.Contains(d2.Reason, "ExecuteTask") || !strings.Contains(d2.Reason, "HelperFunc") {
		t.Errorf("2nd read Reason missing symbol skeleton:\n%s", d2.Reason)
	}

	// 3rd retry remains denied while the sha256 stamp is unchanged (persistent hash ledger).
	d3, err := CheckAndRecordRead(root, "conv-alpha", "internal/service/task.go", 5, 25, 3)
	if err != nil {
		t.Fatalf("3rd CheckAndRecordRead: %v", err)
	}
	if d3.Allowed || d3.HitCount != 3 {
		t.Fatalf("3rd read with unchanged sha256 should remain denied with HitCount=3, got: %+v", d3)
	}

	// Verify read_ledger_stats persisted blocked turns 2 and 3.
	st, _, err := openLedgerStore(root)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	var savedTotal, rowsTotal int
	if err := st.db.QueryRow(`SELECT COALESCE(SUM(saved_count), 0), COUNT(*) FROM read_ledger_stats WHERE conversation_id = 'conv-alpha'`).Scan(&savedTotal, &rowsTotal); err != nil || savedTotal != 2 || rowsTotal != 2 {
		st.close()
		t.Fatalf("read_ledger_stats savedTotal=%d rowsTotal=%d err=%v, want 2, 2", savedTotal, rowsTotal, err)
	}
	st.close()

	// Reading a wider range (1..45) in a conversation that only stamped 5..25 is NOT covered, so it is allowed.
	w1, err := CheckAndRecordRead(root, "conv-wider", "internal/service/task.go", 5, 25, 1)
	if err != nil || !w1.Allowed {
		t.Fatalf("wider setup 5..25 = %+v, err=%v; want Allowed", w1, err)
	}
	d4, err := CheckAndRecordRead(root, "conv-wider", "internal/service/task.go", 1, 45, 2)
	if err != nil {
		t.Fatalf("4th CheckAndRecordRead (wider range): %v", err)
	}
	if !d4.Allowed {
		t.Fatalf("wider range 1..45 should not be covered by 5..25, got Denied: %+v", d4)
	}

	// Modifying the file on disk changes sha256, so the next read is allowed.
	writeFile(t, root, "internal/service/task.go", src.String()+"\n// edited\n")
	d5, err := CheckAndRecordRead(root, "conv-alpha", "internal/service/task.go", 1, 45, 5)
	if err != nil {
		t.Fatalf("5th CheckAndRecordRead (after edit): %v", err)
	}
	if !d5.Allowed || d5.SHA256 == d1.SHA256 {
		t.Fatalf("read after file modification = %+v, want Allowed=true and new SHA256 (was %s)", d5, d1.SHA256)
	}
}

func TestRegisterCapsulePivotsAndCompactionReset(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	writeFile(t, root, "pkg/alpha.go", "package pkg\n\nfunc Alpha() int {\n\treturn 42\n}\n")
	commitAll(t, root, "add alpha.go")

	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(strings.Repeat("x", 2000)), 0o600); err != nil {
		t.Fatal(err)
	}

	checkTurn := func(rawPrompt string) (SessionState, error) {
		st, _, err := openLedgerStore(root)
		if err != nil {
			return SessionState{}, err
		}
		defer st.close()
		state, _, err := checkTurnOrMidTurnWithStore(context.Background(), st, HookInput{
			ConversationID: "conv-compact",
			TranscriptPath: transcript,
		}, rawPrompt)
		return state, err
	}

	st0, err := checkTurn("turn-0")
	if err != nil {
		t.Fatalf("checkTurnOrMidTurnWithStore initial: %v", err)
	}
	if st0.GenerationID != 0 || st0.Compacted || st0.LastTranscriptBytes != 2000 {
		t.Fatalf("initial SessionState = %+v, want Gen=0, Compacted=false, Bytes=2000", st0)
	}

	capsule := "# slice: Alpha (budget: 2000 tokens)\n[Hop 0] pkg/alpha.go:3-5 (Alpha)\n  3: func Alpha() int {\n"
	pivots := ParseCapsulePivots(capsule)
	if len(pivots) != 1 || pivots[0].Path != "pkg/alpha.go" || pivots[0].StartLine != 3 || pivots[0].EndLine != 5 {
		t.Fatalf("ParseCapsulePivots = %+v, want [{Path:pkg/alpha.go StartLine:3 EndLine:5}]", pivots)
	}
	if err := RegisterCapsulePivots(filepath.Join(root, "pkg"), "conv-compact", 1, pivots); err != nil {
		t.Fatalf("RegisterCapsulePivots from subdir: %v", err)
	}
	if got := CanonicalizePath("", "pkg/alpha.go"); !filepath.IsAbs(filepath.FromSlash(got)) {
		t.Fatalf("CanonicalizePath(\"\", relative) = %q, want absolute path", got)
	}
	nonCheckout := t.TempDir()
	if _, err := CheckAndRecordRead(nonCheckout, "conv-compact", "foo.go", 1, 10, 1); err == nil {
		t.Fatalf("CheckAndRecordRead on non-checkout directory succeeded, want errNotCheckout")
	}
	if _, err := os.Stat(filepath.Join(nonCheckout, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("non-checkout directory had .agents created: err=%v", err)
	}

	// Reading lines 3..5 in generation 0 hits the pre-seeded capsule pivot.
	hit, err := CheckAndRecordRead(root, "conv-compact", "pkg/alpha.go", 3, 5, 1)
	if err != nil {
		t.Fatalf("CheckAndRecordRead on pivot: %v", err)
	}
	if hit.Allowed || !strings.HasPrefix(hit.Reason, "[UNCHANGED]") {
		t.Fatalf("expected capsule pivot read to be deduplicated, got %+v", hit)
	}

	// Re-stamp pkg/alpha.go full read at generation 0.
	if dec, err := CheckAndRecordRead(root, "conv-compact", "pkg/alpha.go", 1, 5, 2); err != nil || !dec.Allowed {
		t.Fatalf("re-stamp read: %+v, %v", dec, err)
	}

	// Grow transcript to 2500 bytes: no compaction.
	if err := os.WriteFile(transcript, []byte(strings.Repeat("x", 2500)), 0o600); err != nil {
		t.Fatal(err)
	}
	st1, err := checkTurn("turn-1")
	if err != nil {
		t.Fatalf("checkTurnOrMidTurnWithStore grow: %v", err)
	}
	if st1.GenerationID != 0 || st1.Compacted {
		t.Fatalf("after growth SessionState = %+v, want Gen=0, Compacted=false", st1)
	}

	// Shrink transcript from 2500 to 1500 bytes (40% shrinkage > 20%): increments generation_id.
	if err := os.WriteFile(transcript, []byte(strings.Repeat("x", 1500)), 0o600); err != nil {
		t.Fatal(err)
	}
	st2, err := checkTurn("turn-2")
	if err != nil {
		t.Fatalf("checkTurnOrMidTurnWithStore shrink: %v", err)
	}
	if st2.GenerationID != 1 || !st2.Compacted || st2.LastTranscriptBytes != 1500 {
		t.Fatalf("after >20%% shrinkage SessionState = %+v, want Gen=1, Compacted=true, Bytes=1500", st2)
	}

	// Reading pkg/alpha.go after compaction is allowed because generation_id incremented to 1.
	afterCompact, err := CheckAndRecordRead(root, "conv-compact", "pkg/alpha.go", 1, 5, 3)
	if err != nil {
		t.Fatalf("CheckAndRecordRead after compaction: %v", err)
	}
	if !afterCompact.Allowed || afterCompact.GenerationID != 1 {
		t.Fatalf("read after compaction = %+v, want Allowed=true, GenerationID=1", afterCompact)
	}
}

func TestWorktreeCanonicalizationAndPreWriteHash(t *testing.T) {
	t.Parallel()
	wt1 := newRepo(t)
	wt2 := newRepo(t)
	writeFile(t, wt1, "pkg/shared.go", "package pkg\n\nfunc Shared() string { return \"v1\" }\n")
	writeFile(t, wt2, "pkg/shared.go", "package pkg\n\nfunc Shared() string { return \"v1\" }\n")
	commitAll(t, wt1, "wt1")
	commitAll(t, wt2, "wt2")

	// Create a symlink to wt1 to verify EvalSymlinks canonicalization.
	linkParent := t.TempDir()
	wt1Link := filepath.Join(linkParent, "wt1-symlink")
	if err := os.Symlink(wt1, wt1Link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	c1 := CanonicalizePath(wt1, "pkg/shared.go")
	c1Link := CanonicalizePath(wt1Link, "pkg/shared.go")
	c2 := CanonicalizePath(wt2, "pkg/shared.go")
	if c1 == "" || c1 != c1Link {
		t.Fatalf("CanonicalizePath symlink mismatch: c1=%q c1Link=%q", c1, c1Link)
	}
	if c1 == c2 {
		t.Fatalf("CanonicalizePath worktrees collided: c1=%q c2=%q", c1, c2)
	}

	// Read via wt1, then read via wt1Link in the same DB -> hits the ledger.
	d1, err := CheckAndRecordRead(wt1, "conv-wt", "pkg/shared.go", 0, 0, 1)
	if err != nil || !d1.Allowed {
		t.Fatalf("wt1 1st read: %+v, %v", d1, err)
	}
	d1Link, err := CheckAndRecordRead(wt1, "conv-wt", filepath.Join(wt1Link, "pkg/shared.go"), 0, 0, 2)
	if err != nil || d1Link.Allowed {
		t.Fatalf("wt1 symlink 2nd read should hit same canonical path, got %+v, %v", d1Link, err)
	}
	// Reading wt2's file against wt1's DB does not collide with wt1's file.
	d2, err := CheckAndRecordRead(wt1, "conv-wt", filepath.Join(wt2, "pkg/shared.go"), 0, 0, 2)
	if err != nil || !d2.Allowed {
		t.Fatalf("wt2 read should not collide with wt1, got %+v, %v", d2, err)
	}

	// Pre-write hash check right after a 2nd-read [UNCHANGED] consume (d1Link above consumed wt1's range stamp):
	// external disk edit must still be caught by CheckPreWriteHashWithEdit without requiring a 3rd read.
	writeFile(t, wt1, "pkg/shared.go", "package pkg\n\nfunc Shared() string { return \"external-edit\" }\n")

	pwConflict, err := CheckPreWriteHashWithEdit(wt1, "", wt1, "conv-wt", "pkg/shared.go", nil)
	if err != nil {
		t.Fatalf("CheckPreWriteHashWithEdit on modified file: %v", err)
	}
	if pwConflict.Allowed {
		t.Fatalf("CheckPreWriteHashWithEdit should deny write when disk hash changed after 2nd-read consume: %+v", pwConflict)
	}

	// Re-read the updated file, then verify CheckPreWriteHashWithEdit allows the write when disk matches.
	if dec, err := CheckAndRecordRead(wt1, "conv-wt", "pkg/shared.go", 0, 0, 4); err != nil || !dec.Allowed {
		t.Fatalf("re-read updated file: %+v, %v", dec, err)
	}
	pwOK, err := CheckPreWriteHashWithEdit(wt1, "", wt1, "conv-wt", "pkg/shared.go", nil)
	if err != nil || !pwOK.Allowed {
		t.Fatalf("CheckPreWriteHashWithEdit on matching hash = %+v, %v; want Allowed=true", pwOK, err)
	}
}

func TestParseBashFileReadAndInterception(t *testing.T) {
	t.Parallel()
	valid := []struct {
		cmd       string
		wantTool  string
		wantPath  string
		wantStart int
		wantEnd   int
	}{
		{cmd: "cat foo.go", wantTool: "cat", wantPath: "foo.go", wantStart: 1, wantEnd: 0},
		{cmd: "cat -n internal/search/store.go", wantTool: "cat", wantPath: "internal/search/store.go", wantStart: 1, wantEnd: 0},
		{cmd: "head -n 50 foo.go", wantTool: "head", wantPath: "foo.go", wantStart: 1, wantEnd: 50},
		{cmd: "head -25 foo.go", wantTool: "head", wantPath: "foo.go", wantStart: 1, wantEnd: 25},
		{cmd: "tail -n 30 foo.go", wantTool: "tail", wantPath: "foo.go", wantStart: -30, wantEnd: 0},
		{cmd: "tail -n +15 foo.go", wantTool: "tail", wantPath: "foo.go", wantStart: 15, wantEnd: 0},
		{cmd: "sed -n '1,120p' foo.go", wantTool: "sed", wantPath: "foo.go", wantStart: 1, wantEnd: 120},
		{cmd: "sed -n '10,50p;51q' foo.go", wantTool: "sed", wantPath: "foo.go", wantStart: 10, wantEnd: 50},
		{cmd: "nl -ba foo.go", wantTool: "nl", wantPath: "foo.go", wantStart: 1, wantEnd: 0},
		{cmd: "bat --paging=never -r 10:80 foo.go", wantTool: "bat", wantPath: "foo.go", wantStart: 10, wantEnd: 80},
	}
	for _, tc := range valid {
		reads := ParseBashFileReads(tc.cmd)
		if len(reads) != 1 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want 1 read", tc.cmd, reads)
			continue
		}
		got := reads[0]
		if got.Tool != tc.wantTool || got.Path != tc.wantPath || got.StartLine != tc.wantStart || got.EndLine != tc.wantEnd {
			t.Errorf("ParseBashFileReads(%q)[0] = %+v, want tool=%s path=%s %d..%d",
				tc.cmd, got, tc.wantTool, tc.wantPath, tc.wantStart, tc.wantEnd)
		}
	}

	bypass := []string{
		"cat foo.go | tee out.txt",
		"sed -n '1,120p' foo.go | xargs echo",
		"cat foo.go > out.txt",
		"cat foo.go >> out.txt",
		"cat < foo.go",
		"sed '1,120p' foo.go",
		"sed -i '1,10d' foo.go",
		"tail -f foo.go",
		"go test ./...",
	}
	for _, cmd := range bypass {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v; want empty (bypass)", cmd, got)
		}
	}

	// Route single-file cat and sed -n through ParseBashFileReads + CheckAndRecordReadInSession.
	root := newRepo(t)
	var lines strings.Builder
	lines.WriteString("package main\n\nfunc MainRun() {\n")
	for i := 1; i <= 130; i++ {
		fmt.Fprintf(&lines, "\t_ = %d\n", i)
	}
	lines.WriteString("}\n")
	writeFile(t, root, "foo.go", lines.String())
	commitAll(t, root, "add foo.go")

	reads1 := ParseBashFileReads("cat foo.go")
	if len(reads1) != 1 {
		t.Fatalf("ParseBashFileReads(cat foo.go) = %+v, want 1 read", reads1)
	}
	bfr1 := reads1[0]
	dec1, err := CheckAndRecordReadInSession(root, "", root, "conv-bash", bfr1.Path, bfr1.StartLine, bfr1.EndLine, 1)
	if err != nil || !dec1.Allowed {
		t.Fatalf("1st bash cat foo.go = %+v, err=%v; want Allowed=true", dec1, err)
	}

	// 2nd bash read via sed -n '1,120p' foo.go is covered by full cat foo.go and denied with [UNCHANGED].
	reads2 := ParseBashFileReads("sed -n '1,120p' foo.go")
	if len(reads2) != 1 {
		t.Fatalf("ParseBashFileReads(sed -n '1,120p' foo.go) = %+v, want 1 read", reads2)
	}
	bfr2 := reads2[0]
	dec2, err := CheckAndRecordReadInSession(root, "", root, "conv-bash", bfr2.Path, bfr2.StartLine, bfr2.EndLine, 2)
	if err != nil || dec2.Allowed || !strings.HasPrefix(dec2.Reason, "[UNCHANGED]") {
		t.Fatalf("2nd bash sed -n '1,120p' foo.go = %+v, err=%v; want Denied [UNCHANGED]", dec2, err)
	}

	// Non-read-only pipeline bypasses interception safely.
	if got := ParseBashFileReads("sed -n '1,120p' foo.go | tee out.txt"); len(got) != 0 {
		t.Fatalf("non-read-only pipeline should bypass read ledger, got %+v", got)
	}
}

func TestPreToolUseBlocksUnchangedRereadAndCounts(t *testing.T) {
	t.Parallel()
	repo, hooksDir, home := setupUnignoredPreToolRepo(t)
	invoke := func(payload map[string]any) HookDecisionOutput {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var out bytes.Buffer
		if err := Search(Options{Dir: hooksDir, Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(raw)}, &out); err != nil {
			t.Fatalf("pre-tool-use hook: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal hook decision %q: %v", out.String(), err)
		}
		return dec
	}
	savedCount := func(convID string) int {
		t.Helper()
		db := openDB(t, repo)
		defer db.Close()
		var n int
		_ = db.QueryRow(`SELECT COALESCE(SUM(saved_count), 0) FROM read_ledger_stats WHERE conversation_id = ?`, convID).Scan(&n)
		return n
	}

	assertPreToolFullFileLifecycle(t, repo, hooksDir, invoke, savedCount)
	assertPreToolRangedAndEnvelopeVariants(t, repo, invoke, savedCount)
}

func setupUnignoredPreToolRepo(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	gitT(t, repo, "init", "-q", "-b", "main")
	gitT(t, repo, "config", "user.email", "test@example.invalid")
	gitT(t, repo, "config", "user.name", "test")
	gitT(t, repo, "config", "commit.gpgsign", "false")
	var svc strings.Builder
	svc.WriteString("package demo\n\nfunc ExecuteService(id string) error {\n")
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&svc, "\t_ = %d\n", i)
	}
	svc.WriteString("\treturn nil\n}\n")
	writeFile(t, repo, "service.go", svc.String())

	var rng strings.Builder
	rng.WriteString("package demo\n\nfunc PartOne() int {\n")
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&rng, "\t_ = %d\n", i)
	}
	rng.WriteString("\treturn 1\n}\n\nfunc PartTwo() int {\n")
	for i := 1; i <= 16; i++ {
		fmt.Fprintf(&rng, "\t_ = %d\n", i)
	}
	rng.WriteString("\treturn 2\n}\n")
	writeFile(t, repo, "ranged.go", rng.String())
	commitAll(t, repo, "init unignored repo")
	return repo, t.TempDir(), t.TempDir()
}

func assertPreToolFullFileLifecycle(t *testing.T, repo, nonGitDir string, invoke func(map[string]any) HookDecisionOutput, savedCount func(string) int) {
	t.Helper()
	d1 := invoke(map[string]any{
		"conversationId": "conv-full",
		"workspacePaths": []string{nonGitDir, repo},
		"toolCall":       map[string]any{"name": "read_file", "args": map[string]any{"Path": "service.go"}},
	})
	if d1.Decision != "allow" {
		t.Fatalf("1st full read_file = %+v, want allow", d1)
	}
	d2 := invoke(map[string]any{
		"session_id":     "conv-full",
		"workspacePaths": []string{repo},
		"toolCall":       map[string]any{"name": "default_api:read_file", "args": map[string]any{"path": "service.go"}},
	})
	if d2.Decision != "deny" || !strings.HasPrefix(d2.Reason, "[UNCHANGED]\n") || !strings.Contains(d2.Reason, "Path:      service.go") || !strings.Contains(d2.Reason, "Range:     1-") || !strings.Contains(d2.Reason, "already in context") || !strings.Contains(d2.Reason, "func ExecuteService") {
		t.Fatalf("2nd unchanged full read_file = %+v, want deny with [UNCHANGED] persistent stamp + skeleton", d2)
	}
	if got := savedCount("conv-full"); got != 1 {
		t.Fatalf("saved_count after 1st block = %d, want 1", got)
	}
	if d3 := invoke(map[string]any{"conversationId": "conv-full", "workspacePaths": []string{repo}, "toolCall": map[string]any{"name": "read_file", "args": map[string]any{"Path": "service.go"}}}); d3.Decision != "deny" || savedCount("conv-full") != 2 {
		t.Fatalf("3rd unchanged re-read = %+v (saved=%d), want deny and saved=2", d3, savedCount("conv-full"))
	}
	if d4 := invoke(map[string]any{"conversationId": "conv-full", "workspacePaths": []string{repo}, "toolCall": map[string]any{"name": "read_file", "args": map[string]any{"Path": "service.go"}}}); d4.Decision != "deny" || savedCount("conv-full") != 3 {
		t.Fatalf("4th unchanged re-read = %+v (saved=%d), want deny and saved=3", d4, savedCount("conv-full"))
	}
	writeFile(t, repo, "service.go", "package demo\n\nfunc ExecuteService(id string) error { return nil }\n")
	if dChanged := invoke(map[string]any{"conversationId": "conv-full", "workspacePaths": []string{repo}, "toolCall": map[string]any{"name": "read_file", "args": map[string]any{"Path": "service.go"}}}); dChanged.Decision != "allow" || savedCount("conv-full") != 3 {
		t.Fatalf("read after file change = %+v (saved=%d), want allow and saved=3", dChanged, savedCount("conv-full"))
	}
}

func assertPreToolRangedAndEnvelopeVariants(t *testing.T, repo string, invoke func(map[string]any) HookDecisionOutput, savedCount func(string) int) {
	t.Helper()
	r1 := invoke(map[string]any{
		"sessionId": "conv-range",
		"cwd":       repo,
		"toolCall":  map[string]any{"name": "read_file", "args": map[string]any{"path": "ranged.go", "startLine": 1, "endLine": 20}},
	})
	if r1.Decision != "allow" {
		t.Fatalf("1st camelCase ranged read = %+v, want allow", r1)
	}
	r2 := invoke(map[string]any{
		"conversation_id": "conv-range",
		"tool_call":       map[string]any{"name": "view_file", "args": map[string]any{"absolutePath": filepath.Join(repo, "ranged.go"), "start_line": 21, "end_line": 42}},
	})
	if r2.Decision != "allow" || savedCount("conv-range") != 0 {
		t.Fatalf("2nd novel ranged read (21..42) = %+v (saved=%d), want allow and saved=0", r2, savedCount("conv-range"))
	}
	r3 := invoke(map[string]any{
		"transcriptPath":  "/tmp/agy/brain/conv-range/.system_generated/logs/transcript.jsonl",
		"workspace_paths": []string{repo},
		"tool_name":       "read_file",
		"tool_input":      map[string]any{"Path": "ranged.go", "StartLine": 1, "EndLine": 42},
	})
	if r3.Decision != "deny" || !strings.HasPrefix(r3.Reason, "[UNCHANGED]\n") || !strings.Contains(r3.Reason, "Path:      ranged.go") || !strings.Contains(r3.Reason, "Range:     1-") || savedCount("conv-range") != 1 {
		t.Fatalf("3rd covering re-read across coalesced range = %+v (saved=%d), want deny and saved=1", r3, savedCount("conv-range"))
	}
}

func TestFitLinesToBudgetClampsHeaderAndLedgerPivot(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	var src strings.Builder
	src.WriteString("package main\n\nfunc BigFunc() {\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&src, "\t_ = %d // body line\n", i)
	}
	src.WriteString("}\n")
	writeFile(t, root, "clamp.go", src.String())
	commitAll(t, root, "add clamp.go")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"BigFunc"}})

	st, err := openStore(projectDB(root), bytes.NewBuffer(nil))
	if err != nil {
		t.Fatal(err)
	}
	sc := &scope{st: st, projects: []string{root}}
	sym := RankedSymbol{Path: "clamp.go", Name: "BigFunc", StartLine: 3, EndLine: 16, Sig: "func BigFunc()"}
	block := sc.formatHop0Symbol(sliceLineCache{}, sym, 140)
	st.close()
	if block == "" || strings.Contains(block, ":3-16 ") {
		t.Fatalf("expected clamped [Hop 0] header under tight budget, got:\n%s", block)
	}
	pivots := ParseCapsulePivots(block)
	if len(pivots) != 1 || pivots[0].EndLine >= 16 {
		t.Fatalf("ParseCapsulePivots = %+v, want clamped EndLine < 16", pivots)
	}
	if err := RegisterCapsulePivotsWithHome(root, home, "conv-clamp-hdr", 1, pivots); err != nil {
		t.Fatalf("RegisterCapsulePivotsWithHome: %v", err)
	}
	tailRead, err := CheckAndRecordReadInSession(root, home, root, "conv-clamp-hdr", "clamp.go", pivots[0].EndLine+1, 16, 1)
	if err != nil || !tailRead.Allowed {
		t.Fatalf("omitted tail lines should remain readable, got %+v, err=%v", tailRead, err)
	}
}

func TestPreWritePendingHashAbortedAndMultiOccurrenceEdits(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	content := "package main\n\nfunc One() string { return \"dup\" }\nfunc Two() string { return \"dup\" }\nfunc Unique() string { return \"old\" }\n"
	writeFile(t, root, "multi.go", content)
	commitAll(t, root, "add multi.go")

	if dec, err := CheckAndRecordReadInSession(root, home, root, "conv-pending", "multi.go", 1, 5, 1); err != nil || !dec.Allowed {
		t.Fatalf("initial read: %+v, err=%v", dec, err)
	}
	// 1. Ambiguous multi-occurrence Target without AllowMultiple: disk replaces 1 occurrence, next edit must NOT falsely block.
	if dec, err := CheckPreWriteHashWithEdit(root, home, root, "conv-pending", "multi.go", map[string]any{"Target": `"dup"`, "Replacement": `"one"`}); err != nil || !dec.Allowed {
		t.Fatalf("multi-occ edit 1: %+v, err=%v", dec, err)
	}
	writeFile(t, root, "multi.go", strings.Replace(content, `"dup"`, `"one"`, 1))
	if dec, err := CheckPreWriteHashWithEdit(root, home, root, "conv-pending", "multi.go", map[string]any{"Target": `"dup"`, "Replacement": `"two"`}); err != nil || !dec.Allowed {
		t.Fatalf("next edit after single-occurrence replacement falsely blocked: %+v, err=%v", dec, err)
	}

	// 2. Re-read, then simulate an aborted single-occurrence edit (disk unchanged): next edit must NOT falsely block.
	if dec, err := CheckAndRecordReadInSession(root, home, root, "conv-pending", "multi.go", 1, 5, 2); err != nil || !dec.Allowed {
		t.Fatalf("re-read: %+v, err=%v", dec, err)
	}
	if dec, err := CheckPreWriteHashWithEdit(root, home, root, "conv-pending", "multi.go", map[string]any{"Target": `"old"`, "Replacement": `"new"`}); err != nil || !dec.Allowed {
		t.Fatalf("aborted edit prewrite: %+v, err=%v", dec, err)
	}
	if dec, err := CheckPreWriteHashWithEdit(root, home, root, "conv-pending", "multi.go", map[string]any{"Target": `"old"`, "Replacement": `"new2"`}); err != nil || !dec.Allowed {
		t.Fatalf("next edit after aborted write falsely blocked: %+v, err=%v", dec, err)
	}

	// 3. True external modification still blocks with [MODIFIED].
	writeFile(t, root, "multi.go", content+"\n// external\n")
	if dec, err := CheckPreWriteHashWithEdit(root, home, root, "conv-pending", "multi.go", nil); err != nil || dec.Allowed || !strings.Contains(dec.Reason, "[MODIFIED]") {
		t.Fatalf("external edit should deny with [MODIFIED], got %+v, err=%v", dec, err)
	}
}

func TestPreToolUseV910BashReadDedupAndClamp(t *testing.T) {
	t.Parallel()
	dir, home := setupHookRepo(t), t.TempDir()
	var dense strings.Builder
	dense.WriteString("package sample\n\nfunc DenseBash() int {\n")
	for i := 4; i < 350; i++ {
		prefix := fmt.Sprintf("\t// line %03d ", i)
		dense.WriteString(prefix + strings.Repeat("b", 116-len(prefix)) + "\n")
	}
	dense.WriteString("\treturn 1\n}\n")
	writeFile(t, dir, "dense_bash.go", dense.String())
	writeFile(t, dir, "small.go", "package sample\n\nfunc Small() int { return 1 }\n")
	writeFile(t, dir, "sub/c.go", "package sub\n\nfunc C() int { return 2 }\n")
	commitAll(t, dir, "add bash test fixtures")

	callTool := func(conv, tool string, args map[string]any) HookDecisionOutput {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": tool, "args": args},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(payload)}, &out); err != nil {
			t.Fatalf("pre-tool-use (%s): %v", tool, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	if d := callTool("conv-b-clamp", "run_command", map[string]any{"CommandLine": "grep -n '' dense_bash.go"}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[CLAMPED]\n") {
		t.Fatalf("grep -n '' dense_bash.go = %+v, want deny [CLAMPED]", d)
	}
	if d := callTool("conv-b-sed", "run_command", map[string]any{"CommandLine": "sed -n '1,10p;20,30p' dense_bash.go"}); d.Decision != "allow" {
		t.Fatalf("1st multi-range sed = %+v, want allow", d)
	}
	if d := callTool("conv-b-sed", "read_file", map[string]any{"Path": "dense_bash.go", "StartLine": 11, "EndLine": 19}); d.Decision != "allow" {
		t.Fatalf("unread gap 11..19 after multi-range sed = %+v, want allow", d)
	}
	if d := callTool("conv-b-sed", "read_file", map[string]any{"Path": "dense_bash.go", "StartLine": 20, "EndLine": 30}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]") {
		t.Fatalf("re-read 20..30 after multi-range sed = %+v, want deny [UNCHANGED]", d)
	}
	for _, tc := range []struct{ conv, cmd string }{
		{"conv-b-awk", "awk 'NR>=1 && NR<=10' dense_bash.go 2>/dev/null || true"},
		{"conv-b-py", `python3 -c "print(open('small.go').read())"`},
		{"conv-b-sub", "(cd sub && cat c.go)"},
	} {
		if d := callTool(tc.conv, "run_command", map[string]any{"CommandLine": tc.cmd}); d.Decision != "allow" {
			t.Fatalf("1st %s = %+v, want allow", tc.cmd, d)
		}
		if d := callTool(tc.conv, "run_command", map[string]any{"CommandLine": tc.cmd}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]") {
			t.Fatalf("2nd %s = %+v, want deny [UNCHANGED]", tc.cmd, d)
		}
	}
}

func TestPreToolUseBashMultiLinePipeTracksClampsAndDedupes(t *testing.T) {
	t.Parallel()
	dir, home := setupHookRepo(t), t.TempDir()
	var big strings.Builder
	big.WriteString("package sample\n\n")
	for i := 1; i <= 450; i++ {
		fmt.Fprintf(&big, "func LineFn%03d() int { return %d }\n", i, i)
	}
	writeFile(t, dir, "big_pipe.go", big.String())
	commitAll(t, dir, "add big_pipe.go")

	runBash := func(convID, cmd string) HookDecisionOutput {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": cmd}},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(payload)}, &out); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	cmd1 := "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba big_pipe.go | sed -n '1,50p'"
	if d := runBash("conv-pipe-1", cmd1); d.Decision != "allow" {
		t.Fatalf("1st ranged multi-line pipe read: got %+v, want allow", d)
	}
	if d := runBash("conv-pipe-1", cmd1); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]") {
		t.Fatalf("2nd identical multi-line pipe read: got %+v, want deny [UNCHANGED]", d)
	}
	cmd2 := "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba big_pipe.go | sed -n '51,100p'"
	if d := runBash("conv-pipe-1", cmd2); d.Decision != "allow" {
		t.Fatalf("novel range 51..100 multi-line pipe read: got %+v, want allow", d)
	}
	cmdUnranged := "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba big_pipe.go"
	if d := runBash("conv-pipe-clamp", cmdUnranged); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[CLAMPED]") {
		t.Fatalf("unranged multi-line nl on 450-line file: got %+v, want deny [CLAMPED]", d)
	}
	cmdTee := "cat big_pipe.go | tee .agents/scratch/copy.txt"
	if d := runBash("conv-pipe-tee", cmdTee); d.Decision != "allow" {
		t.Fatalf("1st tee pipe: got %+v, want allow", d)
	}
	if d := runBash("conv-pipe-tee", cmdTee); d.Decision != "allow" {
		t.Fatalf("2nd tee pipe should not be falsely tracked/denied: got %+v", d)
	}
}
