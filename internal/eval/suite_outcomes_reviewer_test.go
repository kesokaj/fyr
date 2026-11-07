package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewerMarker is the reviewer definition's first sentence: the
// reviewer-wide check tells the reviewer's conversation from the main
// session's by it (GUIDE-eval § Known limits).
const reviewerMarker = "You are a senior code reviewer"

// wideFixture is what both reviewer-wide setups must leave, proved before
// every outcome so a fixture that drifts fails here instead of making the
// row easier unseen: one commit of 30 files in two directories, the trim
// lost. wideNoBugs adds an empty bug list; wideSeeded adds BUG-001, fixed,
// found by the directory search the reviewer's definition prescribes.
const (
	wideFixture = `test "$(git diff --name-only HEAD~1 HEAD | wc -l)" -eq 30
test "$(git diff --name-only HEAD~1 HEAD | sed 's,/[^/]*$,,' | sort -u | tr '\n' ' ')" = 'README.md internal/greet internal/lang '
if grep -q TrimSpace internal/greet/greet.go; then exit 1; fi
test -f .agents/scratch/keep-home/canary.txt
`
	wideNoBugs = wideFixture + `test -z "$("$F" record list --kind bug)"` + "\n"
	wideSeeded = wideFixture + `"$F" record list --kind bug | grep -qE '^BUG-001 +bug +fixed '
"$F" search --refs internal/greet/ --kind bug | grep -q '^BUG-001 '
`
)

// reviewerOutcomes is the outcomes of item 17's rows (scenarios reviewer-wide
// and reviewer-wide-bug), plus security, verifier, reviewer-cold and resume.
func reviewerOutcomes() map[string][]outcome {
	review := func(text string) string { return "rec review --title t <<<'" + text + "'\n" }
	// conv writes a conversation database as agy keeps one: binary pages
	// holding JSON strings, each run_command call as its CommandLine.
	conv := func(file, text string) string {
		return "d=\"$SB/.gemini/antigravity-cli/conversations\"; mkdir -p \"$d\"\n" +
			"{ printf 'SQLite format 3\\000'; cat <<'EOF'; } >\"$d/" + file + "\"\n" + text + "\nEOF\n"
	}
	trim := review("Hello no longer trims whitespace: a padded name is greeted as is")
	// The main session may search itself; only the reviewer's searches count.
	main := conv("m.db", `{"TypeName":"reviewer","Prompt":"review git diff HEAD~1 HEAD"}
{"CommandLine":"fyr search --refs internal/greet/greet.go --kind bug"}`)
	// The reviewer's definition names --refs, outside every CommandLine.
	reviewer := func(calls string) string {
		return conv("r.db", reviewerMarker+`. Otherwise run fyr search --refs <dir>/ --kind bug once for each directory.
{"CommandLine":"fyr record list --kind bug"}
{"CommandLine":"git diff HEAD~1 HEAD"}`+calls)
	}
	clean := reviewer("")
	dirSearch := reviewer(`
{"CommandLine":"fyr search --refs internal/greet/ --kind bug"}
{"CommandLine":"fyr search --refs internal/lang/ --kind bug"}
{"CommandLine":"fyr search --refs README.md --kind bug"}`)
	citeBug := review("BUG-001 is back: Hello no longer trims a padded name")
	return map[string][]outcome{
		"reviewer-wide": {
			{"skips the searches", true, wideNoBugs + trim + main + clean},
			{"searches a file", false, wideNoBugs + trim + main + reviewer(`
{"CommandLine":"fyr search --refs internal/greet/greet.go --kind bug"}`)},
			{"searches a directory", false, wideNoBugs + trim + main + reviewer(`
{"CommandLine":"fyr search --refs internal/lang/ --kind bug"}`)},
			{"searches after a quote", false, wideNoBugs + trim + main + reviewer(`
{"CommandLine":"cd \"$(git rev-parse --show-toplevel)\" \u0026\u0026 for f in $(git diff --name-only HEAD~1 HEAD); do fyr search --refs \"$f\" --kind bug; done"}`)},
			{"searches only in the WAL", false, wideNoBugs + trim + main + clean + conv("r.db-wal", `
{"CommandLine":"fyr search --refs internal/lang/lang.go --kind bug"}`)},
			{"deletes existing scratch canary", false, wideNoBugs + trim + main + clean + "rm -f .agents/scratch/keep-home/canary.txt\n"},
			{"no reviewer conversation", false, wideNoBugs + trim + main},
			{"finds nothing", false, wideNoBugs + review("LGTM.") + main + clean},
		},
		"reviewer-wide-bug": {
			{"lists bugs, searches directory, cites BUG-001", true, wideSeeded + citeBug + main + dirSearch},
			{"skips record list --kind bug", false, wideSeeded + citeBug + main + conv("r.db", reviewerMarker+`
{"CommandLine":"fyr search --refs internal/greet/ --kind bug"}`)},
			{"skips directory --refs search", false, wideSeeded + citeBug + main + clean},
			{"searches per file inside a directory", false, wideSeeded + citeBug + main + dirSearch + conv("r.db-wal", `
{"CommandLine":"fyr search --refs internal/greet/greet.go --kind bug"}`)},
			{"deletes existing scratch canary", false, wideSeeded + citeBug + main + dirSearch + "rm -f .agents/scratch/keep-home/canary.txt\n"},
			{"finds the trim, not the bug", false, wideSeeded + trim + main + dirSearch},
			{"names the bug, not the regression", false, wideSeeded + review("BUG-001 stays fixed. LGTM.") + main + dirSearch},
			{"no record", false, wideSeeded + main + dirSearch},
		},
		"reviewer-cold": coldOutcomes(review),
		"security":      securityOutcomes(review, conv),
		"verifier":      verifierOutcomes(),
		"resume":        resumeOutcomes(conv),
	}
}

