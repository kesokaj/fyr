# Roadmap — fyr

<!-- Division of labor: ROADMAP.md = the active plan while one is in progress; TODO.md = items the active plan will consume; the plan records = how. "Now" names the active plan only while one is open and is empty otherwise; "Next" holds at most three scheduled milestones; ideas live in the newest handoff's Next Steps, never here. -->

Last reviewed: 2026-10-27

## Now

- `PLAN-2026-10-27-audit-choices` — the audit's operator choices (rows 2–4, 6–10 and 13–21 of the audit session's closing table, approved "go all approved", 2026-10-27): opt-in registration, allow-listed child environments, eval script hashes, the redactor, `stress-test`, the map; 11 phases and Done. Phase 1 waits for O1 (the R3 wording) and Phase 5 (SD28) for O2. (`PLAN-2026-10-27-funclen` closed 2026-10-27: no non-test function over 50 lines, `TestFunctionLength` guards it.)

## Next

- **Release v3.1.0** — the 2026-10-27 audit fix, cut by `/preflight v3.1.0` from the operator's own turn. The gate ran on 2026-10-27: NO-SHIP (`REVIEW-2026-10-27-1809-preflight-v3-1-0-2026-10-27-no-ship`). § 3c and § 3h now pass, and BUG-188 and BUG-189 were fixed in the gate. Resumed the same day without the operator at the machine ("Finish the project without me atm. Run in sandboxes if you need to verify", 2026-10-27): § 3d's bound re-baselined to 400 ms on that delegation, the real-home smoke run in a sandbox as GUIDE-eval's `smoke` row, BUG-191 fixed in the gate. Before the tag the operator still owes, in their own turn: the bridge confirmation, and their word on AGENTS.md § Release's real-home smoke (the sandbox row does not stand for it). The macOS `fyr dream --all` is lifted to their own update of each place. Scheduled by the operator's approval of the audit session's row 5, 2026-10-27, and approved again to cut now ("go all approved" on Phase 4's closing table, row 1, 2026-10-27), the corpus measurement (row 3) done before it (`TEST-2026-10-27-1512-testing-antigravity-reader-corpus-measurement-af`); since the audit fix the range also holds funclen and BUG-187; it takes its own session, opened by the operator's `/preflight v3.1.0`.
- **`PLAN-2026-10-27-audit-cleanup`** — the deferred audit lows, G26/G27/G29/G30/G33, WS49 and row 20's re-triage; its Phase 1 re-triages with the operator after the audit-choices plan closes.
