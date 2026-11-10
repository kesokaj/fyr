package search

import (
	"strings"
	"testing"
)

func TestBuildTurnEnvelopeCompactStatusAndBanner(t *testing.T) {
	brainCtx := TurnBrainContext{
		Memories: []MatchedRecord{
			{ID: "MEMORY-airy-ux", Kind: "memory", Title: "Short airy replies", Provenance: "operator", Body: "Lead with a short summary."},
		},
		ADRs: []MatchedRecord{
			{ID: "ADR-515", Kind: "adr", Status: "accepted", Title: "Public repository trust model", Body: "Verify SHA256SUMS over HTTPS."},
		},
	}
	res := VetResult{
		Intent:           IntentBug,
		Gate:             GateEnrichAndProceed,
		Score:            85,
		SecurityRequired: true,
		Skills: []SkillMeta{
			{Name: "fyr-bug", Path: "skills/fyr-bug/SKILL.md", Digest: "Bug workflow"},
			{Name: "golang", Path: "skills/golang/SKILL.md", Digest: "Go standards"},
		},
		MatchedRecords: collectMatchedRecords(brainCtx),
	}
	slice := SliceResult{
		Confident:  true,
		Pivots:     2,
		SymbolHits: 3,
		Files:      []string{"internal/search/fence.go"},
		Capsule:    "[Hop 0] internal/search/fence.go:161-197 (buildTurnContract)",
	}

	sysMsg, banner := BuildTurnEnvelope(&res, slice, brainCtx, nil, []string{"🧠 2 pivots"})

	wantStatus := "status_line: 🧭 **fyr** · **[N/6 <Resume|Vet|Plan|Build|Verify & Judge|Handoff>]** · Vet `85/100` · Skills: `fyr-bug, golang`"
	if !strings.Contains(res.TurnContract, wantStatus) {
		t.Errorf("TurnContract missing compact status_line %q:\n%s", wantStatus, res.TurnContract)
	}
	for _, legacy := range []string{"Stage: **[N/6", "Active: `<IDs>`", "Pre-seeded:", "Models: Builder"} {
		if strings.Contains(res.TurnContract, legacy) {
			t.Errorf("TurnContract still contains verbose legacy status field %q:\n%s", legacy, res.TurnContract)
		}
	}

	wantBanner := "🧭 **fyr vet:** `BUG` → `ENRICH_AND_PROCEED` (`85/100`) · 📚 skills: `fyr-bug`, `golang` · 🔒 subagent: `security` required · 🗂️ 2 record(s) · 🧠 2 pivots"
	if banner != wantBanner {
		t.Errorf("banner = %q, want %q", banner, wantBanner)
	}
	for _, noise := range []string{"skills/fyr-bug/SKILL.md", "MEMORY-airy-ux", "ADR-515", "Turn-0 capsule"} {
		if strings.Contains(banner, noise) {
			t.Errorf("banner contains verbose path/ID noise %q: %s", noise, banner)
		}
	}

	// Full skill paths and record IDs remain inside the fenced systemMessage blocks.
	for _, wantInSys := range []string{"skills/fyr-bug/SKILL.md", "BEGIN DATA MEMORY-airy-ux", "BEGIN DATA ADR-515", wantStatus} {
		if !strings.Contains(sysMsg, wantInSys) {
			t.Errorf("systemMessage missing %q:\n%s", wantInSys, sysMsg)
		}
	}
}

func TestBuildTurnEnvelopeNoSkillsAndVetFirstTips(t *testing.T) {
	res := VetResult{
		Intent:         IntentFeature,
		Gate:           GateVetFirst,
		Score:          20,
		MissingAnchors: []string{"target", "scope", "verification"},
	}
	_, banner := BuildTurnEnvelope(&res, SliceResult{}, TurnBrainContext{}, nil, nil)

	wantStatus := "status_line: 🧭 **fyr** · **[N/6 <Resume|Vet|Plan|Build|Verify & Judge|Handoff>]** · Vet `20/100` · Skills: `none`"
	if !strings.Contains(res.TurnContract, wantStatus) {
		t.Errorf("TurnContract missing none-skills status_line %q:\n%s", wantStatus, res.TurnContract)
	}
	wantBanner := "🧭 **fyr vet:** `FEATURE` → `VET_FIRST` (`20/100`) · 💡 tip: missing anchors (target, scope, verification) — prefix `!` to bypass vetting"
	if banner != wantBanner {
		t.Errorf("banner = %q, want %q", banner, wantBanner)
	}
}