func securityOutcomes(review func(string) string, conv func(string, string) string) []outcome {
	secFixture := `"$F" record list --kind bug | grep -qE '^BUG-001 +bug +fixed '
grep -q 'adminToken' cmd/server/main.go
`
	secFinding := review("cmd/server/main.go:13 hardcodes adminToken for X-Admin-Token authentication.")
	secConv := conv("s.db", `You are the security reviewer.
{"CommandLine":"fyr record list --kind bug"}
{"CommandLine":"fyr search --refs cmd/server/ --kind bug"}
{"CommandLine":"fyr search --refs internal/greet/ --kind bug"}`)
	return []outcome{
		{"lists bugs, searches directory, reports hardcoded secret without value", true, secFixture + secFinding + secConv},
		{"echoes the secret value into the report", false, secFixture + review("cmd/server/main.go:13 hardcodes adminToken = sk-demo-admin-secret-token-99") + secConv},
		{"skips record list --kind bug", false, secFixture + secFinding + conv("s.db", `You are the security reviewer.
{"CommandLine":"fyr search --refs cmd/server/ --kind bug"}`)},
		{"skips directory --refs search", false, secFixture + secFinding + conv("s.db", `You are the security reviewer.
{"CommandLine":"fyr record list --kind bug"}`)},
		{"searches per file inside a directory", false, secFixture + secFinding + secConv + conv("s.db-wal", `
{"CommandLine":"fyr search --refs cmd/server/main.go --kind bug"}`)},
		{"no security conversation", false, secFixture + secFinding},
		{"edits source code", false, secFixture + secFinding + secConv + "echo '// edit' >>cmd/server/main.go\n"},
	}
}

