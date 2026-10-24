---
owner: main
date: 2026-10-24
---
# fyr Phase 0 closed: ADR-062, the approved requirements, BUG-172 obsolete     (date: 2026-10-24)

## Summary
The operator approved the Phase 0 report whole ("go all approved", 2026-10-24). The approved `REQUIREMENTS.md` set landed verbatim with ADR-062 and the roadmap in one commit, `92f5c8c`: the requirements now name `fyr` and the Antigravity CLI alone, and R9 has three procedures. ADR-062 supersedes ADR-025, ADR-060 and the report half of ADR-047a, and amends ADR-030, 033, 038, 047, 058, 058a and 061. The same answer closed BUG-172 as obsolete, removed the stale project memory `release-after-session-sized-work` (its release is the plan's Phase 6), and confirmed D2 and D5a (plan D23). The update timer is deferred to Phase 6, as recommended. Phase 0 is closed.

## Type
task (plan phase)

## Files changed
from git log --stat 92f5c8c and git diff --stat HEAD
- `REQUIREMENTS.md` — title, R0–R5, R7–R10, Known limits, Non-goals; R6 unchanged
- `.agents/docs/architecture/DECISIONS.md` — ADR-062
- `ROADMAP.md` — title `fyr`; Next names the Antigravity CLI and the EGM plan
- `.agents/docs/plans/2026-10-24-fyr-agy-only.md` — D9 quoted, D23, the Open row on D5a closed, Phase 0 boxes and Checkpoint ticked
- `.agents/docs/bugs/BUG-172-*.md`, `.agents/docs/bugs/INDEX.md` — status obsolete
- `.agents/memory/` (gitignored, not committed) — `release-after-session-sized-work` removed by hand; no `memory` verb removes one

## Agents dispatched
none this part (the cold read's reviewer is in `changes/2026-10-24-2012-fyr-cold-read-and-old-plan-closed.md`).

## Verification
Each replaced section of `REQUIREMENTS.md` compared with the approved text: 0 mismatches. `gofmt -l cmd internal` empty, `go vet ./...` ok, and `go test -race -timeout 30m ./...` exit 0 with every package ok (`.agents/scratch/fyr/go-test-phase0.txt`). `sh-agent search --open` lists the fyr plan and BUG-139 only.

## Docs
`REQUIREMENTS.md` and `ROADMAP.md` (above). No how-it-works doc changed: the code still says `sh-agent` until Phases 1–4a, as ADR-062 states.

## Requirements
Rewritten under ADR-062 with the operator's approval.
