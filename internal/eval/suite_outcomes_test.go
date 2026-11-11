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
	editPlan := "printf '# PLAN\\n\\n- [x] A tab-only name greets the world: TestHelloTab\\n- [x] Checkpoint\\n- [ ] Document padding behaviour in README.md\\n- [ ] Checkpoint\\n' | \"$F\" record edit PLAN-2026-11-02-greet-tab --body\n"
	storeRecs := "for f in .agents/scratch/work/2026-11-02-1200/greet-tab-p1/records/*.md; do id=$(basename \"$f\" .md); rec \"$(printf '%s' \"${id%%-*}\" | tr A-Z a-z)\" --id \"$id\" --title-file \"${f%.md}.title\" <\"$f\"; done\n"
	bug := "rec bug --title 'Bug: Hello accepted a tab-only name' <.agents/scratch/work/2026-11-02-1200/greet-tab-p1/bug-tab.md\n"
	merge := "git -c merge.directoryRenames=false merge -q --squash work/greet-tab-p1\n" +
		"git commit -qm 'PLAN-2026-11-02-greet-tab p1 (work/greet-tab-p1)'\n"
	drop := "git branch -qD work/greet-tab-p1\n"
	work := merge + drop + storeRecs + editPlan + bug
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
			{"started fyr work", false, goodFeature + "git branch work/p1\n"},
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
		"work": {
			{"merged p1, undid duplicate-ID p2, ticked p1, stored records and bug", true, work},
			{"wrong commit subject", false, strings.Replace(work, "p1 (work/greet-tab-p1)", "merge the worker", 1)},
			{"branch kept", false, merge + storeRecs + editPlan + bug},
			{"duplicate-ID worker merged anyway", false, work + "git -c merge.directoryRenames=false merge -q --squash work/greet-pad-p2 && git commit -qm 'PLAN-2026-11-02-greet-tab p2 (work/greet-pad-p2)'\n"},
			{"duplicate-ID branch deleted", false, work + "git branch -qD work/greet-pad-p2\n"},
			{"duplicate-ID handoff stored", false, work + "rec handoff --id HANDOFF-2026-11-02-1210-greet-pad-p2 --title-file .agents/scratch/work/2026-11-02-1200/greet-pad-p2/records/HANDOFF-2026-11-02-1210-greet-pad-p2.title <.agents/scratch/work/2026-11-02-1200/greet-pad-p2/records/HANDOFF-2026-11-02-1210-greet-pad-p2.md\n"},
			{"records not stored", false, merge + drop + editPlan + bug},
			{"plan not ticked", false, merge + drop + storeRecs + bug},
			{"phase 2 ticked too", false, strings.ReplaceAll(work, "- [ ]", "- [x]")},
			{"bug stored twice", false, work + bug},
			{"bug not stored", false, merge + drop + storeRecs + editPlan},
			{"left dirty", false, work + "echo x >>README.md\n"},
		},
		"retro": {
			{"retro names the correction", true, body("retro", "Proposal: a project rule, server logs go through log/slog.")},
			{"retro misses it", false, body("retro", "No corrections this session.")},
			{"no record", false, ""},
		},
		"distill": {
			{"retro stored", true, body("retro", "Distill: a skill draft for structured logging.")},
			{"only a review", false, body("review", "Distill: a skill draft.")},
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
	return outs
}
