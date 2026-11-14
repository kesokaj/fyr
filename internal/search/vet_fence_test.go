package search

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func parseFencedJSONBanner(t *testing.T, banner string) PreInvocationBanner {
	t.Helper()
	trimmed := strings.TrimSpace(banner)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 || !strings.HasSuffix(lines[0], "json") || lines[len(lines)-1] != strings.TrimSuffix(lines[0], "json") {
		t.Fatalf("banner is not a fenced json block:\n%s", banner)
	}
	raw := strings.Join(lines[1:len(lines)-1], "\n")
	var got PreInvocationBanner
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal PreInvocationBanner: %v\nraw:\n%s", err, raw)
	}
	if want := marshalPrettyJSON(got); raw != want {
		t.Errorf("banner JSON not 2-space pretty-printed Hybrid Example A:\ngot:\n%s\nwant:\n%s", raw, want)
	}
	return got
}

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

	sysMsg, banner := BuildTurnEnvelope(&res, slice, brainCtx, nil, []string{"2 pivots"})

	if sysMsg != banner || res.TurnContract != banner {
		t.Fatalf("expected single unified envelope across sysMsg, banner, TurnContract")
	}
	if !strings.HasPrefix(sysMsg, "```json\n") || !strings.HasSuffix(sysMsg, "\n```") || strings.Count(sysMsg, "```") != 2 {
		t.Fatalf("expected strictly one json fenced block with no surrounding prose, got:\n%s", sysMsg)
	}
	for _, legacy := range []string{`"stage":`, "BEGIN DATA", "status_line:", "Stage: **[N/6", "[N/6", "Skills: `none`", "Active: `<IDs>`", "Pre-seeded:", "Models: Builder", "skills/fyr-bug/SKILL.md", "Turn-0 capsule"} {
		if strings.Contains(sysMsg, legacy) {
			t.Errorf("envelope contains legacy/verbose token %q:\n%s", legacy, sysMsg)
		}
	}

	parsed := parseFencedJSONBanner(t, banner)
	if parsed.Surface != "PRE_INVOCATION" || parsed.Contract != "[FYR_TURN_CONTRACT]" ||
		parsed.Intent != "BUG" || parsed.Gate != "ENRICH_AND_PROCEED" || parsed.Directive != "ENRICH_AND_PROCEED" ||
		parsed.Score == nil || *parsed.Score != 85 || !parsed.SecuritySubagentRequired ||
		parsed.MatchedRecordCount != 2 || parsed.PivotCount != 2 || len(parsed.Skills) != 2 {
		t.Errorf("parsed banner mismatch: %+v\nraw:\n%s", parsed, banner)
	}
	if parsed.Enrichment == nil || len(parsed.Enrichment.Records) != 2 ||
		parsed.Enrichment.Records[0].ID != "MEMORY-airy-ux" || parsed.Enrichment.Records[1].ID != "ADR-515" ||
		!strings.Contains(parsed.Enrichment.AstSlice, "[Hop 0]") || parsed.Enrichment.Trust == "" {
		t.Errorf("parsed enrichment mismatch: %+v\nraw:\n%s", parsed.Enrichment, sysMsg)
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

	if strings.Contains(res.TurnContract, "none") || strings.Contains(res.TurnContract, `"stage":`) || strings.Contains(res.TurnContract, `"enrichment"`) {
		t.Errorf("TurnContract should omit empty skills, bare stage, and empty enrichment:\n%s", res.TurnContract)
	}
	parsed := parseFencedJSONBanner(t, banner)
	if parsed.Surface != "PRE_INVOCATION" || parsed.Contract != "[FYR_TURN_CONTRACT]" ||
		parsed.Intent != "FEATURE" || parsed.Gate != "VET_FIRST" || parsed.Directive != "VET_FIRST" ||
		parsed.Score == nil || *parsed.Score != 20 || len(parsed.Skills) != 0 ||
		len(parsed.MissingAnchors) != 3 || !strings.Contains(parsed.Tip, "missing anchors (target, scope, verification)") ||
		parsed.Enrichment != nil {
		t.Errorf("parsed VET_FIRST banner mismatch: %+v\nraw:\n%s", parsed, banner)
	}

	proceed := VetResult{Intent: IntentTask, Gate: GateEnrichAndProceed, Score: 90}
	BuildTurnEnvelope(&proceed, SliceResult{}, TurnBrainContext{}, nil, nil)
	if strings.Contains(proceed.TurnContract, `"stage":`) || strings.Contains(proceed.TurnContract, `"enrichment"`) {
		t.Errorf("TurnContract without delta contains bare stage or empty enrichment:\n%s", proceed.TurnContract)
	}

	// Unanchored non-code prompt with 1 memory omits verify and targets while keeping the record.
	memCtx := TurnBrainContext{
		Memories: []MatchedRecord{
			{ID: "MEMORY-vet-history", Kind: "memory", Title: "Add vet_history table", Provenance: "operator", Body: "# Add vet_history table\nAdd scoring."},
		},
	}
	uRes := VetPrompt("Reinvention of sandboxing workflow without citing files", IntentTask, SliceResult{}, memCtx)
	uParsed := parseFencedJSONBanner(t, uRes.SystemMessage)
	if uParsed.Enrichment == nil || uParsed.Enrichment.Verify != "" || len(uParsed.Enrichment.Targets) != 0 ||
		len(uParsed.Enrichment.Records) != 1 || strings.Contains(uParsed.Enrichment.Records[0].Note, "#") {
		t.Errorf("unanchored non-code prompt enrichment mismatch: %+v\nraw:\n%s", uParsed.Enrichment, uRes.SystemMessage)
	}
}

