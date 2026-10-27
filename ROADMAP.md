# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; the plan records = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-27

## Now

- `PLAN-2026-10-27-funclen` — functions over 50 lines (54 today): extract-function splits and `TestRefreshCatalogScript`, behind the `TestFunctionLength` guard. Approved by the operator 2026-10-27 ("go all approved", D26); Phase 1 (the guard) done 2026-10-27, 54 rows; Phase 2 (`internal/brain`) done 2026-10-27, 44 rows left; Phase 3 (`internal/metrics`) done 2026-10-27, 34 rows left; Phase 4 (`internal/search`) is next.

## Next

- **Release v3.1.0** — the 2026-10-27 audit fix, cut by `/preflight v3.1.0` from the operator's own turn (the gate's § 3c, § 3e, § 3h items and a macOS run are owed). Scheduled by the operator's approval of the audit session's row 5, 2026-10-27. It runs first, before funclen Phase 2.
- **`PLAN-2026-10-27-audit-choices`** — the audit's operator choices (rows 2–4, 6–10 and 13–21 of the audit session's closing table, approved "go all approved", 2026-10-27): opt-in registration, allow-listed child environments, eval script hashes, the redactor, `stress-test`, the map; 11 phases and Done, after funclen. Phase 1 waits for O1 (the R3 wording) and Phase 5 (SD28) for O2.
- **`PLAN-2026-10-27-audit-cleanup`** — the deferred audit lows, G26/G27/G29/G30/G33, WS49 and row 20's re-triage; its Phase 1 re-triages with the operator after the audit-choices plan closes.
