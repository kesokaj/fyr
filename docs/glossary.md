# Glossary — `fyr`

This glossary defines stores, hooks, and workflow terms in `fyr`. Specs and living records live in `brain.db`, not repo files.

## Stores and Records

Local SQLite stores hold records, indexes, and session telemetry.

| Term | Definition |
|---|---|
| `brain.db` | Store at `~/.config/fyr/brain.db` for project and global records, memory, proposals, and SIM. |
| `search.db` | Derived index at `.agents/scratch/search.db` (`--global`: `~/.config/fyr/search.db`) for FTS, AST graphs, and read ledgers (`fyr index --rebuild`). |
| `metrics.db` | Store at `~/.config/fyr/metrics.db` for session shapes (tools, verbs, tokens, evals, digests) without prose. |
| `trail.jsonl` | Optional repo export at `.agents/trail.jsonl`. Write with `fyr record export`; load with `fyr record sync`. |
| `SPEC-*` | Surface contract in `brain.db`, not a repo file. Read with `fyr record get SPEC-*` or list with `fyr record list --kind spec`. |
| `SIM` | Global Self-Improvement Memory ledger in `brain.db` tracking harness issues, fixes, and verdicts (`fyr auto sim list`). |

## Runtime, Hooks, and Workflows

These terms govern hook gates, worker leases, and component IDs.

| Term | Definition |
|---|---|
| `TokenKnee` | Calibrated context limit (40%–60% of window, cap 180k–300k tokens) where quality drops. Hooks warn here to trigger handoff. |
| `Kickoff` | Zero-question brief setting goal, leased files, checks, and constraints for an autonomous run. |
| `Files:` lease | Allowed path or glob list for a worker. `fyr gate` and `fyr land` reject diffs outside it. |
| `GANNET` | Canary word ending assembled context (`AGENTS.md`). Tests check it to confirm full load. |
| `M1–M26` | Component IDs in `SDD.md` traced in `TDD.md`: `M1`–`M4` harness content, `M5`–`M26` Go packages (`M9` merged; `M16`–`M18`, `M27` retired). |
| `VET_FIRST` | `PreInvocation` gate raised on `PRD.md`/`ADR-*` conflicts or vague prompts, pausing edits until clear. |
| `COORDINATE_SUBTASKS` | `PreInvocation` directive for multi-part edits, activating `[COORDINATOR_GUARD]` to block direct edits and dispatch subagents. |
| `DraftsOnly` | `fyr auto` mode using a scratch workspace without cloning git, writing JSONL drafts on dirty or non-git dirs. |

## Track 1–5 Documentation Taxonomy

`Track 1–5` separates human docs from agent files and SQLite records.

| Track | Scope |
|---|---|
| Track 1 | Git human docs (`README.md`, `SDD.md`, `TDD.md`, `Workflow.md`, `docs/`, `features/`), checked for STE (~80%). |
| Track 2 | Agent files (`AGENTS.md`, `context/`, `rules/`, `skills/`, `agents/`) and SQLite stores (`brain.db`, `search.db`, `metrics.db`). |
| Track 3 | Shared contracts (`PRD.md`, `TODO.md`, plus `SPEC-*` and `PLAN-*` in `brain.db`). |
| Track 4 | Ephemeral projections (`fyr search --slice`, `--contracts`, `--skeleton`), never committed. |
| Track 5 | Immutable trail in `brain.db` (`change`, `adr`, `handoff`, `review`, `retro`, `bug`, `testing`). |
