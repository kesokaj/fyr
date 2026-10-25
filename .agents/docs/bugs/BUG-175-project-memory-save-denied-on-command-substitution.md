---
owner: main
date: 2026-10-25
id: BUG-175
status: fixed
area: skills/project-memory on Antigravity print mode
---
# BUG-175: on agy 1.4.7 the model saves a memory through a `$(date +%F)` substitution, the grant store refuses it, and nothing is saved

## Symptom
In fyr Phase 3b's `project-memory` row (`testing/2026-10-25-verify-fyr-content.md`), 0 of 2 attempts saved the memory. In both, session 1 ran `TODAY=$(date +%F) && sh-agent memory add --type project --name deploy-on-fridays …` and the grant store auto-denied it (attempt 1: step 8; attempt 2: step 6, status 6), so `.agents/memory/` was never created. Session 2 then answered "Friday" without the store, so the recall half proves nothing.

## Repro
`AGY_TAG=<tag> bash .agents/scratch/fyr/kit3c/run.sh project-memory` (a scratch copy of `testing/agy-rel/run.sh` with a second session; grants as skill-matrix.md § Procedure, no `command(*)`), agy 1.4.7.

## Root cause
`skills/project-memory/SKILL.md:50` says "Write dates as `YYYY-MM-DD` from `date +%F`", and the model inlines it as a command substitution. Like BUG-174's `@{u}`, the store refuses a `$( )` form under the plain `command(<name>)` grants. The same skill passed on agy 1.2.14 (`testing/2026-09-30-verify-parity-memory-dream-phase-2.md`); not determined whether the CLI or its model changed. Not introduced by Phase 3b: that line is unchanged since 9174cca.

## Fix
Not yet: outside the phase's standing approvals. Proposed: line 50 says to run `date +%F` as its own call and write the date it printed into the body, and the save is one `sh-agent memory add` call with the body on stdin, nothing joined to it; then the row re-runs.

## Prevention
A skill that tells the agent to compute a value says to run that command as its own call and write the value in by hand; a command a skill names is run once under the kit's real grants before it ships.

## Addendum — 2026-10-25: fixed
The operator approved the proposed fix ("Go all approved", 2026-10-25, plan fyr D27). `skills/project-memory/SKILL.md:50` now says to run `date +%F` as its own command first and type the date it printed into the body, and that the save is the one `sh-agent memory add` command, with nothing joined to it and no `$( )` inside it, because a headless permission check refuses a command substitution. Verified by re-running the row on agy 1.4.7 with the kit's named grants (`AGY_TAG=3d|3e bash .agents/scratch/fyr/kit3c/run.sh project-memory`): **PASS 2 of 2** (42 s, 43 s). In both, the conversation store shows `date +%F` as its own step, then `sh-agent memory add --type project --name deploy-on-fridays …`. Every step has status 3 with no `error_details`. `MEMORY.md` holds `- [deploy-on-fridays](deploy-on-fridays.md) — deploys happen on Fridays`, the memory's `**Why:**` line carries the printed date, session 2 answered "Friday" from the store, and the preamble printed its three clean lines. Log: `testing/2026-10-25-verify-fyr-content.md` § Addendum.