func TestZeroEmojisAndPrettyJSONSurfaces(t *testing.T) {
	emojiRe := regexp.MustCompile("[\U0001F300-\U0001FAFF\u2600-\u27BF]")

	// 1. PreInvocation degraded banner: nil score, STORE_DEGRADED gate, valid 2-space JSON, zero emojis.
	degraded := formatStoreDegradedBanner(errors.New("sqlite busy"))
	if emojiRe.MatchString(degraded) {
		t.Errorf("degraded banner contains emoji:\n%s", degraded)
	}
	parsedDeg := parseFencedJSONBanner(t, degraded)
	if parsedDeg.Gate != "STORE_DEGRADED" || parsedDeg.Score != nil || !strings.Contains(parsedDeg.StoreError, "sqlite busy") {
		t.Errorf("unexpected degraded banner JSON: %+v", parsedDeg)
	}

	// 2. Read-ledger [UNCHANGED] and [MODIFIED] Hybrid Example A payloads.
	unchanged := formatUnchangedLedgerNotice("internal/search/hook.go", 10, 40, "abcdef1234567890", 2, "10-40 func Foo()")
	if !strings.HasPrefix(unchanged, "[UNCHANGED]\n") || emojiRe.MatchString(unchanged) {
		t.Errorf("bad unchanged ledger notice:\n%s", unchanged)
	}
	var lUnch LedgerNotice
	rawUnch := strings.TrimPrefix(unchanged, "[UNCHANGED]\n")
	if err := json.Unmarshal([]byte(rawUnch), &lUnch); err != nil || lUnch.Status != "UNCHANGED" || !lUnch.StampConsumed {
		t.Errorf("invalid UNCHANGED LedgerNotice JSON (%v): %+v", err, lUnch)
	}
	if rawUnch != marshalPrettyJSON(lUnch) {
		t.Errorf("UNCHANGED JSON not 2-space pretty:\n%s", rawUnch)
	}

	modified := formatModifiedLedgerNotice("internal/search/hook.go", "111111111111", "222222222222", "1-50 func Bar()")
	if !strings.HasPrefix(modified, "[MODIFIED]\n") || emojiRe.MatchString(modified) {
		t.Errorf("bad modified ledger notice:\n%s", modified)
	}
	var lMod LedgerNotice
	rawMod := strings.TrimPrefix(modified, "[MODIFIED]\n")
	if err := json.Unmarshal([]byte(rawMod), &lMod); err != nil || lMod.Status != "MODIFIED" || lMod.PreviousSha256 != "111111111111" {
		t.Errorf("invalid MODIFIED LedgerNotice JSON (%v): %+v", err, lMod)
	}

	// 3. ProcessTelemetry artifact is emoji-free and round-trips cleanly.
	procMD := RenderProcessArtifactForHome("", "conv-json", "Build", []VetHistoryRow{{
		TurnNum: 1, Intent: IntentBug, Gate: GateEnrichAndProceed, Score: 88, SuggestedSkills: nil, PivotFiles: []string{"a.go"},
	}}, nil)
	if emojiRe.MatchString(procMD) {
		t.Errorf("fyr-process.md contains emoji:\n%s", procMD)
	}
	rawProc, docProc := extractProcessTelemetryJSON(t, procMD)
	if docProc.Stage != "BUILD" || rawProc != marshalPrettyJSON(docProc) {
		t.Errorf("process telemetry not 2-space pretty Hybrid Example A:\n%s", rawProc)
	}

	// 4. context/*.md, rules/*.md, agents/*/*.md contain zero emojis and declare Hybrid Example A JSON blocks.
	root := filepath.Join("..", "..")
	for _, glob := range []string{"context/*.md", "rules/*.md", "agents/*/*.md"} {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range matches {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if emojiRe.Match(data) {
				t.Errorf("%s contains emoji", p)
			}
		}
	}
	ctxData, err := os.ReadFile(filepath.Join(root, "context", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctxData), "2-space `json` fenced block") || !strings.Contains(string(ctxData), "Never use emojis") {
		t.Errorf("context/AGENTS.md missing Hybrid Example A / zero-emoji contract")
	}
	dispData, err := os.ReadFile(filepath.Join(root, "rules", "agy-dispatch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dispData), "2-space `json` block") {
		t.Errorf("rules/agy-dispatch.md missing per-dispatch JSON block contract")
	}
}
