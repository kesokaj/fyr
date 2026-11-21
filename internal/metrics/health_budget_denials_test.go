package metrics

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubagentBudgetCapsBuilderVsAuditRoles(t *testing.T) {
	for _, tc := range []struct {
		role string
		want int
	}{
		{"self", 120},
		{"SELF", 120},
		{"builder", 120},
		{"Builder", 120},
		{"builder-phase2a", 120},
		{"BUILDER-1", 120},
		{"builder_slice", 120},
		{"reviewer", 40},
		{"security", 40},
		{"verifier", 40},
		{"explorer", 40},
		{"dreamer", 40},
		{"documentation", 40},
		{"research", 40},
		{"", 40},
	} {
		if got := subagentCapForRole(tc.role); got != tc.want {
			t.Errorf("subagentCapForRole(%q) = %d, want %d", tc.role, got, tc.want)
		}
	}

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	ts := now.Add(-2 * time.Hour).UnixMilli()
	home := t.TempDir()
	proj := filepath.Join(t.TempDir(), "proj")
	s := openT(t, Path(home))

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('M1', 'agy', ?, ?, ?)`, proj, ts, ts+60000)

	subs := []struct {
		id, agent string
		tools     int
	}{
		{"B_EXACT", "builder", 120},         // exact cap: OK
		{"B_WORKER", "builder-phase2a", 95}, // 95 <= 120: OK
		{"B_UPPER", "BUILDER-2", 110},       // case-insensitive: OK
		{"B_OVER", "builder-over", 121},     // > 120: OVER
		{"S_OK", "self", 120},               // exact cap: OK
		{"R_EXACT", "reviewer", 40},         // exact cap: OK
		{"R_OVER", "reviewer", 41},          // > 40: OVER
		{"SEC_OVER", "security", 45},        // > 40: OVER
		{"EXP_OK", "explorer", 35},          // <= 40: OK
		{"DOC_OVER", "documentation", 42},   // > 40: OVER
	}
	for idx, sub := range subs {
		start := ts + int64(idx+1)*1000
		exec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, started_at, ended_at) VALUES (?, 'agy', ?, 'M1', ?, ?, ?)`,
			sub.id, proj, sub.agent, start, start+500)
		for i := 0; i < sub.tools; i++ {
			exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', 'Read')`,
				sub.id, fmt.Sprintf("%s:%d", sub.id, i), start+int64(i))
		}
	}

	// Also verify search.db session_meta parity for builder-* vs audit roles.
	sdb := filepath.Join(proj, ".agents", "scratch", "search.db")
	if err := os.MkdirAll(filepath.Dir(sdb), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, sdb,
		`CREATE TABLE session_meta(conversation_id TEXT PRIMARY KEY, generation_id INT, tool_calls INT, agent_name TEXT, parent_conversation_id TEXT)`,
		`INSERT INTO session_meta VALUES ('M1', 0, 150, '', '')`,
		`INSERT INTO session_meta VALUES ('meta-builder-ok', 0, 95, 'builder-wave1', 'M1')`,
		`INSERT INTO session_meta VALUES ('meta-builder-over', 0, 125, 'builder-wave2', 'M1')`,
		`INSERT INTO session_meta VALUES ('meta-verifier-over', 0, 41, 'verifier', 'M1')`,
	)

	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	// Over budget: B_OVER, R_OVER, SEC_OVER, DOC_OVER (4 from steps) + meta-builder-over, meta-verifier-over (2 from search.db) = 6.
	if sum.Health.SubagentsOverBudget != 6 {
		t.Fatalf("SubagentsOverBudget = %d, want 6 (builder<=120 must not be flagged)", sum.Health.SubagentsOverBudget)
	}
}

