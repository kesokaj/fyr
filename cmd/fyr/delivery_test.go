package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// trailRecord is the body of the record id in this repository's committed
// export, .agents/trail.jsonl (brain plan D21): the trail's specs and guides
// are records since the migration, read here without a store.
func trailRecord(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".agents", "trail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(data), "\n") {
		var r struct{ ID, Body string }
		if json.Unmarshal([]byte(l), &r) == nil && r.ID == id {
			return r.Body
		}
	}
	t.Fatalf("no record %s in .agents/trail.jsonl", id)
	return ""
}

// The delivery claim's one home (AGENTS.md rule 6), SPEC-fyr-delivery
// § Where things stand, names the newest tag and every verb the binary ships.
// It said v2.4.1 and left out `metrics` while the repository was at v2.6.2
// (project review 2026-10-09, row 3, BUG-167). The newest tag is git's: the
// tag is the only version tracking (deploy/remote.md § Releases), and the
// release commit updates this paragraph.
func TestDeliverySpecCurrent(t *testing.T) {
	data := trailRecord(t, "SPEC-fyr-delivery")
	_, para, ok := strings.Cut(data, "\n**Built and shipped**")
	if !ok {
		t.Fatal("SPEC-fyr-delivery has no **Built and shipped** paragraph")
	}
	para, _, _ = strings.Cut(para, "\n\n")
	for name := range verbs {
		if !regexp.MustCompile(`\b` + name + `\b`).MatchString(para) {
			t.Errorf("SPEC-fyr-delivery § Where things stand does not name the verb %q", name)
		}
	}
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "tag", "--list", "v*").Output()
	if err != nil {
		t.Skipf("no git tags to compare with (%v): a tree with no history", err)
	}
	var newest [3]int
	version := regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	found := false
	for _, e := range strings.Fields(string(out)) {
		m := version.FindStringSubmatch(e)
		if m == nil {
			continue
		}
		found = true
		var v [3]int
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+1])
		}
		if v[0] > newest[0] || v[0] == newest[0] && (v[1] > newest[1] || v[1] == newest[1] && v[2] > newest[2]) {
			newest = v
		}
	}
	if !found {
		t.Skip("no vX.Y.Z tag in this clone")
	}
	tag := fmt.Sprintf("v%d.%d.%d", newest[0], newest[1], newest[2])
	if !strings.Contains(para, "newest tag "+tag+",") {
		t.Errorf("SPEC-fyr-delivery § Where things stand does not say \"newest tag %s\"; the release commit updates it (deploy/remote.md § Releases)", tag)
	}
}
