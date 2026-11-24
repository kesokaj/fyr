package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validSpecSections = "\n\n## Purpose\n\nOk.\n\n## Interfaces\n\nOk.\n\n## Data\n\nOk.\n\n## Failure modes\n\n| Mode | Trigger | Behavior | Recovery |\n|---|---|---|---|\n| err | bad | fail | retry |\n\n## Verification\n\nOk.\n"

func TestDocsStyleAndAgentsBudget(t *testing.T) {
	t.Run("track1_scope_and_exclusions", testStyleTrack1Scope)
	t.Run("agent_only_db_safety_only", testStyleAgentDBSafetyOnly)
	t.Run("mermaid_node_cap", testStyleMermaidCap)
	t.Run("unfolded_table_row_cap", testStyleTableCap)
	t.Run("destructive_tokens_callout", testStyleDestructiveCallout)
	t.Run("yaml_frontmatter_root_and_records", testStyleYAMLFrontmatter)
	t.Run("banned_fluff_and_latin_words", testStyleBannedWords)
	t.Run("advisory_ste_sentence_length", testStyleSTESentenceLength)
	t.Run("advisory_glossary_json", testStyleGlossaryJSON)
	t.Run("project_agents_budget", testProjectAgentsBudget)
}

func testStyleTrack1Scope(t *testing.T) {
	trail := trailLine(t, "GUIDE-live", "guide", "current", "This uses seamless setup.\n") +
		trailLine(t, "DEPLOY-live", "deploy", "current", "This uses zero-ops deploy.\n") +
		trailLine(t, "GUIDE-old", "guide", "retired", "This uses seamless setup.\n") +
		trailLine(t, "PLAN-1", "plan", "open", "This uses seamless setup.\n") +
		trailLine(t, "SPEC-1", "spec", "current", "Code `internal/a.go`. Seamless spec prose."+validSpecSections)
	dir := repo(t, map[string]string{
		"internal/a.go":          "package a\n",
		"README.md":              "A seamless tool.\n",
		"SDD.md":                 "A foundational architecture.\n",
		"TDD.md":                 "A zero-ops design.\n",
		"Workflow.md":            "Steps, e.g., build.\n",
		"docs/guide.md":          "Use i.e. here.\n",
		"docs/sub/deep.md":       "Items, etc.\n",
		"features/f.md":          "A seamless flow.\n",
		"docs/skip/.fyr-noindex": "", "docs/skip/ignored.md": "A seamless ignored doc.\n",
		"PRD.md": "# PRD\n\nA seamless requirement, e.g., R1.\n", "TODO.md": "# TODO\n\nA seamless task, etc.\n",
		"AGENTS.md":            "# AGENTS\n\nA seamless rule, e.g., check.\n",
		"skills/own/SKILL.md":  "---\nname: own\ndescription: own skill\n---\nA seamless skill.\n",
		"skills/VENDORED.md":   "| Skill | Publisher |\n|---|---|\n| vend | ext |\n",
		"skills/vend/SKILL.md": "---\nname: vend\ndescription: vend skill\n---\nA seamless vendored skill.\n",
		"rules/r.md":           "---\ntrigger: always_on\n---\nA seamless rule.\n",
		"agents/a/agent.md":    "---\nname: a\ndescription: a\n---\nA seamless agent.\n",
		".agents/trail.jsonl":  trail,
	})
	rep := run(t, dir)
	same(t, rep.Findings,
		"README.md:1 docs-style", "SDD.md:1 docs-style", "TDD.md:1 docs-style",
		"Workflow.md:1 docs-style", "docs/guide.md:1 docs-style", "docs/sub/deep.md:1 docs-style",
		"features/f.md:1 docs-style",
	)
	if d, s, r, tr := Counts(rep.Findings); d != 7 || s != 0 || r != 0 || tr != 0 {
		t.Errorf("Counts = (%d,%d,%d,%d), want (7,0,0,0)", d, s, r, tr)
	}
}

