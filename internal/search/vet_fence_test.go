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
	"strconv"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func parseStructuredBanner(t *testing.T, banner string) PreInvocationBanner {
	t.Helper()
	trimmed := strings.TrimSpace(banner)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 || !strings.HasSuffix(lines[0], "text") {
		t.Fatalf("banner is not a fenced text block:\n%s", banner)
	}
	fence := strings.TrimSuffix(lines[0], "text")
	if len(fence) < 3 || strings.Trim(fence, "`") != "" || lines[len(lines)-1] != fence {
		t.Fatalf("banner fence mismatch (%q vs %q):\n%s", lines[0], lines[len(lines)-1], banner)
	}
	bodyLines := lines[1 : len(lines)-1]
	var out PreInvocationBanner
	parseBannerHeaderLine(t, bodyLines[0], &out)

	inDetails := false
	for i := 1; i < len(bodyLines); {
		line := bodyLines[i]
		if !inDetails && line == "---" {
			inDetails = true
			if out.Enrichment == nil {
				out.Enrichment = &PreInvocationEnrichment{}
			}
			i++
			continue
		}
		if !inDetails {
			parseBannerMetaLine(&out, line)
			i++
			continue
		}
		i = parseBannerDetailSection(out.Enrichment, bodyLines, i)
	}
	if out.MatchedRecordCount == 0 && out.Enrichment != nil && len(out.Enrichment.Records) > 0 {
		out.MatchedRecordCount = len(out.Enrichment.Records)
	}
	return out
}

func parseBannerHeaderLine(t *testing.T, hdr string, out *PreInvocationBanner) {
	t.Helper()
	left, right, hasGate := strings.Cut(hdr, " · ")
	fields := strings.Fields(left)
	if len(fields) >= 2 {
		out.Contract = fields[0]
		out.Surface = fields[1]
	} else if len(fields) == 1 {
		out.Surface = fields[0]
	}
	if !hasGate {
		return
	}
	if strings.HasSuffix(right, "/100)") {
		if idx := strings.LastIndex(right, " ("); idx > 0 {
			scoreStr := strings.TrimSuffix(right[idx+2:], "/100)")
			if n, err := strconv.Atoi(scoreStr); err == nil {
				out.Score = &n
				out.Gate = strings.TrimSpace(right[:idx])
				return
			}
		}
	}
	out.Gate = strings.TrimSpace(right)
}

func parseBannerMetaLine(out *PreInvocationBanner, line string) {
	key, rawVal, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	val := strings.TrimSpace(rawVal)
	switch key {
	case "Model":
		head, think, hasThink := strings.Cut(val, " · ")
		if hasThink {
			out.Thinking = think
		}
		if strings.HasSuffix(head, "]") {
			if idx := strings.LastIndex(head, " ["); idx >= 0 {
				out.Model = head[:idx]
				out.ModelDisplay = strings.TrimSuffix(head[idx+2:], "]")
				return
			}
		}
		out.Model = head
	case "Intent", "Status":
		for idx, tok := range strings.Split(val, " · ") {
			switch {
			case strings.HasPrefix(tok, "Directive: "):
				out.Directive = strings.TrimPrefix(tok, "Directive: ")
			case strings.HasPrefix(tok, "Pivots: "):
				out.PivotCount, _ = strconv.Atoi(strings.TrimPrefix(tok, "Pivots: "))
			case strings.HasPrefix(tok, "Compacted: gen "):
				out.CompactedGeneration, _ = strconv.Atoi(strings.TrimPrefix(tok, "Compacted: gen "))
			case tok == "FastPath: dirty":
				out.DirtyFastPath = true
			case tok == "Security: required":
				out.SecuritySubagentRequired = true
			case key == "Intent" && idx == 0:
				out.Intent = tok
			}
		}
	case "Route":
		out.Route = val
	case "Skills":
		out.Skills = splitCSVStrings(val)
	case "Targets":
		if out.Enrichment == nil {
			out.Enrichment = &PreInvocationEnrichment{}
		}
		out.Enrichment.Targets = splitCSVStrings(val)
	case "Verify":
		if out.Enrichment == nil {
			out.Enrichment = &PreInvocationEnrichment{}
		}
		out.Enrichment.Verify = val
	case "Records":
		if strings.HasSuffix(val, " matched") {
			out.MatchedRecordCount, _ = strconv.Atoi(strings.TrimSuffix(val, " matched"))
		} else {
			out.MatchedRecordCount = len(splitCSVStrings(val))
		}
	case "Conflicts":
		out.Conflicts = splitCSVStrings(val)
	case "Missing":
		out.MissingAnchors = splitCSVStrings(val)
	case "TokenKnee":
		out.TokenKneeWarning = val
	case "Tip":
		out.Tip = val
	case "ModelTip":
		out.ModelTip = val
	case "StoreError":
		out.StoreError = val
	case "Notice":
		out.Notices = append(out.Notices, val)
	}
}

