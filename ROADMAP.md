# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; the plan records = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-25

## Now

- `PLAN-2026-10-24-fyr-agy-only` — open at Phase 6, the operator's

## Next

- Metrics fed at every checkpoint and handoff (`fyr metrics ingest` at the moments the context names), roadmap item 2 (ADR-063): the next plan. `internal/metrics` still reads `.agents/docs` for DoD closure and verifier status, so a migrated project gives it nothing until then.
- `fyr check`: docs freshness and drift (docs naming verbs, flags or paths that do not exist; specs older than the code they describe; requirements without an enforcement site), roadmap item 3.
- `fyr eval`: the Antigravity kit moved into `fyr`, runnable on any project, results stored as records and compared over time, roadmap item 4.
