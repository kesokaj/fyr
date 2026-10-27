# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; the plan records = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-27

## Now

- `PLAN-2026-10-27-audit-choices` — the audit's operator choices (rows 2–4, 6–10 and 13–21 of the audit session's closing table, approved "go all approved", 2026-10-27): opt-in registration, allow-listed child environments, eval script hashes, the redactor, `stress-test`, the map; 11 phases and Done. Phase 1 waits for O1 (the R3 wording) and Phase 5 (SD28) for O2. (`PLAN-2026-10-27-funclen` closed 2026-10-27: no non-test function over 50 lines, `TestFunctionLength` guards it.)

## Next

- **Release v3.1.0** — the 2026-10-27 audit fix, cut by `/preflight v3.1.0` from the operator's own turn (the gate's § 3c, § 3e, § 3h items and a macOS run are owed). Scheduled by the operator's approval of the audit session's row 5, 2026-10-27, and approved again to cut now ("go all approved" on Phase 4's closing table, row 1, 2026-10-27), the corpus measurement (row 3) done before it (`TEST-2026-10-27-1512-testing-antigravity-reader-corpus-measurement-af`); since the audit fix the range also holds funclen and BUG-187; it takes its own session, opened by the operator's `/preflight v3.1.0`.
- **`PLAN-2026-10-27-audit-cleanup`** — the deferred audit lows, G26/G27/G29/G30/G33, WS49 and row 20's re-triage; its Phase 1 re-triages with the operator after the audit-choices plan closes.
