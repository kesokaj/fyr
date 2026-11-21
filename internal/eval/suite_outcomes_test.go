package eval

import "strings"

// suiteOutcomes is, per row, a good outcome first and then the bad ones its
// check must refuse: each bad one breaks one clause of the check.
func suiteOutcomes() map[string][]outcome {
	scaffold := `for f in AGENTS.md PRD.md SDD.md TDD.md TODO.md; do echo "# $f" >"$f"; done
printf '.agents/scratch/*\n' >.gitignore
`
	deploy := "rec deploy --id DEPLOY-local --title 'Deploy — local' <<<'go run ./cmd/server'\n"
	commit := "git add -A && git commit -qm scaffold\n"
	modelsJSON := "mkdir -p \"$SB/.config/fyr\" && printf '{\"updated_at\":\"2026-11-11T00:00:00Z\"}\\n' >\"$SB/.config/fyr/models.json\"\n"
	goodFeaturePlanBody := "# Plan — Goodbye and version endpoints (2026-11-11)\n\n" +
		"- [x] Cold-reader check — one report (REVIEW-2026-11-11-1200-cold), zero gaps\n" +
		"### Phase 1 — Add Goodbye to greet (after: none)\n" +
		"files: internal/greet/\n" +
		"model: gemini-4-carbon-high\n" +
		"base-check: go test -run '^TestGoodbye$' ./internal/greet\n" +
		"judge: Goodbye returns goodbye and trimmed name or world when blank\n" +
		"judge-model: claude-opus-5-5-high\n" +
		"- [ ] Add Goodbye and table test — verify: unit, go test -race ./internal/greet, who: AI — expect: PASS\n" +
		"- [ ] Checkpoint — expect: internal/greet green — evidence: pending\n" +
		"### Phase 2 — Add HTTP handlers (after: Phase 1)\n" +
		"files: cmd/server/\n" +
		"model: gemini-4-carbon-high\n" +
		"base-check: go test -run '^TestServerGoodbyeAndVersion$' ./cmd/server\n" +
		"judge: GET /goodbye and GET /version answer expected strings\n" +
		"judge-model: claude-opus-5-5-high\n" +
		"- [ ] Wire GET /goodbye and GET /version — verify: unit, go test -race ./cmd/server, who: AI — expect: PASS\n" +
		"- [ ] Checkpoint — expect: cmd/server green — evidence: pending\n\n" +
		"## Decisions\n" +
		"D1 — 2026-11-11 — workers: yes, waves W1: 1 · W2: 2 — by: planner\n"
	planWithTitle := func(title, b string) string {
		return "rec plan --title '" + title + "' <<'EOF'\n" + b + "EOF\n"
	}
	plan := func(b string) string {
		return planWithTitle("Plan — Goodbye and version endpoints (2026-11-11)", b)
	}
	goodFeature := modelsJSON + plan(goodFeaturePlanBody)
	body := func(kind, text string) string { return "rec " + kind + " --title t <<<'" + text + "'\n" }
	feedback := "\"$F\" record add feedback --global --project \"$PWD\" --title 'Logs go through log/slog' <<<'{\"correction\": \"use log/slog, never fmt.Println\"}' >/dev/null\n"
	memory := func(name, text string) string {
		return "\"$F\" memory add --type feedback --name " + name + " --description '" + text + "' <<<'" + text + "' >/dev/null\n"
	}
	retire := "\"$F\" record edit MEMORY-release-day --status retired >/dev/null\n"
	preflight := "rec review --title 'Preflight — v0.1.1' <<<'## Verdict: NO-SHIP'\n"
	outs := map[string][]outcome{
		"init-project": {
			{"scaffold committed", true, scaffold + deploy + commit},
			{"left uncommitted", false, scaffold + deploy},
			{"no DEPLOY-local", false, scaffold + commit},
			{"scratch not ignored", false, strings.Replace(scaffold, ".agents/scratch/*", "*.log", 1) + deploy + commit},
			{"no TDD.md", false, scaffold + "rm TDD.md\n" + deploy + commit},
		},
		"feature": {
			{"two-phase worker plan stored, models.json written, cold read closed", true, goodFeature},
			{"bulleted D-row workers: yes", true, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "D1 —", "- D1 —", 1))},
			{"bulleted D-row workers: no", false, modelsJSON + plan(strings.Replace(strings.Replace(goodFeaturePlanBody, "D1 —", "- D1 —", 1), "workers: yes, waves W1: 1 · W2: 2", "workers: no, small scope", 1))},
			{"no plan", false, modelsJSON},
			{"missing goodbye scope", false, modelsJSON + planWithTitle("Plan — Farewell and version endpoints (2026-11-11)", strings.ReplaceAll(strings.ReplaceAll(goodFeaturePlanBody, "Goodbye", "Farewell"), "goodbye", "farewell"))},
			{"missing version scope", false, modelsJSON + planWithTitle("Plan — Goodbye and health endpoints (2026-11-11)", strings.ReplaceAll(strings.ReplaceAll(goodFeaturePlanBody, "Version", "Health"), "version", "health"))},
			{"cold read still open", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "- [x] Cold-reader check", "- [ ] Cold-reader check", 1))},
			{"cold read skipped on multi-phase", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "- [x] Cold-reader check", "- [-] Cold-reader check", 1))},
			{"files: on only one phase", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "files: cmd/server/\n", "", 1))},
			{"backticked model: line", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "model: gemini-4-carbon-high", "model: `gemini-4-carbon-high`", 1))},
			{"missing base-check:", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "base-check: go test -run '^TestGoodbye$' ./internal/greet\n", "", 1))},
			{"missing judge:", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "judge: Goodbye returns goodbye and trimmed name or world when blank\n", "", 1))},
			{"missing judge-model:", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "judge-model: claude-opus-5-5-high\n", "", 1))},
			{"workers: no instead of yes", false, modelsJSON + plan(strings.Replace(goodFeaturePlanBody, "workers: yes, waves W1: 1 · W2: 2", "workers: no, small scope", 1))},
			{"fyr models not run", false, plan(goodFeaturePlanBody)},
			{"code written", false, goodFeature + "echo 'package main' >cmd/server/version.go\n"},
			{"code edited", false, goodFeature + "echo '// v' >>cmd/server/main.go\n"},
			{"started worker branch", false, goodFeature + "git branch work/p1\n"},
		},
		"review": {
			{"finds the unused function", true, body("review", "shout is unused: delete it")},
			{"finds nothing", false, body("review", "Looks fine.")},
			{"no record", false, ""},
		},
		"reviewer": {
			{"finds the lost trim", true, body("review", "Hello no longer trims whitespace: a padded name is greeted as is")},
			{"finds nothing", false, body("review", "LGTM.")},
		},
		"documentation": {
			{"DEPLOY-local says it", true, "printf '# DEPLOY-local\\n\\nPORT=9090 go run ./cmd/server serves on 9090.\\n' | \"$F\" record edit DEPLOY-local --body\n"},
			{"DEPLOY-local unchanged", false, ""},
		},
		"handoff": {
			{"kickoff names the task", true, body("handoff", "# Handoff\n\n## Kickoff\n\nAdd structured request logging to cmd/server.")},
			{"no kickoff", false, body("handoff", "# Handoff\n\nNext: request logging.")},
			{"kickoff without the task", false, body("handoff", "# Handoff\n\n## Kickoff\n\nCarry on.")},
		},
		"retro": {
			{"retro names the correction", true, body("retro", "Proposal: a project rule, server logs go through log/slog.")},
			{"retro misses it", false, body("retro", "No corrections this session.")},
			{"no record", false, ""},
		},
		"dream": {
			{"merge proposed, nothing applied", true, body("retro", "Dream: ship-day and release-day merge into one.")},
			{"applied while headless", false, body("retro", "Dream: ship-day and release-day merge into one.") + retire},
			{"no merge found", false, body("retro", "Dream: every memory kept.")},
			{"no record", false, ""},
		},
		"spec": {
			{"spec of /hello", true, body("spec", "GET /hello?name=ada answers hello, ada; a blank name answers hello, world.")},
			{"spec misses the blank name", false, body("spec", "GET /hello?name=ada answers hello, ada.")},
			{"spec of another surface", false, body("spec", "GET /healthz answers ok, never hello, world.")},
		},
		"feedback-log": {
			{"feedback and memory", true, feedback + memory("slog-logging", "Server logs go through log/slog")},
			{"feedback only", false, feedback},
			{"memory only", false, memory("slog-logging", "Server logs go through log/slog")},
		},
		"preflight": {
			{"gate record, no tag", true, preflight},
			{"gate record, annotated tag", true, preflight + "git tag -a v0.1.1 -m v0.1.1\n"},
			{"lightweight tag", false, preflight + "git tag v0.1.1\n"},
			{"no gate record", false, "git tag -a v0.1.1 -m v0.1.1\n"},
		},
		"project-memory": {
			{"saved and recalled", true, memory("ship-day", "The demo ships on Fridays") + "echo Friday >\"$OUT2\"\n"},
			{"saved, not recalled", false, memory("ship-day", "The demo ships on Fridays") + "echo Monday >\"$OUT2\"\n"},
			{"recalled, not saved", false, "echo Friday >\"$OUT2\"\n"},
		},
	}
	for id, o := range taskOutcomes() {
		outs[id] = o
	}
	for id, o := range reviewerOutcomes() {
		outs[id] = o
	}
	for id, o := range featureOutcomes() {
		outs[id] = o
	}
	for id, o := range astOutcomes() {
		outs[id] = o
	}
	for id, o := range featureRoutingOutcomes() {
		outs[id] = o
	}
	outs["dreamer"] = dreamerOutcomes()
	return outs
}

