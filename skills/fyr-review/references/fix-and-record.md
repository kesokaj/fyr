# Review Record, Outcome, and `--fix` Loop Rules

Reference for `skills/fyr-review/SKILL.md` Steps 4–5.

## Rounds, rebuttals, and escalation (Step 4)

- **Mechanism stop rule:** A second round on the same mechanism, or a fix causing the next round's defect, means stop and question what the mechanism is for. Two agents contradicting each other on one mechanism means the underlying rule is the defect — fix the rule before re-dispatching.
- **When Round 2 may be skipped (outside `--fix`):** Allowed when every fix has a regression test that fails without it and `fyr verify` (plus `verifier` when e2e is owed) passes after the fixes — **never** when `security` reported medium severity or higher (then Round 2 resumes `security` over its fixes and `## Outcome` states that in one line).
- **Preference & operator choices:** A fix that shortens the code beats one that adds layers of defense. A low finding whose fix adds a flag, refusal, or operator step is put to the operator as a one-line choice (simpler option recommended) and recorded `left: operator — <choice>` ("fix everything" covers defects, not new gates). A finding counts as fixed only when every trigger it names has a test failing without the fix on the measured arrangement (never a fresh synthetic fixture that hides the trigger).
- **Record shapes:**
  - Single lens (`--code --docs` alone or `--judge` alone) without `--fix`: store the agent's single draft directly as the `review` record.
  - Multi-lens or `--fix`: store a merged `review` record drafted in `.agents/scratch/` (`fyr record add review --title "Review — <scope> (merged)" --body-file <draft>`): `FAIL` first, one verdict per lens, findings with severity + confidence (collapsing same `file:line` hits from multiple lenses into one row), links to every draft, and `Not checked`.
  - **`## Outcome`:** When acted on, the main agent appends `## Outcome` (`fyr record get <ID> --out <scratch>`, append, `fyr record edit <ID> --body-file <scratch>`): one line per finding (`fixed` with commit/`file:line`; `rebutted` with checkable `file:line`/ADR/`AGENTS.md`/user-quote; or `left` with owner) in the same commit as the fix (include `fyr search --refs <path>` for `documentation` handoffs). Reviewers never amend stored reports.

## `--fix` round mechanics (Step 5)

Main agent applies every edit (`reviewer` and `security` have no edit tools). Cap: at most two rounds (full first round, fix-only second round) unless the operator approves another at the cap.

1. Apply or rebut each finding (`left: operator — <choice>` for low findings adding flags/refusals/operator steps).
2. **Apply per lens:**
   - `--code`, `--simplify`, `--security`: Edit source directly.
   - `--docs`: Dispatch `documentation` for its lane (`README.md`, `SDD.md`, `TDD.md`, project `AGENTS.md` setup/deploy sections, exported `spec`/`guide`/`deploy` records) with `fyr search --refs <path>` output; edit remaining docs yourself (`skills`, `agents`, `rules`, `context`, other `AGENTS.md` sections).
   - `--prompts`: Apply `## Proposed diff` hunks yourself (one finding per edit); leave Group 2 volatile/conflicting proposals and `flag` items in the record for the operator. In a harness checkout, run `go test ./internal/harness -run 'TestRenderedContext|TestAlwaysOnContextBudget' -count=1` bare before committing any `context/` or `rules/` edit.
   - `--tests`: Write missing tests yourself (`verifier` never edits tests).
3. Run project checks bare and read exit status.
4. Append `## Round N` to the record: one line per finding quoted with `fixed at <file:line>`, `rebutted: <reason>`, `in flight: <who>`, or `left: <owner>`.
5. **Round 2 resume prompt:** Resume the same lens agents via `send_message` using the conversation ID from `invoke_subagent` (or re-dispatch over the same scope into a new draft/record). Append `---`, only that round's fixed lines (`<file:line> — what changed`), and verbatim:
   `"Verify each fix is correct and complete; look for defects the fixes introduced. Re-read each declaration a fix touched and every reference to it, not only the finding's own lines: a fix that moves or re-declares something leaves the original exported with no caller, which reads as intentional API and passes a review that only re-checks the finding."`
   Keep rebuttal reasons in the record, out of the dispatch.

**Stop & cap:** Stop when a round finds nothing beyond `rebutted`/`left` and nothing stays `in flight`, then write `## Outcome`. At the round-2 cap (a gate under `fyr-feature` § Gates that need the user; inside a `fyr-feature` build or `/fyr-swarm`, `ADR-517` settles in-scope residue via dated D-row), ask with recommendation first (accept as `left` with owner, run one more round, or abandon). In print-mode/headless runs with no reply, record the question in the review record and handoff `Next Steps` and stop.
