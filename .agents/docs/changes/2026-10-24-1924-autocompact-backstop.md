---
owner: main
date: 2026-10-24
---
# The auto-compact backstop: `install` writes Claude Code's `autoCompactWindow` (plan session-sized work, Phase 2b)     (date: 2026-10-24)

## Summary
`sh-agent install` (vendor `all` or `claude`) now writes `"autoCompactWindow": 300000` into `~/.claude/settings.json` when the top-level key is absent, beside the attribution key it already owned, so a fresh Claude Code install compacts at the metric's context target instead of at the model's own limit (967K on a 1M model); `status` prints a note per owned key the file does not set. ADR-060; the `REQUIREMENTS.md` § Non-goals sentence the operator approved on 2026-10-23 (D25) landed verbatim in `154ced4` with the ADR and the `ROADMAP.md` line. D26 measured: Claude Code applies a `# Compact instructions` section from the imported global context (3 of 3 summaries, control 0 of 9), so the handoff skill's compaction clause moved under that heading in `context/AGENTS.md`. Antigravity has no equivalent, recorded in memory-loop § Known differences.

## Type
feature (plan `2026-10-23-session-sized-work.md`, Phase 2b; the BUG-172 kit Task, Phases 3, 4, 5 and 6 remain)

## Files changed
- `internal/harness/settings.go` — `Setting{Key, Fragment, Why, unmet, satisfied}`; `Attribution`, `AutoCompact` (`AutoCompactWindow = 300000`), `OwnedSettings` in write order; `SettingSet(home, s, dryRun, out)` replaces `AttributionSet` with the same insertion, symlink, linked-directory, dry-run and advisory rules; `SettingsHint` replaces `AttributionHint`: the file read once (`readSettingsObject`), one line per unmet key, one shared line when the file cannot be judged; `attributionOff` now judges a raw value
- `cmd/sh-agent/main.go` — install loops over `OwnedSettings` then `SettingsHint`; status calls `SettingsHint`; the `installFiles` comment
- Tests: `internal/harness/settings_test.go` rewritten (`TestSettingSetAutoCompact`, `TestSettingsHintNamesEachAbsentKey`, `TestOwnedSettingsIsTheClosedSet`, `TestSettingSetRefusesALinkedClaudeDir` over both keys; the attribution cases renamed onto the new API); `cmd/sh-agent/main_test.go` (`TestAttributionNote`'s shared problem line, `TestAttributionSet` expects two keys); new `cmd/sh-agent/settings_test.go` (`TestAutoCompactWindowIsTheContextTarget`, D27)
- `context/AGENTS.md` — `# Compact instructions` section (last, after Project layout; D26); the Checkpoints paragraph's pointer to `handoff` § Usage removed; three pointer-only parentheticals removed to stay under the 15,000-byte pin: `(the \`project-memory\` skill saves to it)`, `(cadence: \`retro\` § When to Suggest)`, `(what counts: \`bug\` skill Step 0)` — 14,983 bytes
- `skills/handoff/SKILL.md` — § Usage: the compaction clause becomes a pointer to the context's section with the measurement; the "moved" note
- `README.md` (the install paragraph, the `~/.config/sh-agent` paragraph's exception, the `status` row), `.agents/docs/deploy/local.md` (the install/status sentence), `.agents/docs/deploy/remote.md` (the per-machine check), `.agents/docs/specs/sh-agent/memory-loop.md` § Known differences (the auto-compact row), `.agents/docs/testing/guide.md` § 3 (the scratch-home check lists both keys), `.agents/docs/testing/skill-matrix.md` (rows `autocompact-window`, `compact-instructions`)
- `.agents/docs/architecture/DECISIONS.md` (ADR-060), `REQUIREMENTS.md` § Non-goals (the approved sentence; its final period became the semicolon before "never a permission, never another key", the one punctuation join), `ROADMAP.md` (Last reviewed) — all in `154ced4`
- `.agents/docs/testing/2026-10-24-verify-autocompact.md` — the log; the plan's Phase 2b boxes 1–6 ticked with evidence, D26 decided

## Agents dispatched
none (the plan's Phase 6 dispatches `verifier`, `reviewer` and, now that Phase 2b landed, `security` over the whole feature; D22). The diff was read by the main agent: `git diff --numstat` over the Go and doc files before the trail (11 files, 120/61 in `settings.go`, 276/89 in its test, 7/4 in `main.go`) and the context and skill edits as they were made.

## Verification
`gofmt -l cmd internal` empty, `go vet ./...` exit 0, `go test -race -count=1 -timeout 30m ./...` exit 0 twice (after the Go changes; after the context, skill and matrix changes — `go-test-2.txt`, cited in the Checkpoint box). Tests first (red on `undefined: OwnedSettings`, then green). Scratch homes: an existing one gained the key beside its attribution key, a fresh one got both keys with the formatting the tests pin, `status` without a warning or a note. Four sandbox rows on Claude Code 2.1.310, Sonnet 5.5: window 9 compactions and peak 63,893 against a control with 0 and 430,760 over the same 24 reads; marker 3 of 3 summaries, control 0 of 9; the deployed clause 9 of 9 plan paths kept; `sh-agent metrics` agreed with the transcripts on every row; kit preamble 3 of 3 lines each; BUG-172's WARNING 4 of 4 as before — `testing/2026-10-24-verify-autocompact.md`.

## Docs
As above. The README map is unchanged: a vendor setting is no delivery path (project `AGENTS.md` rule 3 does not apply; ADR-060 says so). The deployed key reaches a real home at that home's next `sh-agent update` after a release (Phase 6's who: user box); the deployed context changes the same way.

## Decisions
ADR-060 (the harness owns one more vendor key; the CLI-side window is the one monitor-and-trigger without a hook; Antigravity has none). D26 decided in the plan (yes; the clause moved). D27 pinned by a test. Two choices inside the plan's wording: install prints two `would create` lines on a dry run against an absent file (one per key, the file absent for both); the context stayed under its pin by dropping three cross-reference parentheticals rather than moving a whole rule — the operator may reverse that (handoff Next Steps).

## Requirements
`REQUIREMENTS.md` § Non-goals edited in `154ced4` with the operator's approved text (D25, "D25 approved as written", 2026-10-23), ADR-060 and `ROADMAP.md` in the same commit.
