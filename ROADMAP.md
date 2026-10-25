# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; .agents/docs/plans/ = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-25

## Now

- `.agents/docs/plans/2026-10-24-fyr-agy-only.md` — fyr: Antigravity only, the report path removed, the rename; approved 2026-10-24; it closed plan `2026-10-23-session-sized-work.md` at Phase 2b (D6); open at Phase 6, which is the operator's
- `.agents/docs/plans/2026-10-25-harness-audit-fix.md` — harness audit fix; Phase 0 done 2026-10-25 (answers D1–D9, cold read answered D10–D19); next the retro (Phase 0b), then Phase 1

## Next

- EGM deltas as their own plan (the document standards, the naive-reader lens, `/review --drift`), from plan `2026-10-23-session-sized-work.md` Phases 3–4; after the fyr plan (its D6).
- Eval runner over the metrics store's labels: a fixture format (from `testing/kit-preamble.sh`), a sandbox run on the Antigravity CLI, a scored result ingested with `metrics ingest --source --label --outcome`; after the fyr plan. Consumers: preflight's Antigravity-run rule, every plan's behavioural runs, the dreamer.
