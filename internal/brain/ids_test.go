package brain

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// IDs (D7): BUG and ADR numbered max+1 per scope and compared as numbers,
// ADR-047a kept, past 999; every other kind <KIND>-<date>-<slug>.
func TestIDs(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	if id := add(t, o, "bug", "a", "b"); id != "BUG-001" {
		t.Fatalf("first: %s", id)
	}
	add(t, o, "bug", "b", "b", "id", "BUG-010")
	if id := add(t, o, "bug", "c", "b"); id != "BUG-011" {
		t.Errorf("after BUG-010: %s, want BUG-011 (max+1)", id)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: "dup", ID: "BUG-010", Body: strings.NewReader("b")}); !IsRefusal(err) {
		t.Errorf("an explicit duplicate id: %v, want a refusal", err)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: "x", ID: "ADR-001", Body: strings.NewReader("b")}); err == nil {
		t.Error("a bug given an ADR id was taken")
	}
	add(t, o, "adr", "lettered", "b", "id", "ADR-047a")
	add(t, o, "adr", "plain", "b", "id", "ADR-047")
	if id := add(t, o, "adr", "next", "b"); id != "ADR-048" {
		t.Errorf("after ADR-047a: %s, want ADR-048", id)
	}
	add(t, o, "bug", "nines", "b", "id", "BUG-999")
	if id := add(t, o, "bug", "past", "b"); id != "BUG-1000" {
		t.Errorf("after BUG-999: %s, want BUG-1000", id)
	}
	if id := add(t, o, "bug", "past2", "b"); id != "BUG-1001" {
		t.Errorf("after BUG-1000: %s, want BUG-1001 (numeric, not string, max)", id)
	}
	// Numbering is per scope: the global scope starts at 001.
	g := o
	g.Global = true
	if id := add(t, g, "bug", "global one", "b"); id != "BUG-001" {
		t.Errorf("global scope: %s, want BUG-001", id)
	}
	if id := add(t, o, "plan", "Brain store: Phase 1!", "- [ ] x"); id != "PLAN-2026-10-25-1430-brain-store-phase-1" {
		t.Errorf("plan id %s", id)
	}
	if id := add(t, o, "plan", "Brain store: Phase 1!", "- [ ] x"); id != "PLAN-2026-10-25-1430-brain-store-phase-1-2" {
		t.Errorf("same minute, same slug: %s, want a -2 suffix", id)
	}
	if id := add(t, o, "testing", "verify", "log"); id != "TEST-2026-10-25-1430-verify" {
		t.Errorf("testing id %s, want the TEST prefix", id)
	}
	add(t, o, "spec", "record", "b", "id", "SPEC-fyr-record")
	if _, err := Add(o, AddInput{Kind: "spec", Title: "x", ID: "PLAN-2026-x", Body: strings.NewReader("b")}); err == nil {
		t.Error("a spec given a PLAN id was taken")
	}
	// The export orders numbers numerically.
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	var bugs []string
	for _, l := range strings.Split(strings.TrimSpace(trailText(t, dir)), "\n") {
		if i := strings.Index(l, `"id":"BUG-`); i >= 0 {
			bugs = append(bugs, l[i+6:strings.Index(l[i+6:], `"`)+i+6])
		}
	}
	if got := strings.Join(bugs, " "); got != "BUG-001 BUG-010 BUG-011 BUG-999 BUG-1000 BUG-1001" {
		t.Errorf("export order %s", got)
	}
}

// TestHelperAdd is not a test: TestTwoProcessesAllocateDistinctIDs runs this
// binary twice at once with BRAIN_HELPER set, and each adds bugs.
func TestHelperAdd(t *testing.T) {
	spec := os.Getenv("BRAIN_HELPER")
	if spec == "" {
		t.Skip("helper process")
	}
	parts := strings.Split(spec, "|")
	dir, home := parts[0], parts[1]
	count, _ := strconv.Atoi(parts[2])
	o := Options{Home: home, Dir: dir}
	for i := 0; i < count; i++ {
		if _, err := Add(o, AddInput{Kind: "bug", Title: fmt.Sprintf("from %d #%d", os.Getpid(), i), Body: strings.NewReader("b")}); err != nil {
			fmt.Fprintln(os.Stderr, "add:", err)
			os.Exit(3)
		}
	}
}

// Two processes allocating at once get distinct IDs: the number is taken
// inside the write's transaction (D7, D20), and the export agrees.
func TestTwoProcessesAllocateDistinctIDs(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	const each = 15
	var wg sync.WaitGroup
	errs := make([]error, 2)
	outs := make([]bytes.Buffer, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := exec.Command(os.Args[0], "-test.run=^TestHelperAdd$")
			c.Env = append(os.Environ(), "BRAIN_HELPER="+dir+"|"+home+"|"+strconv.Itoa(each))
			c.Stdout, c.Stderr = &outs[i], &outs[i]
			errs[i] = c.Run()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i].String())
		}
	}
	list, err := List(opts(dir, home), ListFilter{Kind: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range list {
		if seen[r.ID] {
			t.Errorf("duplicate id %s", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != 2*each {
		t.Fatalf("%d distinct ids, want %d", len(seen), 2*each)
	}
	for n := 1; n <= 2*each; n++ {
		if id := fmt.Sprintf("BUG-%03d", n); !seen[id] {
			t.Errorf("%s missing: allocation skipped a number", id)
		}
	}
	if _, err := Export(opts(dir, home)); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(trailText(t, dir), "\n"); lines != 2*each {
		t.Errorf("trail.jsonl has %d lines, want %d: the export lost a concurrent write", lines, 2*each)
	}
}
