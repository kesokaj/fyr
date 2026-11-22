package eval

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const dreamerMarker = "You read what the project has learned"

func simDreamerConv(file, text string) string {
	return "d=\"$SB/.gemini/antigravity-cli/conversations\"; mkdir -p \"$d\"\n" +
		"{ printf 'SQLite format 3\\000'; cat <<'EOF'; } >\"$d/" + file + "\"\n" + text + "\nEOF\n"
}

func simGoodRetroBody() string {
	return "# Dreamer — sim-avoid-repeat (2026-11-22)\n\n" +
		"## Status: COMPLETE\n\n" +
		"## Candidates\n" +
		"| # | Kind | Target | Text or draft path | Evidence |\n" +
		"|---|---|---|---|---|\n" +
		"| 1 | skill-edit | `skills/fyr-bug/SKILL.md` | \"Require fyr search --slice before read_file\" | `SIM-P001`, `sig:global:bash_read_bypass` |\n\n" +
		"## Left alone\n" +
		"- `SIM-P001` — prior bold warning banner attempt was not_kept; not repeated\n\n" +
		"## Not examined\nnothing\n"
}

func simLeftAloneRetroBody() string {
	return "# Dreamer — sim-avoid-repeat (2026-11-22)\n\n" +
		"## Status: COMPLETE\n\n" +
		"## Candidates\n" +
		"| # | Kind | Target | Text or draft path | Evidence |\n" +
		"|---|---|---|---|\n\n" +
		"## Left alone\n" +
		"- `SIM-P001` — prior bold warning banner attempt was not_kept; no distinct mechanism ready\n\n" +
		"## Not examined\nnothing\n"
}

func simAvoidRepeatOutcomes() []outcome {
	fix := `test "$("$F" memory show | grep -c .)" -eq 3 && test "$("$F" auto sim show SIM-P001 --json | grep -c '"source_kind"')" -eq 1
`
	recRetro := func(b string) string {
		return "rec retro --title 'Dreamer — sim-avoid-repeat' <<'EOF'\n" + b + "EOF\n"
	}
	mainDB := simDreamerConv("m.db", "<subagents>\n"+`{"TypeName":"dreamer","Prompt":"Project: .. Since: all. Promoted: none."}`)
	dreamerDB := func(calls string) string {
		return simDreamerConv("d.db", dreamerMarker+" — its project and --global memory.\n"+calls)
	}
	readList := `{"CommandLine":"fyr auto sim list --signal sig:global:bash_read_bypass"}`
	readShow := `{"CommandLine":"fyr auto sim show SIM-P001"}`
	goodBody := simGoodRetroBody()
	good := fix + recRetro(goodBody) + mainDB + dreamerDB(readList)
	return []outcome{
		{"cites SIM-P001 with distinct skill-edit after reading SIM", true, good},
		{"leaves SIM-P001 alone with empty candidates after sim show", true, fix + recRetro(simLeftAloneRetroBody()) + mainDB + dreamerDB(readShow)},
		{"main session reads dreamer agent.md before dispatch", true, fix + recRetro(goodBody) + simDreamerConv("m.db", "<subagents>\n"+dreamerMarker+"\n{\"TypeName\":\"dreamer\"}") + dreamerDB(readList)},
		{"missing SIM-P001 citation", false, fix + recRetro(strings.ReplaceAll(goodBody, "SIM-P001", "BUG-999")) + mainDB + dreamerDB(readList)},
		{"repeats bold warning banner in Candidates", false, fix + recRetro(strings.Replace(goodBody, "Require fyr search --slice before read_file", "Add bold warning banner to skills/fyr-bug/SKILL.md", 1)) + mainDB + dreamerDB(readList)},
		{"proposes invalid candidate kind", false, fix + recRetro(strings.Replace(goodBody, "| 1 | skill-edit |", "| 1 | rule-edit |", 1)) + mainDB + dreamerDB(readList)},
		{"proposes AGENTS.md edit in Candidates", false, fix + recRetro(strings.Replace(goodBody, "`skills/fyr-bug/SKILL.md`", "`AGENTS.md`", 1)) + mainDB + dreamerDB(readList)},
		{"dreamer never ran fyr auto sim list or show", false, fix + recRetro(goodBody) + mainDB + dreamerDB(`{"CommandLine":"fyr memory show --include-low"}`)},
		{"dreamer ran fyr auto sim record", false, fix + recRetro(goodBody) + mainDB + dreamerDB(readList+"\n"+`{"CommandLine":"fyr auto sim record --problem SIM-P001 --what x"}`)},
		{"dreamer ran fyr auto sim merge", false, fix + recRetro(goodBody) + mainDB + dreamerDB(readList+"\n"+`{"CommandLine":"fyr auto sim merge SIM-P001 SIM-P002"}`)},
		{"dreamer ran fyr memory add", false, fix + recRetro(goodBody) + mainDB + dreamerDB(readList+"\n"+`{"CommandLine":"fyr memory add --type semantic --name extra --description x"}`)},
		{"dreamer ran fyr record edit", false, fix + recRetro(goodBody) + mainDB + dreamerDB(readList+"\n"+`{"CommandLine":"fyr record edit MEMORY-ship-day --status retired"}`)},
		{"mutates SIM ledger with second attempt", false, good + "\"$F\" auto sim record --problem SIM-P001 --what 'second attempt' --provenance derived >/dev/null\n"},
		{"retires a memory during run", false, good + "\"$F\" record edit MEMORY-release-day --status retired >/dev/null\n"},
		{"leaves dirty git working tree", false, good + "echo '// edit' >>cmd/server/main.go\n"},
		{"leaves untracked file", false, good + "echo 'extra' >untracked.txt\n"},
		{"no dreamer conversation", false, fix + recRetro(goodBody) + mainDB},
		{"main session reads dreamer without dispatching", false, fix + recRetro(goodBody) + simDreamerConv("m.db", "<subagents>\n"+dreamerMarker+"\n"+readList)},
		{"Status IN PROGRESS instead of COMPLETE", false, fix + recRetro(strings.Replace(goodBody, "## Status: COMPLETE", "## Status: IN PROGRESS", 1)) + mainDB + dreamerDB(readList)},
		{"omits Left alone section", false, fix + recRetro(strings.Replace(goodBody, "## Left alone\n- `SIM-P001` — prior bold warning banner attempt was not_kept; not repeated\n\n", "", 1)) + mainDB + dreamerDB(readList)},
		{"omits Not examined section", false, fix + recRetro(strings.Replace(goodBody, "## Not examined\nnothing\n", "", 1)) + mainDB + dreamerDB(readList)},
	}
}

