# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 7 — Cross-Agent Citation Rules, Docs & Byte-Budget Verification (after: Phase 6)
- [ ] Update `context/AGENTS.md` (`<= 444` B net delta so `TestRenderedContextUnderTarget <= 13,000` B holds), `agents/explorer/agent.md`, `agents/reviewer/agent.md`, `agents/security/agent.md`, `skills/feature/SKILL.md`, `skills/bug/SKILL.md`, and `skills/work/SKILL.md` to require `fyr search --slice`/`--affected`/`--path` first and `path:start-end (Symbol)` citations — verify: unit, `go test -race ./internal/harness/... .`, who: AI — expect: `TestRenderedContextUnderTarget` (`<= 13,000` B), `TestRenderedContextFitsAntigravityBlock` (`<= 19,500` B), and `TestSkillsText` pass — docs: context/AGENTS.md
- [ ] Update `README.md`, `SDD.md`, `TDD.md`, and `TODO.md` to document `--slice`, `--skeleton`, `--path`, `--affected`, `--map`, `--vet`, `--hook`, and `~/.gemini/config/hooks.json` — verify: unit, `go test -race ./...`, who: AI — expect: all repository tests, `funclen_test.go`, `size_test.go`, `place_matrix_test.go`, and `help_test.go` pass with zero warnings — docs: README.md
- [ ] Checkpoint — expect: all 7 phases complete, `fyr install` deploys updated context and hooks, and full test suite passes — evidence: `go test -race ./...`, pending

## Next

(none)

## Backlog
