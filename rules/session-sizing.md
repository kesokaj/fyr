---
trigger: always_on
description: "Session sizing: a session is one Task or one plan phase and ends at its checkpoint with a handoff whose Kickoff starts the next session; a second ask waits for its own Kickoff."
---

# Session sizing

- A session is one Task or one plan phase. A plan phase ends at its Checkpoint box: tick it, store the `change` record, commit, push, then write the handoff — the `handoff` skill; its `## Kickoff` is a fenced block — and END the session; the next phase begins in a fresh conversation from that kickoff. A Task ends the same way at Definition of Done step 5.
- A second, unrelated ask — in the same turn or a later one, whatever its size — is not done in place: the session does one, and the handoff's Kickoff names the rest. Only the operator's own words in the same turn ("do both here", "continue here") keep a session past its boundary.
- A session whose only work is a Trivial change, with nothing queued, needs no kickoff.
- A session handed the Kickoff, or the previous report that points at one, runs that Kickoff in the same turn under its standing approvals; it never asks for the Kickoff to be re-sent.
- Why: a long context degrades on a gradient long before it compacts, and the model cannot see its own fill; the checkpoint is the boundary it can see.