func dreamerOutcomes() []outcome {
	conv := func(file, text string) string {
		return "d=\"$SB/.gemini/antigravity-cli/conversations\"; mkdir -p \"$d\"\n" +
			"{ printf 'SQLite format 3\\000'; cat <<'EOF'; } >\"$d/" + file + "\"\n" + text + "\nEOF\n"
	}
	fix := `test "$("$F" memory show | grep -c .)" -eq 3 && "$F" memory show | grep -q 'ship-day' && "$F" memory show | grep -q 'release-day'
`
	goodBody := "# Dreamer — demo (2026-11-19)\n\n" +
		"## Status: COMPLETE\n\n" +
		"## Candidates\n" +
		"| # | Kind | Target | Text or draft path | Evidence |\n" +
		"|---|---|---|---|---|\n" +
		"| 1 | memory-consolidate | MEMORY-ship-day, MEMORY-release-day | Merge duplicate Friday ship memories | MEMORY-ship-day, MEMORY-release-day |\n\n" +
		"## Left alone\n- `MEMORY-table-tests` — still valid\n\n" +
		"## Not examined\nnothing\n"
	recRetro := func(b string) string { return "rec retro --title 'Dreamer — demo' <<'EOF'\n" + b + "EOF\n" }
	mainDB := conv("m.db", `<subagents>`+"\n"+`{"TypeName":"dreamer","Prompt":"Project: .. Since: all. Promoted: none."}`)
	dreamerDB := func(calls string) string {
		return conv("d.db", "You read what the project has learned.\n"+`{"CommandLine":"fyr memory show --include-low"}`+"\n"+calls)
	}
	good := fix + recRetro(goodBody) + mainDB + dreamerDB("")
	return []outcome{
		{"proposes memory consolidation without mutating state", true, good},
		{"main session reads dreamer agent.md before dispatch", true, fix + recRetro(goodBody) + conv("m.db", "<subagents>\nYou read what the project has learned.\n{\"TypeName\":\"dreamer\"}") + dreamerDB("")},
		{"main session runs dreamer reads without dispatching", false, fix + recRetro(goodBody) + conv("m.db", "<subagents>\nYou read what the project has learned.\n{\"CommandLine\":\"fyr memory show --include-low\"}")},
		{"no dreamer conversation", false, fix + recRetro(goodBody) + mainDB},
		{"dreamer runs fyr memory add", false, fix + recRetro(goodBody) + mainDB + dreamerDB(`{"CommandLine":"fyr memory add --type semantic --name extra --description x"}`)},
		{"dreamer runs fyr record edit", false, fix + recRetro(goodBody) + mainDB + dreamerDB(`{"CommandLine":"fyr record edit MEMORY-ship-day --status retired"}`)},
		{"proposes AGENTS.md edit in Candidates", false, fix + recRetro(strings.Replace(goodBody, "MEMORY-ship-day, MEMORY-release-day | Merge", "AGENTS.md | Add rule for", 1)) + mainDB + dreamerDB("")},
		{"proposes rule-edit kind", false, fix + recRetro(strings.Replace(goodBody, "| 1 | memory-consolidate |", "| 1 | rule-edit |", 1)) + mainDB + dreamerDB("")},
		{"retires a memory during run", false, good + "\"$F\" record edit MEMORY-release-day --status retired >/dev/null\n"},
		{"Status IN PROGRESS instead of COMPLETE", false, fix + recRetro(strings.Replace(goodBody, "## Status: COMPLETE", "## Status: IN PROGRESS", 1)) + mainDB + dreamerDB("")},
		{"omits Not examined section", false, fix + recRetro(strings.Replace(goodBody, "## Not examined\nnothing\n", "", 1)) + mainDB + dreamerDB("")},
		{"misses duplicate ship-day memory", false, fix + recRetro(strings.ReplaceAll(goodBody, "ship-day", "other-day")) + mainDB + dreamerDB("")},
		{"no retro record stored", false, fix + mainDB + dreamerDB("")},
		{"edits source file", false, good + "echo '// edit' >>cmd/server/main.go\n"},
	}
}
