---
trigger: always_on
description: "Session sizing: a session is one Task, one plan phase or a lead's work loop and ends at its checkpoint with a handoff whose Kickoff starts the next session; a second ask waits for its own Kickoff."
---

# Session sizing

- A session is one Task, one plan phase, or a lead's planning/dispatch loop (`feature` workers or `/work`). A plan phase ends at its Checkpoint box: tick it, store the `change` record, then run `/handoff` (which commits, pushes and runs `fyr metrics ingest` once; its `## Kickoff` is a fenced block) and END the session; the next phase starts in a fresh conversation from that Kickoff. A Task ends the same way at Definition of Done step 5.
- Never do a second, unrelated ask in place: finish the current one and queue the rest in the handoff's Kickoff, unless the operator explicitly says "do both here" or "continue here" in their turn.
- A session with only a Trivial change and nothing queued needs no Kickoff.
- When handed a Kickoff (or a report pointing to one), execute it in the same turn under its standing approvals without asking to re-send.
- Why: long contexts degrade gradually before compaction and the model cannot see its own fill; the checkpoint is the visible boundary.
