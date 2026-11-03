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
	plan := func(cold string) string {
		return "rec plan --title 'GET /version' <<'EOF'\n# PLAN: GET /version\n\n" + cold +
			"\n- [ ] Phase 1: the version handler, who: AI\n- [ ] Checkpoint\nEOF\n"
	}
	skipped := "- [-] Cold-reader check — skipped: one phase, under 5 files"
	body := func(kind, text string) string { return "rec " + kind + " --title t <<<'" + text + "'\n" }
	editPlan := "printf '# PLAN\\n\\n- [x] A tab-only name greets the world: TestHelloTab\\n- [x] Checkpoint\\n' | \"$F\" record edit PLAN-2026-11-02-greet-tab --body\n"
	bug := "rec bug --title 'Bug: Hello accepted a tab-only name' <.agents/scratch/work/2026-11-02-1200/greet-tab-p1/bug-tab.md\n"
	merge := "git -c merge.directoryRenames=false merge -q --squash work/greet-tab-p1\n" +
		"git commit -qm 'PLAN-2026-11-02-greet-tab p1 (work/greet-tab-p1)'\n"
	drop := "git branch -qD work/greet-tab-p1\n"
	work := merge + drop + editPlan + bug
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
			{"plan stored, cold read closed", true, plan(skipped)},
			{"no plan", false, ""},
			{"cold read still open", false, plan("- [ ] Cold-reader check")},
			{"code written", false, plan(skipped) + "echo 'package main' >cmd/server/version.go\n"},
			{"code edited", false, plan(skipped) + "echo '// v' >>cmd/server/main.go\n"},
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
			{"merged, ticked, bug stored", true, work},
			{"wrong commit subject", false, strings.Replace(work, "p1 (work/greet-tab-p1)", "merge the worker", 1)},
			{"branch kept", false, merge + editPlan + bug},
			{"plan not ticked", false, merge + drop + bug},
			{"bug stored twice", false, work + bug},
			{"bug not stored", false, merge + drop + editPlan},
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
	return outs
}