func testStyleAgentDBSafetyOnly(t *testing.T) {
	bigMermaid := "```mermaid\nflowchart LR\n  n1 --> n2\n  n2 --> n3\n  n3 --> n4\n  n4 --> n5\n  n5 --> n6\n  n6 --> n7\n  n7 --> n8\n  n8 --> n9\n  n9 --> n10\n  n10 --> n11\n  n11 --> n12\n  n12 --> n13\n```\n"
	longSentence := "This single agent-only database sentence intentionally exceeds twenty five words by adding extra descriptive clauses and seamless foundational zero-ops e.g. whitelist prose so only safety checks should apply.\n"
	safeDBBody := "# Agent DB Record\n\n" + longSentence + "\n" + bigMermaid + "\n" + makeTable(14) + "\n> [!CAUTION]\n> Deletes scratch.\n\n```bash\nrm -rf ./scratch\n```\n"
	trailSafe := trailLine(t, "GUIDE-agent", "guide", "current", safeDBBody) +
		trailLine(t, "DEPLOY-agent", "deploy", "current", safeDBBody)
	repSafe := run(t, repo(t, map[string]string{
		".agents/glossary.json": `{"whitelist":"allowlist"}`,
		".agents/trail.jsonl":   trailSafe,
	}))
	same(t, repSafe.Findings)
	if n := notes(repSafe); strings.Contains(n, "GUIDE-agent") || strings.Contains(n, "DEPLOY-agent") || strings.Contains(n, "docs-style-note") {
		t.Errorf("agent-only brain.db records must emit 0 docs-style-note lines, got: %q", n)
	}

	// Must-fail: YAML frontmatter and unguarded destructive commands still fail docs-style on current guide/deploy records,
	// while retired guide/deploy records remain untouched.
	trailBad := trailLine(t, "GUIDE-fm", "guide", "current", "---\ntitle: bad\n---\n\n# Guide\n") +
		trailLine(t, "DEPLOY-rm", "deploy", "current", "# Deploy\n\n```bash\nrm -rf ./data\n```\n") +
		trailLine(t, "GUIDE-sql", "guide", "current", "# Guide\n\n```sql\nDROP TABLE sessions;\n```\n") +
		trailLine(t, "DEPLOY-retired", "deploy", "retired", "---\ntitle: old\n---\n\n```bash\ngit push --force\n```\n")
	repBad := run(t, repo(t, map[string]string{".agents/trail.jsonl": trailBad}))
	same(t, repBad.Findings, "DEPLOY-rm:4 docs-style", "GUIDE-fm:1 docs-style", "GUIDE-sql:4 docs-style")
}

func testStyleMermaidCap(t *testing.T) {
	flow12 := "```mermaid\nflowchart LR\n  direction LR\n  subgraph sg1[\"Group One\"]\n" +
		"    n1[\"1\"] --> n2[\"2\"]\n    n2 --> n3[\"3\"]\n    n3 --> n4[\"4\"]\n    n4 --> n5[\"5\"]\n    n5 --> n6[\"6\"]\n  end\n" +
		"  subgraph sg2[\"Group Two\"]\n" +
		"    n7[\"7\"] --> n8[\"8\"]\n    n8 --> n9[\"9\"]\n    n9 --> n10[\"10\"]\n    n10 --> n11[\"11\"]\n    n11 --> n12[\"12\"]\n  end\n" +
		"  n6 -- \"handoff\" --> n7\n  %% fake13 --> fake14\n  classDef hi fill:#fff\n  class n1 hi\n  style n2 stroke:#000\n  linkStyle 0 stroke:#0B7A74\n  click n1 \"https://example.invalid\"\n```\n"
	flow13 := strings.Replace(flow12, "n11 --> n12[\"12\"]", "n11 --> n12[\"12\"]\n    n12 --> n13[\"13\"]", 1)

	seq12 := "```mermaid\nsequenceDiagram\n"
	for i := 0; i < 12; i++ {
		seq12 += "  participant P" + string(rune('A'+i)) + " as Service\n"
	}
	seq12 += "  PA->>PB: call\n```\n"
	seq13 := strings.Replace(seq12, "PA->>PB: call", "  actor PM as Extra\n  PA->>PM: call", 1)

	same(t, run(t, repo(t, map[string]string{"README.md": flow12 + "\n" + seq12})).Findings)
	same(t, run(t, repo(t, map[string]string{"README.md": flow13})).Findings, "README.md:1 docs-style")
	same(t, run(t, repo(t, map[string]string{"README.md": seq13})).Findings, "README.md:1 docs-style")

	// README.md's actual 11-node delivery map must pass.
	readmeBytes, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read repo README.md: %v", err)
	}
	txt := string(readmeBytes)
	s, e := strings.Index(txt, "### Delivery, Release & Local Loop"), strings.Index(txt, "### On-Machine Sessions, Dream, Eval & Auto")
	if s < 0 || e <= s {
		t.Fatal("could not locate README.md delivery map section")
	}
	same(t, run(t, repo(t, map[string]string{"README.md": txt[s:e]})).Findings)

	// Non-flowchart Mermaid diagrams (erDiagram, classDiagram, xychart-beta, pie, gantt)
	// with > 12 alphanumeric tokens must not be parsed as flowcharts or hard-fail docs-style.
	otherMermaid := strings.Join([]string{
		"```mermaid",
		"erDiagram",
		"  ORDER ||--o{ LINE_ITEM : contains",
		"  ORDER {",
		"    string id",
		"    string customer_id",
		"    string status",
		"    int total_cents",
		"    string currency",
		"    string created_at",
		"    string updated_at",
		"    string shipped_at",
		"  }",
		"```",
		"```mermaid",
		"classDiagram",
		"  class Engine {",
		"    +string id",
		"    +string name",
		"    +int retries",
		"    +Start() error",
		"    +Stop() error",
		"    +Status() string",
		"    +Reload() error",
		"  }",
		"```",
		"```mermaid",
		"pie title Share of Tokens",
		"  \"A1\" : 10", "\"B2\" : 10", "\"C3\" : 10", "\"D4\" : 10", "\"E5\" : 10", "\"F6\" : 10", "\"G7\" : 10",
		"```",
	}, "\n") + "\n"
	same(t, run(t, repo(t, map[string]string{"README.md": otherMermaid})).Findings)

	// Adversarial repeated "-- " tokens without a closer must scan in linear O(N) time.
	advLine := "  A " + strings.Repeat("-- ", 40000) + "B"
	start := time.Now()
	got := countMermaidNodes([]string{"flowchart LR", advLine})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("countMermaidNodes on 40k unclosed '-- ' tokens took %v (quadratic regression)", elapsed)
	}
	if got != 2 {
		t.Errorf("countMermaidNodes adversarial line = %d, want 2", got)
	}
}