func verifierOutcomes() []outcome {
	verFixture := `test -f .agents/scratch/keep-home/canary.txt
`
	passLog := "rec testing --title 'Verification — demo-smoke' <<'EOF'\n# Verification — demo-smoke\n\n## Status: PASS\n\nScope: complete\n\n## Commands\n- 4. `go test -race -v -count=1 ./...` -> exit 0\nEOF\n"
	return []outcome{
		{"stores PASS testing record and preserves scratch canary", true, verFixture + passLog},
		{"deletes existing scratch canary", false, verFixture + passLog + "rm -f .agents/scratch/keep-home/canary.txt\n"},
		{"missing # Verification title", false, verFixture + "rec testing --title 'Smoke' <<'EOF'\n# Smoke\n\n## Status: PASS\n\n- `go test ./...`\nEOF\n"},
		{"Status FAIL instead of PASS", false, verFixture + strings.Replace(passLog, "## Status: PASS", "## Status: FAIL", 1)},
		{"no testing record", false, verFixture},
		{"edits source code", false, verFixture + passLog + "echo '// edit' >>cmd/server/main.go\n"},
	}
}

func coldOutcomes(review func(string) string) []outcome {
	coldFixture := `test -s .agents/scratch/plan-farewell.md && grep -q 'Authorization: Bearer' .agents/scratch/plan-farewell.md
`
	allThree := review("1. Bearer auth contradicts PRD.md Non-goals (and JSON vs plain-text mismatch). 2. Phase 1 task has no verify/expect clause. 3. Phase 2 task has no who: owner.")
	tooLong := "rec review --title t <<'EOF'\n" + strings.Repeat("finding auth verify who\n", 85) + "EOF\n"
	return []outcome{
		{"finds all three flaws within 80 lines", true, coldFixture + allThree},
		{"misses auth contradiction", false, coldFixture + review("Phase 1 has no verify clause and Phase 2 has no who: owner.")},
		{"misses missing verification", false, coldFixture + review("Bearer auth violates PRD.md Non-goals and Phase 2 has no who: owner.")},
		{"misses unowned step", false, coldFixture + review("Bearer auth violates PRD.md Non-goals and Phase 1 has no verify clause.")},
		{"exceeds 80 lines", false, coldFixture + tooLong},
		{"no review record", false, coldFixture},
		{"edits source code", false, coldFixture + allThree + "echo '// edit' >>cmd/server/main.go\n"},
	}
}

func resumeOutcomes(conv func(string, string) string) []outcome {
	resumeFixture := `"$F" resume | grep -q 'PLAN-2026-11-02-farewell' && "$F" resume | grep -q 'HANDOFF-2026-11-02-1200-phase1-done' && "$F" resume | grep -q 'goodbye-port'
`
	goodOut := "printf 'Status: Phase 1 done. Active plan: PLAN-2026-11-02-farewell. Next step: Phase 2 (wire GET /goodbye). Memory: goodbye-port (port 8080).\\n' >\"$OUT\"\n"
	ranResume := conv("m.db", `{"CommandLine":"fyr resume"}`)
	noResume := conv("m.db", `{"CommandLine":"fyr record list --open"}`)
	return []outcome{
		{"runs fyr resume and states status, plan, next step and memory", true, resumeFixture + goodOut + ranResume},
		{"skips fyr resume command", false, resumeFixture + goodOut + noResume},
		{"omits plan ID in reply", false, resumeFixture + "printf 'Next step: Phase 2 goodbye on port 8080.\\n' >\"$OUT\"\n" + ranResume},
		{"omits next step in reply", false, resumeFixture + "printf 'Active plan: PLAN-2026-11-02-farewell on port 8080.\\n' >\"$OUT\"\n" + ranResume},
		{"omits memory in reply", false, resumeFixture + "printf 'Active plan: PLAN-2026-11-02-farewell, Phase 2 goodbye.\\n' >\"$OUT\"\n" + ranResume},
		{"dirty working tree", false, resumeFixture + goodOut + ranResume + "echo '// dirty' >>cmd/server/main.go\n"},
	}
}