func parseBannerDetailSection(e *PreInvocationEnrichment, lines []string, i int) int {
	key, rawVal, ok := strings.Cut(lines[i], ":")
	if !ok {
		return i + 1
	}
	val := strings.TrimSpace(rawVal)
	i++
	switch key {
	case "Trust":
		e.Trust = val
		return i
	case "Prior":
		e.PriorTurnContext = val
		return i
	case "ImprovedPrompt", "AstSlice":
		var blk []string
		for i < len(lines) && strings.HasPrefix(lines[i], "  ") {
			blk = append(blk, strings.TrimPrefix(lines[i], "  "))
			i++
		}
		joined := strings.Join(blk, "\n")
		if key == "ImprovedPrompt" {
			e.ImprovedPrompt = joined
		} else {
			e.AstSlice = joined
		}
		return i
	case "SkillDigests", "RecordNotes", "SessionContext", "Subtasks":
		for i < len(lines) && strings.HasPrefix(lines[i], "  ") {
			item := strings.TrimPrefix(lines[i], "  - ")
			i++
			for i < len(lines) && strings.HasPrefix(lines[i], "    ") {
				item += "\n" + strings.TrimPrefix(lines[i], "    ")
				i++
			}
			switch key {
			case "SessionContext":
				e.SessionContext = append(e.SessionContext, item)
			case "SkillDigests":
				head, dig, _ := strings.Cut(item, ": ")
				name, path := head, ""
				if strings.HasSuffix(head, ")") {
					if idx := strings.LastIndex(head, " ("); idx >= 0 {
						name = head[:idx]
						path = strings.TrimSuffix(head[idx+2:], ")")
					}
				}
				e.SkillDigests = append(e.SkillDigests, PreInvocationSkillDigest{Name: name, Path: path, Digest: dig})
			case "RecordNotes":
				rec := PreInvocationRecord{}
				rest := item
				if strings.HasPrefix(rest, "[") {
					if idx := strings.Index(rest, "] "); idx > 0 {
						tag := rest[1:idx]
						rec.Kind, rec.Provenance, _ = strings.Cut(tag, "/")
						rest = rest[idx+2:]
					}
				}
				if strings.HasSuffix(rest, "]") {
					if idx := strings.LastIndex(rest, " [stale: "); idx >= 0 {
						rec.Stale = strings.TrimSuffix(rest[idx+len(" [stale: "):], "]")
						rest = rest[:idx]
					}
				}
				idPart, titleNote, _ := strings.Cut(rest, ": ")
				rec.ID = idPart
				rec.Title, rec.Note, _ = strings.Cut(titleNote, " — ")
				e.Records = append(e.Records, rec)
			case "Subtasks":
				var st SubtaskSlice
				for pIdx, seg := range strings.Split(item, " | ") {
					switch {
					case pIdx == 0:
						idPart, restPart, hasBracket := strings.Cut(seg, " [")
						st.ID = idPart
						if hasBracket {
							st.Role, st.Goal, _ = strings.Cut(restPart, "] ")
						} else if idSpace, g, ok := strings.Cut(seg, " "); ok {
							st.ID = idSpace
							st.Goal = g
						}
					case strings.HasPrefix(seg, "files: "):
						st.Files = splitCSVStrings(strings.TrimPrefix(seg, "files: "))
					case strings.HasPrefix(seg, "skills: "):
						st.Skills = splitCSVStrings(strings.TrimPrefix(seg, "skills: "))
					case strings.HasPrefix(seg, "verify: "):
						st.Verify = strings.TrimPrefix(seg, "verify: ")
					}
				}
				e.Subtasks = append(e.Subtasks, st)
			}
		}
		return i
	}
	return i
}