func makeTable(rows int) string {
	return "| ColA | ColB |\n|---|---|\n" + strings.Repeat("| a | b |\n", rows)
}

func testStyleTableCap(t *testing.T) {
	same(t, run(t, repo(t, map[string]string{"README.md": "# Doc\n\n" + makeTable(12)})).Findings)
	same(t, run(t, repo(t, map[string]string{"README.md": "# Doc\n\n<details>\n<summary>Big</summary>\n\n" + makeTable(15) + "\n</details>\n"})).Findings)
	same(t, run(t, repo(t, map[string]string{"README.md": "# Doc\n\n" + makeTable(13)})).Findings, "README.md:3 docs-style")
	same(t, run(t, repo(t, map[string]string{"README.md": "# Doc\n\nUse `<details>` around large tables.\n\n" + makeTable(13)})).Findings, "README.md:5 docs-style")
	same(t, run(t, repo(t, map[string]string{"README.md": "# Doc\n\n<!-- <details> -->\n\n" + makeTable(13)})).Findings, "README.md:5 docs-style")
	same(t, run(t, repo(t, map[string]string{"README.md": "# Doc\n\n<!-- comment\n<details>\n-->\n\n" + makeTable(13)})).Findings, "README.md:7 docs-style")
}

func testStyleDestructiveCallout(t *testing.T) {
	for _, tc := range []struct{ name, lang, cmd string }{
		{"rm-rf", "bash", "rm -rf ./build"},
		{"auto-approve", "sh", "terraform apply -auto-approve"},
		{"reset-hard", "zsh", "git reset --hard HEAD~1"},
		{"drop-table", "sql", "DROP TABLE sessions;"},
		{"force-shell", "shell", "git push --force"},
		{"force-plain", "", "git push --force"},
	} {
		t.Run("fail_"+tc.name, func(t *testing.T) {
			doc := "# Ops\n\nRun:\n\n```" + tc.lang + "\n" + tc.cmd + "\n```\n"
			same(t, run(t, repo(t, map[string]string{"README.md": doc})).Findings, "README.md:6 docs-style")
		})
	}
	for i, c := range []string{
		"> [!CAUTION]\n> Destructive.", "> [!WARNING]\n> Destructive.",
		"Caution: deletes files.", "Warning: deletes files.",
		"**Caution** deletes files.", "**Warning** deletes files.", "> [!caution]\n> lowercase.",
	} {
		if fs := run(t, repo(t, map[string]string{"README.md": "# Ops\n\n" + c + "\n\n```bash\nrm -rf ./build\n```\n"})).Findings; len(fs) != 0 {
			t.Errorf("callout[%d] %q unexpectedly failed: %+v", i, c, fs)
		}
	}
	tooFar := "# Ops\n\n> [!CAUTION]\n" + strings.Repeat("filler\n", 8) + "```bash\nrm -rf ./build\n```\n"
	same(t, run(t, repo(t, map[string]string{"README.md": tooFar})).Findings, "README.md:13 docs-style")

	safe := "# Ops\n\nAvoid `rm -rf`, `--force`, `-auto-approve`, `git reset --hard`, `DROP TABLE`.\n\n| Flag | Note |\n|---|---|\n| `--force` | Overwrites |\n\n```go\nconst s = \"rm -rf --force DROP TABLE\"\n```\n"
	same(t, run(t, repo(t, map[string]string{"README.md": safe})).Findings)
}

