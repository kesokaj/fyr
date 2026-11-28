# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

- `PLAN-2026-11-28-conversations-retention` (`ADR-538`) — Workstream 2: Automatic Antigravity Conversation Retention (`fyr conversations`)
  - [x] Phase 0: Live spike (Path A confirmed, `ConversationService` wire shapes confirmed), `ADR-538`, `SPEC-fyr-conversations`, `PRD.md` R0/R3/R4 amendment
  - [ ] Wave 1 (Phase 1 + Phase 2): `internal/conversations/**` core engine & read-only store adapters (`internal/{metrics,search,dream,auto}/retention_read.go`)
  - [ ] Wave 2 (Phase 3): `cmd/fyr/verb_conversations.go`, `SessionStart` hook Path A/B integration, `fyr status` `convs` line, `verb.go`, `docs/cli-reference.md`
  - [ ] Wave 3 (Phase 4): `SDD.md`/`TDD.md`/`features/`/`areas.md`/`fyr-handoff`, full `fyr-dev verify` + `golangci-lint`, independent `reviewer` + `security`, close plan

## Next

- Release Preflight — `/fyr-preflight v9.14.0` once Workstream 2 verifies green (followed by read-only `fyr conversations prune --dry-run` preview after install)

## Backlog

- 2026-11-28 — Unexport internal-only helpers across Phase 4 extracted subpackages (`internal/symgraph`, `internal/hooks`, `internal/metrics/{report,ingest,show}`, `internal/readledger`, `internal/vet`, `internal/isolate/{acp,creds}`, `internal/sim`) and move parent white-box tests into owning subpackages; remove test-only `defaultPorts` fallback from `internal/auto/ports.go` (source: `reviewer-phase4-2026-11-28.md` M1/M2).
- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
