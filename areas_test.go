package fyr

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const areasPath = "skills/fyr-harness-audit/references/areas.md"

var (
	// runPattern reads a `-run` argument, quoted or bare, as areas.md writes it.
	runPattern = regexp.MustCompile(`-run (?:'([^']+)'|"([^"]+)"|([A-Za-z0-9_|]+))`)
	testFunc   = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
)

// TestAreasRunPatternsExist pins WS53 of the 2026-10-27 audit: every
// alternative of every `-run` pattern the harness-audit areas hand an agent
// names the start of a test function in a tracked _test.go. Go prints `ok`
// when a pattern matches nothing, so a renamed test turned an area's baseline
// into a silent no-op (WS43).
func TestAreasRunPatternsExist(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout: tracked test files cannot be listed")
	}
	text, err := os.ReadFile(areasPath)
	if err != nil {
		t.Fatal(err)
	}
	funcs := trackedTestFuncs(t)
	if missing, n := missingRunPatterns(string(text), funcs); n == 0 {
		t.Fatalf("%s holds no -run pattern; the reader no longer matches its shape", areasPath)
	} else {
		for _, m := range missing {
			t.Errorf("%s: -run alternative %q names no test function in a tracked _test.go", areasPath, m)
		}
	}
	// The check bites: a copy with one pattern renamed is caught.
	renamed := strings.Replace(string(text), "-run TestHarnessMap", "-run TestHarnessMapRenamedAway", 1)
	if renamed == string(text) {
		t.Fatal("areas.md no longer holds `-run TestHarnessMap`; pick another pattern for the must-fail copy")
	}
	if missing, _ := missingRunPatterns(renamed, funcs); !slices.Contains(missing, "TestHarnessMapRenamedAway") {
		t.Errorf("a renamed pattern was not caught: missing=%q", missing)
	}
}

// missingRunPatterns returns the `-run` alternatives in text that start no
// name in funcs, and how many alternatives it read.
func missingRunPatterns(text string, funcs []string) ([]string, int) {
	var missing []string
	n := 0
	for _, m := range runPattern.FindAllStringSubmatch(text, -1) {
		for _, alt := range strings.Split(m[1]+m[2]+m[3], "|") {
			n++
			found := false
			for _, f := range funcs {
				if strings.HasPrefix(f, alt) {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, alt)
			}
		}
	}
	return missing, n
}

// trackedTestFuncs lists every top-level Test function in the _test.go files
// git tracks.
func trackedTestFuncs(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "ls-files", "-z", "--", "*_test.go").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var funcs []string
	for _, f := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		src, err := os.ReadFile(string(f))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range testFunc.FindAllSubmatch(src, -1) {
			funcs = append(funcs, string(m[1]))
		}
	}
	return funcs
}