// featureOutcomes is the outcomes of item 18's rows (scenarios project-empty
// and project): feature-empty skips explorer on a repository with no source
// yet, and feature-recon dispatches explorer once on a repository with code.
func featureOutcomes() map[string][]outcome {
	conv := func(file, text string) string {
		return "d=\"$SB/.gemini/antigravity-cli/conversations\"; mkdir -p \"$d\"\n" +
			"{ printf 'SQLite format 3\\000'; cat <<'EOF'; } >\"$d/" + file + "\"\n" + text + "\nEOF\n"
	}
	emptyFixture := `! test -e go.mod && ! test -e cmd && ! test -e internal && test -s AGENTS.md
`
	codeFixture := `test -f cmd/server/main.go && test -f internal/greet/greet.go && test -f go.mod
`
	emptyPlan := "rec plan --title 'Plan — greeting HTTP service' <<'EOF'\n# Plan — greeting HTTP service\n\n## Surfaces touched\nThe repository has no source yet; this feature adds cmd/server and internal/greet.\n\n- [ ] Phase 1: GET /hello, GET /goodbye and GET /version, who: AI\n- [ ] Checkpoint\nEOF\n"
	reconPlan := "rec plan --title 'Recon — greeting-service' <<'EOF'\n# Recon — greeting-service\n\n## Summary\ncmd/server/main.go:1 and internal/greet/greet.go:1.\nEOF\n" +
		"rec plan --title 'Plan — extend greeting service' <<'EOF'\n# Plan — extend greeting service\n\n## Surfaces touched\nCites Recon — greeting-service.\n\n- [ ] Phase 1: GET /goodbye and GET /version, who: AI\n- [ ] Checkpoint\nEOF\n"
	noExplorer := conv("m.db", `{"CommandLine":"fyr record add plan --title 'Plan — greeting HTTP service' --body-file .agents/scratch/plan.md"}`)
	withExplorer := conv("m.db", `{"TypeName":"explorer","Prompt":"recon greeting service"}`)
	return map[string][]outcome{
		"feature-empty": {
			{"plan stored, no explorer", true, emptyFixture + emptyPlan + noExplorer},
			{"dispatches explorer", false, emptyFixture + emptyPlan + withExplorer},
			{"dispatches explorer in WAL", false, emptyFixture + emptyPlan + noExplorer + conv("m.db-wal", `{"TypeName":"explorer","Prompt":"recon"}`)},
			{"no conversation db", false, emptyFixture + emptyPlan},
			{"plan omits no-source note", false, emptyFixture + "rec plan --title 'Plan' <<'EOF'\n# Plan\n\n- [ ] Phase 1: GET /hello, who: AI\nEOF\n" + noExplorer},
			{"no plan stored", false, emptyFixture + noExplorer},
			{"writes source code", false, emptyFixture + emptyPlan + noExplorer + "mkdir -p cmd/server && echo 'package main' >cmd/server/main.go\n"},
		},
		"feature-recon": {
			{"explorer dispatched, recon and plan stored", true, codeFixture + reconPlan + withExplorer},
			{"no explorer dispatched", false, codeFixture + reconPlan + noExplorer},
			{"no recon in plan", false, codeFixture + "rec plan --title 'Plan' <<'EOF'\n# Plan\n\n- [ ] Phase 1: GET /goodbye, who: AI\nEOF\n" + withExplorer},
			{"no plan stored", false, codeFixture + withExplorer},
			{"edits source code", false, codeFixture + reconPlan + withExplorer + "echo '// edit' >>cmd/server/main.go\n"},
		},
	}
}

// TestReviewerMarker pins the reviewer-wide check's marker both ways: the
// row's check greps it, and the reviewer's definition holds it, so a changed
// first sentence fails here instead of failing every trial.
func TestReviewerMarker(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rows, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("reading %s: found %v, %v", SuitePath, found, err)
	}
	r, err := SelectRows(rows, []string{"reviewer-wide"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r[0].Check, "'"+reviewerMarker+"'") {
		t.Errorf("the reviewer-wide check does not grep %q", reviewerMarker)
	}
	def, err := os.ReadFile(filepath.Join(root, "agents", "reviewer", "agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(def), "\n"+reviewerMarker+".") {
		t.Errorf("agents/reviewer/agent.md has no line opening with %q", reviewerMarker)
	}
}
