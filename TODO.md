# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 7 — Release `v9.0.0` (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- [x] `/fyr-preflight v9.0.0` (parallel review plus security, isolated changed-row eval, checksums) — verify: SHIP — evidence: `REVIEW-2026-11-16-1125-preflight-v9-0-0`, tag `v9.0.0` at `c28e88de`, 5 assets published (`SHA256SUMS`, `fyr-linux-amd64`, `fyr-darwin-arm64`, `fyr-linux-arm64`, `fyr-v9.0.0.zip`)
- [ ] After the tag: a 7-day `fyr report --md` comparison stored as a `retro` record — targets: repeat reads under 10%, search:grep at 1.5 or higher, under 20% of steps at 150k+, tokens per turn down 45%

## Next


## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
