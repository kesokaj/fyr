---
name: fyr-bug
description: "Bug-fix workflow (/fyr-bug): problem note, goldfish diagnosis, failing test first, smallest fix, review, BUG record. Not features."
argument-hint: "[symptom, repro, or #issue]"
---

# Bug workflow

Diagnose without anchoring, capture the bug as a failing test, fix the smallest thing, record it so it is not repeated.

**Trail:** `~/.gemini/config/skills/fyr-init-project/references/trail.md`; dates from `date +%F`; draft record bodies in `.agents/scratch/`.

## Gates that need the user

Two points always wait for the user in standalone invocations: a missing repro when the bug is not obvious from one file (Step 1), and a review loop that hits its two-round cap (the `fyr-review` skill Step 5, reached from Step 5 here). What waives a gate, and what a run does at one when no reply can arrive, is the gate rule in the `fyr-feature` skill's § Gates that need the user. Use `ask_question` (`(Recommended)` first): for a missing repro, give one, let me infer one, or stop; at the review cap, accept the remaining findings as `left` with an owner, run one more round, or abandon the fix.

## Step 0: Triviality gate

Typo in copy or a comment, dead-code removal, version bump, formatter-only diff, single-line config value: skip the ceremony, fix it, run `fyr verify` (or the project tests), Definition of Done steps 2 and 3. Everything else, including one-line code fixes, runs the full path — small diffs hide bugs well.

**What counts as a failure** (every one gets a BUG number): a test failing for a reason other than the change under test, a fix that took more than one attempt, a regression, a failed build or deploy, an abandoned approach, a review finding on committed work that was a real defect, or the user correcting a mistake. The boundary is one fact: did it reach a commit, a deploy or the user? A defect found and fixed before its change's commit — by that change's review, tests or verifier, in however many attempts — is not a failure; the review's `## Outcome` records it. A record the main agent writes carries `owner: main`.

## Step 1: Problem note (in chat)

```
PROBLEM
- Symptom: <what is observed, verbatim error text or failing test name>
- Repro: <exact steps or command; "none given" if so>
- Suspected area: <package / route / component / path:start-end (Symbol)>
- Hypothesis: <one sentence — keep it to yourself in Step 2>
- Blast radius: <what else the same cause could affect>
- Fixed means: <the test that passes / the behavior / the output>
```

Check the `bug` records and call graph first (`fyr record list --kind bug`; `fyr search --slice <symptom_or_symbol>`, `fyr search --affected <path_or_symbol>`, `fyr search --path <from> [--to <to>]`, and `fyr search --refs <path> --kind bug` lists the records and callers naming each file involved before any `grep` or whole-file read): if the area has a record, read it — the prevention rule may already name the cause. If there is no repro and the bug is not obvious from one file, **ask the user for one** (steps, URL, log line, failing test; a gate, above). Never invent a repro on your own: one is inferred only when the user picks that option at the gate, and it is printed before Step 2 runs, because a wrong-shaped guess wastes the diagnosis.

## Step 2: Goldfish diagnosis

Dispatch the `explorer` agent with **only the symptom and the repro** — not your hypothesis — a budget of 30 tool calls, "write the artifact early, then update it", and this brief:

> Independent diagnosis. Symptom: … Repro: … Investigate the codebase (starting with `fyr search --slice`, `--affected`, and `--path`) and rank the top one to three candidate root causes by likelihood, each with `path:start-end (Symbol)` (file:line), the evidence for it, and what would falsify it. Do not propose a fix. End with `diagnosis complete`.

Compare with your hypothesis. Agreement: proceed. Disagreement: read its evidence and re-investigate before choosing; if it was right, update the problem note and say so. Skip the dispatch only when the location is mechanically obvious (a one-to-three-line fix in a clearly identified place).

