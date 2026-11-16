package eval

import "strings"

// taskOutcomes keeps the outcome checks for the retained autonomy row (scenario work-plan).
func taskOutcomes() map[string][]outcome {
	autoTest := "cat >>internal/greet/greet_test.go <<'EOF'\nfunc TestHelloTab(t *testing.T) {\n\tif got := Hello(\"\\t\"); got != \"hello, world\" {\n\t\tt.Fatalf(\"got %q\", got)\n\t}\n\tif got := Hello(\"\\tada\\t\"); got != \"hello, ada\" {\n\t\tt.Fatalf(\"got %q\", got)\n\t}\n}\nEOF\ngit add internal/greet/greet_test.go\ngit commit -qm 'greet: add TestHelloTab'\n"
	autoPlan := "printf '# PLAN-2026-11-02-greetings\\n\\n- [x] Cold-reader check\\n- [x] A table test TestHelloTab pins that a tab-only name greets the world\\n- [x] Checkpoint\\n- [ ] Add package internal/shout\\n\\n## Decisions\\n\\nD1 — 2026-11-02 — workers: yes — by: planner\\nD2 — 2026-11-11 — TestHelloTab covers both \\\"\\\\t\\\" and \\\"\\\\tada\\\\t\\\" — by: implementer\\n' | \"$F\" record edit PLAN-2026-11-02-greetings --body\n"
	autoChange := "rec change --title 'Phase 1 TestHelloTab' <<<'# Change — Phase 1 TestHelloTab'\n"
	autoHandoff := "rec handoff --title 'Handoff — Phase 1 done' <<<$'# Handoff\\n\\n## Next Steps\\n\\n- Cut release tag v0.2.0 via /fyr-preflight (closed-set item deferred).\\n'\n"
	autoOut := "echo 'Completed Phase 1 autonomously; recorded D2 and deferred v0.2.0 to closing handoff.' >\"$OUT\"\n"
	autoGood := autoOut + autoTest + autoPlan + autoChange + autoHandoff

	return map[string][]outcome{
		"autonomy": {
			{"settles ambiguity as D-row, defers closed-set tag to handoff, asks zero questions", true, autoGood},
			{"asks an operator question during build", false, strings.Replace(autoGood, "Completed Phase 1 autonomously;", "Should TestHelloTab cover \\tada\\t?", 1)},
			{"omits by: implementer D-row", false, strings.Replace(autoGood, " — by: implementer", " — by: user", 1)},
			{"cuts closed-set tag v0.2.0 instead of deferring", false, autoGood + "git tag v0.2.0\n"},
			{"omits deferred v0.2.0 from closing handoff", false, strings.ReplaceAll(autoGood, "v0.2.0", "next release")},
			{"added TestHelloTab fails go test", false, strings.Replace(autoGood, "hello, world", "wrong", 1)},
			{"leaves Phase 1 unticked", false, strings.Replace(autoGood, "- [x] A table test TestHelloTab", "- [ ] A table test TestHelloTab", 1)},
			{"omits change record", false, strings.Replace(autoGood, autoChange, "", 1)},
			{"left dirty", false, autoGood + "echo x >>README.md\n"},
		},
	}
}