func testStyleYAMLFrontmatter(t *testing.T) {
	fm := "---\ntitle: Doc\n---\n\n# Doc\n"
	trail := trailLine(t, "GUIDE-fm", "guide", "current", fm) +
		trailLine(t, "DEPLOY-fm", "deploy", "current", fm) +
		trailLine(t, "GUIDE-hr", "guide", "current", "# Guide\n\n---\n\nBody.\n")
	dir := repo(t, map[string]string{
		"README.md": fm, "SDD.md": fm, "TDD.md": fm, "Workflow.md": fm,
		"docs/ok.md": fm, "features/ok.md": fm, ".agents/trail.jsonl": trail,
	})
	same(t, run(t, dir).Findings,
		"DEPLOY-fm:1 docs-style", "GUIDE-fm:1 docs-style", "README.md:1 docs-style",
		"SDD.md:1 docs-style", "TDD.md:1 docs-style", "Workflow.md:1 docs-style",
	)
	same(t, run(t, repo(t, map[string]string{
		"README.md": "# Title\n\n---\n\nSection.\n\n---\n",
		"SDD.md":    "---\n# No closing frontmatter\n",
	})).Findings)
}

func testStyleBannedWords(t *testing.T) {
	bad := "Seamless onboarding.\nA foundational layer.\nZero-Ops ready.\nUse tools (e.g., git).\nOne flag, i.e., quiet.\nFiles, logs, etc.\n"
	same(t, run(t, repo(t, map[string]string{"README.md": bad})).Findings,
		"README.md:1 docs-style", "README.md:2 docs-style", "README.md:3 docs-style",
		"README.md:4 docs-style", "README.md:5 docs-style", "README.md:6 docs-style",
	)
	ok := "Avoid \"seamless\", \"foundational\", \"zero-ops\", \"e.g.\", \"i.e.\", and \"etc.\" in docs.\n" +
		"Backticks `seamless`, `foundational`, `zero-ops`, `e.g.`, `i.e.`, and `etc.` pass.\n" +
		"Substrings sketch, fetch, and path/etc.conf pass.\n" +
		"The branch is locked. The hook is enabled. The old spec is retired.\n" +
		"```text\nseamless foundational zero-ops e.g. i.e. etc.\n```\n"
	same(t, run(t, repo(t, map[string]string{"README.md": ok})).Findings)
}

func testStyleSTESentenceLength(t *testing.T) {
	longProse := "# Guide\n\nThis single prose sentence intentionally exceeds twenty five words by adding many extra descriptive clauses so the deterministic style checker emits an advisory note without failing exit status.\n\n" +
		"1. This single procedural list item intentionally exceeds twenty words by adding extra descriptive clauses so the advisory counter flags it.\n"
	rep := run(t, repo(t, map[string]string{"README.md": longProse}))
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "docs-style-note") || !strings.Contains(notes(rep), "README.md") {
		t.Errorf("expected docs-style-note for README.md, got: %q", notes(rep))
	}
	skipped := "# " + strings.Repeat("word ", 30) + "\n\n> " + strings.Repeat("quote ", 30) + "\n\n<!-- " + strings.Repeat("c ", 30) + "\nmore -->\n\n" +
		"[One](a.md) · [Two](b.md)\n\n| A | " + strings.Repeat("cell ", 30) + " |\n|---|---|\n| x | " + strings.Repeat("cell ", 30) + " |\n\n" +
		"```bash\necho " + strings.Repeat("arg ", 30) + "\n```\n\nRun the build. Check output.\n\n1. Run tests.\n2. Read log.\n"
	repOK := run(t, repo(t, map[string]string{"README.md": skipped}))
	same(t, repOK.Findings)
	if strings.Contains(notes(repOK), "docs-style-note") {
		t.Errorf("unexpected docs-style-note on clean STE doc: %q", notes(repOK))
	}
}

