---
owner: main
date: 2026-10-24
---
# Handoff — 2026-10-24 — Phase 2b, the auto-compact backstop, done; the BUG-172 kit Task next in a fresh conversation

Session ID: e612aff4-6803-4225-8d71-31528ed08da1        (same machine: `claude --resume e612aff4-6803-4225-8d71-31528ed08da1`)
Project: /home/kesokaj/workspace/styrman-harness
Branch: main

## Context

The operator started this session from `handoffs/2026-10-23-1721-metric-context-target-done.md`'s Kickoff ("continue where you left off. Read the handoff"): Phase 2b — The auto-compact backstop — of plan `2026-10-23-session-sized-work.md`, nothing else. The phase is checkpointed at `447788b` (ADR-060 and the `REQUIREMENTS.md` edit in `154ced4` before it) and this session ends here.

## Completed

- `154ced4` (pushed): ADR-060; `REQUIREMENTS.md` § Non-goals gains the operator's approved sentence verbatim (D25; its final period became the semicolon before "never a permission, never another key"); `ROADMAP.md` Last reviewed.
- `447788b` (pushed): `sh-agent install` (vendor `all` or `claude`) writes `"autoCompactWindow": 300000` into `~/.claude/settings.json` when the top-level key is absent, through the same writer as the attribution key (`SettingSet` over `OwnedSettings`; `SettingsHint` prints one `note` per owned key the file does not set); D27 pinned (`TestAutoCompactWindowIsTheContextTarget`). D26 measured yes: Claude Code applies a `# Compact instructions` section from the imported global context (3 of 3 summaries, control 0 of 9), so the handoff skill's compaction clause moved under that heading at the end of `context/AGENTS.md` (14,983 of 15,000 bytes) and the skill points at it. README (three places), `deploy/local.md`, `deploy/remote.md`, memory-loop § Known differences (Antigravity: no compaction window), guide § 3, skill-matrix rows `autocompact-window` and `compact-instructions`.
- Verified: tests first (red on the new API, then green); `gofmt`, `go vet`, `go test -race -count=1 -timeout 30m ./...` exit 0 twice; two scratch homes; four sandbox rows on Claude Code 2.1.310 with Sonnet 5.5 — window (key at the 100K minimum) 9 compactions and peak 63,893 against a control with 0 compactions at a 430,760 peak over the same 24 reads; marker 3 of 3; the deployed clause 9 of 9 plan paths kept; `sh-agent metrics` agreed with every transcript number — `testing/2026-10-24-verify-autocompact.md`. Entry: `changes/2026-10-24-1924-autocompact-backstop.md`. Plan: Phase 2b's seven boxes ticked, D26 decided (this commit carries the Checkpoint tick naming `447788b`).

## Open / In Progress

