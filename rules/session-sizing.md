---
trigger: model_decision
description: "Apply when executing a Kickoff or plan phase autonomously, sizing phases, running /fyr-swarm waves, or checking token knee."
---

# Session sizing

- A session is one Task, one plan phase, or a `/fyr-swarm` wave loop (`<= 180,000`-token knee). End each phase at its Checkpoint box via `/fyr-handoff` (commits, pushes, `fyr metrics ingest` once; fenced `## Kickoff` + `Next prompt:`) and start the next phase fresh. A `/fyr-swarm` lead chains waves, running `fyr metrics ingest` + `fyr metrics trajectory` between waves and handing off on `KNEE_EXCEEDED` (`>= 180,000` tokens) or compaction.
- Queue unrelated asks in the closing Kickoff unless told `"do both here"` / `"continue here"`; Trivial edits need no Kickoff.
- Execute a handed Kickoff under its standing approvals with zero build-time questions (`ADR-517`): record choices as dated `D-row`s (`by: implementer`/`lead`) and in the **Autonomous Decisions Log**, defer closed-set extras to Next Steps, and stop early only on a worker gate or safety `FAIL`.
