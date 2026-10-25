---
owner: main
date: 2026-10-25
---
# BUG-175 fixed: project-memory's date line, no command substitution (plan fyr D27)

## What changed
- `skills/project-memory/SKILL.md:50`: the agent now runs `date +%F` as its own command first and types the date it printed into the memory body. The save is the one `sh-agent memory add` command, with nothing joined to it and no `$( )` inside it. Before, the line said "Write dates as `YYYY-MM-DD` from `date +%F`", and on agy 1.4.7 the model ran `TODAY=$(date +%F) && sh-agent memory add …`, which the grant store refused, so nothing was saved.
- `bugs/BUG-175-*`: `status: fixed`, plus an addendum. `bugs/INDEX.md`: the row is now fixed.
- `plans/2026-10-24-fyr-agy-only.md`: D27, the operator's approval ("Go all approved", 2026-10-25, item 2).
- `testing/2026-10-25-verify-fyr-content.md`: an addendum with the re-run. On the final tree 13 of 13 rows pass.
- `reviews/2026-10-25-review-bug175-project-memory-line.md`: the review, with its `## Outcome`.

## Verification
- `project-memory` row on agy 1.4.7 with named grants (`.agents/scratch/fyr/kit3c/run.sh`, tags 3d and 3e): **PASS 2 of 2**.
  - In both, the conversation store shows `date +%F` as its own step, then `sh-agent memory add`. Every step has status 3 and none has `error_details`.
  - The memory and its index line were written. Session 2 recalled "Friday" with the store present.
  - The preamble printed its three clean lines.
- `gofmt -l cmd internal` printed nothing. `go vet ./...` and `go test -race -count=1 -timeout 30m ./...` exit 0 on all 9 packages (go1.24.4; `.agents/scratch/fyr/go-test-bug175.txt`). The size pins are among those tests; the skill's description is unchanged, so the listing budget is unchanged.

## Agents dispatched
- `reviewer` (code and docs lenses, given the diff only): APPROVE WITH NITS.
  - Finding 1, the context's "same call" date rule against the skill's separate `date +%F`: deferred to the operator.
  - Finding 2, `feedback-log` rule 7: rebutted.

## Not covered
- `feedback-log` rule 7's save under named grants. Its row runs with `command(*)`.
- Other skills' `$(date …)` forms (`handoff`, the verifier's log name) under the grant store. Their rows passed and none was refused, but they were not probed one by one.
