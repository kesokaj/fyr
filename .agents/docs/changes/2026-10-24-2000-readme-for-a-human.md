---
owner: main
date: 2026-10-24
---
# README.md rewritten for a human reader     (date: 2026-10-24)

## Summary
The README now opens with what the harness is and why it exists, then a quick start, the daily commands in one short table, the map with a plain-words paragraph under it, the three by-hand install routes, how to add skills, agents and rules, the skills by purpose, the layout and where to read more. The operator's words (2026-10-24, their own turn): "please write the README.md for a human. The current one is shit." Written for today's product (two CLIs, `sh-agent`); the fyr plan's Phase 4 renames it.

## Type
docs (Task; feedback log 2026-10-24, category output, signal correction)

## Files changed
- `README.md` — 296 → 232 lines. Kept byte for byte: the mermaid map (`TestHarnessMap` pins it; project `AGENTS.md` rule 3 forbids changing a line without an ADR), the legend sentence, and the commands of the three install chains (gh, `go install`, browser) that `release.go`'s messages and BUG-161 point at by section name. Kept by name: the headings `How it works`, `Daily commands`, `Install without cloning`, `Install from a zip (no SSH, no gh)`, `Skills` (pointers from `deploy/`, `testing/guide.md`, `usage_test.go`, the binary). Cut: the per-verb paragraphs (each verb's exact behaviour is its spec under `specs/sh-agent/`), the three-paragraph settings and permission prose (now four sentences), the resync-loop section (the map's paragraph and `deploy/remote.md` hold it), the "What gets installed where" table (folded into "Adding your own skills, agents and rules"), the BUG-numbered asides except BUG-078 (the one a reader acts on). The delivery claim is pointed at, not restated (rule 6).

## Agents dispatched
none (docs only; the diff read by the main agent).

## Verification
`go test -count=1 ./cmd/sh-agent -run TestHarnessMap` ok; `./internal/harness -run 'TestNoTmpInHarnessProse|TestRenderedContext'` ok; the root package ok; `grep -c /tmp README.md` 0. No behavioural run: no deployed file changed.

## Docs
This is the doc. The README map is unchanged.

## Requirements
unchanged.
