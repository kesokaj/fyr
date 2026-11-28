# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

## Next

- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`)
- Release Preflight — `/fyr-preflight v9.14.0` once Workstream 2 verifies green (followed by read-only `fyr conversations prune --dry-run` preview after install)

## Backlog

- 2026-11-28 — Unexport internal-only helpers across Phase 4 extracted subpackages (`internal/symgraph`, `internal/hooks`, `internal/metrics/{report,ingest,show}`, `internal/readledger`, `internal/vet`, `internal/isolate/{acp,creds}`, `internal/sim`) and move parent white-box tests into owning subpackages; remove test-only `defaultPorts` fallback from `internal/auto/ports.go` (source: `reviewer-phase4-2026-11-28.md` M1/M2).
- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