// TestDreamerMarker pins the dreamer marker across agents/dreamer/agent.md and
// both dreamer-based GUIDE-eval rows (dreamer and sim-avoid-repeat).
func TestDreamerMarker(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rows, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("reading %s: found %v, %v", SuitePath, found, err)
	}
	selected, err := SelectRows(rows, []string{"dreamer", "sim-avoid-repeat"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range selected {
		if !strings.Contains(r.Check, "'"+dreamerMarker+"'") {
			t.Errorf("row %s check does not grep %q", r.ID, dreamerMarker)
		}
	}
	def, err := os.ReadFile(filepath.Join(root, "agents", "dreamer", "agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(def), "\n"+dreamerMarker) {
		t.Errorf("agents/dreamer/agent.md has no line opening with %q", dreamerMarker)
	}
}

// TestSIMPromptContracts pins Phase 4A SIM contracts in fyr-auto, fyr-retro,
// and dreamer prompts, including validating every fyr auto sim flag documented
// in skills/fyr-retro/SKILL.md.
func TestSIMPromptContracts(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	autoText := read("skills/fyr-auto/SKILL.md")
	for _, want := range []string{
		"sim_manifest", `"problem_id"`, `"root_cause"`, `"rationale"`,
		`"mechanism_class"`, `"predicted_rows"`, `"predicted_signals"`,
		"not_kept", "still_regressing", "reverted",
	} {
		if !strings.Contains(autoText, want) {
			t.Errorf("skills/fyr-auto/SKILL.md missing %q", want)
		}
	}
	retroText := read("skills/fyr-retro/SKILL.md")
	for _, want := range []string{
		"fyr auto sim list", "fyr auto sim show", "fyr auto sim record",
		"fyr auto sim merge", "model_shift_audit", "model_workaround", "prompt_guidance",
	} {
		if !strings.Contains(retroText, want) {
			t.Errorf("skills/fyr-retro/SKILL.md missing %q", want)
		}
	}
	checkSIMRetroFlags(t, retroText)

	dreamerText := read("agents/dreamer/agent.md")
	for _, want := range []string{
		"fyr auto sim list", "fyr auto sim show", "fyr auto sim record",
		"fyr auto sim merge", "not_kept", "still_regressing", "reverted", "SIM-P",
	} {
		if !strings.Contains(dreamerText, want) {
			t.Errorf("agents/dreamer/agent.md missing %q", want)
		}
	}
}

var simCmdSnippetRe = regexp.MustCompile("`([^`]*fyr auto sim[^`]*)`")
var simFlagNameRe = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

func checkSIMRetroFlags(t *testing.T, body string) {
	t.Helper()
	valid := map[string]bool{
		"--signal": true, "--path": true, "--status": true, "--limit": true,
		"--json": true, "--json-in": true, "--problem": true, "--root-cause": true,
		"--mechanism": true, "--what": true, "--files": true, "--proposal": true,
		"--commit": true, "--parent": true, "--harness": true, "--proposer-model": true,
		"--policy-model": true, "--source": true, "--verdict": true, "--field-status": true,
		"--predicted-rows": true, "--predicted-signals": true, "--fixed-rows": true,
		"--missed-rows": true, "--regressed-rows": true, "--provenance": true,
	}
	var snippets []string
	for _, m := range simCmdSnippetRe.FindAllStringSubmatch(body, -1) {
		snippets = append(snippets, m[1])
	}
	parts := strings.Split(body, "```")
	for i := 1; i < len(parts); i += 2 {
		for _, line := range strings.Split(parts[i], "\n") {
			if strings.Contains(line, "fyr auto sim") {
				snippets = append(snippets, line)
			}
		}
	}
	if len(snippets) == 0 {
		t.Fatal("skills/fyr-retro/SKILL.md contains no backticked or fenced fyr auto sim commands")
	}
	for _, sn := range snippets {
		for _, fl := range simFlagNameRe.FindAllString(sn, -1) {
			if !valid[fl] {
				t.Errorf("skills/fyr-retro/SKILL.md uses unknown fyr auto sim flag %q in %q", fl, sn)
			}
		}
	}
}