**Escalate to `/fyr-feature` when scope grows:** if Step 2's diagnosis (or Step 4 implementation) shows that fixing the root cause requires more than one phase, an architectural/interface/schema redesign, or a `PRD.md`/ADR decision, stop `/fyr-bug` before making multi-phase edits, store the open `BUG-NNN` record capturing the symptom, repro, and diagnosed root cause (`owner: main`), and hand off to `/fyr-feature` referencing that `BUG-NNN` record so it runs the phased `PLAN-*`, cold-reader check, and pre-build interview (moving `BUG-NNN` to `--status fixed` or `--status graduated` once `/fyr-feature` lands the covering test and fix).

## Step 3: Failing test first

Write the test that captures the bug at the right tier (unit beside the code; integration when a real dependency is involved; e2e when only the running artifact shows it). Run it. It must fail for the reason in the problem note; if it fails for another reason, fix the test before touching the implementation. A must-fail mutation cited as evidence follows the protocol in review-standards § 2 (unique target, restore from a saved copy, `git diff` read, compiled and a named test failed). For UI bugs that a spec cannot capture, script the repro in the browser and confirm the bug is observable; that script is re-run after the fix.

## Step 4: Smallest fix

Change the least that turns the test green and satisfies "fixed means". No adjacent refactors (note what you saw under the active plan's Follow-ups, or `TODO.md` Backlog when no plan is active), no defensive code for cases the bug did not show, no feature flags. When the root cause is a CLASS — a call, a pattern, a shape of check — the smallest fix covers every site of that class, which is not an adjacent refactor: `git grep` the same call across the tree before the fix is called done, fix every site that has the defect in the same change, and name the sites in the bug record; a class fixed at one site is the next review's finding. Re-run the test: green, or the bug is not fixed — do not rewrite the test. Re-run the original repro once more.

## Step 5: Definition of Done and the record

Follow Definition of Done steps 1–3 (`documentation` carries `fyr search --refs <path>` from `fyr-feature` Step 6; any dispatch to `verifier`, `reviewer`, or `security` names a 30-call budget and `"write the artifact early, then update it"`):

1. **Verify:** Run `fyr verify` (no flags: `gofmt -l`, `go vet ./...`, `go test -race ./...`, and `fyr check` -> `.agents/scratch/verify.stamp`; on an explicit operator test override such as `run go test ./... once`, run `gofmt -l .`, `go vet ./...`, and that test command only without `fyr verify` or `fyr check`). Dispatch `verifier` after review fixes only when browser, container, or live local HTTP e2e is owed.
2. **Review & Docs:** Dispatch `reviewer` with the diff only regardless of size (Step 0 exempted trivial edits; use `--docs` if an interface, command, or deploy step changed), and `security` when the area is sensitive. Update docs only if behavior, interfaces, the plan, or a deploy path changed (skip reading unchanged root docs; self-edit up to 30 changed lines, else `documentation`).
3. **Records (batch scratch drafts and record commands in parallel):** Write or update the `bug` record yourself (`owner: main`, symptom, repro, root cause, fix `path:start-end (Symbol)` / file:line, prevention rule; `fyr record edit <BUG-ID> --status fixed --body-file <path>` or `--status graduated` once encoded in a regression test, or `fyr record add bug --title <text> --body-file <path>`). If Step 2 ran and review/verifier/fix surfaced an unranked cause, add `Diagnosis missed:` naming it and which Step 2 brief line would have caught it. Closing writes the single `handoff` record in step 4 (citing commit, `path:start-end (Symbol)`, checks/agents, verification digest, and BUG ID) rather than a mandatory per-task `change` record (`fyr record add change --title <text> --body-file <path>` is optional when a standalone changelog note is requested).
4. **Commit, push, and close:** Stage files by name, check `git diff --cached --stat`, commit, and push (obey explicit no-commit/no-push instructions). Then store the `handoff` with `--id HANDOFF-<stamp>-<slug>` directly via `fyr close` when `.agents/scratch/verify.stamp` exists, or `fyr record add handoff` when `fyr verify` did not run — skipping `/fyr-handoff`, Kickoff blocks, and `fyr metrics ingest` on single tasks (run `/fyr-handoff` only at phase checkpoints or explicit handoff requests).

Report in chat: bug in one line, root cause, fix (`path:start-end (Symbol)` / file:line), the test that captures it, goldfish agreement or divergence, review outcome, BUG id.
