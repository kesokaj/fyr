package search

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func queryTestCoActivity(t *testing.T, dir, rel, kind string, minCount int) []coActivityRow {
	t.Helper()
	st, err := openStore(projectDB(dir), io.Discard)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.close()
	rows, err := queryCoActivityDB(st, rel, kind, minCount)
	if err != nil {
		t.Fatalf("queryCoActivityDB: %v", err)
	}
	return rows
}

func initCoActivityRepo(t *testing.T) string {
	t.Helper()
	dir := newRepo(t)
	writeFile(t, dir, "pkg/alpha/alpha.go", `package alpha

// TargetAlpha handles alpha requests; see BUG-042.
func TargetAlpha() string {
	return helperAlpha()
}

func helperAlpha() string {
	return "alpha"
}
`)
	writeFile(t, dir, "pkg/beta/beta.go", `package beta

import "example/pkg/alpha"

func CallerBeta() string {
	return alpha.TargetAlpha()
}
`)
	writeFile(t, dir, "pkg/gamma/gamma.go", `package gamma

func GammaHelper() string {
	return "gamma"
}
`)
	commitAll(t, dir, "seed co-activity repo")
	return dir
}

func TestStopHookRecordsCoActivityAndSurfacesHints(t *testing.T) {
	dir := initCoActivityRepo(t)
	home := t.TempDir()
	var idxOut bytes.Buffer
	if err := Index(Options{Dir: dir, Home: home}, &idxOut); err != nil {
		t.Fatalf("Index: %v", err)
	}

	alphaPath := filepath.Join(dir, "pkg", "alpha", "alpha.go")
	betaPath := filepath.Join(dir, "pkg", "beta", "beta.go")
	gammaPath := filepath.Join(dir, "pkg", "gamma", "gamma.go")

	// Simulate 2 sessions co-modifying alpha.go and beta.go (below count >= 3 threshold).
	// Also fire Stop twice within each session to verify multi-turn Stop deduplication (Finding 2).
	for i := 1; i <= 2; i++ {
		sess := "sess-co-" + string(rune('0'+i))
		if _, err := CheckAndRecordRead(dir, sess, alphaPath, 1, 10, 1); err != nil {
			t.Fatalf("CheckAndRecordRead alpha: %v", err)
		}
		if _, err := CheckAndRecordRead(dir, sess, betaPath, 1, 10, 1); err != nil {
			t.Fatalf("CheckAndRecordRead beta: %v", err)
		}
		if _, err := CheckAndRecordRead(dir, sess, gammaPath, 1, 10, 1); err != nil {
			t.Fatalf("CheckAndRecordRead gamma: %v", err)
		}
		if _, err := CheckPreWriteHash(dir, sess, alphaPath); err != nil {
			t.Fatalf("CheckPreWriteHash alpha: %v", err)
		}
		if _, err := CheckPreWriteHash(dir, sess, betaPath); err != nil {
			t.Fatalf("CheckPreWriteHash beta: %v", err)
		}
		payload := `{"conversationId":"` + sess + `","workspacePaths":["` + filepath.ToSlash(dir) + `"]}`
		for turn := 1; turn <= 2; turn++ {
			var stopOut bytes.Buffer
			if err := RunHook(Options{Dir: dir, Home: home, Hook: "stop"}, strings.NewReader(payload), &stopOut); err != nil {
				t.Fatalf("RunHook stop %d turn %d: %v", i, turn, err)
			}
		}
	}

	// At count == 2 (even though Stop ran twice per session), minCount 3 should return empty.
	if rows := queryTestCoActivity(t, dir, "pkg/alpha/alpha.go", "co_modified", 3); len(rows) != 0 {
		t.Fatalf("expected 0 rows at count=2 (minCount=3), got %+v", rows)
	}

	// Session 3 modifies alpha.go (read -> CheckPreWriteHash -> disk edit -> post-edit re-read, Finding 1)
	// and beta.go (via on-disk edit after read).
	sess3 := "sess-co-3"
	if _, err := CheckAndRecordRead(dir, sess3, alphaPath, 1, 10, 1); err != nil {
		t.Fatalf("CheckAndRecordRead alpha s3: %v", err)
	}
	if _, err := CheckAndRecordRead(dir, sess3, betaPath, 1, 10, 1); err != nil {
		t.Fatalf("CheckAndRecordRead beta s3: %v", err)
	}
	if _, err := CheckPreWriteHash(dir, sess3, alphaPath); err != nil {
		t.Fatalf("CheckPreWriteHash alpha s3: %v", err)
	}
	origAlpha, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatalf("ReadFile alpha: %v", err)
	}
	if err := os.WriteFile(alphaPath, append(origAlpha, []byte("\n// alpha edited in s3\n")...), 0o644); err != nil {
		t.Fatalf("WriteFile alpha: %v", err)
	}
	// Post-edit re-read in the same session before Stop (Finding 1 regression check):
	if _, err := CheckAndRecordRead(dir, sess3, alphaPath, 1, 12, 2); err != nil {
		t.Fatalf("post-edit CheckAndRecordRead alpha s3: %v", err)
	}
	origBeta, err := os.ReadFile(betaPath)
	if err != nil {
		t.Fatalf("ReadFile beta: %v", err)
	}
	if err := os.WriteFile(betaPath, append(origBeta, []byte("\n// touched in s3\n")...), 0o644); err != nil {
		t.Fatalf("WriteFile beta: %v", err)
	}
	var stopOut3 bytes.Buffer
	payload3 := `{"conversationId":"` + sess3 + `","workspacePaths":["` + filepath.ToSlash(dir) + `"]}`
	if err := RunHook(Options{Dir: dir, Home: home, Hook: "stop"}, strings.NewReader(payload3), &stopOut3); err != nil {
		t.Fatalf("RunHook stop 3: %v", err)
	}
	if err := os.WriteFile(alphaPath, origAlpha, 0o644); err != nil {
		t.Fatalf("restore alpha: %v", err)
	}
	if err := os.WriteFile(betaPath, origBeta, 0o644); err != nil {
		t.Fatalf("restore beta: %v", err)
	}

	rows := queryTestCoActivity(t, dir, "pkg/alpha/alpha.go", "co_modified", 3)
	if len(rows) != 1 || rows[0].FileA != "pkg/alpha/alpha.go" || rows[0].FileB != "pkg/beta/beta.go" || rows[0].Count != 3 {
		t.Fatalf("expected 1 co_modified row with count 3, got %+v", rows)
	}

	readRows := queryTestCoActivity(t, dir, "pkg/gamma/gamma.go", "co_read", 2)
	if len(readRows) == 0 {
		t.Fatalf("expected co_read rows for pkg/gamma/gamma.go, got none")
	}

	brainRefs := func(target string) []LinkedRecord {
		if strings.Contains(target, "alpha.go") {
			return []LinkedRecord{
				{ID: "BUG-101", Status: "open", Title: "Alpha nil pointer on retry"},
				{ID: "BUG-102", Status: "fixed", Title: "Old fixed bug should be hidden"},
				{ID: "ADR-050", Status: "accepted", Title: "Alpha uses extracted call edges"},
				{ID: "ADR-049", Status: "superseded", Title: "Superseded ADR should be hidden"},
			}
		}
		if target == "BUG-042" {
			return []LinkedRecord{
				{ID: "BUG-042", Status: "open", Title: "CallerOne retry loop"},
			}
		}
		return nil
	}

	var sliceOut bytes.Buffer
	if err := Search(Options{
		Dir:       dir,
		Home:      home,
		Slice:     "TargetAlpha",
		Hops:      2,
		Budget:    150,
		BrainRefs: brainRefs,
	}, &sliceOut); err != nil {
		t.Fatalf("Search --slice: %v", err)
	}
	gotSlice := sliceOut.String()
	for _, want := range []string{
		"co_change_hint:",
		"pkg/alpha/alpha.go <-> pkg/beta/beta.go (co_modified: 3)",
		"linked_records:",
		"BUG-042 [open] CallerOne retry loop",
		"BUG-101 [open] Alpha nil pointer on retry",
		"ADR-050 [accepted] Alpha uses extracted call edges",
	} {
		if !strings.Contains(gotSlice, want) {
			t.Errorf("--slice output missing %q in:\n%s", want, gotSlice)
		}
	}
	for _, forbid := range []string{"BUG-102", "ADR-049"} {
		if strings.Contains(gotSlice, forbid) {
			t.Errorf("--slice output should filter out inactive record %q in:\n%s", forbid, gotSlice)
		}
	}

	var affOut bytes.Buffer
	if err := Search(Options{
		Dir:       dir,
		Home:      home,
		Affected:  "TargetAlpha",
		BrainRefs: brainRefs,
	}, &affOut); err != nil {
		t.Fatalf("Search --affected: %v", err)
	}
	gotAff := affOut.String()
	for _, want := range []string{
		"co_change_hint:",
		"pkg/alpha/alpha.go <-> pkg/beta/beta.go (co_modified: 3)",
		"linked_records:",
		"BUG-042 [open] CallerOne retry loop",
		"BUG-101 [open] Alpha nil pointer on retry",
		"ADR-050 [accepted] Alpha uses extracted call edges",
	} {
		if !strings.Contains(gotAff, want) {
			t.Errorf("--affected output missing %q in:\n%s", want, gotAff)
		}
	}
	for _, forbid := range []string{"BUG-102", "ADR-049"} {
		if strings.Contains(gotAff, forbid) {
			t.Errorf("--affected output should filter out inactive record %q in:\n%s", forbid, gotAff)
		}
	}
}
