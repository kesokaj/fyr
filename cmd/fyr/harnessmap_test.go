package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	mermaidQuotedRe = regexp.MustCompile(`"[^"]*"`)
	mermaidPipeRe   = regexp.MustCompile(`\|[^|]*\|`)
	mermaidClassRe  = regexp.MustCompile(`:::[A-Za-z0-9_-]+`)
	mermaidShapeRe  = regexp.MustCompile(`\[[^\[\]]*\]|\([^()]*\)|\{[^{}]*\}`)
	mermaidEdgeRe   = regexp.MustCompile(`(?:<)?(?:-\.+->?|==+>?|--+>?|<-->|---)|-\.|\.->|--|&`)
	mermaidNodeIDRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	mermaidVerbRe   = regexp.MustCompile(`fyr ([a-z][a-z-]*)`)
	mermaidSpanRe   = regexp.MustCompile("`fyr ([a-z][a-z-]*)`")
)

var mermaidIgnoredKeywords = map[string]bool{
	"flowchart": true, "graph": true, "direction": true, "subgraph": true,
	"end": true, "style": true, "classDef": true, "class": true,
	"linkStyle": true, "click": true, "accTitle": true, "accDescr": true,
}

func extractMermaidBlocks(section string) []string {
	parts := strings.Split(section, "```mermaid\n")
	if len(parts) <= 1 {
		return nil
	}
	var blocks []string
	for _, part := range parts[1:] {
		block, _, ok := strings.Cut(part, "\n```")
		if !ok {
			block, _, _ = strings.Cut(part, "```")
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func countMermaidNodes(block string) int {
	nodes := map[string]bool{}
	for _, raw := range strings.Split(block, "\n") {
		line, _, _ := strings.Cut(raw, "%%")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		if mermaidIgnoredKeywords[fields[0]] {
			continue
		}
		clean := mermaidQuotedRe.ReplaceAllString(line, "")
		clean = mermaidPipeRe.ReplaceAllString(clean, " ")
		clean = mermaidClassRe.ReplaceAllString(clean, "")
		for i := 0; i < 4; i++ {
			next := mermaidShapeRe.ReplaceAllString(clean, "")
			if next == clean {
				break
			}
			clean = next
		}
		clean = mermaidEdgeRe.ReplaceAllString(clean, " ")
		for _, tok := range strings.Fields(clean) {
			if mermaidNodeIDRe.MatchString(tok) {
				nodes[tok] = true
			}
		}
	}
	return len(nodes)
}

func checkHarnessMapVerbs(t *testing.T, text string, re *regexp.Regexp) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		seen[m[1]] = true
		if !strings.Contains(shortPage, "\n  "+m[1]+" ") {
			t.Errorf("the map names `fyr %s`, which the binary does not have", m[1])
		}
	}
	return seen
}

// TestHarnessMap pins the map in README.md § How it works to the binary: the
// map is the contract for install, update and release (R9), so it must exist,
// split into <= 12-node Mermaid diagrams (ADR-527), name only verbs the binary
// has, and carry the verbs those procedures and the dream run through. Nothing
// sends experience off the machine any more (ADR-062), so `report` is never on
// the map (`fyr report` is the local-only usage report verb, ADR-516).
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
	blocks := extractMermaidBlocks(section)
	if len(blocks) < 2 {
		t.Fatalf("README.md § How it works has %d mermaid blocks, want >= 2 (ADR-527)", len(blocks))
	}
	for i, b := range blocks {
		if n := countMermaidNodes(b); n < 3 || n > 12 {
			t.Errorf("README.md § How it works diagram %d has %d distinct nodes, want 3..12", i+1, n)
		}
	}
	allDiagrams := strings.Join(blocks, "\n")
	seen := checkHarnessMapVerbs(t, allDiagrams, mermaidVerbRe)
	seenSec := checkHarnessMapVerbs(t, section, mermaidSpanRe)
	for _, verb := range []string{"install", "update", "dream", "auto"} {
		if !seen[verb] {
			t.Errorf("the map diagrams do not show `fyr %s`", verb)
		}
	}
	if seen["report"] || seenSec["report"] || strings.Contains(section, "fyr report") ||
		strings.Contains(allDiagrams, "styrman-harness-feedback") || strings.Contains(section, "styrman-harness-feedback") {
		t.Error("the map still draws the report path back to the feedback repository")
	}
	if seen["models"] || seenSec["models"] || strings.Contains(section, "fyr models") {
		t.Error("the map shows fyr models, which is a local-only catalog verb (ADR-519)")
	}
}

func TestCountMermaidNodes(t *testing.T) {
	sample := `flowchart LR
    %% comment nodeIgnored --> otherIgnored
    subgraph lane["Lane"]
        a["Node (one)"] --> b[("Node two")]
        b -. "fyr fake-edge" .-> c["end"]
    end
    classDef hi fill:#fff
    class a hi
    linkStyle 0 stroke:#000,stroke-width:2px`
	if got := countMermaidNodes(sample); got != 3 {
		t.Fatalf("countMermaidNodes = %d, want 3", got)
	}
}