func splitCSVStrings(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseStructuredLedgerNotice(t *testing.T, wantStatus, raw string) LedgerNotice {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) < 2 || lines[0] != "["+wantStatus+"]" {
		t.Fatalf("ledger notice missing [%s] header:\n%s", wantStatus, raw)
	}
	n := LedgerNotice{Status: wantStatus}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "Skeleton:" {
			for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], "  "); j++ {
				n.Skeleton = append(n.Skeleton, strings.TrimPrefix(lines[j], "  "))
			}
			break
		}
		key, rawVal, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val := strings.TrimSpace(rawVal)
		switch key {
		case "Path":
			n.Path = val
		case "Range":
			a, b, _ := strings.Cut(val, "-")
			n.StartLine, _ = strconv.Atoi(a)
			n.EndLine, _ = strconv.Atoi(b)
		case "Lines":
			n.TotalLines, _ = strconv.Atoi(val)
		case "Sha256":
			n.Sha256 = val
		case "PreviousSha":
			n.PreviousSha256 = val
		case "CurrentSha":
			n.CurrentSha256 = val
		case "Hits":
			n.HitCount, _ = strconv.Atoi(val)
		case "Stamp":
			n.StampConsumed = val == "consumed"
		case "Note":
			n.Message = val
		}
	}
	return n
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
	if !strings.HasPrefix(sysMsg, "```text\n") || !strings.HasSuffix(sysMsg, "\n```") || strings.Count(sysMsg, "```") != 2 {
		t.Fatalf("expected strictly one text fenced block with no surrounding prose, got:\n%s", sysMsg)
	}
	for _, legacy := range []string{`"stage":`, `"surface":`, "BEGIN DATA", "status_line:", "Stage: **[N/6", "[N/6", "Skills: `none`", "Active: `<IDs>`", "Pre-seeded:", "Models: Builder", "Turn-0 capsule"} {
		if strings.Contains(sysMsg, legacy) {
			t.Errorf("envelope contains legacy/verbose token %q:\n%s", legacy, sysMsg)
		}
	}

	parsed := parseStructuredBanner(t, banner)
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
		t.Errorf("parsed SkillDigests mismatch: %+v\nraw:\n%s", parsed.Enrichment.SkillDigests, sysMsg)
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

	if strings.Contains(res.TurnContract, "none") || strings.Contains(res.TurnContract, `"stage":`) || strings.Contains(res.TurnContract, `"enrichment"`) || strings.Contains(res.TurnContract, "\n---\n") {
		t.Errorf("TurnContract should omit empty skills, bare stage, and empty enrichment:\n%s", res.TurnContract)
	}
	parsed := parseStructuredBanner(t, banner)
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
	parsedConflict := parseStructuredBanner(t, conflictBanner)
	if parsedConflict.Model != "claude-opus-5-5-high" || parsedConflict.Thinking != "HIGH" || !strings.Contains(parsedConflict.Tip, "clarify via ask_question") {
		t.Errorf("conflict VET_FIRST banner mismatch: %+v\nraw:\n%s", parsedConflict, conflictBanner)
	}

	proceed := VetResult{Intent: IntentTask, Gate: GateEnrichAndProceed, Score: 90}
	BuildTurnEnvelope(&proceed, SliceResult{}, TurnBrainContext{}, nil, nil)
	if strings.Contains(proceed.TurnContract, `"stage":`) || strings.Contains(proceed.TurnContract, `"enrichment"`) || strings.Contains(proceed.TurnContract, "\n---\n") {
		t.Errorf("TurnContract without delta contains bare stage or empty enrichment:\n%s", proceed.TurnContract)
	}

	// Unanchored non-code prompt with 1 memory omits verify and targets while keeping the record.
	memCtx := TurnBrainContext{
		Memories: []MatchedRecord{
			{ID: "MEMORY-vet-history", Kind: "memory", Title: "Add vet_history table", Provenance: "operator", Body: "# Add vet_history table\nAdd scoring."},
		},
	}
	uRes := VetPrompt("Reinvention of sandboxing workflow without citing files", IntentTask, SliceResult{}, memCtx)
	uParsed := parseStructuredBanner(t, uRes.SystemMessage)
	if uParsed.Enrichment == nil || uParsed.Enrichment.Verify != "" || len(uParsed.Enrichment.Targets) != 0 ||
		len(uParsed.Enrichment.Records) != 1 || strings.Contains(uParsed.Enrichment.Records[0].Note, "#") {
		t.Errorf("unanchored non-code prompt enrichment mismatch: %+v\nraw:\n%s", uParsed.Enrichment, uRes.SystemMessage)
	}
}

