---
owner: main
date: 2026-10-23
---
# Plan amended: sh-agent kickoff verb (D28, D29) and the release owed at close (D30)     (date: 2026-10-23)

## Summary
On the operator's words the plan's Phase 1 gains the `sh-agent kickoff` verb (prints the newest handoff's Kickoff block, so `claude "$(sh-agent kickoff)"` replaces the paste; D28 supersedes D3's "no verb"), the headless loop is documented rather than built (D29), and Phase 6 gains the operator's release box with a project memory behind it (D30).

## Type
plan (no source change)

## Files changed
- `.agents/docs/plans/2026-10-23-session-sized-work.md` — Phase 1 two boxes, Phase 6 release box, E2E wording, D3 note, D28–D30
- `.agents/memory/release-after-session-sized-work.md` — the memory and its index line; the store is gitignored (`.gitignore:45`), so it lives on this box only and the plan's Phase 6 box carries the release to the other machines
- this entry

## Agents dispatched
none

## Verification
none owed (plan and memory text); `sh-agent memory show` lists the new memory.

## Docs
no how-it-works docs changed

## Requirements
unchanged (D25 still awaits approval)
