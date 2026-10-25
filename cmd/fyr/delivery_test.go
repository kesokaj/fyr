package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The delivery claim's one home (AGENTS.md rule 6), specs/fyr/delivery.md
// § Where things stand, names the newest tag and every verb the binary ships.
// It said v2.4.1 and left out `metrics` while the repository was at v2.6.2
// (project review 2026-10-09, row 3, BUG-167). The newest tag is the newest
// release entry under changes/, which the release commit writes together with
// this paragraph (deploy/remote.md § Releases).
func TestDeliverySpecCurrent(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".agents", "docs", "specs", "fyr", "delivery.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, para, ok := strings.Cut(string(data), "\n**Built and shipped**")
	if !ok {
		t.Fatal("delivery.md has no **Built and shipped** paragraph")
	}
	para, _, _ = strings.Cut(para, "\n\n")
	for name := range verbs {
		if !regexp.MustCompile(`\b` + name + `\b`).MatchString(para) {
			t.Errorf("delivery.md § Where things stand does not name the verb %q", name)
		}
	}
	entries, err := filepath.Glob(filepath.Join("..", "..", ".agents", "docs", "changes", "*-release-v*.md"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no release entries under changes/: %v", err)
	}
	var newest [3]int
	version := regexp.MustCompile(`-release-v(\d+)\.(\d+)\.(\d+)\.md$`)
	for _, e := range entries {
		m := version.FindStringSubmatch(e)
		if m == nil {
			continue
		}
		var v [3]int
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+1])
		}
		if v[0] > newest[0] || v[0] == newest[0] && (v[1] > newest[1] || v[1] == newest[1] && v[2] > newest[2]) {
			newest = v
		}
	}
	tag := fmt.Sprintf("v%d.%d.%d", newest[0], newest[1], newest[2])
	if !strings.Contains(para, "newest tag "+tag+",") {
		t.Errorf("delivery.md § Where things stand does not say \"newest tag %s\"; the release commit updates it (deploy/remote.md § Releases)", tag)
	}
}
