---
owner: main
date: 2026-10-23
---
# The peak-context metric: sessions over the context target (plan session-sized work, Phase 2)     (date: 2026-10-23)

## Summary
`sh-agent metrics show` and `report` now say how many main sessions passed the context target (300,000 tokens, or `--context-target N`), as `k of n` per CLI, and `show --json` lists every main session's peak context, largest first. The number is what the session-sizing rule is measured by: `/retro` tunes phase sizing from it (D4), and Phase 6 reads this plan's own sessions from it. On this box's data since 2026-09-01: 33 of 54 Claude Code sessions over the target.

## Type
feature (plan `2026-10-23-session-sized-work.md`, Phase 2 of 6; Phases 2b, 3, 4, 5, 6 remain)

## Files changed
- `internal/metrics/show.go` — `ShowOptions.ContextTarget` (0 means `DefaultContextTarget`, 300000); `Efficiency.ContextTarget`, `SessionsOverContextTarget map[string]*int`, `SessionPeaks map[string][]SessionPeak`; `SessionPeak{Session, Date, Peak}`; `add` keeps a per-session peak under the same replay dedupe as the vendor peak, main sessions only; `finishContext` sorts the peaks, counts those over the target and, for a vendor with no peak in the window, sets null and a note; the text table's new row `over context target <N>` as `k of n`
- `internal/metrics/report.go` — summary-table row `sessions over context target (<N>)`, `k of n` or `n/a`
- `internal/metrics/numbers.go` — `B.context_target` and `B.sessions_over_context_target.<vendor>` (absent when null) reach the dream's numbers-only summary; the per-session list does not
- `cmd/sh-agent/main.go` — `--context-target N` on `metrics show` and `report`, in the `own` set so `ingest` and `export` refuse it; a non-positive value is the usage error `--context-target takes a token count above 0`; the two usage lines and report's own usage line
- Tests: `show_test.go` (`TestShowContextTarget`), `report_test.go` (`TestReportContextTargetRow`), `numbers_test.go`, `cmd/sh-agent/metrics_test.go` (the flag on show and report, four refusal rows, the JSON read instead of text matching), `usage_test.go` (one row); `agy_test.go` — the fixture's `t0` comment corrected to the date its epoch value is (2026-10-07 10:00Z)
- `.agents/docs/specs/sh-agent/metrics.md` — § Verbs (both lines, the flag row), § Output (the three fields in the example and the sentence after it), § Report (the summary-table row), B3 (the per-session peak, the replay rule, the subagent rule), two failure-mode rows
- `README.md` — the `metrics show` and `metrics report` rows
- `.agents/docs/testing/2026-10-23-verify-metric-context-target.md` — the log; the plan's four Phase 2 boxes ticked with evidence

## Agents dispatched
none (the plan's Phase 6 dispatches `verifier` and `reviewer` over the whole feature; D22: metrics arithmetic does not make `security` due). The diff was read by the main agent: `git diff --numstat`, 12 files, 287 insertions, 21 deletions before the trail files.

## Verification
`gofmt -l cmd internal` empty, `go vet ./...` exit 0, the § 3g tier (`go test -race -count=1 -timeout 30m ./internal/metrics ./internal/trail ./internal/dream ./internal/search ./cmd/sh-agent`) exit 0, the full suite exit 0, every package ok. Real data from copies: ingest twice (second adds 0), `show --json --since 2026-09-01` 33 of 54 Claude Code sessions over 300000, equal to an independent count over the transcript copy with the same top three; the report's row in headless Chromium light, dark and 400 px (screenshots under `.agents/scratch/metrics-phase2/`); the copy deleted after — `testing/2026-10-23-verify-metric-context-target.md`. No behavioural runs: no context, agent or skill file changed.

## Docs
Spec and README as above. The README map is unchanged: no delivery path moves (project `AGENTS.md` rule 3 does not apply). The deployed binary at any real place changes at that place's next `sh-agent update` after a release (Phase 6's who: user box).

## Decisions
No new ADR: the shape follows the plan's Interfaces (D4, D6, D12, D15, D16) as written. Two choices inside them: `n` in `k of n` is the main sessions that have a peak at all (a session with no model call cannot be over or under), and the human table carries the count as its own row under `peak context` rather than inside that row, which the plan's wording allowed either way.

## Requirements
unchanged. D25's `REQUIREMENTS.md` sentence stays for Phase 2b's session.
