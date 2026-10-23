---
owner: main
date: 2026-10-23
---
# Handoff — 2026-10-23 — Phase 2, the peak-context metric, done; Phase 2b — The auto-compact backstop — next in a fresh conversation

Session ID: 5f812c94-bcbc-4ea2-95c5-57d957ddfc2b        (same machine: `claude --resume 5f812c94-bcbc-4ea2-95c5-57d957ddfc2b`; matches the feedback log's session field)
Project: /home/kesokaj/workspace/styrman-harness
Branch: main

## Context

The operator started this session from `handoffs/2026-10-23-1705-remove-kickoff-verb-done.md`'s Kickoff: Phase 2 — The metric — of plan `2026-10-23-session-sized-work.md`, nothing else. The phase is checkpointed at `3d2d5f9` and this session ends here.

## Completed

- `3d2d5f9` (pushed): `sh-agent metrics show` and `report` count the main sessions whose peak context passed the context target (300,000 tokens, or `--context-target N`) as `k of n` per CLI, n the sessions with a peak at all; `show --json` carries `context_target`, `sessions_over_context_target` (null with a note for a vendor with no such session, never 0) and `session_peaks` (id, start day, peak; largest first; main sessions only, under the replay dedupe); the dream's numbers summary carries the target and the count, not the list; `ingest` and `export` refuse the flag; a non-positive value is a usage error. Spec `metrics.md` (§ Verbs, flag table, § Output, § Report, B3, failure modes) and README's two metrics rows say so.
- Verified: tests first (red on the missing field, then green); `gofmt`, `go vet`, the § 3g tier and `go test -race -count=1 -timeout 30m ./...` exit 0; real data from copies: ingest twice (second adds 0), `show --json --since 2026-09-01` 33 of 54 Claude Code sessions over 300K, equal to an independent count over the transcript copy with the same top three; the report's row in headless Chromium light, dark and 400 px (`.agents/scratch/metrics-phase2/report-{light,dark,400}.png`); the copy deleted — `testing/2026-10-23-verify-metric-context-target.md`. Entry: `changes/2026-10-23-1720-metric-context-target.md`. Plan: Phase 2's four boxes ticked (this commit carries the tick naming `3d2d5f9`).

## Open / In Progress

- Phase 2b — The auto-compact backstop: next, from the Kickoff below; nothing started. Its first box (who: user, D25 answered) is already answered by the plan's D25 row ("D25 approved as written", 2026-10-23): the next session ticks it from that row and goes on; the `REQUIREMENTS.md` edit, ADR-060 and the `ROADMAP.md` line land in ONE commit, the approved text verbatim.
- Queued behind it, each its own session: the BUG-172 kit change under option (a) (a Task under the `bug` skill: `testing/ab-kit/run.sh`, `testing/agy-rel/run.sh`, the session-sizing kit, guide § 4); Phase 3; Phase 4; Phase 5 under D10 (a); Phase 6 (Close), whose release box is the operator's.
- Open: D26 (whether Claude Code applies `# Compact instructions` from the imported global context) — measured in Phase 2b.
- Requirements edits awaiting approval: none (D25's sentence is approved; it is applied in Phase 2b's session).

## Key Decisions

- No new ADR: the metric's shape is the plan's Interfaces (D4, D6, D12, D15, D16). Two choices inside them, recorded in the changes entry: n in `k of n` is the main sessions that have a peak; the human table carries the count as its own row under `peak context`.
- Not done on purpose: behavioural runs (no context, agent or skill file changed), any agent dispatch (Phase 6 covers the whole feature; D22), a real-home install or ingest.
- Observed, not changed: with no Antigravity session in the window the report still draws an Antigravity line at 0 on the per-day charts and shows `0` for its main sessions and peak context — as before this change; a candidate for Phase 6's review, not this phase.

## Corrections

- none this session (no feedback log entry).

## Agent notes (unverified)

- This box's one Antigravity conversation has no step timestamps and no `gen_metadata`, so its over-target count is null here; the number path is pinned by `TestShowContextTarget` only. A box with real Antigravity sessions is where the agy number is first seen on real data (Phase 6's E2E log, or the operator's own `metrics show`).
- `/usr/bin/go` go1.24.4 ran the checks; `~/.local/bin/go` (the go1.27.2 SDK `AGENTS.md` names) is a dangling symlink on this shell.

## Environment State

- Branch: main, clean after this handoff's commit; pushed.
- Running processes: none.
- Scratch (gitignored, deletable): `.agents/scratch/metrics-phase2/` (the built binary, the two Go logs, `show-since-0901.json`, `independent_count.py`, `report.html`, the three screenshots); the transcript copy `.agents/scratch/metrics-home/` is already deleted. Older: `.agents/scratch/{egm-review,remove-kickoff,session-sizing,home}/`.

## Artifacts to Read

- `.agents/docs/plans/2026-10-23-session-sized-work.md` — Phase 2b's boxes, D25 (approved text), D26 (open), D27.
- `REQUIREMENTS.md` § Non-goals third bullet — where D25's sentence is appended, after the memory-keys clause.
- `internal/harness/settings.go` and its test — the attribution writer Phase 2b generalises.
- `.agents/docs/specs/sh-agent/memory-loop.md` § Known differences — gains Antigravity's missing compaction window.

## Next Steps

1. Phase 2b — The auto-compact backstop, in a fresh conversation, from the Kickoff below.
2. After it, each from its own Kickoff: the BUG-172 kit Task, Phase 3, Phase 4, Phase 5, Phase 6. The release owed at close stays Phase 6's who: user box (memory `release-after-session-sized-work`).

## Kickoff

```text
Project: /home/kesokaj/workspace/styrman-harness. Read AGENTS.md, then .agents/docs/handoffs/2026-10-23-1721-metric-context-target-done.md, then .agents/docs/plans/2026-10-23-session-sized-work.md; do Phase 2b — The auto-compact backstop only.
Standing approvals (closed set): the phase's boxes marked who: AI and the tests they name; the checkpoint's changes/ entry, commit and push; the closing handoff. Never: a who: user box, an Open: row, a release, a deploy, a real-home install, anything outside this repository. Ask only outside the set or on a critical failure.
Done when: a fresh Claude Code install compacts at the window the harness set unless the operator overrode it; the spec and README say so; Antigravity's gap named. Then write the handoff with the next Kickoff and end the session.
```
