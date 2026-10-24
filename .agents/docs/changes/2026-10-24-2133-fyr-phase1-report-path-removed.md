---
owner: main
date: 2026-10-24
---
# fyr Phase 1: the report path leaves; the redactor moves to internal/redact     (date: 2026-10-24)

## Summary
`sh-agent report` is gone, with its code and the `styrman-harness-feedback` path (ADR-062, plan `2026-10-24-fyr-agy-only.md` Phase 1, D4, D22). Nothing in the tree now sends anything off the machine but the dreamer's redacted prompt. The redactor (`Redact`, `RedactJSONL`, the needle lists, the key-block finder) moved to a new stdlib-only package, `internal/redact`, with every redaction test case. `internal/dream` imports it, so the dream no longer depends on the installer's code for it. The README map lost the amber report line, the `sh-agent report` node, the feedback-repository node and the "read reports" gap. Amber is now the loop on this machine: `~/.config/sh-agent/` and `/retro --harness`. The project `AGENTS.md` rule 1 is now "Three ways in, no way back". `delivery.md` § The loop back became § What stays on the machine.

## Type
task (plan phase)

## Files changed
from git diff --numstat (+830 −534 in the working tree; the two deleted files on top)
- `internal/harness/report.go` (−1,102), `internal/harness/report_test.go` (−1,584): deleted
- `internal/redact/redact.go` (+337), `redact_test.go` (+388): new; the needles verbatim; the marker reads `[redacted by sh-agent]`, without `report`
- `cmd/sh-agent/main.go`: the `report` verb, its usage and doc-comment lines, `--feedback-repo`, `runReport`, `report()`, and `shedKeptClone` with its two calls in `update` deleted
- `internal/harness/repo.go`: `ResolveSourceLenient` (report-only) and `gitOutC` (its one caller was report's push) deleted; `state.go`'s `ConfigDir` comment
- `internal/dream/collect.go`: imports `internal/redact`
- Tests: `TestHarnessMap` requires install, update and dream, and refuses `report` both as a verb and on the map, along with the feedback repository; `TestMainExitStatus` gains `report is gone` (exit 1, `unknown command "report"`); `TestReorder`'s value-flag rows use `dream --project` instead of `report --feedback-repo`; deleted: `TestUpdateKeptCloneCleanupOnlyWhenUpdating`, `TestReportWithoutCheckout`, `TestReleaseBinaryUpdateShedsKeptClone`, `TestReportOnReleaseBinaryIgnoresRepoFlags`, `TestThrowawayParentIsPrivate`, `TestSweepInterruptedClonesGlobCharsInHome`, `TestResolveSourceLenient`, the report rows of `TestUsageErrorsExit2` and `TestFromZipRefusedOnOtherCommands`, the record half of `TestEmbeddedInstallStateAndRecord` (now `TestEmbeddedInstallState`)
- Docs: `README.md` (map, verbs table, prose), `AGENTS.md` (rules 1, 3, 5, the needles rule, Layout, Key files), `specs/sh-agent/{delivery,dream,memory-loop,metrics,search}.md`, `deploy/{local,remote}.md` (§ Report back became § What stays on this machine), `testing/guide.md` (§ 3b is now the redactor), `testing/sh-agent-matrix.md` (two report rows removed), `testing/kit-preamble.sh` (comment path), `internal/search/testdata/recall.tsv` (row 21's gold file), `skills/project-memory`, `skills/retro`

## Deviations from the plan's box
- The redaction tests kept every case that exercises `Redact`, `RedactJSONL` or the line redactor. The shape, over-redaction and benign near-miss lists are byte-identical to the old ones (checked with diff). The cases that exercised `redactDocument` were dropped. That function redacted `state.json` for report alone, so it would have been dead code (review-standards). Two state.json cases went from `TestReportRedactStructuralGaps`, one from `TestReportRedactCleanIsByteIdentical`, and the first two of `TestReportRedactDocument`. Its third case, a needle in a JSON key, is now `TestRedactKeyHit`.
- `skills/feedback-log` and `skills/dream` named no report path; they are unchanged. `skills/retro` gained "on this machine" in its usage line.
- Two report words stay on purpose. `internal/metrics/verb.go` keeps `report` in sh-agent's subcommand vocabulary, because it classifies historical transcripts that ran the verb. `TestHarnessMap` names `styrman-harness-feedback` to refuse it. That guard is the only hit of the plan's `git grep -n 'styrman-harness-feedback' -- ':!.agents/docs' ':!skills/google-catalog/catalog'`.
- An old place's kept feedback clone (`~/.config/sh-agent/feedback`) and an interrupted run's `report-<pid>` directory are no longer removed by `update`. `deploy/local.md` § What stays on this machine says to read them and delete them by hand.

## Agents dispatched
none. The plan's Phase 5 holds the Definition of Done over the whole feature: the `verifier` log, `reviewer --code --docs` and `security`. This phase's diff was read by the main agent only.

## Verification
- Tests first: `go test ./internal/redact` without `redact.go` failed (`undefined: redactLines`, `marker`, `redactedValue`), then passed with it. `TestHarnessMap` and `TestMainExitStatus/report_is_gone` failed before the code went (`the binary still has a report verb`, exit 0) and passed after.
- Full suite: `gofmt -l cmd internal` empty, `go vet ./...` ok, `go test -race -count=1 -timeout 30m ./...` exit 0, all 9 packages ok (`internal/redact` the new one; `.agents/scratch/fyr/go-test-phase1.txt`). It ran on `/usr/bin/go` go1.24.4, because the user-level SDK `AGENTS.md` names is gone: `~/.local/bin/go` links to a missing `~/sdk/go1.27.1`. `govulncheck` on that toolchain reports 15 standard-library advisories the code reaches (`os`, `os/exec`, `html/template`, `net/url`, `database/sql`), all fixed in later Go and none from a module this change touches; the fix is the machine's Go, never a pin (ADR-029).
- Scratch home: `go run ./cmd/sh-agent install --home .agents/scratch/fyr/home-p1` exit 0; `status` exit 0, `drift none — 1791 files match the repo`, the `binary` line without a warning; `sh-agent report --dry-run` there prints `unknown command "report"`, exit 1.
- No sending code left: `git grep` over non-test Go for `push`, `clone`, `net/http` and `gh` finds only `release.go`'s `gh release view` and `download`, which are update's reads.

## Docs
Every file above. The ADR is ADR-062, which covered this removal from Phase 0. The map, the code and the docs land in this one commit (project `AGENTS.md` rule 3).
