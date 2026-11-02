# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Item 3 — macOS test suite, of `PLAN-2026-11-02-1806-fyr-speed` (make building with fyr faster without lowering quality):

- [ ] Item 3 — macOS test suite — verify: unit, `gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...`, who: AI — expect: `gofmt -l cmd internal` prints nothing, and `go vet ./...` and `go test -race -timeout 30m ./...` exit 0 on this Mac, in a `verifier` PASS; each real bug has a bug record (numbered from BUG-502) and a test that fails without its fix; `reviewer` read the diff

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 4 — a fresh eval suite in a tracked file (D2) and the v7.0.0 baselines, before any skill or agent edit.
- Item 9 — worker records reach the `/work` lead (BUG-500); its work-skill edit is measured against item 4's work row.
- Item 5 — `--body-file` and `--out` for the record and memory verbs, with the handoff's `--id` (BUG-501).

## Backlog

- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
