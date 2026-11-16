package brain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMemoryPartsAndGlobalScope(t *testing.T) {
	// 1. NormalizeMemoryType covers canonical parts and legacy aliases.
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"semantic", "semantic", true},
		{"procedural", "procedural", true},
		{"episodic", "episodic", true},
		{"user", "semantic", true},
		{"project", "semantic", true},
		{"reference", "semantic", true},
		{"feedback", "procedural", true},
		{"", "", false},
		{"unknown", "", false},
	}
	for _, tc := range cases {
		got, err := NormalizeMemoryType(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("NormalizeMemoryType(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
		}
		if !tc.ok && !IsRefusal(err) {
			t.Errorf("NormalizeMemoryType(%q) err = %v, want refusal", tc.in, err)
		}
	}

	// 2. Global memory works outside any git checkout.
	nonGitDir := t.TempDir()
	home := t.TempDir()
	g := Options{Home: home, Dir: nonGitDir, Global: true, Now: func() time.Time { return fixed }}

	idG, err := MemoryAdd(g, MemoryEntry{
		Type:           "procedural",
		Name:           "global-rule",
		Description:    "Always run -race",
		Global:         true,
		EvidenceCount:  2,
		SourceSessions: []string{"s2", "s1"},
	}, strings.NewReader("Use go test -race everywhere."))
	if err != nil {
		t.Fatalf("global MemoryAdd outside git checkout: %v", err)
	}
	if idG != "MEMORY-global-rule" {
		t.Errorf("global ID = %q, want MEMORY-global-rule", idG)
	}

	gShow, err := MemoryShow(g)
	if err != nil {
		t.Fatalf("global MemoryShow: %v", err)
	}
	if !strings.Contains(gShow, "- global-rule (procedural, 1.00) [global] — Always run -race") {
		t.Errorf("global MemoryShow = %q", gShow)
	}

	// Global semantic memory is loaded into project Resume when >= 0.40.
	if _, err := MemoryAdd(g, MemoryEntry{
		Type:        "semantic",
		Name:        "shared-sem",
		Description: "Global semantic fact",
		Global:      true,
	}, strings.NewReader("Global fact.")); err != nil {
		t.Fatal(err)
	}

	// 3. Project scope: Resume loads semantic >= 0.40 (project + global), hides procedural/episodic and < 0.40.
	dir := repo(t)
	o := opts(dir, home)
	if _, err := MemoryAdd(o, MemoryEntry{Type: "semantic", Name: "proj-sem", Description: "Project semantic"}, strings.NewReader("Proj fact.")); err != nil {
		t.Fatal(err)
	}
	if _, err := MemoryAdd(o, MemoryEntry{Type: "episodic", Name: "proj-ep", Description: "Recent episode"}, strings.NewReader("Ep body.")); err != nil {
		t.Fatal(err)
	}
	// Aged derived episodic card (validity 0.55 * 0.40 = 0.22 < 0.40).
	oldOpts := Options{Home: home, Dir: dir, Now: func() time.Time { return fixed.Add(-200 * 24 * time.Hour) }}
	if _, err := MemoryAdd(oldOpts, MemoryEntry{
		Type:        "episodic",
		Name:        "old-derived",
		Description: "Stale derived card",
		Provenance:  "derived",
	}, strings.NewReader("Old body.")); err != nil {
		t.Fatal(err)
	}

	showActive, err := MemoryShow(o)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(showActive, "old-derived") {
		t.Errorf("MemoryShow hid check failed; showed < 0.40 card:\n%s", showActive)
	}
	if !strings.Contains(showActive, "proj-sem (semantic, 1.00)") || !strings.Contains(showActive, "proj-ep (episodic, 1.00)") {
		t.Errorf("MemoryShow missing active cards:\n%s", showActive)
	}

	showAll, err := MemoryShowWithOptions(o, MemoryShowOptions{IncludeLow: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showAll, "old-derived (episodic, 0.22)") {
		t.Errorf("MemoryShowWithOptions(IncludeLow: true) missing low card:\n%s", showAll)
	}

	rawJSON, err := MemoryShowJSONWithOptions(g, MemoryShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var items []MemorySummary
	if err := json.Unmarshal([]byte(rawJSON), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].Global || items[0].Validity <= 0 {
		t.Errorf("MemoryShowJSONWithOptions(global) = %+v", items)
	}

	res, err := Resume(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "proj-sem (semantic, 1.00)") || !strings.Contains(res, "shared-sem (semantic, 1.00) [global]") {
		t.Errorf("Resume missing project or global semantic card:\n%s", res)
	}
	if strings.Contains(res, "proj-ep") || strings.Contains(res, "global-rule") || strings.Contains(res, "old-derived") {
		t.Errorf("Resume leaked non-semantic or low-validity card:\n%s", res)
	}

	// 4. MemoryDraftWithOptions: global refuses retire/merge; source_sessions capped at 20 sorted deduplicated.
	if _, err := MemoryDraftWithOptions(g, "retire", "MEMORY-global-rule", "nope", MemoryDraftOptions{}); !IsRefusal(err) {
		t.Errorf("global draft retire: %v, want refusal", err)
	}
	if _, err := MemoryDraftWithOptions(g, "merge", "MEMORY-global-rule", "nope", MemoryDraftOptions{}); !IsRefusal(err) {
		t.Errorf("global draft merge: %v, want refusal", err)
	}
	var many []string
	for i := 25; i >= 1; i-- {
		many = append(many, fmt.Sprintf("sess-%02d", i))
	}
	many = append(many, "sess-01", "sess-05")
	idDraft, err := MemoryDraftWithOptions(o, "add", "draft-many", "Draft summary\n\nBody.", MemoryDraftOptions{
		Type:           "procedural",
		EvidenceCount:  3,
		SourceSessions: many,
	})
	if err != nil {
		t.Fatal(err)
	}
	recs, _, err := Get(o, []string{idDraft})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Get draft: %v", err)
	}
	gotSessions := strings.Split(recs[0].SourceSessions, ",")
	if len(gotSessions) != 20 || gotSessions[0] != "sess-01" || gotSessions[19] != "sess-20" {
		t.Errorf("SourceSessions len=%d first=%q last=%q, want 20 sorted sess-01..sess-20", len(gotSessions), gotSessions[0], gotSessions[len(gotSessions)-1])
	}
	if recs[0].MemoryKind != "procedural" || recs[0].Validity != 0.70 {
		t.Errorf("draft MemoryKind=%q Validity=%.2f, want procedural 0.70", recs[0].MemoryKind, recs[0].Validity)
	}
}
