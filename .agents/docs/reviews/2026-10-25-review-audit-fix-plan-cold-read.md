---
owner: reviewer
date: 2026-10-25
---
# Review — audit-fix plan cold read   (2026-10-25)

Range reviewed: `.agents/docs/plans/2026-10-25-harness-audit-fix.md` at 454d1a6 (cold-reader check; the plan document plus what it cites: `reviews/2026-10-25-harness-audit.md`, `plans/2026-10-25-recon-harness-audit.md`, `REQUIREMENTS.md`, `AGENTS.md`, and the skills, kit and context lines its boxes name). Judged as a plan, not as an audit.

## Status: COMPLETE

## Verdict: REQUEST CHANGES

The plan routes its one `REQUIREMENTS.md` edit correctly (propose, approval in the operator's own turn, ADR, then REQUIREMENTS.md plus ROADMAP.md in one commit), and all 27 medium IDs have a phase. It cannot be executed as written, for two reasons. First, several in-scope IDs and Decisions have no box at all: G16, G17, TR10 (D1) and C29. Second, about 40 low IDs are covered by one sentence ("the remaining lows as their area's phase reaches them") with no list, and a dozen of them are "operator's choice" items that no D-row decides. Worse, D4 rests on a premise that is false in the tree: `handoff` has no `git log` step, and `preflight`'s is a range read, not a pre-push check. Several `expect:` lines name a process (a pass of `TestVendored*`, a review with an Outcome) rather than the thing the box changes. The remaining findings are medium or low.

## Findings

1. **[high / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:13-22, 56-83` — In-scope IDs and Decisions with no executable home.
   - **G16 and G17** (`feature` checks `REQUIREMENTS.md`; a trust-boundary row in the plan template) are in Scope (line 21) and `feature` is in Surfaces. No phase title or box names them, and Phase 3's title (line 73) lists only WS23–WS29 and TR8.
   - **TR10 / D1** (the `context/AGENTS.md:52` date line) appears only in Phase 5's header (line 78). Phase 5's two boxes (lines 79-80) never say to edit the context line, run the size pins, or run a behavioural row for a changed rule (AGENTS § Testing requires one).
   - **D1 restatements.** D1 changes a rule but the plan has no restatement grep. `skills/handoff/SKILL.md:158` ("the stamp is run in the same call that writes the file") restates the old rule and is not in any box.
   - **C29** (the 14,500 goal, which is already missed at 14,623) is in Scope line 14 as `fix`, but the audit's recommendation is "strike the goal, or trim ~125 B; operator's choice". No D-row decides it. D6 (line 94) only notes the miss. The goal lives in root `AGENTS.md:26-27`, which is not in Surfaces.
   - **Fix:** add a box for each: G16 and G17 in Phase 3 (with the `feature` Antigravity row), TR10 in Phase 5 (edit, both size pins, a `ctx-deploy` or canary row, a restatement grep over `skills/ context/ agents/ README.md`), and a D10 that decides C29 (strike the goal, or trim), adding root `AGENTS.md` to Surfaces.

2. **[high / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:22, 97` — The lows have no list, and the operator-choice lows have no decision.
   - **What the plan says.** "The remaining lows as their area's phase reaches them" (line 22) plus D9 row 19, "fix per area".
   - **What an executor cannot tell.** The audit has 53 lows, and the plan names about 14. It cannot tell which of the others are in. Many of them the audit recommends `defer` (IN17, IN18, IN21, IN29, IN32, IN-S2, C32, C33, WS37, AG21, AG22, SD16, TR12, TR13), which D9 row 19 as written reverses without saying so.
   - **Items that need a choice no D-row makes:** WS30, WS39, IN16, IN26 (security judgement), AG20, SD-S2, TS-S1, IN-S1, TR14. Phase 2 and Phase 4 are written as closed lists, so IN27/IN28/IN30/TS34 have no phase.
   - **Phase 6 consequence.** Phase 6's pass condition is "no med open without a D-row" (line 82), and the Verification line demands every ID be `fixed`, `partly`, `struck (D<n>)` or `deferred`. Nothing is recorded to check against.
   - **Fix:** add a disposition table to the plan (or a sibling file the plan names), with one row for every audit ID. Columns: ID, disposition (fix, defer, strike, ask), phase and box, and the D-row if a choice was needed. Ask the operator for the "ask" rows in one numbered table, then record the answers as D-rows. Without it the Phase 6 expect cannot be checked.

3. **[medium / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:92, 74` (D4, Phase 3) — D4's premise is false in the tree.
   - **D4 claims** "the `git log` step `handoff` and `preflight` already run".
   - **What is there.** `grep "git log" skills/handoff/SKILL.md` has no hit. Step 6 (`:184`) is commit, then push, with no log read. `skills/preflight/SKILL.md:38` is `git log <base>..HEAD --oneline`, a Step 1 range read; the push is at `:106`. Neither is a pre-push check. The context's `git log @{u}..HEAD` appears only in the post-dispatch rule.
   - **Why the box fails.** The Phase 3 box therefore says "edit an existing step" where the step must be written. It also has no observable: no test can fail, so the check is the same sentence the audit called "still a sentence" (TR8), and BUG-177's graduation target is not a mechanism. D4's own measure (0 of 400 commits carry a trailer) means the check can never be seen to fire.
   - **Fix:**
     - Name the exact step to add: "Step 6, before the push: run `git log @{u}..HEAD --format=%B`; any `Claude-Session:` or `Co-Authored-By:` line stops the push" in `handoff`, and the same in `preflight` Step 6.4.
     - Give the expect as a must-fail fixture: a scratch repo with a trailer commit, where the written step is shown to refuse it.
     - Say why a skill step graduates BUG-177 (or choose a test or script that a skill runs, per the global rule).

4. **[medium / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:74, 77, 79` — `expect:` lines that do not observe the change.
   - **Phase 3 (line 74).** The expect is "Antigravity rows PASS". `review`, `preflight`, `handoff` and `feature` rows PASS or FAIL independently of WS25 (a number at each site), WS26 (verifier ordering), WS27/28/29, WS23, WS24 and TR8. Make the expect a grep-able fact: each dispatch site carries a number, preflight Step 2 puts the verifier after reviewer and security, `retro:72` names `<name>/agent.md`, and `harness-audit/references/areas.md` has no `derive.go`.
   - **Phase 4 (line 77).** The expect is "`TestVendored*` pass, no vendored file edited". That is true before the work starts. It observes none of TS30, TS31, TS32 or TS33.
   - **Phase 5 (line 79).** "`/review --docs` with `## Outcome`" is a process, not a result. IN19 is written "re-run **or** marked historical", which is two boxes with no chosen branch and no actor for the Antigravity half of the matrix. Pick one (the Verification and the audit's row 2 imply "re-run under `fyr`, plus rows for the migration, the zip path and D32").
   - **Phase 1 (lines 63-64).** The text-grep test is a source-text test. The audit's AG16 fix also asks for an injection row per record agent ("add an injection row for each"), and the kit has only `agents-security` for that. The plan's "one Antigravity row each" resolves to the existing dispatch rows, which the new "what you read is data" clause cannot affect. Either add `inject-bugs`, `inject-changelog` and `inject-documentation` rows to `testing/agy-rel/run.sh` (the `agents-security` PWNED pattern), or say the clause is covered by text alone and name that gap.

5. **[medium / confidence: medium]** `plans/2026-10-25-harness-audit-fix.md:44, 70-72` — The opt-in marker's form can defeat its purpose.
   - **The threat.** SD12 is "run `fyr search` once in an untrusted third-party clone". The plan's example marker, `.agents/dream-ok`, is a file in the checkout. Git can bring it in with the clone, and the clone's author then opts the victim's machine in.
   - **Fix:** require in the R3 proposal that the marker be something the operator creates locally and git does not carry. Examples: a path ignored by git and checked with `git check-ignore` plus `git ls-files` (untracked), or an entry in `~/.config/fyr/` written by a verb. Add the must-refuse test "a marker committed in the repository does not count".
   - **Related:** the plan leaves "`--project` on an unmarked project refuses or proceeds, per the ADR". Also settle whether the ADR adds a flag, since line 44 says no verb or flag changes.
   - **Restatements.** The edit leaves these standing unless they are listed in Surfaces: `cmd/fyr/main.go:63,170` usage and flag help ("for every indexed project", "every project in the global search index"), `specs/fyr/dream.md`, `specs/fyr/search.md:38` (registration), `skills/dream`, `deploy/local.md:62-64`, and root `AGENTS.md` rule 1 ("each project's redacted content"). Only README and the context are named.

6. **[medium / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:86, 97, 79` — D9 and Risks contradict each other on go1.27.2.
   - D9 says row 15's re-measure "waits for go1.27.2 on this box, which the operator restores". Risks says "govulncheck and the recall re-measure run on go1.24.4 until the operator restores the SDK". Phase 5's box is `who: AI` with no wait condition.
   - The `~/.local/bin/go` link is dangling: it points to `sdk/go1.27.1`, while the docs say 1.27.2. Executed in order, Phase 5 either blocks on an operator action nobody routes, or silently re-measures on the old toolchain.
   - Also, SD13 needs more than a re-measure: q41's gold list must be re-pinned (audit line 167) before the spec row is updated. The box does not say so.
   - **Fix:** choose one. If "run on 1.24.4", the spec row must say which toolchain measured it. If "wait", make it an operator box with an expect.
   - **govulncheck.** It exits 3 on go1.24.4 with 15 stdlib advisories (audit § Measurements). "Verifier PASS" at checkpoints needs the dispatch to say the pass condition is "no advisory beyond the recorded 15 and no module-reached finding".

7. **[medium / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:50-53, 62-83` — Verification tiers and checkpoints the plan owes.
   - **Missing checkpoint boxes:** Phase 2b and Phase 4 have none, and Phase 0b has none by design. Phase 1 and Phase 3 have a checkpoint, but only Phase 2 names a verifier.
   - **`/review --docs`.** AGENTS § Testing says a change to `context/`, `agents/` or `skills/` gets `/review --docs` over its range. The plan runs it once, "over the docs phase", so Phases 1, 3 and 4 change agents and skills with no docs lens. A `reviewer` for 50+ lines or 5+ files (the global DoD) is named in no phase.
   - **Behavioural tiers.** AGENTS § Testing also asks for "one sandbox start of the CLI" and results "reported as n of N". Verification line 51 has the scratch-home install and the rows, but not these.
   - **The Phase 6 "E2E" is a re-audit.** The operator's ask (Why, line 10) was "an e2e and full audit". The earlier E2E (`testing/2026-10-25-verify-fyr-e2e-c4885ae.md`: E2E 1–4 plus four Antigravity rows) is not repeated. Phase 6 is a document audit, so the fix plan ends with no runtime E2E after Phases 1–5 changed agents, skills, the lint and `dream`. Add the E2E re-run (or state that the harness audit's own E2E is the one to repeat), reachable on this box with the kit.

8. **[medium / confidence: medium]** `plans/2026-10-25-harness-audit-fix.md:77` — Phase 4's mechanisms.
   - **TS30.** The box is "a `VENDORED.md` override note for `find-skills`". `VENDORED.md` is read by maintainers, not by the model that loads `skills/find-skills/SKILL.md`, so the verbatim `npx skills add … -g -y` step (`SKILL.md:100`) still ships and still reaches the model. The audit gives the same recommendation as an alternative to "fork under another name". Say how the model sees the override (a context line, a `google-catalog`-style router note, or a fork) or accept that TS30 is documentation only and record it as `partly`.
   - **TS32 missing fact.** The fingerprint check needs Google's current apt signing key fingerprint and URL. The audit marks them "unverified: needs cloud.google.com page", and nothing in the plan says who fetches them or from where. Name the source (the install page) and the actor, or point to the page instead of embedding a fingerprint.
   - **TS33.** The box lists "override notes for `find-skills` and `gke-basics`". TS33 is a direct edit of `gke-basics/SKILL.md:22` (drop the `~/.claude` path). `gke-basics` is the one non-verbatim fork (AGENTS, `VENDORED.md:21`), so the edit is allowed, but then it is not an "override note". `gke-basics` is also absent from Surfaces, and `find-skills` appears there only through `VENDORED.md`.

9. **[low / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:94, 74` — WS25's "six dispatch sites" does not match the audit.
   - The audit's WS25 lists `feature:15`/`:90`/`:114`/`:23`, `bug:39`, `review:45`, `preflight:44,57,59`, `handoff:167` and `explain-project:12`. That is six skills but at least nine sites, and `preflight:44` is the verifier re-run.
   - Write the list (file:line, number) into the box, and add the check as the expect.
   - Also decide whether 30 suits the strongest-model reviewer in `preflight` Step 2, since D6's numbers (30, and 15 for a verifier re-run) are the only cap.

10. **[low / confidence: high]** `plans/2026-10-25-harness-audit-fix.md:67` — Facts a cold executor lacks in Phase 2.
   - "An old-named unit" and "F4" are defined only in plan `2026-10-24-fyr-agy-only.md` (D28: `sh-agent-dream` and `com.styrman.sh-agent-dream`; F4 at `:33`; D30 and D33 at `:129-150`). Cite them in the box.
   - The allow-list's membership is unstated (which keys skills, commands and rules may carry). The test "every skill in the tree accepted" fixes the minimum, but a vendored skill that uses another key forces the list to grow, since vendored files cannot be edited. Say that.
   - `fyr diff` "in memory" (D8): say how it compares (stdin to the system `diff`, or a Go comparison). Hand-rolling a diff algorithm is not stdlib.

11. **[low / confidence: medium]** `plans/2026-10-25-harness-audit-fix.md:27-38, 85` — Surfaces is incomplete against the boxes and the Scope.
   - Missing from Surfaces: `cmd/fyr/main.go` (IN15 `list -h`, the usage text for SD12), `internal/harness/{release,state}.go` if IN28/IN27/IN30 are fixed, `skills/{gke-basics,review-standards,grilling,research,feedback-log}` for TS33 and WS28/WS33–WS35, `.agents/docs/specs/fyr/dream.md`, root `AGENTS.md`, and `ROADMAP.md` (2b's one-commit rule).
   - Risks (line 85) allows plan `2026-10-24-fyr-agy-only.md` Phase 6 (v3.0.0) to land during this plan. AGENTS § Release makes a gate that names an unrun Antigravity run of a changed skill or agent NO-SHIP. Say which of Phases 1–4 must be complete before that tag.

12. **[low / confidence: medium]** `plans/2026-10-25-harness-audit-fix.md:60-61, 67, 70` — Interfaces and Phase 2b ordering.
   - Phase 2b is positioned before Phase 3 but depends on an operator turn that may not come. "Or the phase struck" is covered, but nothing tells the executor to continue with Phase 3 while the proposal waits. State that Phases 3–5 do not depend on it (Phase 5 touches the context's dream mention under D2, so say the order).
   - Line 44 says "No other verb or flag changes", while Phase 2 changes behaviour of `list -h` (exit code) and `status` (new line), and Phase 2b may add one. Reword to "no new verb or flag, unless the ADR adds one".

## Criteria
(Applied to the plan document and what it cites, since there is no code diff.)

1. Dead code — n/a (no code). Related: the plan carries stale references of its own (the `go1.27.2` link, "six sites"); see 6 and 9.
2. Flow tracing — fail. Every Decision was traced to a box. D1 and D4 have no executable box (findings 1, 3), D6/D9 flow through unlisted sites and lows (9, 2). Phase order is otherwise sound: tests precede code; the lint is run over the tree before it is made strict; 2b is gated by approval, ADR, then code. Findings 5 and 6 name the order problems.
3. Standards conformance — fail on Verification (finding 7): `/review --docs` per range, the sandbox start and n of N, a restatement grep for D1 (finding 1). No contradiction with R3 (the trailer check is a written step, not a hook), with the vendored-files rule (provided the TS33 edit is recognised as a fork edit), or with the delivery claim's one home. The REQUIREMENTS routing is right; finding 5 adds the marker's form to the proposal.
4. Directory layout — pass. No new files are proposed (the disposition table of finding 2 belongs in `plans/`).
5. Simplicity (DIY-except-security) — pass with a note: D8's "in memory" must not become a hand-written diff (finding 10). The IN23 and IN25 lint changes go through `security` (Phase 2).
6. Decoupling — pass. Phases map to areas that different engineers could take in parallel, apart from the two plans that both touch the dream timer and the v3.0.0 gate (finding 11).

## Not examined
- Antigravity or sandbox behaviour: no Antigravity run was made, so whether the `agy-rel` rows can show the changes is judged from `run.sh` and `skill-matrix.md` ids only.
- The area briefs `recon-harness-audit-{AG,C,G,IN-1,IN-2,SD,TR,TS,WS}.md` were not read. IDs were taken from the consolidated recon table, which has all of them.
- `skills/{feature,review,preflight,handoff}/SKILL.md` were grepped at the lines the plan and audit name, not read whole. `internal/harness/inventory.go` and `apply.go` were not read; the Phase 2 code boxes are judged as stated.
- `testing/guide.md` § 1–5 and `specs/fyr/dream.md` were not read; the tier list comes from the root `AGENTS.md` § Testing.
- The "last 400 commits" count in D4 and the byte math (+40 bytes for D1 against 377 B of headroom) were not re-measured. `go test ./internal/harness -run TestRenderedContext` passes on the current tree.

## Outcome
Answered by the main agent on 2026-10-25, in `plans/2026-10-25-harness-audit-fix.md`. The operator answered the six findings that needed a choice, in their own turn. Each answer is quoted in its D-row.

| # | Outcome | D-row |
|---|---|---|
| 1 | Fixed. G16 and G17 have a Phase 3 box. TR10 has a Phase 5 box with both pins, two rows and a restatement grep naming `handoff:158`. C29: operator "Strike the goal". | D10 |
| 2 | Fixed. § Dispositions gives one row per audit ID. Deferred lows: operator "Fix them all". Operator-choice lows: "Approve all as listed". | D11, D12 |
| 3 | Fixed. D4's premise was false, confirmed: `grep "git log" skills/handoff/SKILL.md` has no hit. Operator: "Script the skill runs", a new step before the push, a must-fail fixture test, and the BUG-177 graduation. | D13 |
| 4 | Fixed. Every expect is a grep, test or row fact. Injection rows were added for the three record agents. IN19: operator "Re-run under fyr". | D14 |
| 5 | Fixed. Operator "Local only": the marker is one git does not carry, with the must-refuse test. The restatements are listed in Surfaces and in Phase 2b. | D15 |
| 6 | Fixed. Confirmed: `~/.local/bin/go` points at the absent `~/sdk/go1.27.1`. Operator "go1.24.4, recorded". The govulncheck pass condition is stated. q41's gold is re-pinned before the measurement. | D16 |
| 7 | Fixed. Every phase has a checkpoint with the tiers. Phase 6 re-runs the E2E before the re-audit. | D17 |
| 8 | Fixed. TS30: operator "Fork under a new name". TS32 points at Google's page and prints the key with `gpg --show-keys`. TS33 and TS34 are direct edits of the fork. | D18 |
| 9 | Fixed. The sites are listed, eleven sentences in six skills, re-grepped first. | D19 |
| 10 | Fixed. The unit names and F4 are cited. The allow list grows only by D-row. `diff` is compared in Go and piped to the system `diff`. | D19 |
| 11 | Fixed. Surfaces is completed. A tag waits for a checkpoint. | D19 |
| 12 | Fixed. 2b does not block other phases, and the Interfaces wording is corrected. | D19 |

No second round. Only finding 3 was re-checked against the tree, and it held. Findings 1–2 and 4–12 were taken as written.
