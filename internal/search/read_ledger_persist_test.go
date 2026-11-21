package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestReadLedgerPersistenceEditAndExternalCheckout(t *testing.T) {
	runReadHook := func(dir, home, convID string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "read_file", "args": args},
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("read_file hook: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal read_file decision: %v (raw=%q)", err, out.String())
		}
		return dec
	}

	runEditHook := func(dir, home, convID string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "edit_file", "args": args},
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("edit_file hook: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal edit_file decision: %v (raw=%q)", err, out.String())
		}
		return dec
	}

	makeNumberedFile := func() string {
		var b strings.Builder
		b.WriteString("package persist\n")
		for i := 2; i <= 60; i++ {
			fmt.Fprintf(&b, "func fn%02d() int { return %d }\n", i, i)
		}
		return b.String()
	}

	t.Run("2nd_and_3rd_reads_stay_blocked_until_content_hash_changes", func(t *testing.T) {
		dir, home := setupHookRepo(t), t.TempDir()
		if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Index: %v", err)
		}
		d1 := runReadHook(dir, home, "conv-p1", map[string]any{"Path": "service.go"})
		if d1.Decision != "allow" {
			t.Fatalf("1st read = %+v, want allow", d1)
		}
		for hit := 2; hit <= 4; hit++ {
			d := runReadHook(dir, home, "conv-p1", map[string]any{"Path": "service.go"})
			if d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]\n") {
				t.Fatalf("read #%d = %+v, want deny [UNCHANGED]", hit, d)
			}
			notice := parseStructuredLedgerNotice(t, "UNCHANGED", d.Reason)
			if notice.StampConsumed || notice.HitCount != hit {
				t.Fatalf("read #%d notice = %+v, want StampConsumed=false HitCount=%d", hit, notice, hit)
			}
		}

		// Content-hash change unlocks the next read cleanly.
		writeFile(t, dir, "service.go", hookSampleServiceSrc+"\n// hash changed\n")
		dAfter := runReadHook(dir, home, "conv-p1", map[string]any{"Path": "service.go"})
		if dAfter.Decision != "allow" {
			t.Fatalf("read after hash change = %+v, want allow", dAfter)
		}
		dRepeat := runReadHook(dir, home, "conv-p1", map[string]any{"Path": "service.go"})
		if dRepeat.Decision != "deny" {
			t.Fatalf("re-read after hash change = %+v, want deny", dRepeat)
		}
	})

	t.Run("overlapping_vs_non_overlapping_edit", func(t *testing.T) {
		dir, home := setupHookRepo(t), t.TempDir()
		content := makeNumberedFile()
		writeFile(t, dir, "big_split.go", content)
		commitAll(t, dir, "add big_split.go")
		if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Index: %v", err)
		}

		// Stamp three disjoint ranges: head (1..15), mid (25..35), tail (45..60).
		for _, r := range [][2]int{{1, 15}, {25, 35}, {45, 60}} {
			d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": r[0], "EndLine": r[1]})
			if d.Decision != "allow" {
				t.Fatalf("initial read %v = %+v, want allow", r, d)
			}
		}

		// Same-line-count edit inside line 28..29: only mid (25..35) overlaps; head and tail keep stamps.
		oldMid := "func fn28() int { return 28 }\nfunc fn29() int { return 29 }\n"
		newMid := "func fn28() int { return 280 }\nfunc fn29() int { return 290 }\n"
		e1 := runEditHook(dir, home, "conv-edit", map[string]any{
			"Path":        "big_split.go",
			"Target":      oldMid,
			"Replacement": newMid,
		})
		if e1.Decision != "allow" {
			t.Fatalf("edit_file same-line-count = %+v, want allow", e1)
		}
		content = strings.ReplaceAll(content, oldMid, newMid)
		writeFile(t, dir, "big_split.go", content)

		if d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": 1, "EndLine": 15}); d.Decision != "deny" {
			t.Fatalf("head 1..15 after mid edit = %+v, want deny [UNCHANGED]", d)
		}
		if d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": 45, "EndLine": 60}); d.Decision != "deny" {
			t.Fatalf("tail 45..60 after zero-delta mid edit = %+v, want deny [UNCHANGED]", d)
		}
		if d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": 25, "EndLine": 35}); d.Decision != "allow" {
			t.Fatalf("edited mid 25..35 = %+v, want allow", d)
		}

		// Line-inserting edit (+3 lines: 1 line -> 4 lines) inside line 29: head 1..15 keeps exact span; tail shifts 45..60 -> 48..63.
		oldIns := "func fn29() int { return 290 }\n"
		newIns := "func fn29() int {\n\tx := 290\n\treturn x\n}\n"
		e2 := runEditHook(dir, home, "conv-edit", map[string]any{
			"Path":        "big_split.go",
			"Target":      oldIns,
			"Replacement": newIns,
		})
		if e2.Decision != "allow" {
			t.Fatalf("edit_file +3 lines = %+v, want allow", e2)
		}
		content = strings.ReplaceAll(content, oldIns, newIns)
		writeFile(t, dir, "big_split.go", content)

		if d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": 1, "EndLine": 15}); d.Decision != "deny" {
			t.Fatalf("head 1..15 after +3-line mid edit = %+v, want deny [UNCHANGED]", d)
		}
		if d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": 25, "EndLine": 38}); d.Decision != "allow" {
			t.Fatalf("overlapping mid 25..38 after +3-line edit = %+v, want allow", d)
		}
		if d := runReadHook(dir, home, "conv-edit", map[string]any{"Path": "big_split.go", "StartLine": 48, "EndLine": 63}); d.Decision != "deny" {
			t.Fatalf("shifted tail 48..63 after +3-line edit = %+v, want deny [UNCHANGED]", d)
		}
	})

	t.Run("out_of_root_and_unindexed_reads_bypass_clamp_and_unchanged", func(t *testing.T) {
		primaryRepo, home := setupHookRepo(t), t.TempDir()
		if err := Index(Options{Dir: primaryRepo, Home: home}, &bytes.Buffer{}); err != nil {
			t.Fatalf("Index: %v", err)
		}

		// 1. Out-of-root ~/.config/fyr/models.json (even >400 lines) bypasses [CLAMPED] and [UNCHANGED].
		modelsPath := filepath.Join(home, harness.StateDir, "models.json")
		if err := os.MkdirAll(filepath.Dir(modelsPath), 0o700); err != nil {
			t.Fatal(err)
		}
		var bigModels strings.Builder
		bigModels.WriteString("{\n  \"entries\": [\n")
		for i := 0; i < 420; i++ {
			sep := ","
			if i == 419 {
				sep = ""
			}
			fmt.Fprintf(&bigModels, "    {\"id\": \"gemini-4-neon-%03d\"}%s\n", i, sep)
		}
		bigModels.WriteString("  ]\n}\n")
		if err := os.WriteFile(modelsPath, []byte(bigModels.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		for attempt := 1; attempt <= 3; attempt++ {
			if dec := runReadHook(primaryRepo, home, "conv-ext", map[string]any{"Path": modelsPath}); dec.Decision != "allow" {
				t.Fatalf("models.json read #%d = %+v, want allow bypass", attempt, dec)
			}
		}

		// 2. External unindexed checkout (small file + >400-line file) bypasses [CLAMPED] and [UNCHANGED] without creating .agents.
		extRepo := t.TempDir()
		gitT(t, extRepo, "init")
		gitT(t, extRepo, "config", "user.email", "test@example.com")
		gitT(t, extRepo, "config", "user.name", "Test")
		extFile := filepath.Join(extRepo, "pkg", "ext.go")
		writeFile(t, extRepo, "pkg/ext.go", "package pkg\n\nfunc ExtHelper() int { return 7 }\n")
		var bigExt strings.Builder
		bigExt.WriteString("package pkg\n\n")
		for i := 1; i <= 450; i++ {
			fmt.Fprintf(&bigExt, "func ExtFn%03d() int { return %d }\n", i, i)
		}
		bigExtFile := filepath.Join(extRepo, "pkg", "big_ext.go")
		writeFile(t, extRepo, "pkg/big_ext.go", bigExt.String())
		commitAll(t, extRepo, "init ext without agents ignore")

		for attempt := 1; attempt <= 3; attempt++ {
			if dec := runReadHook(primaryRepo, home, "conv-ext", map[string]any{"Path": extFile}); dec.Decision != "allow" {
				t.Fatalf("external small read #%d = %+v, want allow bypass", attempt, dec)
			}
			if dec := runReadHook(primaryRepo, home, "conv-ext", map[string]any{"Path": bigExtFile}); dec.Decision != "allow" {
				t.Fatalf("external >400-line read #%d = %+v, want allow bypass without [CLAMPED]", attempt, dec)
			}
		}
		if _, err := os.Stat(filepath.Join(extRepo, ".agents")); !os.IsNotExist(err) {
			t.Fatalf("external checkout was polluted with .agents: err=%v", err)
		}
		db := openDB(t, primaryRepo)
		if n := count(t, db, `SELECT count(*) FROM read_ledger WHERE conversation_id = 'conv-ext'`); n != 0 {
			t.Fatalf("out-of-root reads wrote %d rows to primary read_ledger, want 0", n)
		}

		// 3. Standalone transcript & non-checkout / unindexed directory bypass without creating .agents or global search.db.
		transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
		if err := os.WriteFile(transcript, []byte(strings.Repeat("{\"step\":1}\n", 450)), 0o600); err != nil {
			t.Fatal(err)
		}
		for attempt := 1; attempt <= 2; attempt++ {
			if dec := runReadHook(primaryRepo, home, "conv-ext", map[string]any{"Path": transcript}); dec.Decision != "allow" {
				t.Fatalf("standalone transcript read #%d = %+v, want allow bypass", attempt, dec)
			}
		}

		nonCheckout := t.TempDir()
		cleanHome := t.TempDir()
		for attempt := 1; attempt <= 2; attempt++ {
			if dec := runReadHook(nonCheckout, cleanHome, "conv-noncheckout", map[string]any{"Path": bigExtFile}); dec.Decision != "allow" {
				t.Fatalf("non-checkout read #%d = %+v, want allow bypass", attempt, dec)
			}
		}
		if _, err := os.Stat(filepath.Join(nonCheckout, ".agents")); !os.IsNotExist(err) {
			t.Fatalf("non-checkout had .agents created: err=%v", err)
		}
		if _, err := os.Stat(globalDB(cleanHome)); !os.IsNotExist(err) {
			t.Fatalf("non-checkout bypass unexpectedly created global search.db: err=%v", err)
		}

		// Unindexed repo without session conversationId (wdSt == nil and no .agents/) also bypasses without creating .agents.
		unindexedOnly := t.TempDir()
		gitT(t, unindexedOnly, "init")
		writeFile(t, unindexedOnly, "huge.go", bigExt.String())
		for attempt := 1; attempt <= 2; attempt++ {
			if dec := runReadHook(unindexedOnly, cleanHome, "", map[string]any{"Path": "huge.go"}); dec.Decision != "allow" {
				t.Fatalf("unindexed repo read #%d = %+v, want allow bypass", attempt, dec)
			}
		}
		if _, err := os.Stat(filepath.Join(unindexedOnly, ".agents")); !os.IsNotExist(err) {
			t.Fatalf("unindexed repo had .agents created: err=%v", err)
		}
	})
}
