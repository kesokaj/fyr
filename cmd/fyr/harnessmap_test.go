package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestHarnessMap pins the map in README.md § How it works to the binary: the
// map is the contract for install, update and release (R9), so it must exist,
// name only verbs the binary has, and carry the verbs those procedures and the
// dream run through. A verb renamed or removed fails here, not silently in a
// picture nobody re-reads. Nothing sends experience off the machine any more
// (ADR-062), so `report` is never on the map (`fyr report` is the local-only
// usage report verb, ADR-516).
func TestHarnessMap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n## How it works\n")
	if !ok {
		t.Fatal("README.md has no § How it works; the map is required by R9")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	_, block, ok := strings.Cut(section, "```mermaid\n")
	if !ok {
		t.Fatal("README.md § How it works has no mermaid map")
	}
	block, _, _ = strings.Cut(block, "```")
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`fyr ([a-z][a-z-]*)`).FindAllStringSubmatch(block, -1) {
		seen[m[1]] = true
		if !strings.Contains(shortPage, "\n  "+m[1]+" ") {
			t.Errorf("the map names `fyr %s`, which the binary does not have", m[1])
		}
	}
	for _, verb := range []string{"install", "update", "dream", "auto"} {
		if !seen[verb] {
			t.Errorf("the map does not show `fyr %s`", verb)
		}
	}
	if seen["report"] || strings.Contains(block, "styrman-harness-feedback") {
		t.Error("the map still draws the report path back to the feedback repository")
	}
	if seen["models"] {
		t.Error("the map shows fyr models, which is a local-only catalog verb (ADR-519)")
	}
}
