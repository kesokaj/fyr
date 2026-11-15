# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 1 — Context diet (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
Shrink repo `AGENTS.md` (to `<= 4,500` bytes) and global always-on context/rules using scoped `trigger: glob` / `model_decision` rules if honoured by Antigravity CLI, splitting size-test budgets for always-on vs triggered content.

- [ ] Probe in a scratch home whether `~/.gemini/config/rules/` honours `trigger: glob` and `trigger: model_decision` (an `agy -p` run with one marker rule of each kind) — verify: a marker shows up only when its trigger fires. If not, fall back to shrinking text only and record a D-row.
- [ ] Add `"globs"` to `ruleKeys` (`inventory.go:210`) and drop the duplicate inventory from `render.go:53`, keeping Marker and Canary — verify: `go test -race ./internal/harness/...`
- [ ] Split always-on from triggered bytes in `size_test.go` while keeping `TestRenderedContextFitsAntigravityBlock` on the combined block — verify: the tests pass, and a must-fail run (an oversized rule) fails
- [ ] Shrink repo `AGENTS.md` (13,867 B → about 3,800 B, plus `.agents/rules/*.md`) and global context plus rules (19,445 B → about 8,200 B always-on) — verify: `wc -c AGENTS.md` ≤ 4,500; `fyr install --home .agents/scratch/p1` is clean; `fyr eval` passes on the rows mapped to `context/` and `rules/`
- [ ] Checkpoint — evidence: `go test -race ./...`, `fyr check`, eval n of N

## Next

- Phase 2a — Read engine and hook safety (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 2b — Hook events and routing (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 3 — Deterministic gates and subagent pruning (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
