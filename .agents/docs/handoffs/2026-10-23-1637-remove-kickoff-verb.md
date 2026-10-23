---
owner: main
date: 2026-10-23
---
# Handoff — 2026-10-23 — remove the `sh-agent kickoff` verb (D34), then Phase 2

Session ID: e0707672-f357-4bf7-bd2b-bba4433b5725        (same machine: `claude --resume e0707672-f357-4bf7-bd2b-bba4433b5725`; matches the feedback log's session field)
Project: /home/kesokaj/workspace/styrman-harness
Branch: main

## Context

After Phase 1 closed (`handoffs/2026-10-23-1505-session-sizing-phase-1.md`, commits `af9f7c0`, `866f1bd`), the operator ran the one-command path the report named and it failed: their shell runs the installed release binary v2.7.0, which has no `kickoff` verb. They decided to remove the verb and keep the rest ("go approved 2", 2026-10-23; plan D34). This handoff replaces the previous one's Kickoff: the removal is one Task in a fresh session, and that session's handoff carries the Phase 2 Kickoff.

## Completed

- Nothing in code since `866f1bd`. Plan D34 added (this commit). Feedback log entries 2026-10-23T15:46:12+02:00 (the unverified command) and 2026-10-23T16:26:53+02:00 (the redirection); project memory `verify-installed-command-before-naming-it`.

## Open / In Progress

- The removal Task (Next Steps 1); nothing started.
- Open: D25, D10, D26 as in the previous handoff; BUG-172 (open) waits on the operator's option.
- Requirements edits awaiting approval: D25's sentence, as quoted in `handoffs/2026-10-23-1505-session-sizing-phase-1.md`.

## Key Decisions

- D34: the verb goes, the rule and the handoff's `## Kickoff` block stay; D28, D29, D32, D33 superseded; D3's "no verb" stands again.
- Rejected: fixing the version skew in the handoff skill's Step 7 instead (a sentence pointing at `go run`); the operator chose removal.

## Corrections

- "I ran it and it did not work." — feedback log 2026-10-23T15:46:12+02:00: a command named to the operator is verified by the shell that will run it; `go run` from the checkout is not that shell.
- "The kickoff skill is shit. I think we should remove it entirely and focus on all other stuff." — feedback log 2026-10-23T16:26:53+02:00 (redirection, D34).

## Agent notes (unverified)

- The verb's code passed its tests and judged the twelve sandbox runs; the failure the operator saw was the installed binary being behind `main`. Removal is a product decision, recorded as such in D34 and ADR-058a.

## Environment State

- Branch: main, clean after this handoff's commit; pushed.
- Running processes: none.
- Scratch (gitignored, deletable): `.agents/scratch/session-sizing/`, `.agents/scratch/home/`; outside the checkout `~/sandbox/session-sizing/`.

## Artifacts to Read

- `.agents/docs/plans/2026-10-23-session-sized-work.md` — D34 and the two Phase 1 verb boxes to mark `[-]`.
- `.agents/docs/changes/2026-10-23-1504-session-sizing-phase-1.md` — the files the verb touched, to reverse.
- `.agents/docs/testing/2026-10-23-verify-session-sizing.md` — the kit; its `check.sh` reads the Kickoff through the verb and must read the section directly after the removal.

## Next Steps

1. The removal Task (one session): delete `cmd/sh-agent/kickoff.go` and `kickoff_test.go`; in `main.go` the `kickoff` verb entry, the `local` field and its refusal block, the usage line and the package-comment line, and the verb-table comment back to its pre-Phase-1 wording; the four `kickoff` rows in `usage_test.go`; the README verbs-table row; the delivery spec's kickoff clause (`TestDeliverySpecCurrent`); the `kickoff` row in `sh-agent-matrix.md`; `skill-matrix.md`'s `session-sizing` check reads the newest handoff's `## Kickoff` section with awk instead of the verb; `rules/session-sizing.md` drops "(`sh-agent kickoff` prints it)" (size pins); `skills/handoff/SKILL.md` Step 7 item 2 becomes paste only (no one-command path, no headless loop) and the Writing Rules bullet drops the verb sentence; ADR-058a in `DECISIONS.md` (amends ADR-058: the verb removed, why, what stays); plan: D28, D29, D32, D33 marked superseded by D34, the two verb boxes `[-] superseded by D34`. Verify: `gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...`, scratch install and status, the two context rows on both CLIs (the rule text changes) with the kit's `check.sh` adjusted; `changes/` entry; commit; push; handoff whose Kickoff names Phase 2 — The metric.
2. Operator: D25, D10, BUG-172's option, one line each, in any session.

## Kickoff

```text
Project: /home/kesokaj/workspace/styrman-harness. Read AGENTS.md, then .agents/docs/handoffs/2026-10-23-1637-remove-kickoff-verb.md, then .agents/docs/plans/2026-10-23-session-sized-work.md; do: remove the sh-agent kickoff verb (plan D34) exactly as this handoff's Next Steps 1 lists it, nothing else.
Standing approvals (closed set): the phase's boxes marked who: AI and the tests they name; the checkpoint's changes/ entry, commit and push; the closing handoff. Never: a who: user box, an Open: row, a release, a deploy, a real-home install, anything outside this repository. Ask only outside the set or on a critical failure.
Done when: the Definition of Done for that Task — tests green, scratch install clean, the two context rows PASS on both CLIs, ADR-058a and the plan rows written, changes/ entry, commit, push. Then write the handoff with the next Kickoff (Phase 2 — The metric) and end the session.
```
