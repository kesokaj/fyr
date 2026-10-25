---
owner: main
date: 2026-10-25
---
# Brain Phase 1 checkpoint closed; Phase 2's import and memory alias built and reviewed     (date: 2026-10-25)

## Summary
Phase 1's checkpoint: D24–D27 record the build decisions and settle the handoff's three contradictions; `security` over the store found 8 (2 medium: a planted `trail.jsonl` could exhaust memory, titles had no cap), all fixed but one rebutted, then a round-2 `reviewer` over the fixes (nits, fixed). Phase 2: `fyr record import` (D8's keep-and-closure, D13's printed removal list), `import --global`, `fyr memory` over `memory` records (D14), the file index no longer lists `.agents/trail.jsonl` (records showed twice), and the kit moved to `.agents/kit/` (D17). Phase 2's code got a `reviewer` and a `security` pass (round 1: 1 high, several medium) and a round-2 `security` pass (5 low), each finding fixed with a mutation-checked test or rebutted in its report's `## Outcome`.

## Type
task

## Files changed
from the dispatch's git log --stat ca246a5..76327b8
- internal/brain/export.go — the load streams (`readRecords`, `hashTrail`, `capped`), line and file caps, `O_NOFOLLOW` open, title and control-character rules, the unreachable constraint branch removed
- internal/brain/record.go, brain.go, store.go, search.go — 512-byte titles, C0 controls refused, export filter built from the map, full-root `--global` labels
- internal/brain/import.go (new) — `Import`, `ImportGlobal`, `readRegular`, `checkRecord`, `budget`, `ShellQuote`
- internal/brain/memory.go (new) — `MemoryAdd`, `MemoryShow`
- internal/brain/*_test.go — security_test.go, import_test.go, import_security_test.go
- internal/memory/memory.go, memory_test.go — `Add`/`Show` removed; `Root`/`Store` stay for the dream until Phase 4
- internal/search/reconcile.go — the index skips `.agents/trail.jsonl`
- cmd/fyr/main.go, record.go — `record import`, the memory alias, filtered errors, usage text
- .agents/kit/agy-rel/run.sh, .agents/kit/kit-preamble.sh — moved from `.agents/docs/testing/`, repo depth fixed
- internal/harness/catalog_test.go — the kit's new path

## Agents dispatched
security (×3), reviewer (×2), verifier (×3: one stopped, one FAIL on coverage, one at 76327b8)

## Verification
`testing/2026-10-25-verify-brain-phase2-ddaa522.md` (FAIL: one uncovered function, removed as unreachable); the run at 76327b8 is the checkpoint's (plan Phase 1 Checkpoint box). Scratch-home install: `status` binary line clean (8ab2bf9).

## Docs
`specs/fyr/record.md` (§ Import, § Memory, the load's caps, search labels), `specs/fyr/search.md` (labels), `specs/fyr/memory-loop.md` (the store), `.agents/docs/testing/guide.md` and `skill-matrix.md` (kit path), root `AGENTS.md` (kit path).

## Deviations
- The kit's `git mv` rode along in `f28e8bc` before its path edits; the preamble then resolved the harness checkout to the repository's parent, and a local kit-test run wrote `bin/fyr` into the repo and was SIGTERMed by the preamble's exit trap. Fixed in 726138d; a bug record follows the migration.
- The verifier on ddaa522 ran one `fyr` call without `--home`, which moved the operator's real `~/.config/sh-agent` to `~/.config/fyr` (its log, step 4). Not restored: outside this session's approval. The operator's command is in the handoff; a bug record follows the migration.

## Requirements
R4, R7, R8 (brain plan Phase 0's text).
