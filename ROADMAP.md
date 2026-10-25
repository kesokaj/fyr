# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; .agents/docs/plans/ = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-25

## Now

- `.agents/docs/plans/2026-10-25-sqlite-trail.md` — the brain store (ADR-063); requirements approved 2026-10-25; next Phase 1
- `.agents/docs/plans/2026-10-25-harness-audit-fix.md` — audit fix; Phases 1–4 and 2c merged, Phase 5 partly; its remaining boxes and Phase 6 struck in favour of the brain plan (its D22)
- `.agents/docs/plans/2026-10-24-fyr-agy-only.md` — open at Phase 6, the operator's

## Next

- Metrics fed at every checkpoint and handoff (`fyr metrics ingest` at the moments the context names), roadmap item 2 (ADR-063).
- `fyr check`: docs freshness and drift (docs naming verbs, flags or paths that do not exist; specs older than the code they describe; requirements without an enforcement site), roadmap item 3.
- `fyr eval`: the Antigravity kit moved into `fyr`, runnable on any project, results stored as records and compared over time, roadmap item 4.
