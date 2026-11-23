# Retro Record Template and Apply Procedures

Reference for `skills/fyr-retro/SKILL.md` Steps 4–6.

## Project retro record template (Step 4)

Draft in `.agents/scratch/retro-<stamp>.md` and store via `fyr record add retro --title "Retro — <project name> (<date>)" --body-file .agents/scratch/retro-<stamp>.md` (never overwrite an existing record; follow-up status updates write a new record). Include project-scope proposals in full; include harness-scope proposals as pointer lines only (title, target, one-line why, status, `PROPOSAL-ID`), since harness state stays global and never exports (`PRD.md` R8).

```markdown
# Retro — <project name>   (<date>)

Session: <first 8 chars of the conversation id>

## Summary
<two sentences>

## Findings
<the dreamer's record ID, when one was dispatched>
| # | Signal | Evidence (user words or file:line) | Category | rule_ref |

## Proposals
### Proposal N: <title>
Scope: project | harness — Target: `<path>` — Status: proposed | applied | rejected | queued (`<PROPOSAL-ID>`)
Scope rule: target-in-project | target-in-harness | promoted: <line>
Trigger / Current behavior / Expected behavior / diff / Rationale     ← project scope only; harness scope stops at the Status line

## Metrics
<source 11: both windows' dates, each worsened key with its two values (or "none worsened"), triggered `diagnostics` `signal_id`s (or "no diagnostic signals"); or "no metrics store">

## Feedback records
<the IDs Step 1 read for this project, or "none">

## Memory
<the `fyr-dream` skill's memory line, counts per verdict, its table, what was applied and the full text of every retired memory — or "no memory">

## Not proposed
<what was deliberately left alone, and why>
```

## Detailed apply rules (Step 6)

- **Project scope, approved:** Apply as a DoD task — edit target, run project checks, commit by explicit path, then record status `applied <date> (<commit>)` in a new retro record citing the original (and in the global index) **only after** review and verification pass. When applying a bug's prevention rule, graduate the bug in the same task: `fyr record edit BUG-NNN --status graduated` + `Graduated to:` line in its body.
- **Harness scope from another project:** Always remains `queued`, even if approved — never write the harness checkout from another project (`ADR-070`); apply inside the harness repo via `/fyr-retro --harness`.
- **Harness scope inside the harness checkout (`fyr status` first line + `test -f context/AGENTS.md`):**
  - Apply under harness rules (`fyr install --home .agents/scratch/<name>`, never real `$HOME` outside `DEPLOY-local` § Operator steps; run `AGENTS.md` tests; commit and push), then set `fyr record edit <ID> --global --status applied:<commit>` after review and verification.
  - Record outcome in Self-Improvement Memory:
    `fyr auto sim record --provenance operator --source retro_harness --verdict operator_verified --commit <SHA> --proposal <ID> --files <CSV> --what "<summary>" --problem <SIM-ID>` (or `--root-cause "<cause>"` with optional `--mechanism structural|threshold|model_workaround|prompt_guidance` and `--signal <SIG>` for a new problem; `--status resolved|obsolete` when closing/pruning; `fyr auto sim merge <OLD-ID> <TARGET-ID> --provenance operator` to consolidate duplicates).
  - For any `context/*.md` or `rules/*.md` edit, run `go test ./internal/harness -run 'TestRenderedContext|TestAlwaysOnContextBudget' -count=1` bare before commit (pins: `19,500 B` combined, `13,000 B` rendered context, `9,000 B` always-on). If red, delete/merge text in the same edit or retarget to the owning skill.
- **Memory verdicts, approved:** Execute `fyr-dream` Step 3 edits (retire, rewrite, merge) in the same task and finish `## Memory` (no separate dream record needed); `promote` memories stay while their Findings row follows normal proposal approval.
- **Rejected:** Update via `fyr record edit <ID> --global --status rejected` with the reason in the body (never delete). Under `--harness`, log rejection in SIM via `fyr auto sim record --provenance operator --source retro_harness --verdict not_kept --proposal <ID> --what "<rejected approach and reason>" --problem <SIM-ID>` (or `--root-cause "<cause>"`).
- **Headless / no user turn:** Apply nothing; keep every status `proposed` or `queued` and note it in the record.
