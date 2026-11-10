# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 3 — `fyr report --project | --global` (`--md` & HTML), `ADR-516` & Docs (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- [ ] Record `ADR-516` (`fyr report --project | --global` local usage report verb and `metrics.db` schema v6 for second-brain and `fyr work` wave telemetry) and update `AGENTS.md` Rule 5 and `cmd/fyr/harnessmap_test.go:44-49` so `TestHarnessMap` still forbids `fyr report` in `README.md` § How it works while allowing the local-only `fyr report` verb — verify: unit, `go test -race ./cmd/fyr -run TestHarnessMap`, who: AI — expect: `PASS` — docs: `AGENTS.md`
- [ ] Implement `RenderMarkdownReport` in `internal/metrics/report_md.go`, extend `RenderReport` in `internal/metrics/report.go` with the 5 airy sections (Executive Pulse, Second-Brain Leverage, Quality & Learning Loop, Autonomy & Model Routing, and Per-Project / Project Breakdown), and wire `fyr report` / `fyr metrics report` in `cmd/fyr/verb_report.go`, `cmd/fyr/verb_metrics.go`, `cmd/fyr/run_dispatch.go` (including `reorder` bare `--project` normalization and `--global` / `--md` flags), and `cmd/fyr/help_text.go`, then update `SPEC-fyr-metrics`, `README.md`, `SDD.md`, and `TDD.md` — verify: unit, `go test -race ./internal/metrics/... ./cmd/fyr/...`, who: AI — expect: `PASS` — docs: `SPEC-fyr-metrics`, `README.md`, `SDD.md`, `TDD.md`
- [ ] Checkpoint — expect: `go test -race ./internal/metrics/... ./cmd/fyr/...` and `fyr check` pass with 0 findings — evidence: command and exit status, date

## Next

- Phase 4 — `fyr-docs`, `/fyr-triage` & Bash Wizard Skills Integration (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- Phase 5 — End-to-End Verification, Reviews & Release Readiness (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)

## Backlog
