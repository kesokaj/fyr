# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Wave 1 (`Phase 1a`–`1d`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` — Security, `-cover`, hook, redactor, watch, dream & auto hardening:

- [x] Phase 1a (`internal/isolate/`, `internal/models/`): `exportBoxRecords`/`seedRecords` verified lead binary (`IN58`), ACP subshell quote stack (`IN59`), `GOCOVERDIR` PID-1 forward/unset (`IN60`) & `Setsid` (`TR24`), `probeWorkDir` `--home`/`CheckHomeDir` (`IN61`), `SaveCatalog` `CreateTemp` (`IN62`), ACP/models fixes (`IN67`, `IN69`, `IN72`, `IN93`–`IN103`)
- [x] Phase 1b (`after: Phase 1a`; `internal/harness/`, `internal/eval/`): reject column-0 YAML lists & duplicate keys (`IN79`, `IN81`), `installRelease` symlink/dir guards (`IN80`, `IN82`–`IN85`), `invokedNotes` main-transcript filter (`IN92`) & `go test -race -cover ./internal/eval`
- [ ] Phase 1c (`internal/search/`, `internal/redact/`, `internal/watch/`): `redact`/`watch`/hook/watchdog/vet/workflow/`BUG-558` landed (`21753ce0`, `5b9e69d2`); remaining: `refs_oid` index (`SD60`), clamp `read_ledger` (`SD62`), `synchronous(NORMAL)` + `BEGIN IMMEDIATE`, `co_activity`/`fence`/`global`/`dream`/`telemetry` (`SD72`–`SD86`)
- [ ] Phase 1d (`internal/auto/`, `internal/dream/`, `internal/trail/`): `internal/trail/` `DocsRoot` & `SameDir` landed (`19eccbe5`); remaining: `dream` `223`-proposal spam fix (`SD65`, `SD100`–`SD105`), `auto` cutoff/holdouts/redaction/fencing (`SD63`–`SD64`, `SD92`–`SD99`), Checkpoint

## Next

- Wave 2 (`Phase 2a`–`2c`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` (`internal/brain/`, `internal/check/`, `internal/gate/`, `internal/metrics/`, `cmd/fyr/`, `workflows/`, `agents/`, `context/`, `rules/`, `skills/`, `.agents/eval/`)
- Wave 3–4 (`Phase 3a`–`4a`) of `PLAN-2026-11-19-0852-plan-harness-audit-fix-2026-11-19` (`25` dead-code deletions, `>350`/`>600` LOC splits, doc/record sync, and `v9.3.0` behavioral upgrades)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
