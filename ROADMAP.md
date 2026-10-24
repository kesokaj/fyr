# Roadmap — styrman-harness

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; .agents/docs/plans/ = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-24

## Now

- `.agents/docs/plans/2026-10-24-fyr-agy-only.md` — fyr: Antigravity only, the report path removed, the rename; approved 2026-10-24; its Phase 0 closes the plan below at Phase 2b (D6)
- `.agents/docs/plans/2026-10-23-session-sized-work.md` — session-sized work: the always-on rule, the handoff kickoff, phase sizing, the peak-context metric, then the EGM deltas as its first phases

## Next

- Eval runner over the metrics store's labels: a fixture format (from `testing/kit-preamble.sh`), a sandbox run per CLI, a scored result ingested with `metrics ingest --source --label --outcome`; after the session-sized-work plan. Consumers: preflight's Antigravity-run rule, every plan's behavioural runs, the dreamer.
