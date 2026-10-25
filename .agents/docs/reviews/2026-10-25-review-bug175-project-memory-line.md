---
owner: reviewer
date: 2026-10-25
---
# Review — bug175-project-memory-line   (2026-10-25)

Range reviewed: .agents/scratch/fyr/bug175-diff.patch (skills/project-memory/SKILL.md line 50, one line)

## Status: COMPLETE

## Verdict: APPROVE WITH NITS

The instruction does what it says, is followable, and agrees with the heredoc example (lines 38-46: literal date, no substitution, one command). One restatement disagrees: the global rule that a date comes from the shell "in the same call that writes" it. No blocker.

## Findings
- **[low / confidence: medium]** `context/AGENTS.md:52` says dates "come from the shell (`date +%F`, ...) in the same call that writes them"; `skills/project-memory/SKILL.md:50` now says run `date +%F` as its own earlier command and type the date. Different surface (file stamps vs a memory body), so the skill reads as a specific exception, but neither names the other. Fix: add "except where a skill says otherwise (project-memory)" to the context line, or leave and accept (the size pins in internal/harness/size_test.go apply to context edits). Not blocking: the specific skill wins when it is loaded.
- **[low / confidence: low]** `skills/feedback-log/SKILL.md:90` (rule 7) also calls `sh-agent memory add` with a stdin body and does not repeat the one-command / no-$( ) constraint; it points to project-memory for non-corrections only. A correction body with a date could inline `$(date)` the same way. The body there does not ask for a date, so no concrete failure shown; the 2/2 PASS covers project-memory only.
- Note (not a finding): other files use `$(date ...)` (skills/handoff/SKILL.md:158, agents/verifier/agent.md:37, skills/bash/SKILL.md:44 recommends `$()`); they are other surfaces and the bug record scopes the failure to the memory save. If the same grant store refuses them, those would fail alike; untested here.

## Criteria
1. Dead code — pass; one line replaced, nothing orphaned.
2. Flow tracing — pass; the line matches the heredoc example and the BUG-175 verified re-run (2 of 2, per the record; I did not re-run it).
3. Standards conformance — pass with the restatement note above; the other date rules (handoff, retro, dream, preflight) say "run date +%F", compatible.
4. Directory layout — pass.
5. Simplicity — pass.
6. Decoupling — pass.

## Not examined
Did not re-run the agy row or read the testing addendum. Did not grep outside the listed living-doc paths. specs/ and *-matrix.md hits (memory-loop.md, sh-agent-matrix.md) were matched by grep only and none restates the date or substitution claim.

## Outcome
- **1 (the context's "same call" date rule vs. the skill's separate `date +%F`): deferred to the operator.** The skill's line is the narrower rule for a headless save, and the operator approved it (plan D27). Changing `context/AGENTS.md:52` would mean editing size-pinned context, and the canary row would have to run again. That is outside this fix's approval, so it goes to the handoff's Next Steps with a recommendation.
- **2 (`feedback-log` rule 7 does not repeat the one-command constraint): rebutted, no change.** Rule 7's body asks for no date, so there is nothing to substitute. The `feedback` row passed (log, kit3c) and saved its memory through `sh-agent memory add`. That row ran with `command(*)`, and the log names this under "Not checked".
