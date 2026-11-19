package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReadLedgerClampsEndLineAtByteLimit(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	// 250 lines x 232 bytes = 58,000 bytes (> 46,080 B, <= 800 lines): lines 1..198 = 45,936 B.
	var big strings.Builder
	big.WriteString("package sample // " + strings.Repeat("p", 231-len("package sample // ")) + "\n")
	for line := 2; line <= 250; line++ {
		prefix := fmt.Sprintf("// line %03d ", line)
		big.WriteString(prefix + strings.Repeat("x", 231-len(prefix)) + "\n")
	}
	writeFile(t, dir, "big.go", big.String())

	// 2,000 lines x 30 bytes = 60,000 bytes (> 46,080 B AND > 800 lines; byte cap alone would reach line 1536).
	var shortBig strings.Builder
	shortBig.WriteString("package sample // 0001 xxxxxx\n")
	for line := 2; line <= 2000; line++ {
		prefix := fmt.Sprintf("// line %04d ", line)
		shortBig.WriteString(prefix + strings.Repeat("s", 29-len(prefix)) + "\n")
	}
	writeFile(t, dir, "short_big.go", shortBig.String())

	// 900 lines x 20 bytes = 18,000 bytes (<= 46,080 B, > 800 lines; hits early-return when unclamped).
	var underBytes strings.Builder
	underBytes.WriteString("package sample // 1\n")
	for line := 2; line <= 900; line++ {
		prefix := fmt.Sprintf("// %04d ", line)
		underBytes.WriteString(prefix + strings.Repeat("u", 19-len(prefix)) + "\n")
	}
	writeFile(t, dir, "under_bytes.go", underBytes.String())

	callReadHook := func(convID string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, _ := json.Marshal(map[string]any{"conversationId": convID, "workspacePaths": []string{dir}, "toolCall": map[string]any{"name": "read_file", "args": args}})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return dec
	}

	t.Run("byte-limit-clamp-and-continuation", func(t *testing.T) {
		// With "%d: " prefixes, lines 1..194 reach 45,869 B (<= 46,080 B) while line 195 reaches 46,106 B (> 46,080 B).
		if dec := callReadHook("conv-clamp", map[string]any{"Path": "big.go", "StartLine": 1, "EndLine": 250}); dec.Decision != "allow" {
			t.Fatalf("1st read 1..250: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-clamp", map[string]any{"Path": "big.go", "StartLine": 195, "EndLine": 198}); dec.Decision != "allow" {
			t.Fatalf("follow-up read of prefix-truncated lines 195..198: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-clamp", map[string]any{"Path": "big.go", "StartLine": 199, "EndLine": 250}); dec.Decision != "allow" {
			t.Fatalf("follow-up read of truncated tail 199..250: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-clamp", map[string]any{"Path": "big.go", "StartLine": 1, "EndLine": 194}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("re-read of delivered head 1..194: got %+v, want deny [UNCHANGED]", dec)
		}
		if dec := callReadHook("conv-offset", map[string]any{"Path": "big.go", "StartLine": 1, "EndLine": 250}); dec.Decision != "allow" {
			t.Fatalf("conv-offset initial read: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-offset", map[string]any{"Path": "big.go", "StartLine": 1, "EndLine": 250, "ByteOffset": 46080}); dec.Decision != "allow" {
			t.Fatalf("conv-offset ByteOffset=46080 continuation read: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-offset", map[string]any{"Path": "big.go", "StartLine": 1, "EndLine": 250, "ByteOffset": 46080}); dec.Decision != "deny" || !strings.HasPrefix(dec.Reason, "[UNCHANGED]\n") || !strings.Contains(dec.Reason, `"path": "big.go"`) || !strings.Contains(dec.Reason, `"startLine": 195`) || !strings.Contains(dec.Reason, `"endLine": 250`) {
			t.Fatalf("conv-offset duplicate ByteOffset=46080 read: got %+v, want deny [UNCHANGED] big.go:195-250", dec)
		}
	})

	t.Run("raw-under-46080-formatted-over-46080-clamps-at-prefix-budget", func(t *testing.T) {
		// 750 lines x 60 raw bytes = 45,000 raw bytes (<= 46,080 B), but "%d: " prefixes bring
		// lines 1..750 to 48,641 formatted bytes (> 46,080 B), truncating inside line 711 (line 710 ends at 46,041 B).
		var boundary strings.Builder
		for line := 1; line <= 750; line++ {
			prefix := fmt.Sprintf("// %04d ", line)
			boundary.WriteString(prefix + strings.Repeat("b", 59-len(prefix)) + "\n")
		}
		writeFile(t, dir, "boundary.go", boundary.String())
		if dec := callReadHook("conv-prefix-cap", map[string]any{"Path": "boundary.go", "StartLine": 1, "EndLine": 750}); dec.Decision != "allow" {
			t.Fatalf("1st read 1..750: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-prefix-cap", map[string]any{"Path": "boundary.go", "StartLine": 711, "EndLine": 750}); dec.Decision != "allow" {
			t.Fatalf("follow-up read of prefix-truncated tail 711..750: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-prefix-cap", map[string]any{"Path": "boundary.go", "StartLine": 1, "EndLine": 710}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("re-read of delivered 1..710: got %+v, want deny [UNCHANGED]", dec)
		}

		writeFile(t, dir, "empty.go", "")
		if dec := callReadHook("conv-empty", map[string]any{"Path": "empty.go"}); dec.Decision != "allow" {
			t.Fatalf("empty file 1st read: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-empty", map[string]any{"Path": "empty.go"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("empty file 2nd read: got %+v, want deny [UNCHANGED]", dec)
		}
	})

	t.Run("800-line-cap-over-46080-bytes", func(t *testing.T) {
		if dec := callReadHook("conv-lines-big", map[string]any{"Path": "short_big.go"}); dec.Decision != "deny" || !strings.HasPrefix(dec.Reason, "[CLAMPED]\n") {
			t.Fatalf("1st unranged read of short_big.go: got %+v, want deny [CLAMPED]", dec)
		}
		if dec := callReadHook("conv-lines-big", map[string]any{"Path": "short_big.go"}); dec.Decision != "allow" {
			t.Fatalf("2nd unranged retry of short_big.go: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-lines-big", map[string]any{"Path": "short_big.go", "StartLine": 801, "EndLine": 900}); dec.Decision != "allow" {
			t.Fatalf("follow-up read of unread lines 801..900 on >46KB short-line file: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-lines-big", map[string]any{"Path": "short_big.go", "StartLine": 1, "EndLine": 800}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("re-read of delivered lines 1..800: got %+v, want deny [UNCHANGED]", dec)
		}
	})

	t.Run("800-line-cap-under-46080-bytes", func(t *testing.T) {
		if dec := callReadHook("conv-lines-small", map[string]any{"Path": "under_bytes.go"}); dec.Decision != "deny" || !strings.HasPrefix(dec.Reason, "[CLAMPED]\n") {
			t.Fatalf("1st unranged read of under_bytes.go: got %+v, want deny [CLAMPED]", dec)
		}
		if dec := callReadHook("conv-lines-small", map[string]any{"Path": "under_bytes.go"}); dec.Decision != "allow" {
			t.Fatalf("2nd unranged retry of under_bytes.go: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-lines-small", map[string]any{"Path": "under_bytes.go", "StartLine": 801, "EndLine": 900}); dec.Decision != "allow" {
			t.Fatalf("follow-up read of unread lines 801..900 on <=46KB file: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-lines-ranged", map[string]any{"Path": "under_bytes.go", "StartLine": 50, "EndLine": 900}); dec.Decision != "allow" {
			t.Fatalf("ranged read 50..900: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-lines-ranged", map[string]any{"Path": "under_bytes.go", "StartLine": 850, "EndLine": 900}); dec.Decision != "allow" {
			t.Fatalf("follow-up read 850..900 past 50..849 cap: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-lines-ranged", map[string]any{"Path": "under_bytes.go", "StartLine": 50, "EndLine": 849}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("re-read of delivered 50..849: got %+v, want deny [UNCHANGED]", dec)
		}
	})

	t.Run("byte-offset-past-range-records-nothing", func(t *testing.T) {
		if dec := callReadHook("conv-past-offset", map[string]any{"Path": "under_bytes.go", "StartLine": 1, "EndLine": 50, "ByteOffset": 999999}); dec.Decision != "allow" {
			t.Fatalf("past-range ByteOffset call: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-past-offset", map[string]any{"Path": "under_bytes.go", "StartLine": 1, "EndLine": 50}); dec.Decision != "allow" {
			t.Fatalf("first real read of 1..50 after past-range ByteOffset: got %+v, want allow (nothing should have been stamped)", dec)
		}
		if dec := callReadHook("conv-neg-offset", map[string]any{"Path": "under_bytes.go", "StartLine": 1, "EndLine": 50, "ByteOffset": -1}); dec.Decision != "allow" {
			t.Fatalf("negative ByteOffset call: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-neg-offset", map[string]any{"Path": "under_bytes.go", "StartLine": 1, "EndLine": 50}); dec.Decision != "allow" {
			t.Fatalf("first real read after negative ByteOffset: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-past-start", map[string]any{"Path": "under_bytes.go", "StartLine": 999, "EndLine": 1050}); dec.Decision != "allow" {
			t.Fatalf("StartLine past EOF 1st call: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-past-start", map[string]any{"Path": "under_bytes.go", "StartLine": 999, "EndLine": 1050}); dec.Decision != "allow" {
			t.Fatalf("StartLine past EOF 2nd call: got %+v, want allow (nothing should have been stamped)", dec)
		}
	})

	t.Run("single-line-over-46080-bytes-allows-continuation", func(t *testing.T) {
		writeFile(t, dir, "minified.json", `{"blob":"`+strings.Repeat("a", 60000)+`"}`+"\n")
		if dec := callReadHook("conv-huge-line", map[string]any{"Path": "minified.json", "StartLine": 1, "EndLine": 1}); dec.Decision != "allow" {
			t.Fatalf("1st truncated read of 60KB single line: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-huge-line", map[string]any{"Path": "minified.json", "StartLine": 1, "EndLine": 1, "ByteOffset": 46080}); dec.Decision != "allow" {
			t.Fatalf("ByteOffset=46080 continuation on 60KB single line: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-huge-line", map[string]any{"Path": "minified.json", "StartLine": 1, "EndLine": 1, "ByteOffset": 46080}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("duplicate ByteOffset=46080 continuation after full tail delivered: got %+v, want deny [UNCHANGED]", dec)
		}
	})

	t.Run("byte-offset-within-800-line-cap-stops-at-line-800", func(t *testing.T) {
		// 1,000 lines x 100 bytes = 100,000 B; lines 1..800 = 80,000 B (> 46,080 B).
		// ByteOffset=46080 on 1..1000 delivers lines 462..800, never 801..1000.
		var med strings.Builder
		med.WriteString("package sample // " + strings.Repeat("m", 99-len("package sample // ")) + "\n")
		for line := 2; line <= 1000; line++ {
			prefix := fmt.Sprintf("// line %04d ", line)
			med.WriteString(prefix + strings.Repeat("m", 99-len(prefix)) + "\n")
		}
		writeFile(t, dir, "med.go", med.String())

		if dec := callReadHook("conv-both", map[string]any{"Path": "med.go", "StartLine": 1, "EndLine": 1000, "ByteOffset": 46080}); dec.Decision != "allow" {
			t.Fatalf("ByteOffset=46080 on 1..1000: got %+v, want allow", dec)
		}
		if dec := callReadHook("conv-both", map[string]any{"Path": "med.go", "StartLine": 801, "EndLine": 900}); dec.Decision != "allow" {
			t.Fatalf("follow-up read 801..900 after ByteOffset=46080 on 1..1000: got %+v, want allow on 1st attempt", dec)
		}
		if dec := callReadHook("conv-both", map[string]any{"Path": "med.go", "StartLine": 500, "EndLine": 800}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[UNCHANGED]") {
			t.Fatalf("re-read of delivered 500..800: got %+v, want deny [UNCHANGED]", dec)
		}
	})
}

func TestUnrangedReadOf600LineFileDeniedWithSkeletonAndRangedRetryAllowed(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()

	var goSrc strings.Builder
	goSrc.WriteString("package sample\n\n")
	for sec := 1; sec <= 25; sec++ {
		fmt.Fprintf(&goSrc, "func Section%02d() int {\n", sec)
		for j := 0; j < 22; j++ {
			fmt.Fprintf(&goSrc, "\t_ = %d\n", sec*100+j)
		}
		goSrc.WriteString("\treturn 1\n}\n\n")
	}
	writeFile(t, dir, "large.go", goSrc.String())

	var mdSrc strings.Builder
	mdSrc.WriteString("# Large Design Doc\n\n")
	for sec := 1; sec <= 25; sec++ {
		fmt.Fprintf(&mdSrc, "## Part %02d\n\n", sec)
		for j := 0; j < 22; j++ {
			fmt.Fprintf(&mdSrc, "- detail line %d for part %02d\n", j, sec)
		}
		mdSrc.WriteString("\n")
	}
	writeFile(t, dir, "large.md", mdSrc.String())

	callReadHook := func(convID string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "read_file", "args": args},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal: %v (raw=%q)", err, out.String())
		}
		return dec
	}

	assertClampedThenRanged := func(conv, file, wantSkel string) {
		t.Helper()
		d1 := callReadHook(conv, map[string]any{"Path": file})
		if d1.Decision != "deny" || len(d1.Overwrite) != 0 || !strings.HasPrefix(d1.Reason, "[CLAMPED]\n") {
			t.Fatalf("%s unranged = %+v, want deny [CLAMPED] with nil Overwrite", file, d1)
		}
		var notice LedgerNotice
		raw := strings.TrimPrefix(d1.Reason, "[CLAMPED]\n")
		if err := json.Unmarshal([]byte(raw), &notice); err != nil {
			t.Fatalf("%s invalid [CLAMPED] JSON: %v\n%s", file, err, raw)
		}
		if notice.Status != "CLAMPED" || notice.TotalLines < 600 || len(notice.Skeleton) == 0 || !strings.Contains(strings.Join(notice.Skeleton, "\n"), wantSkel) {
			t.Fatalf("%s notice = %+v, want Status=CLAMPED TotalLines>=600 Skeleton containing %q", file, notice, wantSkel)
		}
		d2 := callReadHook(conv, map[string]any{"Path": file, "StartLine": 1, "EndLine": 120})
		if d2.Decision != "allow" {
			t.Fatalf("%s ranged retry 1..120 = %+v, want allow", file, d2)
		}
		d3 := callReadHook(conv, map[string]any{"Path": file, "StartLine": 1, "EndLine": 120})
		if d3.Decision != "deny" || !strings.HasPrefix(d3.Reason, "[UNCHANGED]\n") {
			t.Fatalf("%s duplicate ranged 1..120 = %+v, want deny [UNCHANGED]", file, d3)
		}
	}

	assertClampedThenRanged("conv-clamp-go", "large.go", "func Section01")
	assertClampedThenRanged("conv-clamp-md", "large.md", "## Part 01")

	// Regression tests for review & security findings on hasExplicitReadRange and buildClampSkeleton redaction.
	t.Run("range_edge_cases_and_secret_redaction", func(t *testing.T) {
		var secretEnv strings.Builder
		secretToken := strings.Join([]string{"gh", "p_", "1234567890abcdef1234567890abcdef1234"}, "")
		for i := 0; i < 420; i++ {
			if i%35 == 0 {
				fmt.Fprintf(&secretEnv, "GITHUB_TOKEN=%s\n", secretToken)
			} else {
				fmt.Fprintf(&secretEnv, "APP_SETTING_%03d=value_%03d\n", i, i)
			}
		}
		writeFile(t, dir, "secrets.env", secretEnv.String())

		// Unbounded StartLine=1 without EndLine/Limit/ByteOffset must clamp, and sampled secrets must be redacted.
		dUnbounded := callReadHook("conv-clamp-start1", map[string]any{"Path": "secrets.env", "StartLine": 1})
		if dUnbounded.Decision != "deny" || !strings.HasPrefix(dUnbounded.Reason, "[CLAMPED]\n") {
			t.Fatalf("StartLine=1 without EndLine/Limit = %+v, want deny [CLAMPED]", dUnbounded)
		}
		if strings.Contains(dUnbounded.Reason, secretToken) || strings.Contains(dUnbounded.Reason, "GITHUB_TOKEN="+secretToken) {
			t.Fatalf("clamped skeleton leaked secret in deny reason: %s", dUnbounded.Reason)
		}
		if !strings.Contains(dUnbounded.Reason, "[redacted by fyr]") {
			t.Fatalf("expected [redacted by fyr] in clamped non-code skeleton: %s", dUnbounded.Reason)
		}

		// Leading 0-valued StartLine/Offset with explicit EndLine/Limit must be treated as ranged (allow on 1st attempt).
		if dZeroOffset := callReadHook("conv-zero-offset", map[string]any{"Path": "large.go", "Offset": 0, "Limit": 80}); dZeroOffset.Decision != "allow" {
			t.Fatalf("Offset=0 Limit=80 = %+v, want allow", dZeroOffset)
		}
		if dZeroStart := callReadHook("conv-zero-start", map[string]any{"Path": "large.go", "StartLine": 0, "EndLine": 80}); dZeroStart.Decision != "allow" {
			t.Fatalf("StartLine=0 EndLine=80 = %+v, want allow", dZeroStart)
		}
	})
}

func TestPreToolUseReadReusesStoreAndRecordsAtomicWatchdog(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	var large strings.Builder
	large.WriteString("package sample\n\n")
	for i := 3; i <= 420; i++ {
		fmt.Fprintf(&large, "func fn%03d() int { return %d }\n", i, i)
	}
	writeFile(t, dir, "large_reuse.go", large.String())

	call := func(id string, args map[string]any) HookDecisionOutput {
		raw, _ := json.Marshal(map[string]any{
			"conversationId": "conv-reuse",
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"id": id, "name": "read_file", "args": args},
		})
		var out bytes.Buffer
		if err := RunHook(Options{Dir: dir, Home: home, Hook: "pre-tool-use"}, bytes.NewReader(raw), &out); err != nil {
			t.Fatalf("RunHook: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	d1 := call("c1", map[string]any{"Path": "large_reuse.go"})
	if d1.Decision != "deny" || !strings.HasPrefix(d1.Reason, "[CLAMPED]\n") {
		t.Fatalf("first unranged read = %+v, want [CLAMPED] deny", d1)
	}
	d2 := call("c2", map[string]any{"Path": "large_reuse.go", "StartLine": 1, "EndLine": 40})
	if d2.Decision != "allow" {
		t.Fatalf("ranged read = %+v, want allow", d2)
	}
	d3 := call("c3", map[string]any{"Path": "large_reuse.go", "StartLine": 1, "EndLine": 40})
	if d3.Decision != "deny" || !strings.HasPrefix(d3.Reason, "[UNCHANGED]") {
		t.Fatalf("repeat ranged read = %+v, want [UNCHANGED] deny", d3)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()
	var calls, ringCount int
	if err := st.db.QueryRow(`SELECT COALESCE(tool_calls, 0) FROM session_meta WHERE conversation_id = 'conv-reuse'`).Scan(&calls); err != nil || calls != 1 {
		t.Fatalf("session_meta.tool_calls = %d, err=%v; want 1", calls, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM watchdog_ring WHERE conversation_id = 'conv-reuse'`).Scan(&ringCount); err != nil || ringCount != 1 {
		t.Fatalf("watchdog_ring count = %d, err=%v; want 1", ringCount, err)
	}
}
