# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 0b — Prove the graph works: A/B baseline (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
Prove whether the code graph already cuts reads and tokens via 5+5 `ab-pipeline-on` / `ab-pipeline-off` eval runs, ingesting metrics and recording the median/spread baseline and verdict rule in a `retro` record.

- [ ] Run both rows 5 times each (`fyr eval --home ~/.config/fyr/dev/fyr-eval ab-pipeline-on ab-pipeline-off`, repeated). Then ingest each run's sandbox conversations into a scratch metrics store (`fyr metrics ingest --home .agents/scratch/ab`) — verify: 10 runs ingested, with session IDs listed
- [ ] Compute per arm the median and spread of: `read_file` calls, repeat reads, in+out tokens, cache-read tokens, wall seconds, and pass rate. Store the table as a `retro` record titled "Graph A/B baseline" — verify: the record exists. If an arm has under 4 of 5 passes, the token comparison is invalid; fix the row first.
- [ ] Verdict rule, written into the record: the graph "works" only if the on arm has at least 20% fewer reads and at least 10% fewer total tokens at an equal or better pass rate. If it does not, Phase 2a's first task becomes "find out why the graph does not reduce reads", before any other read-engine work — verify: the verdict line is present
- [ ] Checkpoint — evidence: the `retro` record ID and the A/B table

## Next

- Phase 1 — Context diet (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 2a — Read engine and hook safety (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
- Phase 2b — Hook events and routing (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
