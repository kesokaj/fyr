package search

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
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
	for _, legacy := range []string{`"stage":`, "BEGIN DATA", "status_line:", "Stage: **[N/6", "[N/6", "Skills: `none`", "Active: `<IDs>`", "Pre-seeded:", "Models: Builder", "Turn-0 capsule"} {
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
	if len(parsed.Enrichment.SkillDigests) != 2 ||
		parsed.Enrichment.SkillDigests[0] != (PreInvocationSkillDigest{Name: "fyr-bug", Path: "skills/fyr-bug/SKILL.md", Digest: "Bug workflow"}) ||
		parsed.Enrichment.SkillDigests[1] != (PreInvocationSkillDigest{Name: "golang", Path: "skills/golang/SKILL.md", Digest: "Go standards"}) {
		t.Errorf("parsed skillDigests mismatch: %+v\nraw:\n%s", parsed.Enrichment.SkillDigests, sysMsg)
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

	conflictVet := VetResult{
		SessionModel: "claude-opus-5-5-high",
		Intent:       IntentFeature,
		Gate:         GateVetFirst,
		Score:        10,
		Conflicts:    []string{"ADR-001"},
	}
	_, conflictBanner := BuildTurnEnvelope(&conflictVet, SliceResult{}, TurnBrainContext{}, nil, nil)
	parsedConflict := parseFencedJSONBanner(t, conflictBanner)
	if parsedConflict.Model != "claude-opus-5-5-high" || parsedConflict.Thinking != "HIGH" || !strings.Contains(parsedConflict.Tip, "clarify via ask_question") {
		t.Errorf("conflict VET_FIRST banner mismatch: %+v\nraw:\n%s", parsedConflict, conflictBanner)
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
	if err := json.Unmarshal([]byte(rawUnch), &lUnch); err != nil || lUnch.Status != "UNCHANGED" || lUnch.StampConsumed {
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
	if !strings.Contains(string(dispData), "2-space `json` block") ||
		!strings.Contains(string(dispData), `"model": "<model-id>", "thinking": "HIGH"`) ||
		strings.Contains(string(dispData), `"tier":`) ||
		strings.Contains(string(dispData), `"model": "agy:pro"`) {
		t.Errorf("rules/agy-dispatch.md missing exact model+thinking dispatch contract or still mentions tier/agy:pro")
	}
}

func TestResolveModelAndThinkingAndHookFallback(t *testing.T) {
	home := t.TempDir()
	catDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catJSON := `{"entries":[
		{"id":"claude-opus-5-5-high","display_name":"Claude Opus 5.5 (High)","effort":"high"},
		{"id":"gemini-4-carbon-medium","display_name":"Gemini 4 Carbon (Medium)","effort":"medium"},
		{"id":"gemini-pro-orchestrator","display_name":"Orchestrator (Carbon)","effort":"orchestrator"},
		{"id":"custom-next-xhigh","display_name":"Custom Next (XHigh)","effort":"xhigh"},
		{"id":"gemini-4-hydrogen-xhigh","display_name":"","effort":""}
	]}`
	if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(catJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		homeArg, raw, wantModel, wantDisplay, wantThink string
	}{
		{home, "claude-opus-5-5-high", "claude-opus-5-5-high", "Claude Opus 5.5 (High)", "HIGH"},
		{home, "models/claude-opus-5-5-high", "claude-opus-5-5-high", "Claude Opus 5.5 (High)", "HIGH"},
		{home, "Gemini 4 Carbon (Medium)", "gemini-4-carbon-medium", "Gemini 4 Carbon (Medium)", "MEDIUM"},
		{home, "gemini-4-carbon-medium", "gemini-4-carbon-medium", "Gemini 4 Carbon (Medium)", "MEDIUM"},
		{home, "gemini-pro-orchestrator", "gemini-pro-orchestrator", "Orchestrator (Carbon)", "DYNAMIC"},
		{home, "custom-next-xhigh", "custom-next-xhigh", "Custom Next (XHigh)", "XHIGH"},
		{home, "gemini-4-hydrogen-xhigh", "gemini-4-hydrogen-xhigh", "", "XHIGH"},
		{"", "claude-opus-5-5-high", "claude-opus-5-5-high", "", "HIGH"},
		{"", "gemini-4-carbon-medium", "gemini-4-carbon-medium", "", "MEDIUM"},
		{"", "gemini-4-hydrogen-xhigh", "gemini-4-hydrogen-xhigh", "", "XHIGH"},
		{"", "gemini-3.8-flash-tiered", "gemini-3.8-flash-tiered", "", "TIERED"},
		{"", "gemini-flash-worker-agy", "gemini-flash-worker-agy", "", "FAST"},
		{"", "gemini-pro-orchestrator", "gemini-pro-orchestrator", "", "DYNAMIC"},
		{"", "gemini-pro-orchestrator-neon", "gemini-pro-orchestrator-neon", "", "DYNAMIC"},
		{"", "gemini-4-neon-high", "gemini-4-neon-high", "", "HIGH"},
		{"", "gemini-5-xenon-medium", "gemini-5-xenon-medium", "", "MEDIUM"},
		{"", "gpt-oss-120b-medium", "gpt-oss-120b-medium", "", "MEDIUM"},
		{"", "auto", "", "", ""},
		{"", "", "", "", ""},
	}
	for _, tc := range cases {
		gotModel, gotDisplay, gotThink := ResolveModelDetails(tc.homeArg, tc.raw)
		if gotModel != tc.wantModel || gotDisplay != tc.wantDisplay || gotThink != tc.wantThink {
			t.Errorf("ResolveModelDetails(%q, %q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.homeArg, tc.raw, gotModel, gotDisplay, gotThink, tc.wantModel, tc.wantDisplay, tc.wantThink)
		}
	}
	if id, disp, th := ResolveModelDetails(home, "  claude-opus-5-5-high  "); id != "claude-opus-5-5-high" || disp != "Claude Opus 5.5 (High)" || th != "HIGH" {
		t.Errorf("ResolveModelDetails leading/trailing spaces = (%q, %q, %q), want (claude-opus-5-5-high, Claude Opus 5.5 (High), HIGH)", id, disp, th)
	}
	if got := formatModelTag("", "gemini-4-hydrogen-medium"); got != "gemini-4-hydrogen-medium, thinking=MEDIUM" {
		t.Errorf("formatModelTag = %q, want gemini-4-hydrogen-medium, thinking=MEDIUM", got)
	}
	if got := formatModelTag(home, "claude-opus-5-5-high"); got != "claude-opus-5-5-high [Claude Opus 5.5 (High)], thinking=HIGH" {
		t.Errorf("formatModelTag catalog = %q, want claude-opus-5-5-high [Claude Opus 5.5 (High)], thinking=HIGH", got)
	}

	// Verify gen_metadata field (1, 19) fallback when hook stdin carries modelName="auto".
	appDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convID := "conv-model-fallback"
	brainDir := filepath.Join(appDir, "brain", convID)
	convDir := filepath.Join(appDir, "conversations")
	if err := os.MkdirAll(brainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(convDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(convDir, convID+".db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE gen_metadata (idx INTEGER PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	modelVal := []byte("models/claude-opus-5-5-high")
	inner := binary.AppendUvarint(nil, uint64((19<<3)|2))
	inner = binary.AppendUvarint(inner, uint64(len(modelVal)))
	inner = append(inner, modelVal...)
	outer := binary.AppendUvarint(nil, uint64((1<<3)|2))
	outer = binary.AppendUvarint(outer, uint64(len(inner)))
	outer = append(outer, inner...)
	if _, err := db.Exec(`INSERT INTO gen_metadata(idx, data) VALUES (0, ?)`, outer); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	hookIn := HookInput{
		ConversationID:        convID,
		ArtifactDirectoryPath: brainDir,
		ModelName:             "auto",
	}
	assertCleanDirs := func(phase string) {
		t.Helper()
		for _, dir := range []string{brainDir, filepath.Dir(brainDir)} {
			ents, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("%s ReadDir(%s): %v", phase, dir, err)
			}
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), ".fyr-conv-") {
					t.Errorf("%s leaked temp dir %s in %s", phase, e.Name(), dir)
				}
			}
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
				t.Errorf("%s created sidecar %s in convDir (err=%v)", phase, dbPath+suffix, err)
			}
		}
	}
	if got := resolveHookSessionModelForHome("", hookIn); got != "claude-opus-5-5-high" {
		t.Fatalf("resolveHookSessionModelForHome = %q, want claude-opus-5-5-high", got)
	}
	assertCleanDirs("after resolveHookSessionModelForHome")

	encodeField := func(num uint64, val []byte) []byte {
		b := binary.AppendUvarint(nil, (num<<3)|2)
		b = binary.AppendUvarint(b, uint64(len(val)))
		return append(b, val...)
	}
	buildMetaBlob := func(conc328, fb119, disp121 string) []byte {
		var msg1 []byte
		if fb119 != "" {
			msg1 = append(msg1, encodeField(19, []byte(fb119))...)
		}
		if disp121 != "" {
			msg1 = append(msg1, encodeField(21, []byte(disp121))...)
		}
		var out []byte
		if len(msg1) > 0 {
			out = append(out, encodeField(1, msg1)...)
		}
		if conc328 != "" {
			out = append(out, encodeField(3, encodeField(28, []byte(conc328)))...)
		}
		return out
	}

	// Verify (3, 28) concrete model ID takes precedence over (1, 19), pairs with (1, 21) display,
	// and works even if brain/<convID> does not exist yet on Turn 0.
	db328, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	combinedBlob := buildMetaBlob("gemini-4-neon-high", "gemini-4-neon", "Gemini 4 Neon (High)")
	if _, err := db328.Exec(`INSERT INTO gen_metadata(idx, data) VALUES (1, ?)`, combinedBlob); err != nil {
		t.Fatal(err)
	}
	_ = db328.Close()
	if got := resolveHookSessionModelForHome("", hookIn); got != "gemini-4-neon-high" {
		t.Fatalf("resolveHookSessionModelForHome with (3,28) = %q, want gemini-4-neon-high", got)
	}
	assertCleanDirs("after (3,28) resolveHookSessionModelForHome")
	_ = os.RemoveAll(brainDir)
	absentHookIn := HookInput{ConversationID: convID, ArtifactDirectoryPath: brainDir, ModelName: "auto"}
	if got := resolveHookSessionModelForHome(home, absentHookIn); got != "gemini-4-neon-high" {
		t.Fatalf("resolveHookSessionModelForHome with absent brainSubdir = %q, want gemini-4-neon-high", got)
	}
	if err := os.MkdirAll(brainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	assertCleanDirs("after absent brainSubdir fallback")

	runPre := func(payloadMap map[string]any) PreInvocationBanner {
		t.Helper()
		payload, _ := json.Marshal(payloadMap)
		var out bytes.Buffer
		if err := RunHook(Options{Home: home, Hook: "pre-invocation"}, bytes.NewReader(payload), &out); err != nil {
			t.Fatalf("RunHook pre-invocation: %v", err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) == 0 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("unexpected PreInvocationOutput (%v): %s", err, out.String())
		}
		return parseFencedJSONBanner(t, resp.InjectSteps[0].SystemMessage.SystemMessage)
	}

	banner := runPre(map[string]any{
		"conversationId":        convID,
		"artifactDirectoryPath": brainDir,
		"modelName":             "auto",
		"lastUserInput":         "!review internal/search/telemetry_model.go with ResolveModelAndThinking",
		"workspacePaths":        []string{t.TempDir()},
	})
	if banner.Model != "gemini-4-neon-high" || banner.ModelDisplay != "Gemini 4 Neon (High)" || banner.Thinking != "HIGH" {
		t.Errorf("PreInvocation fallback resolved model=%q display=%q thinking=%q, want gemini-4-neon-high / Gemini 4 Neon (High) / HIGH",
			banner.Model, banner.ModelDisplay, banner.Thinking)
	}
	assertCleanDirs("after RunHook")

	// Mixed-model DB guard: newer orchestrator (1, 21) MUST NOT leak onto gemini-flash-worker-agy turn.
	convMixed := "conv-mixed-worker"
	brainMixed := filepath.Join(appDir, "brain", convMixed)
	if err := os.MkdirAll(brainMixed, 0o700); err != nil {
		t.Fatal(err)
	}
	mixedDBPath := filepath.Join(convDir, convMixed+".db")
	mdb, err := sql.Open("sqlite", mixedDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Exec(`CREATE TABLE gen_metadata (idx INTEGER PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Exec(`INSERT INTO gen_metadata(idx, data) VALUES (0, ?)`, buildMetaBlob("", "gemini-pro-orchestrator-neon", "Orchestrator (Neon)")); err != nil {
		t.Fatal(err)
	}
	_ = mdb.Close()
	repoDir := setupHookRepo(t)
	if err := Index(Options{Dir: repoDir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index repoDir: %v", err)
	}
	mixedBanner := runPre(map[string]any{
		"conversationId":        convMixed,
		"artifactDirectoryPath": brainMixed,
		"modelName":             "gemini-flash-worker-agy",
		"lastUserInput":         "!inspect ReconcileProjectIndex in service.go",
		"workspacePaths":        []string{repoDir},
	})
	if mixedBanner.Model != "gemini-flash-worker-agy" || mixedBanner.ModelDisplay != "" || mixedBanner.Thinking != "FAST" {
		t.Errorf("mixed conv leaked wrong display: got model=%q display=%q thinking=%q, want gemini-flash-worker-agy / \"\" / FAST",
			mixedBanner.Model, mixedBanner.ModelDisplay, mixedBanner.Thinking)
	}

	// Once a matching worker row exists in gen_metadata, (1, 21) resolves AND persists to search.db meta cache
	// so even when the conv DB later exceeds maxHookConvDBBytes (or disappears), modelDisplay remains populated.
	mdb, err = sql.Open("sqlite", mixedDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Exec(`INSERT INTO gen_metadata(idx, data) VALUES (1, ?)`, buildMetaBlob("", "gemini-flash-worker-agy", "Gemini 4 Hydrogen (Orchestrator)")); err != nil {
		t.Fatal(err)
	}
	_ = mdb.Close()
	workerBanner := runPre(map[string]any{
		"conversationId":        convMixed,
		"artifactDirectoryPath": brainMixed,
		"modelName":             "gemini-flash-worker-agy",
		"lastUserInput":         "!inspect AlphaHelper in service.go",
		"workspacePaths":        []string{repoDir},
	})
	if workerBanner.Model != "gemini-flash-worker-agy" || workerBanner.ModelDisplay != "Gemini 4 Hydrogen (Orchestrator)" || workerBanner.Thinking != "FAST" {
		t.Errorf("worker conv resolved model=%q display=%q thinking=%q, want gemini-flash-worker-agy / Gemini 4 Hydrogen (Orchestrator) / FAST",
			workerBanner.Model, workerBanner.ModelDisplay, workerBanner.Thinking)
	}
	_ = os.Remove(mixedDBPath)
	cachedBanner := runPre(map[string]any{
		"conversationId":        convMixed,
		"artifactDirectoryPath": brainMixed,
		"modelName":             "gemini-flash-worker-agy",
		"lastUserInput":         "!inspect BetaValidator in service.go",
		"workspacePaths":        []string{repoDir},
	})
	if cachedBanner.Model != "gemini-flash-worker-agy" || cachedBanner.ModelDisplay != "Gemini 4 Hydrogen (Orchestrator)" || cachedBanner.Thinking != "FAST" {
		t.Errorf("cached worker display after conv DB removal = (%q, %q, %q), want (gemini-flash-worker-agy, Gemini 4 Hydrogen (Orchestrator), FAST)",
			cachedBanner.Model, cachedBanner.ModelDisplay, cachedBanner.Thinking)
	}
}

func TestBuildTurnEnvelopeWithMetaAndUTF8SkillTruncate(t *testing.T) {
	res := VetResult{
		Intent:              IntentBug,
		Gate:                GateEnrichAndProceed,
		Score:               90,
		SessionModel:        "gemini-4-carbon-high",
		SessionModelDisplay: "Gemini 4 Carbon (High)",
		SessionThinking:     "HIGH",
		Skills: []SkillMeta{{
			Name:   "fyr-bug",
			Path:   "skills/fyr-bug/SKILL.md",
			Digest: strings.Repeat("é", 400),
		}},
	}
	matched, _ := MatchSkills("fix bug", IntentBug, nil, res.Skills)
	if len(matched) == 0 || strings.ToValidUTF8(matched[0].Digest, "") != matched[0].Digest {
		t.Fatalf("MatchSkills produced invalid UTF-8 digest: %+v", matched)
	}
	meta := TurnBannerMeta{
		CompactedGeneration:  2,
		DirtyFastPath:        true,
		PivotCount:           4,
		ContextKneeWarning:   "305k tokens (>= 300K 60% model knee)",
		ModelTip:             "consider Carbon",
		SessionModelResolved: true,
	}
	msg, _ := BuildTurnEnvelopeWithMeta(&res, SliceResult{}, TurnBrainContext{}, meta)
	b := parseFencedJSONBanner(t, msg)
	if b.Model != "gemini-4-carbon-high" || b.ModelDisplay != "Gemini 4 Carbon (High)" || b.Thinking != "HIGH" ||
		b.CompactedGeneration != 2 || !b.DirtyFastPath || b.PivotCount != 4 || b.ModelTip != "consider Carbon" ||
		b.TokenKneeWarning != meta.ContextKneeWarning || len(b.Notices) != 0 {
		t.Fatalf("unexpected typed TurnBannerMeta output: %+v", b)
	}
	for _, want := range []string{
		"305k tokens (>= 300K 60% model knee)",
		"80k tokens (>= 77K 60% model knee)",
		"185k tokens (>= 180K knee)",
	} {
		var parsed PreInvocationBanner
		applyExtraBanner(&parsed, want)
		if parsed.TokenKneeWarning != want || len(parsed.Notices) != 0 {
			t.Fatalf("applyExtraBanner(%q) = %+v", want, parsed)
		}
	}
}
