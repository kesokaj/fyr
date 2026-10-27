# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; the plan records = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-27

## Now

- `PLAN-2026-10-27-funclen` — functions over 50 lines (54 today): extract-function splits and `TestRefreshCatalogScript`, behind the `TestFunctionLength` guard. Approved by the operator 2026-10-27 ("go all approved", D26); Phase 1 (the guard) done 2026-10-27, 54 rows; Phase 2 (`internal/brain`) is next.

## Next

- **Release v3.1.0** — the 2026-10-27 audit fix, cut by `/preflight v3.1.0` from the operator's own turn (the gate's § 3c, § 3e, § 3h items and a macOS run are owed). Scheduled by the operator's approval of the audit session's row 5, 2026-10-27.
- **The audit's operator choices** — rows 2–4 and 6–21 of the 2026-10-27 audit session's closing table, approved as recommended ("go all approved"): a Feature with its own plan and cold read; rows 2, 6 and 8 need an ADR, and row 2 an R3 wording check first. The approved answers are HANDOFF-2026-10-27-1042-handoff-2026-10-27-every-audit-session-row-appro's Key Decisions.