func testStyleGlossaryJSON(t *testing.T) {
	dir := repo(t, map[string]string{
		".agents/glossary.json": `{"whitelist":"allowlist","master":"main"}`,
		"README.md":             "Add the host to the whitelist.\nKeep `whitelist` and \"master\" literal.\n",
		"PRD.md":                "# PRD\n\nLegacy whitelist mention.\n",
	})
	rep := run(t, dir)
	same(t, rep.Findings)
	if n := notes(rep); !strings.Contains(n, "docs-style-note") || !strings.Contains(n, "allowlist") || !strings.Contains(n, "README.md:1") || strings.Contains(n, "docs-style-note: PRD.md") || strings.Contains(n, "README.md:2") {
		t.Errorf("unexpected valid glossary notes: %q", n)
	}
	for name, body := range map[string]string{
		"malformed": `{bad json`,
		"oversize":  `{"a":"` + strings.Repeat("b", 64*1024+16) + `"}`,
	} {
		r := run(t, repo(t, map[string]string{".agents/glossary.json": body, "README.md": "Hello.\n"}))
		same(t, r.Findings)
		if !strings.Contains(notes(r), "docs-style-note") || !strings.Contains(notes(r), "glossary.json") {
			t.Errorf("%s glossary missing skip note: %q", name, notes(r))
		}
	}
	outside := filepath.Join(t.TempDir(), "g.json")
	if err := os.WriteFile(outside, []byte(`{"whitelist":"allowlist"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"outside_symlink": outside, "intra_symlink": "real.json"} {
		d := repo(t, map[string]string{".agents/real.json": `{"whitelist":"allowlist"}`, "README.md": "Use whitelist here.\n"})
		if err := os.Symlink(target, filepath.Join(d, ".agents", "glossary.json")); err != nil {
			t.Fatal(err)
		}
		r := run(t, d)
		same(t, r.Findings)
		if n := notes(r); !strings.Contains(n, "docs-style-note") || !strings.Contains(n, "symlink") || strings.Contains(n, "allowlist") {
			t.Errorf("%s: unexpected notes: %q", name, n)
		}
	}

	// Entry cap (> 512 entries) and per-file note cap (> 50 matches) bound CPU and output.
	var gBuf strings.Builder
	gBuf.WriteString("{")
	for i := 0; i < 600; i++ {
		if i > 0 {
			gBuf.WriteString(",")
		}
		fmt.Fprintf(&gBuf, "\"term%04d\":\"pref%04d\"", i, i)
	}
	gBuf.WriteString("}")
	var docBuf strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&docBuf, "Use term%04d here.\n", i)
	}
	capped := run(t, repo(t, map[string]string{".agents/glossary.json": gBuf.String(), "README.md": docBuf.String()}))
	same(t, capped.Findings)
	cappedNotes := notes(capped)
	if !strings.Contains(cappedNotes, "checking first 512 only") || !strings.Contains(cappedNotes, "glossary notes capped at 50") {
		t.Errorf("expected entry and note cap warnings, got: %q", cappedNotes)
	}
	if len(capped.Notes) > 55 {
		t.Errorf("expected bounded notes, got %d", len(capped.Notes))
	}
}

func testProjectAgentsBudget(t *testing.T) {
	tree15 := "```\n" + strings.Repeat("├── dir/\n", 14) + "└── file\n```\n"
	same(t, run(t, repo(t, map[string]string{"AGENTS.md": tree15 + strings.Repeat("a", 4500-len(tree15))})).Findings)

	repOver := run(t, repo(t, map[string]string{"AGENTS.md": strings.Repeat("a", 4501)}))
	same(t, repOver.Findings, "AGENTS.md:0 budget")
	if len(repOver.Findings) != 1 || repOver.Findings[0].Text != "AGENTS.md is 4501 bytes, over the 4500-byte ceiling" {
		t.Errorf("budget finding = %+v", repOver.Findings)
	}

	tree16 := "# AGENTS\n\n```\n" + strings.Repeat("├── dir/\n", 15) + "└── last\n```\n"
	same(t, run(t, repo(t, map[string]string{"AGENTS.md": tree16})).Findings, "AGENTS.md:4 budget")

	twoTrees := "# AGENTS\n\n" + strings.Repeat("├── a\n", 10) + "\nText.\n\n" + strings.Repeat("└── b\n", 10)
	same(t, run(t, repo(t, map[string]string{"AGENTS.md": twoTrees})).Findings)
}

func TestSpecStructure(t *testing.T) {
	t.Run("valid_spec_passes", func(t *testing.T) {
		body := "# SPEC-ok\nCode: `internal/a.go`.\n## Purpose\nx\n## interfaces\nx\n## Data\nx\n## Failure Modes\n| M | T | O | R |\n|---|---|---|---|\n| e | b | f | r |\n## Verification\n`a_test.go`\n"
		dir := repo(t, map[string]string{"internal/a.go": "package a\n", "internal/a_test.go": "package a\n", ".agents/trail.jsonl": specTrail(t, "SPEC-ok", "2026-10-25T10:00:00Z", body)})
		rep := run(t, dir)
		same(t, rep.Findings)
		if strings.Contains(notes(rep), "Failure modes") {
			t.Errorf("unexpected Failure modes note: %q", notes(rep))
		}
	})

	t.Run("missing_headings_fenced_and_h3", func(t *testing.T) {
		body := "Code: `internal/a.go`.\n## Purpose\nx\n### Interfaces\nx\n```md\n## Data\n## Failure modes\n```\n## Verification\nx\n"
		dir := repo(t, map[string]string{
			"internal/a.go":       "package a\n",
			".agents/trail.jsonl": trailLine(t, "SPEC-bad", "spec", "current", body) + trailLine(t, "SPEC-old", "spec", "retired", "empty"),
		})
		rep := run(t, dir)
		same(t, rep.Findings, "SPEC-bad:0 spec-structure")
		if len(rep.Findings) == 1 {
			for _, miss := range []string{"Interfaces", "Data", "Failure modes"} {
				if !strings.Contains(rep.Findings[0].Text, miss) {
					t.Errorf("text %q missing %q", rep.Findings[0].Text, miss)
				}
			}
		}
		if d, s, r, tr := Counts(rep.Findings); d != 0 || s != 1 || r != 0 || tr != 0 {
			t.Errorf("Counts = (%d,%d,%d,%d), want (0,1,0,0)", d, s, r, tr)
		}
	})

	t.Run("shallow_clone_still_checks", func(t *testing.T) {
		src := repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "SPEC-sh", "spec", "current", "Code: `internal/a.go`.\n")})
		shallow := filepath.Join(t.TempDir(), "shallow")
		git(t, src, "clone", "-q", "--depth", "1", "file://"+src, shallow)
		rep := run(t, shallow)
		same(t, rep.Findings, "SPEC-sh:0 spec-structure")
		if !strings.Contains(notes(rep), "a shallow clone: specs not checked") {
			t.Errorf("expected shallow clone note, got %q", notes(rep))
		}
	})

	t.Run("failure_modes_table_advisory", func(t *testing.T) {
		body3 := "Code: `internal/a.go`.\n## Purpose\nx\n## Interfaces\nx\n## Data\nx\n## Failure modes\n| M | T | R |\n|---|---|---|\n| e | b | r |\n## Verification\nx\n"
		rep := run(t, repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "SPEC-3col", "spec", "current", body3)}))
		same(t, rep.Findings)
		if !strings.Contains(notes(rep), "SPEC-3col") || !strings.Contains(notes(rep), "4-column") {
			t.Errorf("expected 4-column advisory note, got: %q", notes(rep))
		}
		bodyEOF := "Code: `internal/a.go`.\n## Purpose\nx\n## Interfaces\nx\n## Data\nx\n## Verification\nx\n## Failure modes\n| M | T | O | R |\n|---|---|---|---|\n"
		repEOF := run(t, repo(t, map[string]string{"internal/a.go": "package a\n", ".agents/trail.jsonl": trailLine(t, "SPEC-eof", "spec", "current", bodyEOF)}))
		same(t, repEOF.Findings)
		if strings.Contains(notes(repEOF), "SPEC-eof") {
			t.Errorf("unexpected note for SPEC-eof: %q", notes(repEOF))
		}
	})
}
