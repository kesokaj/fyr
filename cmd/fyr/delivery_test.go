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
// (project review 2026-10-09, row 3, BUG-167).
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
	// The version to name is the newest of git's tags and the release change
	// records: the release commit stores its change record and updates this
	// paragraph before it is tagged (DEPLOY-remote § Releases), so on that
	// commit the record is ahead of the tag.
	// Without tags (a tree with no history) the old release records the
	// migration kept are not the whole history, so nothing can be checked.
	out, err := exec.Command("git", "-C", filepath.Join("..", ".."), "tag", "--list", "v*").Output()
	versions := strings.Fields(string(out))
	if err != nil || len(versions) == 0 {
		t.Skipf("no vX.Y.Z tag to compare with (%v)", err)
	}
	trailData, err := os.ReadFile(filepath.Join("..", "..", ".agents", "trail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	newest, found := newestVersion(versions, trailData)
	if !found {
		t.Skip("no vX.Y.Z tag in this clone")
	}
	tag := newest
	if !strings.Contains(para, "newest tag "+tag+",") {
		t.Errorf("SPEC-fyr-delivery § Where things stand does not say \"newest tag %s\"; the release commit updates it (deploy/remote.md § Releases)", tag)
	}
}

// newestVersion is the newest vX.Y.Z among the tags and the release change
// records in a trail.jsonl (an ID ending -release-vX.Y.Z, or -release-vX-Y-Z
// as a title's slug gives it, with an optional trailing -YYYY-MM-DD when the
// title ends with a date).
func newestVersion(tags []string, trail []byte) (string, bool) {
	versions := append([]string(nil), tags...)
	release := regexp.MustCompile(`"id":"CHANGE-[^"]*-release-v(\d+)[.-](\d+)[.-](\d+)(?:-\d{4}-\d{2}-\d{2})?"`)
	for _, m := range release.FindAllStringSubmatch(string(trail), -1) {
		versions = append(versions, "v"+m[1]+"."+m[2]+"."+m[3])
	}
	var newest [3]int
	version := regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	found := false
	for _, e := range versions {
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
	return fmt.Sprintf("v%d.%d.%d", newest[0], newest[1], newest[2]), found
}

// On a release commit the release record is ahead of the tags, and the
// paragraph must name it (DEPLOY-remote § Releases runs the test before the tag).
func TestNewestVersion(t *testing.T) {
	trail := []byte(`{"date":"2026-10-25","id":"CHANGE-2026-10-25-1200-release-v3-0-0","kind":"change"}` + "\n" + `{"id":"CHANGE-2026-09-18-release-v1.2.3"}`)
	if got, ok := newestVersion([]string{"v2.7.0", "v2.10.1", "junk"}, trail); !ok || got != "v3.0.0" {
		t.Errorf("newestVersion = %s %v, want v3.0.0", got, ok)
	}
	dated := []byte(`{"id":"CHANGE-2026-11-01-2004-change-release-v6-1-0-2026-11-01"}`)
	if got, ok := newestVersion([]string{"v6.0.0"}, dated); !ok || got != "v6.1.0" {
		t.Errorf("dated release record = %s %v, want v6.1.0", got, ok)
	}
	if got, _ := newestVersion([]string{"v2.10.1", "v2.9.9"}, nil); got != "v2.10.1" {
		t.Errorf("tags only = %s, want v2.10.1 (numbers, not text)", got)
	}
}
