package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckAndRecordReadConsumeOnHit(t *testing.T) {
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

	// 3rd retry is unconditionally allowed because the 2nd read consumed the stamp.
	d3, err := CheckAndRecordRead(root, "conv-alpha", "internal/service/task.go", 5, 25, 3)
	if err != nil {
		t.Fatalf("3rd CheckAndRecordRead: %v", err)
	}
	if !d3.Allowed {
		t.Fatalf("3rd retry after consume-on-hit should be allowed, got Denied: %+v", d3)
	}

	// Reading a wider range (1..45) when only 5..25 is stamped is NOT covered, so it is allowed.
	d4, err := CheckAndRecordRead(root, "conv-alpha", "internal/service/task.go", 1, 45, 4)
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
	root := newRepo(t)
	writeFile(t, root, "pkg/alpha.go", "package pkg\n\nfunc Alpha() int {\n\treturn 42\n}\n")
	commitAll(t, root, "add alpha.go")

	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(strings.Repeat("x", 2000)), 0o600); err != nil {
		t.Fatal(err)
	}

	st0, err := CheckCompaction(root, "conv-compact", transcript)
	if err != nil {
		t.Fatalf("CheckCompaction initial: %v", err)
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
	st1, err := CheckCompaction(root, "conv-compact", transcript)
	if err != nil {
		t.Fatalf("CheckCompaction grow: %v", err)
	}
	if st1.GenerationID != 0 || st1.Compacted {
		t.Fatalf("after growth SessionState = %+v, want Gen=0, Compacted=false", st1)
	}

	// Shrink transcript from 2500 to 1500 bytes (40% shrinkage > 20%): increments generation_id.
	if err := os.WriteFile(transcript, []byte(strings.Repeat("x", 1500)), 0o600); err != nil {
		t.Fatal(err)
	}
	st2, err := CheckCompaction(root, "conv-compact", transcript)
	if err != nil {
		t.Fatalf("CheckCompaction shrink: %v", err)
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
	// external disk edit must still be caught by CheckPreWriteHash without requiring a 3rd read.
	writeFile(t, wt1, "pkg/shared.go", "package pkg\n\nfunc Shared() string { return \"external-edit\" }\n")

	pwConflict, err := CheckPreWriteHash(wt1, "conv-wt", "pkg/shared.go")
	if err != nil {
		t.Fatalf("CheckPreWriteHash on modified file: %v", err)
	}
	if pwConflict.Allowed {
		t.Fatalf("CheckPreWriteHash should deny write when disk hash changed after 2nd-read consume: %+v", pwConflict)
	}

	// Re-read the updated file, then verify CheckPreWriteHash allows the write when disk matches.
	if dec, err := CheckAndRecordRead(wt1, "conv-wt", "pkg/shared.go", 0, 0, 4); err != nil || !dec.Allowed {
		t.Fatalf("re-read updated file: %+v, %v", dec, err)
	}
	pwOK, err := CheckPreWriteHash(wt1, "conv-wt", "pkg/shared.go")
	if err != nil || !pwOK.Allowed {
		t.Fatalf("CheckPreWriteHash on matching hash = %+v, %v; want Allowed=true", pwOK, err)
	}
}

func TestParseBashFileReadAndInterception(t *testing.T) {
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
		got, ok := ParseBashFileRead(tc.cmd)
		if !ok {
			t.Errorf("ParseBashFileRead(%q) = false, want true", tc.cmd)
			continue
		}
		if got.Tool != tc.wantTool || got.Path != tc.wantPath || got.StartLine != tc.wantStart || got.EndLine != tc.wantEnd {
			t.Errorf("ParseBashFileRead(%q) = %+v, want tool=%s path=%s %d..%d",
				tc.cmd, got, tc.wantTool, tc.wantPath, tc.wantStart, tc.wantEnd)
		}
	}

	bypass := []string{
		"cat foo.go | grep bar",
		"sed -n '1,120p' foo.go | wc -l",
		"cat foo.go > out.txt",
		"cat foo.go >> out.txt",
		"cat < foo.go",
		"cat foo.go && rm foo.go",
		"cat foo.go; ls",
		"cat a.go b.go",
		"sed '1,120p' foo.go",
		"sed -i '1,10d' foo.go",
		"tail -f foo.go",
		"go test ./...",
	}
	for _, cmd := range bypass {
		if got, ok := ParseBashFileRead(cmd); ok {
			t.Errorf("ParseBashFileRead(%q) = %+v, true; want bypass (false)", cmd, got)
		}
	}

	// Route single-file cat and sed -n through CheckAndRecordBashRead.
	root := newRepo(t)
	var lines strings.Builder
	lines.WriteString("package main\n\nfunc MainRun() {\n")
	for i := 1; i <= 130; i++ {
		fmt.Fprintf(&lines, "\t_ = %d\n", i)
	}
	lines.WriteString("}\n")
	writeFile(t, root, "foo.go", lines.String())
	commitAll(t, root, "add foo.go")

	dec1, intercepted, err := CheckAndRecordBashRead(root, "conv-bash", "cat foo.go", 1)
	if err != nil || !intercepted || !dec1.Allowed {
		t.Fatalf("1st bash cat foo.go = %+v, intercepted=%v, err=%v; want Allowed=true", dec1, intercepted, err)
	}

	// 2nd bash read via sed -n '1,120p' foo.go is covered by full cat foo.go and denied with [UNCHANGED].
	dec2, intercepted, err := CheckAndRecordBashRead(root, "conv-bash", "sed -n '1,120p' foo.go", 2)
	if err != nil || !intercepted || dec2.Allowed || !strings.HasPrefix(dec2.Reason, "[UNCHANGED]") {
		t.Fatalf("2nd bash sed -n '1,120p' foo.go = %+v, intercepted=%v, err=%v; want Denied [UNCHANGED]", dec2, intercepted, err)
	}

	// Pipeline bypasses interception safely.
	_, intercepted, err = CheckAndRecordBashRead(root, "conv-bash", "sed -n '1,120p' foo.go | head -n 5", 3)
	if err != nil || intercepted {
		t.Fatalf("pipeline should bypass read ledger, got intercepted=%v, err=%v", intercepted, err)
	}
}