func TestZeroEmojisAndStructuredTelemetrySurfaces(t *testing.T) {
	emojiRe := regexp.MustCompile("[\U0001F300-\U0001FAFF\u2600-\u27BF]")

	// 1. PreInvocation degraded banner: nil score, STORE_DEGRADED gate, text fence, zero emojis.
	degraded := formatStoreDegradedBanner(errors.New("sqlite busy"))
	if emojiRe.MatchString(degraded) {
		t.Errorf("degraded banner contains emoji:\n%s", degraded)
	}
	parsedDeg := parseStructuredBanner(t, degraded)
	if parsedDeg.Gate != "STORE_DEGRADED" || parsedDeg.Score != nil || !strings.Contains(parsedDeg.StoreError, "sqlite busy") {
		t.Errorf("unexpected degraded banner: %+v\nraw:\n%s", parsedDeg, degraded)
	}

	// 2. Read-ledger [UNCHANGED] and [MODIFIED] key-value payloads.
	unchanged := formatUnchangedLedgerNotice("internal/search/hook.go", 10, 40, "abcdef1234567890", 2, "10-40 func Foo()")
	if !strings.HasPrefix(unchanged, "[UNCHANGED]\n") || emojiRe.MatchString(unchanged) {
		t.Errorf("bad unchanged ledger notice:\n%s", unchanged)
	}
	lUnch := parseStructuredLedgerNotice(t, "UNCHANGED", unchanged)
	if lUnch.StampConsumed || lUnch.StartLine != 10 || lUnch.EndLine != 40 || lUnch.HitCount != 2 || len(lUnch.Skeleton) != 1 {
		t.Errorf("invalid UNCHANGED LedgerNotice: %+v\nraw:\n%s", lUnch, unchanged)
	}
	if unchanged != renderLedgerNotice(lUnch) {
		t.Errorf("UNCHANGED notice did not round-trip:\ngot:\n%s\nwant:\n%s", unchanged, renderLedgerNotice(lUnch))
	}

	modified := formatModifiedLedgerNotice("internal/search/hook.go", "111111111111", "222222222222", "1-50 func Bar()")
	if !strings.HasPrefix(modified, "[MODIFIED]\n") || emojiRe.MatchString(modified) {
		t.Errorf("bad modified ledger notice:\n%s", modified)
	}
	lMod := parseStructuredLedgerNotice(t, "MODIFIED", modified)
	if lMod.PreviousSha256 != "111111111111" || lMod.CurrentSha256 != "222222222222" {
		t.Errorf("invalid MODIFIED LedgerNotice: %+v\nraw:\n%s", lMod, modified)
	}

	// 3. context/*.md, rules/*.md, agents/*/*.md contain zero emojis and declare structured Markdown callouts/dispatch lines.
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
	if !strings.Contains(string(ctxData), "> **fyr** — <action>") ||
		!strings.Contains(string(ctxData), "never JSON blocks") ||
		!strings.Contains(string(ctxData), "Never use emojis") {
		t.Errorf("context/AGENTS.md missing structured Markdown callout / zero-emoji contract")
	}
	dispData, err := os.ReadFile(filepath.Join(root, "rules", "agy-dispatch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dispData), "Dispatch: REVIEWER") ||
		!strings.Contains(string(dispData), "<model-id> · HIGH") ||
		strings.Contains(string(dispData), `"tier":`) ||
		strings.Contains(string(dispData), `"model": "agy:pro"`) {
		t.Errorf("rules/agy-dispatch.md missing structured dispatch line contract or still mentions tier/agy:pro")
	}
}

func TestResolveModelDetailsAndHookFallback(t *testing.T) {
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
	if got := resolveHookSessionModelForHome("", hookIn); got != "" {
		t.Fatalf("resolveHookSessionModelForHome with empty home = %q, want empty", got)
	}
	if got := resolveHookSessionModelForHome(home, hookIn); got != "claude-opus-5-5-high" {
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
	if got := resolveHookSessionModelForHome(home, hookIn); got != "gemini-4-neon-high" {
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
		return parseStructuredBanner(t, resp.InjectSteps[0].SystemMessage.SystemMessage)
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
	b := parseStructuredBanner(t, msg)
	wantKnee := "305k tokens (>= 300K 60% model knee) — " + contextKneeHandoffCue
	if b.Model != "gemini-4-carbon-high" || b.ModelDisplay != "Gemini 4 Carbon (High)" || b.Thinking != "HIGH" ||
		b.CompactedGeneration != 2 || !b.DirtyFastPath || b.PivotCount != 4 || b.ModelTip != "consider Carbon" ||
		b.TokenKneeWarning != wantKnee || len(b.Notices) != 0 {
		t.Fatalf("unexpected typed TurnBannerMeta output: %+v", b)
	}
	// Already-cued knee warning preserves existing cue without duplicating /fyr-handoff.
	metaAlreadyCued := TurnBannerMeta{
		ContextKneeWarning: wantKnee,
	}
	msgCued, _ := BuildTurnEnvelopeWithMeta(&res, SliceResult{}, TurnBrainContext{}, metaAlreadyCued)
	bCued := parseStructuredBanner(t, msgCued)
	if bCued.TokenKneeWarning != wantKnee || strings.Count(bCued.TokenKneeWarning, "/fyr-handoff") != 1 {
		t.Fatalf("unexpected deduplicated knee cue: %q", bCued.TokenKneeWarning)
	}
	// ExtraSections fallback [CONTEXT_KNEE_WARNING] appends /fyr-handoff cue when absent and stays out of SessionContext.
	metaFallback := TurnBannerMeta{
		ExtraSections: []string{"[CONTEXT_KNEE_WARNING] 190k tokens (>= 180K 60% model knee)"},
	}
	msgFallback, _ := BuildTurnEnvelopeWithMeta(&res, SliceResult{}, TurnBrainContext{}, metaFallback)
	bFallback := parseStructuredBanner(t, msgFallback)
	wantFallbackKnee := "190k tokens (>= 180K 60% model knee) — " + contextKneeHandoffCue
	if bFallback.TokenKneeWarning != wantFallbackKnee || (bFallback.Enrichment != nil && len(bFallback.Enrichment.SessionContext) != 0) {
		t.Fatalf("unexpected extraSections fallback knee warning: %+v", bFallback)
	}
	// Trust triggers for ImprovedPrompt-only enrichment (naming SkillDigests), and omits for Targets-only.
	resPromptOnly := VetResult{
		Intent:         IntentTask,
		Gate:           GateEnrichAndProceed,
		Score:          65,
		ImprovedPrompt: "[VET_IMPROVED_PROMPT] fix foo",
	}
	msgPromptOnly, _ := BuildTurnEnvelopeWithMeta(&resPromptOnly, SliceResult{}, TurnBrainContext{}, TurnBannerMeta{})
	bPromptOnly := parseStructuredBanner(t, msgPromptOnly)
	if bPromptOnly.Enrichment == nil || !strings.Contains(bPromptOnly.Enrichment.Trust, "SkillDigests") {
		t.Fatalf("expected Trust naming SkillDigests on ImprovedPrompt-only enrichment, got %+v", bPromptOnly.Enrichment)
	}
	resTargetsOnly := VetResult{
		Intent:      IntentTask,
		Gate:        GateEnrichAndProceed,
		Score:       85,
		TargetFiles: []string{"internal/search/fence.go"},
	}
	msgTargetsOnly, _ := BuildTurnEnvelopeWithMeta(&resTargetsOnly, SliceResult{}, TurnBrainContext{}, TurnBannerMeta{})
	if strings.Contains(msgTargetsOnly, "\n---\n") || strings.Contains(msgTargetsOnly, "Trust:") {
		t.Fatalf("expected Targets-only enrichment to omit Trust footer, got:\n%s", msgTargetsOnly)
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
