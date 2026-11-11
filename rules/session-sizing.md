---
trigger: always_on
description: "Session sizing: a session is one Task, one plan phase or a lead's work loop and ends at its checkpoint with a handoff whose Kickoff starts the next session; a second ask waits for its own Kickoff."
---

# Session sizing

- A session is one Task, one plan phase, or a lead's wave loop (`fyr-feature` workers or `/fyr-work`). End each phase at its Checkpoint box and each Task at DoD step 5 via `/fyr-handoff` (commits, pushes, `fyr metrics ingest` once; fenced `## Kickoff`); start the next phase in a fresh session. A `/fyr-work` lead chains waves in one session, checking `fyr metrics ingest` + `fyr metrics trajectory` between waves and writing a `/fyr-work <PLAN-ID>` resume handoff on `KNEE_EXCEEDED` (`>= 180,000` tokens) or compaction.
- Never do a second, unrelated ask in place: queue it in the handoff's Kickoff unless the operator says "do both here" or "continue here" in their turn.
- A Trivial change with nothing queued needs no Kickoff.
- Execute a handed Kickoff in the same turn under its standing approvals with zero build-time questions (`ADR-517`): unknowns become dated `D-row`s (`by: implementer` or `by: lead`), closed-set items go to the closing handoff, and only a `fyr work` safety `FAIL` stops a lead loop early.
- Why: context degrades past the `180,000`-token knee; checkpoints and `fyr metrics trajectory` mark the boundary.
