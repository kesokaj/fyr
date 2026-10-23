---
owner: main
date: 2026-10-23
---
# The `sh-agent kickoff` verb removed (plan D34, ADR-058a)     (date: 2026-10-23)

## Summary
The verb ADR-058 (3) added on 2026-10-23 is gone before it reached a tag: the operator's shell runs the installed release binary, which never had it, and a verb in the binary skews against a Kickoff in the handoff whenever a place is behind the release; the paste has no such gap. The rule, the handoff's `## Kickoff` block, the phase sizing and the context target stay (ADR-058a). Plan D28, D29, D32, D33 superseded by D34; D3's "no verb" stands again. The operator's three decisions from the kickoff turn are recorded, not acted on: D25 approved as written, D10 option (a), BUG-172 option (a).

## Type
removal (one Task inside the open plan `2026-10-23-session-sized-work.md`)

## Files changed
- `cmd/sh-agent/kickoff.go`, `kickoff_test.go` — deleted (173 and 159 lines)
- `cmd/sh-agent/main.go` — the `kickoff` entry, the `local` field and its global-flag refusal block, the usage line, the package-comment line, the verb-table comment back to its pre-Phase-1 wording; `usage_test.go` — the four `kickoff` rows
- `README.md` — the verbs-table row; `.agents/docs/specs/sh-agent/delivery.md` — the kickoff clause of § Where things stand (`TestDeliverySpecCurrent`); the "2 rules on `main`" parenthesis stays
- `.agents/docs/testing/sh-agent-matrix.md` — the `kickoff` row; `skill-matrix.md` — the `session-sizing` check reads the newest handoff's `## Kickoff` section with awk instead of the verb
- `rules/session-sizing.md` — drops "(`sh-agent kickoff` prints it)": the Antigravity rules block is 18,490 of 22,000 bytes, the hand-written context 14,936 of 15,000 (unchanged)
- `skills/handoff/SKILL.md` — Step 7 item 2 is paste only (no one-command path, no headless loop; "no command prints the block, ADR-058a"); the Writing Rules bullet drops the verb-and-prose-fallback sentence
- `.agents/docs/architecture/DECISIONS.md` — ADR-058a (amends ADR-058: (3) withdrawn, why, what stays)
- `.agents/docs/plans/2026-10-23-session-sized-work.md` — the two verb boxes `[-] superseded by D34`; D28, D29, D32, D33 marked superseded; D3's note; the E2E golden path reads "(pasted)"; `Open: D25` and `Open: D10` become decided rows quoting the operator's words (the REQUIREMENTS edit, ADR-060 and Phase 5's who: user box are left for their own sessions); D26 stays open
- `.agents/docs/bugs/BUG-172-….md` — § Fix records the operator's choice (a); status stays open until the kit change lands
- `.agents/docs/testing/2026-10-23-verify-remove-kickoff-verb.md` — the log

## Agents dispatched
none (the plan's Phase 6 dispatches `verifier` and `reviewer` over the whole feature). The diff was read by the main agent: `git diff --numstat`, 13 files, 23 insertions, 374 deletions.

## Verification
`gofmt -l cmd internal` empty, `go vet ./...` exit 0, `go test -race -count=1 -timeout 30m ./...` exit 0 (every package ok); scratch-home install `5 written, 1787 unchanged`, `status` binary line without a warning, drift none, both rule files in both vendors' rules directories; the `session-sizing-context` row 1 of 1 on Claude Code and 1 of 1 on Antigravity from homes installed from this tree, kit preamble 3 of 3 lines each — `testing/2026-10-23-verify-remove-kickoff-verb.md`.

## Docs
README verbs table, the delivery spec, the two matrices, the rule and the handoff skill (their own docs). The README map is unchanged: the verb drew no line (ADR-058, ADR-058a). The deployed context in any real home changes at that home's next `sh-agent update` (not run here).

## Requirements
unchanged in this commit. D25's `REQUIREMENTS.md` sentence is approved by the operator (2026-10-23) and lands with ADR-060 in Phase 2b's session, in one commit.
