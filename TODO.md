# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 1 — Compact Status Badge, Slim Hook Banner & Airy Reply Rules (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- [ ] Update `buildTurnContract` and `buildUserMarkdownBanner` in `internal/search/fence.go:161-227` and `buildHealthyHookTurn` in `internal/search/process_artifact.go:15-41` to emit the compact 1-line `status_line` (`🧭 **fyr** · **[N/6 <Resume|Vet|Plan|Build|Verify & Judge|Handoff>]** · Vet \`<score>/100\` · Skills: \`<skills|none>\``) and compact hook banner (`🧭 **fyr vet:** \`<INTENT>\` → \`<GATE>\` (\`<score>/100\`)` + skill names without file paths + record/pivot counts), keeping full record IDs, pivot file paths, and model routing inside `fyr-process.md` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: `PASS` including updated `TestHookFyrProcessArtifact` and `TestBuildTurnEnvelope*`
- [ ] Update `context/AGENTS.md:1-7` to use the compact 1-line `🧭 **fyr**` badge and replace the rigid mandatory-table rule with the short-first airy reply rule (lead with a concise 1–2 line summary, use a numbered list or a compact table depending on what fits best, never force a 1-row table, keep symbol citations at the end of bullets, and put deep details in `.agents/scratch/` shown on request) — verify: unit, `go test -race ./internal/harness/...`, who: AI — expect: `PASS` on `TestRenderedContextFitsAntigravityBlock` and `TestRenderedContextUnderTarget` — docs: `context/AGENTS.md`
- [ ] Checkpoint — expect: compact status badge and airy reply rules tested and passing `go test -race ./internal/search/... ./internal/harness/...` — evidence: command and exit status, date

## Next

- Phase 2 — Metrics Schema v6 (`turn_vets`, `work_waves`) & Second-Brain/Wave Ingest (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- Phase 3 — `fyr report --project | --global` (`--md` & HTML), `ADR-516` & Docs (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- Phase 4 — `fyr-docs`, `/fyr-triage` & Bash Wizard Skills Integration (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)

## Backlog
