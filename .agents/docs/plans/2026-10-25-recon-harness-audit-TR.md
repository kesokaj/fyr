---
owner: explorer
date: 2026-10-25
---
# Recon — harness-audit TR (2026-10-25)

## Status: COMPLETE

Scope: trail and learning loop at c4885ae. Read: bugs INDEX (177 rows), BUG-177, three newest handoffs, the fyr plan's open boxes and D28-D32, the v2.7.0 gate report, the Phase 5 changes entry, ROADMAP/TODO, the baseline outputs.

## Summary
Append-only held (all 5 commits added lines, 0 deleted). Every sampled graduated mechanism exists. The trail's weak spot is items that live in one place only: three operator questions (Phase 4b report items 2 and 3, the `context/AGENTS.md:52` date rule) sit in a `changes/` "Not done" list, in no plan box, and the retro loop has been silent for 25 days across about 40 new bug rows.

| ID | Sev | Where | Finding | Recommendation |
|---|---|---|---|---|
| TR1 | med | .agents/docs/bugs/INDEX.md:129 (BUG-177), :37 (BUG-039) | The "no CLI trailer in commits" rule has been written three times (context § Git, BUG-039, BUG-177) and BUG-177's prevention is still a sentence ("read `git log` before push"). BUG-039's mechanism was a Claude Code setting that ADR-062 removed. This session's own system reminder again asks for a `Claude-Session:` trailer. Recurrence is likely. *checked*: `git log -60` has 0 trailers, so the amend worked once. | Operator's choice: a test or script the push step runs, or a one-line plan note. Graduate or strike BUG-177 |
| TR2 | med | .agents/docs/changes/2026-10-25-1238-fyr-phase5-close.md:59-63 | "Not done" carries three open operator questions: Phase 4b report items 2 (`uninstall`/`--remove-timer` leave an old-named unit) and 3 (F4 status line), plus the `context/AGENTS.md:52` date rule. None is a box in `plans/2026-10-24-fyr-agy-only.md` (only F4 is described, at :33; its box is absent) and TODO.md is empty. A resume reads handoff then plan, not `changes/`. | fix: put them in the closing handoff's Next Steps (the operator's answer is needed), or as plan boxes |
| TR3 | med | context/AGENTS.md:52 | The "same call that writes them" date rule contradicts `project-memory`'s separate `date +%F` (BUG-175 review finding 1). It was deferred in three consecutive handoffs ("Phase 5's context pass"), and Phase 5 did not do it. The text is unchanged and no operator answer is recorded. A recurring BUG-136/175-class conflict stays a live contradiction. | fix or strike in one edit; operator's choice |
| TR4 | med | .agents/docs/retros/ (newest 2026-09-30) | No retro, dream or distill record in 25 days, while BUG-136..177 (about 40 rows), 2 plans and a rename came in. The context says a session with 2+ corrections gets `/retro`. The pending harness-target proposals queue was not looked at (outside the checkout). | defer; operator runs `/retro` once after v3.0.0 |
| TR5 | low | .agents/docs/bugs/INDEX.md:131 onward (Graduated) | Many graduated rows name paths from before the rename or ADR-062: `agents/verifier.md` (now `agents/verifier/agent.md`), `cmd/sh-agent/main.go`, `testing/sh-agent/guide.md`, "both CLIs". The trail keeps old names by design (D8), but the mechanism is not findable by path. *checked*: the 14 named tests all exist, but `TestUpdateRefusesABadVendorFirst` (BUG-171) is not found by `git grep "func Test..."`; it was probably removed with the vendor flag. | defer; one note under the table saying old names are historical. Re-point BUG-171 to what replaced it or mark it obsolete |
| TR6 | low | .agents/docs/bugs/INDEX.md (rules for BUG-070/080/121/130/144; 071/145/150; 148/155/157/138) | Rules recurring by meaning, counted by group: "read-only/dry-run path honours the real run's guards" 5 rows; "a test proves its own premise or the guard's absence" 4 (071, 104, 145, 150); "script pipe or early-exit under pipefail" 4 (138, 148, 155, 157; 155/157 became a test, 138/148 did not); "restatement of a changed rule" 6 (003, 023, 084, 120, 176, 167; a script exists via `restatement_script_test.go`). The read-only and premise groups are still sentences in the context. | defer; BUG-148 (loop-body `test && cmd`) could join `TestScriptsHaveNoEarlyExitPipe` |
| TR7 | low | .agents/docs/bugs/INDEX.md:BUG-139 | The only open row, age 24 days, "skipped by decision" with a 5-of-6 measured rate. It reads as accepted risk and not as open work. | strike: mark `accepted` or graduate it with the lane sentence |
| TR8 | low | .agents/docs/handoffs/2026-10-25-1111-fyr-phase4b-done.md:80-86 | Its Kickoff says "do Phase 5" though Phase 5 and 5b are now done. The note says a closing handoff is still to be written, so this is not a defect, only a pointer to keep fresh. | none |
| TR-S1 | | .agents/docs/bugs/INDEX.md:131-188 | One 57-row Graduated table plus the main table with the same IDs, with a 2-3 line prevention cell in the main table. A reader checks one row per bug. | simplify: drop the Prevention text from rows once graduated, keep ID and target |

## Measurements
- `git log --diff-filter=M --numstat v2.7.0..HEAD -- changes DECISIONS.md`: 5 commits, additions only (8,8,5+35,5+37,5+20; 0 deletions). Append-only held. *checked*
- `grep -c '^| BUG-' INDEX.md` gives 177 rows. Status `open`: BUG-139 only. The newest, BUG-172, is `obsolete`.
- `.agents/docs/plans/2026-10-24-fyr-agy-only.md:90-95`: 6 unticked boxes, all Phase 6, all operator-gated. Nothing else in the plan is unticked.
- The v2.7.0 gate (`reviews/2026-10-12-preflight-v2.7.0.md:78-93`, SHIP) left 7 "not checked" items. The Mac real home, the § 3c matrix kit and the § 3g real-data ingest are in no plan or handoff I found (`git grep` of "Mac real home" hits only older handoffs). The `index --rebuild` timing is also not carried. Those are release gate notes, not a promise, so low.
- `git grep` of the named graduated tests: all but `TestUpdateRefusesABadVendorFirst` found.
- TODO.md is empty. ROADMAP "Now" names the fyr plan.

## Refuted
- "Append-only was broken": the 5 commits only add lines.
- "A fresh session cannot resume from the newest handoff": the 1111 handoff is complete on Next Steps and Kickoff, within the limits of TR2 and TR8. Not read for gaps in the Phase 5 state.
- No trail text was found telling a later agent to skip a gate or trust a source.
- BUG-177 describes what happened (commit, amend, `c565b27`), not a hope. I did not check the other nine records.

## Not examined
- The ten newest bug records except BUG-177; the earlier two handoffs in full; retros content; `changes/` of v2.6.2..v2.7.0; reviews' `## Outcome` sections; `~/.config/fyr/feedback.jsonl` (outside the checkout, so I cannot say if a correction was logged and never acted on); each graduated row's cited sentence (only the tests were looked up).
