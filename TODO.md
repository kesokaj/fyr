# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2 — Live `fyr eval` Behavioural Proofs for AST Citations, Read Ledger & Prompt Vetter (`PLAN-2026-11-08-1907-plan-verification-a-b-benchmark-of-second-brain`)
- [ ] Check quota via `agy -p "/usage"` and run live `fyr eval` trials for the 5 behavioural proof rows (`ast-slice-citations`, `read-ledger-dedup`, `vet-first-gate`, `vet-question-and-bypass`, `fence-injection-guard`) against `--home ~/.config/fyr/dev/fyr-eval`
- [ ] Checkpoint

## Next

- Phase 3 — Paired A/B Token, Latency & Zero-Unnecessary-Verification Benchmark & DoD (`PLAN-2026-11-08-1907-plan-verification-a-b-benchmark-of-second-brain`)

## Backlog

- 2026-11-08 — Update stored trail records `GUIDE-onboarding` (lines 24, 50, 65: `search.db` schema `"4"`, AST graph / hooks / vetter surfaces, `fyr search --slice`), `SPEC-fyr-metrics` (lines 289, 353: `"r:"`/`"f:"` `steps.cmd_hash` read prefixes and `RedundantReads`/`RangedReadPct`), and `DEPLOY-remote` (line 4: clarify `no hooks` as deploy/git hooks alongside `ADR-511`) (`REVIEW-2026-11-08-1757-review-worktree-code-docs-2026-11-08`, `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T10)