func TestHookDenialsSeparatedFromToolErrorsAcrossMetrics(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, filepath.Join(agyDir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C_MAIN', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('C_BLD', '2026-10-08 10:06:00+00:00', '["file://`+root+`"]', 'builder-1', 'C_MAIN')`,
	)

	const t0 = 1791367200
	writeConv := func(id string, rows []struct {
		idx, typ, status int
		meta, errd       []byte
	}) {
		cp := filepath.Join(agyDir, "conversations", id+".db")
		sqliteT(t, cp,
			"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
			"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		db, err := sql.Open("sqlite", cp)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, r := range rows {
			if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, NULL)`, r.idx, r.typ, r.status, r.meta, r.errd); err != nil {
				t.Fatal(err)
			}
		}
	}

	writeConv("C_MAIN", []struct {
		idx, typ, status int
		meta, errd       []byte
	}{
		{0, agyUser, 3, agyStepMeta(t0, "", nil), nil},
		// Genuine Bash error.
		{1, agyTool, agyFailed, agyStepMeta(t0+1, "run_command", map[string]string{"CommandLine": "go test ./..."}), pbB(2, []byte("exit status 1"))},
		// [CLAMPED] denial.
		{2, agyTool, agyFailed, cat(agyStepMeta(t0+2, "read_file", map[string]string{"Path": filepath.Join(root, "big.go")}), pbB(34, cat(pbB(4, []byte("deny")), pbB(5, []byte("[CLAMPED] file too big"))))), nil},
		// [LEDGER] denial (new marker!).
		{3, agyTool, agyFailed, agyStepMeta(t0+3, "read_file", map[string]string{"Path": filepath.Join(root, "a.go")}), pbB(2, []byte("[LEDGER] already read this turn"))},
		// [MODIFIED] denial (edit guard).
		{4, agyTool, agyFailed, agyStepMeta(t0+4, "edit_file", map[string]string{"Path": filepath.Join(root, "a.go")}), pbB(2, []byte("[MODIFIED] file changed externally"))},
		// [VERIFY_STAMP] + [ASK_GUARD] denials.
		{5, agyTool, agyFailed, agyStepMeta(t0+5, "run_command", map[string]string{"CommandLine": "git commit -m x"}), pbB(2, []byte("[VERIFY_STAMP] dirty go"))},
		{6, agyTool, agyFailed, agyStepMeta(t0+6, "ask_question", map[string]string{}), pbB(2, []byte("[ASK_GUARD] no build questions"))},
		// Hook-denied SKILL.md read must not count as loaded skill.
		{7, agyTool, agyFailed, agyStepMeta(t0+7, "read_file", map[string]string{"Path": "/home/u/.gemini/config/skills/golang/SKILL.md"}), pbB(2, []byte("[UNCHANGED] already read"))},
		// Succeeded tool call.
		{8, agyTool, 3, agyStepMeta(t0+8, "read_file", map[string]string{"Path": filepath.Join(root, "ok.go")}), nil},
	})
	writeConv("C_BLD", []struct {
		idx, typ, status int
		meta, errd       []byte
	}{
		{0, agyUser, 3, agyStepMeta(t0+10, "", nil), nil},
		{1, agyTool, agyFailed, agyStepMeta(t0+11, "read_file", map[string]string{"Path": filepath.Join(root, "b.go")}), pbB(2, []byte("pre-tool hook denied: [CLAMPED]"))},
		{2, agyTool, 3, agyStepMeta(t0+12, "edit_file", map[string]string{"Path": filepath.Join(root, "b.go")}), nil},
	})

	s := openT(t, Path(t.TempDir()))
	ingestT(t, s, Options{Home: home})

	// Legacy row with both is_error=1 and denied=1 on builder-1 must still be excluded from errors.
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, denied) VALUES ('C_BLD', 'C_BLD:99', ?, 'tool', 'Read', 1, 1)`, int64(t0+99)*1000); err != nil {
		t.Fatal(err)
	}

	// Hook-denied SKILL.md read must not be reported as loaded.
	loadedSkills := s.readLoadedSkillsByConv()
	if loadedSkills["C_MAIN"]["golang"] {
		t.Errorf("hook-denied SKILL.md read was falsely treated as loaded skill")
	}

	sum, err := s.Show(ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}

	// 1 genuine error (C_MAIN:1); 8 hook denials (6 on C_MAIN + 2 on C_BLD including legacy row).
	if sum.D.ToolErrors["agy"] != 1 {
		t.Errorf("D.ToolErrors[agy] = %d, want 1", sum.D.ToolErrors["agy"])
	}
	if sum.D.HookDenials["agy"] != 8 || sum.Health.HookDenials != 8 {
		t.Errorf("HookDenials: D=%d Health=%d, want 8", sum.D.HookDenials["agy"], sum.Health.HookDenials)
	}
	if sum.D.Denials["agy"] != nil {
		t.Errorf("D.Denials[agy] = %v, want nil for backward compatibility", sum.D.Denials["agy"])
	}
	if sum.Health.ClampedSaves != 2 || sum.Health.HookBlocksLedger != 3 || sum.Health.HookBlocksGuards != 3 {
		t.Errorf("Health breakdown: clamped=%d ledger=%d guards=%d, want 2/3/3",
			sum.Health.ClampedSaves, sum.Health.HookBlocksLedger, sum.Health.HookBlocksGuards)
	}
	if len(sum.Daily) != 1 || sum.Daily[0].Errors["agy"] != 1 || sum.Daily[0].Denials["agy"] != 8 {
		t.Errorf("Daily[0] = %+v, want Errors[agy]=1 Denials[agy]=8", sum.Daily)
	}

	rolesByName := map[string]AgentRoleMetrics{}
	for _, r := range sum.AgentRoles {
		rolesByName[r.Role] = r
	}
	if m := rolesByName["main"]; m.ToolErrors != 1 || m.HookDenials != 6 {
		t.Errorf("AgentRole main = %+v, want ToolErrors=1 HookDenials=6", m)
	}
	if b := rolesByName["builder-1"]; b.ToolErrors != 0 || b.HookDenials != 2 {
		t.Errorf("AgentRole builder-1 = %+v, want ToolErrors=0 HookDenials=2", b)
	}

	var txt, md bytes.Buffer
	sum.WriteText(&txt)
	if !strings.Contains(txt.String(), "hook denials (excl. errors)") {
		t.Errorf("WriteText missing hook denials line:\n%s", txt.String())
	}
	if err := RenderMarkdownReport(&md, sum, nil, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "| Role | Sessions | Tool calls | Tool error % | Hook denials |") {
		t.Errorf("Markdown Agent Roles table missing Hook denials column:\n%s", md.String())
	}
}