- Next, from the Kickoff below: the BUG-172 kit change under option (a), a Task under the `bug` skill — before deleting the sandbox credential copy, a kit compares the token values with the real file's and, when they differ, keeps the copy and stops with its path; the real file is never written by a kit. Files: `testing/ab-kit/run.sh`, `testing/agy-rel/run.sh`, the session-sizing and autocompact kits (scratch, rebuilt from their logs), guide § 4, `kit-preamble.sh` if the check belongs there; the bug record closes when it lands. The WARNING fired 4 of 4 runs again this session.
- Queued behind it, each its own session: Phase 3 (EGM standards and the naive-reader lens), Phase 4 (`--drift`), Phase 5 under D10 (a), Phase 6 (Close; `security` is now due there, D22; the release box is the operator's).
- Open in the plan's D26 row: whether the context's `# Compact instructions` section shows in a real session's summary (the fixture had only a plan path to keep); first observable in Phase 6's E2E or any session that compacts.
- Requirements edits awaiting approval: none.

## Key Decisions

- ADR-060 (DECISIONS.md): the harness owns one more vendor key; the CLI-side window is the one monitor-and-trigger without a hook; a `/autocompact` value under `modelSettings`, `--autocompact` and `CLAUDE_CODE_AUTO_COMPACT_WINDOW` take precedence and are never touched; Antigravity has none.
- D26 decided from the plan's default (yes → the clause moves). To stay under the context's 15,000-byte pin, three pointer-only parentheticals left `context/AGENTS.md` (`(the \`project-memory\` skill saves to it)`, `(cadence: \`retro\` § When to Suggest)`, `(what counts: \`bug\` skill Step 0)`); the size test's own advice is to move a whole rule instead — the operator's call (Next Steps 2).
- Not done on purpose: any agent dispatch (Phase 6 covers the whole feature, D22), a real-home install, a run at 300,000 itself (the arms ran at the 100,000 minimum; the installer's write of 300,000 was asserted in each run's install step).
- Observed, not changed: at the 100K minimum with 17K-token files Claude Code's own guard ended the marker run after three compactions ("Autocompact is thrashing"); the Read tool returns about 611 lines of a 71 KB file, so each fixture file took two reads; with the key absent on a 1M model the control never compacted at 430K.

## Corrections

- none this session (no feedback log entry).

## Agent notes (unverified)

- The installed `handoff` skill in the real home still carries the pre-Phase-1 template (no `## Kickoff`) and the old compaction clause: the real home has not been updated since the last release. The repo's skill was followed.
- A dry run of `install` against an absent settings file prints two `[dry-run] would create` lines, one per owned key; a reader may prefer one line.

## Environment State

- Branch: main, clean after this handoff's commit; pushed.
- Running processes: none (the Monitor on the sandbox log expires on its own).
- Scratch (gitignored, deletable): `.agents/scratch/autocompact/` (the kit, the two Go logs), `.agents/scratch/autocompact-home/`; sandbox `~/sandbox/autocompact/` (fixture, four runs and homes, the scratch metrics store; no credential left, 4 of 4). Older: `.agents/scratch/{metrics-phase2,egm-review,remove-kickoff,session-sizing,home}/`, `~/sandbox/session-sizing/`.

## Artifacts to Read

- `.agents/docs/plans/2026-10-23-session-sized-work.md` — Phase 3's boxes are next after the BUG-172 Task; D26 row.
- `.agents/docs/bugs/BUG-172-sandbox-credential-link-replaced-by-claude-code.md` — the finding, the operator's option (a), the prevention rule.
- `.agents/docs/testing/2026-10-24-verify-autocompact.md` — the four rows and the kit shape the BUG-172 Task will touch.
- `.agents/docs/testing/kit-preamble.sh` and `testing/ab-kit/run.sh` — where the credential link is made and removed today.

## Next Steps

1. The BUG-172 kit Task, in a fresh conversation, from the Kickoff below.
2. Operator: say whether the three cross-reference parentheticals dropped from `context/AGENTS.md` stay dropped, or a whole rule moves to its skill instead (restoring them costs 125 bytes against 17 of headroom).
3. Then Phase 3, 4, 5, 6, each from its own Kickoff. The release owed at close stays Phase 6's who: user box (memory `release-after-session-sized-work`).

## Kickoff

````text
Project: /home/kesokaj/workspace/styrman-harness. Read AGENTS.md, then .agents/docs/handoffs/2026-10-24-1927-autocompact-backstop-done.md, then .agents/docs/bugs/BUG-172-sandbox-credential-link-replaced-by-claude-code.md; do: the BUG-172 kit change under option (a) — before a kit deletes its sandbox credential copy it compares the token values with the real file's and, when they differ, keeps the copy and stops with its path; the real file is never written by a kit — as a Task under the bug skill (testing/ab-kit/run.sh, testing/agy-rel/run.sh, kit-preamble.sh or the kits, guide § 4; the record closes when it lands).
Standing approvals (closed set): the Task's edits and the tests they name; the changes/ entry, commit and push; the closing handoff. Never: a who: user box, an Open: row, a release, a deploy, a real-home install, anything outside this repository. Ask only outside the set or on a critical failure.
Done when: the Definition of Done. Then write the handoff with the next Kickoff (Phase 3 — EGM standards and the naive-reader lens) and end the session.
````
