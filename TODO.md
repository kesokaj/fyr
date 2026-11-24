# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

- `PLAN-2026-11-24-1704-plan-deterministic-dual-track-docs-harness-contr` — Phase 4 (final review remediation, eval & close):
  - [x] Phase 1 — Pure-Go deterministic `docs-ste100-80` linter, `AGENTS.md` budget guard & `SPEC-*` structure check (`1625b732`)
  - [x] Phase 2 — Explicit audience split across skills, project templates, documentation agent & global context (`2123ff95`)
  - [x] Phase 3 — Pure-Go `fyr search --contracts` projector & bounded hook/slice spec enrichment (`f09f624d`, pilot hardening `8bfd5763`)
  - [x] Phase 4a–4b — `ADR-535`, `SPEC-fyr-docs-harness`, living docs & `>= 80%` STE pilot (`50b2869d`), whole-diff `reviewer` & `security` audits stored
  - [ ] Phase 4 close — Remediate `0H/5M/2L` `reviewer` (`REVIEW-2026-11-24-1916-review-dual-track-docs-harness-v9-12-0-50b2869d`) + `0H/2M/2L` `security` (`REVIEW-2026-11-24-1916-security-review-dual-track-docs-harness-v9-12-0`) findings, run scratch-`--home` `fyr eval` on doc/routing rows, run full `fyr verify`, push `main`, and close via `fyr close`

## Next

None.

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
