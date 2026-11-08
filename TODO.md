# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 4 — Agent Client Protocol (`agy acp`) Sandbox Driver, Subagent-Style `/work` Overview & Multi-Turn `VET_FIRST` E2E Proof (`PLAN-2026-11-08-1907-plan-verification-a-b-benchmark-of-second-brain`)
- [ ] Add `internal/sandbox/acp.go`, `internal/sandbox/acp_prompt.go`, and `internal/sandbox/acp_test.go` implementing `ACPClient`, `session/request_permission` grant matching against `b.c.Grants`, dual `out.txt` + `events.jsonl` output, `Config.OnEvent` callbacks, warm multi-turn `sessionId` reuse in `Box.acpSession`, and clean teardown in `Box.closeACP()` inside `After()` and `ExitChecks()`
- [ ] Add `internal/work/progress.go` and `internal/work/progress_test.go` implementing `progressTracker`, subagent-style live progress lines on `Options.Progress`, host-side `<work-root>/<stamp>/status.json`, and `fyr work --status` + `--acp` in `cmd/fyr/verb_work.go` (updating `skills/work/SKILL.md`); wire `ACP bool` into `internal/eval/`, add the 2-turn `acp-vet-multi-turn` row to `.agents/eval/GUIDE-eval.md`, pin it in `internal/eval/suite_test.go`, and add positive/negative outcomes in `internal/eval/suite_outcomes_ast_test.go` and `internal/eval/suite_checks_test.go`
- [ ] Commit Phase 4 code + `GUIDE-eval.md`, check quota via `agy -p "/usage"`, and run live `fyr eval` for `acp-vet-multi-turn` against `--home ~/.config/fyr/dev/fyr-eval`, then complete Definition of Done (`verifier`, `reviewer`, `security`, `change` record, `ADR`, and `/handoff`)
- [ ] Checkpoint

## Next

(none)

## Backlog

- 2026-11-08 — Update stored trail records `GUIDE-onboarding` (lines 24, 50, 65: `search.db` schema `"4"`, AST graph / hooks / vetter surfaces, `fyr search --slice`), `SPEC-fyr-metrics` (lines 289, 353: `"r:"`/`"f:"` `steps.cmd_hash` read prefixes and `RedundantReads`/`RangedReadPct`), and `DEPLOY-remote` (line 4: clarify `no hooks` as deploy/git hooks alongside `ADR-511`) (`REVIEW-2026-11-08-1757-review-worktree-code-docs-2026-11-08`, `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T10)
