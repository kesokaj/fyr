# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 1 (by-hand bootstrap before `/fyr-work` W1 resume, `D20`, `BUG-538`) — `PLAN-2026-11-11-0640-plan-fyr-autonomy-and-full-feature-use`: fix host `scanCreds` self-hosting false positives (`search.db`, `agy` transcripts, `> 64 MiB` `go-build` `-d` ELF cache, `.rodata` needles) and install to `~/.local/bin/fyr` on `dave`.

- [ ] `TestScanCredsIgnoresFyrOwnPatterns` (red first in `internal/sandbox/creds_test.go`, covering `BUG-538`)
- [ ] Fix all four `BUG-538` false-positive vectors without path blind spots (`internal/sandbox/creds.go`, `internal/redact/redact.go`) and update `BUG-538`
- [ ] Checkpoint: tests green, committed & pushed on `main`, and installed on `dave` (`go run ./cmd/fyr install --home ~`) before resuming `/fyr-work`

## Next

- Wave 1 resume (Phases 2, 5, 6, 9) — Autonomy model, Trigger-coverage KPIs, read-ledger fix, and live model catalog (`fyr models`)
- Wave 2 (Phases 3, 7) — Workers by default in plan template (`→ T2, T13`) and worker latency budget (`→ T10, T17, T18`)
- Wave 3 (Phase 4) + Phase 8 — Always-on feature-routing rule + advisory vet routing, then closing range review, `fyr eval`, registration/timer, and `fyr report --md`

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
