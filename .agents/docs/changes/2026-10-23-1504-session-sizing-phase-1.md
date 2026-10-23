---
owner: main
date: 2026-10-23
---
# Session-sized work, Phase 1: the rule, the kickoff, the sizing, the kickoff verb     (date: 2026-10-23)

## Summary
A session is now one Task or one plan phase: `rules/session-sizing.md` (the second always-on rule on both CLIs) ends every session at its checkpoint with a handoff whose `## Kickoff` block starts the next, `sh-agent kickoff` prints that block so one shell command starts the next session, the feature skill sizes a phase to one fresh session and asks its cold reader a fourth question, and the handoff skill's report is the Kickoff. ADR-058. Plan `2026-10-23-session-sized-work.md` Phase 1, boxes 1–10 ticked.

## Type
feature (Phase 1 of 6)

## Files changed
- `rules/session-sizing.md` — new, 1,158 bytes; the rules block is 18,521 of 22,000 bytes
- `skills/handoff/SKILL.md` — the template's `## Kickoff` (outer fence four backticks), a Writing Rules bullet (closed approvals, the nothing-queued line, the unscaffolded case, prose as a fallback), Step 7 rewritten (paste, `claude "$(sh-agent kickoff)"` / `agy -i "$(sh-agent kickoff)"`, the headless loop, the session ends)
- `skills/feature/SKILL.md` — the Phases note in the plan template, "(one session)" on the phase heading, the cold-read brief's (4) Sizing and the naive-reader clause under (1), the Step 4 checkpoint ending in the handoff and the session's end
- `cmd/sh-agent/kickoff.go`, `kickoff_test.go` — the verb: the checkout's docs root, the newest handoff by its stamp, the fenced block or (D33) the section's text, nothing queued as a plain error (exit 1, stdout empty)
- `cmd/sh-agent/main.go` — the `kickoff` verb entry, `local` on the verb struct and its refusal of `--home`/`--repo`/`--vendor` before the verb runs, the usage line, the package comment's line, the verb-table comment (a consult verb draws no map line); `usage_test.go` four rows
- `context/AGENTS.md` — DoD step 5 ends the session (`rules/session-sizing.md`), 14,936 of 15,000 bytes
- `AGENTS.md` (Layout: both rule files), `README.md` (the verbs table row, the rules line, the `handoff` skill row)
- `.agents/docs/architecture/DECISIONS.md` — ADR-058
- `.agents/docs/specs/sh-agent/delivery.md` — the verb named as on `main` and in no tag (`TestDeliverySpecCurrent`)
- `.agents/docs/testing/sh-agent-matrix.md` (the `kickoff` row), `skill-matrix.md` (`session-sizing`, `session-sizing-context`), `2026-10-23-verify-session-sizing.md` (the log)
- `.agents/docs/bugs/BUG-172-….md` and `INDEX.md` — the credential link finding (open)
- `.agents/docs/plans/2026-10-23-session-sized-work.md` — ticks, D31 (the rule's second-ask and Trivial wording), D32 (`agy -i`), D33 (a prose Kickoff is printed)

## Agents dispatched
none (the plan's Phase 6 dispatches `verifier` and `reviewer` over the whole feature). The diff was read by the main agent: `git diff --numstat`, 17 files.

## Verification
`gofmt -l cmd internal` empty, `go vet ./...` ok, `go test -race -count=1 -timeout 30m ./...` exit 0 (every package ok; the first run found two doc pins the verb owed, fixed); scratch-home install and status clean, both rule files deployed to both vendors; the verb exercised with a development and a release-style build; behavioural runs on both CLIs, batch 2: Claude Code context 1 of 1, rule 3 of 3, control 2 of 2; Antigravity context 1 of 1, rule 3 of 3, control 1 of 1, every kit log with the preamble's three lines — `testing/2026-10-23-verify-session-sizing.md`.

## Docs
README (verbs table, rules line, handoff row), the two skills (their own docs), the delivery spec's verb list, the two matrices; the deployed global context in any real home changes at that home's next `sh-agent update` (not run here).

## Requirements
unchanged. D25 (the `autoCompactWindow` sentence) and D10 (reviewer isolation) stay open; Phases 2b and 5 wait on them.
