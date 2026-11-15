---
trigger: model_decision
description: "Apply when executing a Kickoff or plan phase autonomously, sizing a session or phase, running /fyr-work waves, checking token knee, or closing at a checkpoint."
---

# Session sizing

- A session is one Task, one plan phase, or a lead's wave loop (`fyr-feature` workers or `/fyr-work`). End each phase at its Checkpoint box and each Task at DoD step 5 via `/fyr-handoff` (commits, pushes, `fyr metrics ingest` once; fenced `## Kickoff`); start next phase fresh. A `/fyr-work` lead chains waves in one session, running `fyr metrics ingest` + `fyr metrics trajectory` between waves and handing off on `KNEE_EXCEEDED` (`>= 180,000` tokens) or compaction.
- Queue unrelated asks in the closing Kickoff unless the operator says "do both here" / "continue here"; Trivial changes need no Kickoff.
- Execute a handed Kickoff under its standing approvals with zero build-time questions (`ADR-517`): log unknowns as dated `D-row`s (`by: implementer`/`lead`), defer out-of-set items to Next Steps, stop early only on a `fyr work` safety `FAIL`.
- Why: quality degrades past the `180,000`-token knee.
