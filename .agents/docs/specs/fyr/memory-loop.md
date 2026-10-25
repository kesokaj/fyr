---
owner: main
date: 2026-10-24
---
# The memory loop — one store, and the `dreamer` agent

Status: built 2026-09-29 (plan `plans/2026-09-29-dreamer-memory-loop.md`); writing and loading moved to `fyr memory` on 2026-09-30 (R10, plan `plans/2026-09-30-parity-memory-dream.md`). The nightly background job is `specs/fyr/dream.md`.

## The store

- **Path:** `<repository root>/.agents/memory/`, inside the project and gitignored — one store per checkout (the operator's choice, "Option 2", 2026-09-29, over a harness-owned directory under `~/.config/fyr/`). Never committed (personal and unreviewed content) — though with the dream timer installed every memory file goes nightly, redacted, to the model provider of the CLI the manifest names (`specs/fyr/dream.md`), the one thing that leaves the machine (ADR-062).
- **Layout:** `MEMORY.md` (the index: one line per memory, `- [Title](file.md) — hook`; `add` refuses the 201st line, and `show` prints at most 200 lines and 25 KB) and one file per memory with the frontmatter `fyr memory add` writes (`name`, `description`, `metadata.node_type: memory`, `metadata.type` in `user` / `feedback` / `project` / `reference`, `metadata.modified`).
- **Who writes:** `fyr memory add` (`internal/memory`; serialised by a flock on `.agents/memory/.lock`, kernel-released), called by the `project-memory` skill (the four types and when to save) and by `feedback-log` rule 7 (a correction or re-stated preference). A file the verb writes carries no `originSessionId` (no CLI gives a shell its session id); `/dream`'s promote test counts it as one session. The root is the main worktree's top level, so worktrees share one store.
- **Who reads:** every session runs `fyr memory show` at the end of the Resume Protocol (context § Resume); it prints `MEMORY.md` capped at 200 lines and 25 KB and nothing else. `/dream` consolidates the store; its store is always `<root>/.agents/memory`. The `dreamer` agent reads it.
- **Set up by** `/init-project`, new and update paths (Step 5): add `.agents/memory` (no trailing slash, so the line also covers a symlink) to `.gitignore` before the store exists, then create the store (mode 700). Nothing else is written for memory. The store must be the project's own directory: a symlinked `.agents` or `.agents/memory`, or anything git tracks under it, stops `/init-project` and is refused by `/dream`, the `dreamer` and the capture rule.

## The `dreamer` agent

- **What it is:** the consolidation judgement of the learning loop, run by an author-independent reader: given the store and the trail, what should become a rule, a skill or a procedure, and what should be left alone. It proposes; it never applies.
- **Definition:** `agents/dreamer/agent.md` — tools `view_file, grep_search, find_by_name, run_command, write_to_file`; no `model` key (the CLI's default); installed as written like every agent (`delivery.md` § Agents).
- **Dispatch text (the contract):** `Store: <path>. Docs root: <docs-root>. Since: <date of the newest retros/*-retro*.md record, or "all">. Promoted: <the memory files /dream flagged promote, or none>.` plus the standard artifact and budget sentences. Never the conversation. The dreamer does not re-derive `/dream`'s verdicts: the promoted files are its rule-candidate inputs, one candidate row per memory file.
- **Reads:** the store (`MEMORY.md` and every memory file); the trail since the date — `handoffs/` (newest three), `bugs/INDEX.md` rows and their prevention rules, `changes/` entries; what already exists — the global context, the global rule files (`~/.gemini/config/rules/*.md`), the project `AGENTS.md` and `.agents/rules/`, the installed skills' descriptions (`fyr list` or the skill directories), so a candidate is never something a rule already says.
- **Candidate tests:** a **rule** for each file on the dispatch's `Promoted:` line (given, never re-judged: `/dream` owns the two-memory test), for a bug row whose prevention rule is recorded nowhere loaded and that recurs (two rows, or a row and a memory on the same fact), and for a memory of any type that a trail row restates (a handoff or `changes/` line on the same fact) when no loaded rule holds it; a **skill** when a memory or the trail shows a procedure of three or more steps done more than once (`/distill`'s bar); a **procedure** (a `references/` page for an existing skill) when the steps belong to a skill that exists; **none** otherwise, listed under "Left alone" with the reason.
- **Writes:** one artifact, `<docs-root>/retros/<date>-dreamer.md` (`owner: dreamer`, `date:`), written early with `Status: IN PROGRESS` and finished with `COMPLETE`: Scope, Summary, a Candidates table (`#`, kind, target file, the exact rule text or the draft's path, evidence as `file:line` and memory file names), "Left alone", "Not examined". Drafts for skills go under `.agents/scratch/dreamer/<date>/<name>/SKILL.md`. No other file, ever; no edit to source, config, rules, skills or memory.
- **Consumers:** `/retro` dispatches it (Step 1 source 8) and turns rule candidates into proposals through its own Step 2 (target, scope, promotion check) — the one place that decides where a rule lives; `/distill` dispatches it first and takes its skill and procedure candidates as candidates. No `retros.jsonl` line of its own; the retro or distill record cites the artifact.

## Error handling

| Situation | Behaviour |
|---|---|
| No store, or an empty one | `/dream` and the `dreamer` say so in one line; the resume clause reads nothing |
| `~/.config/fyr/` denied to the session | the reader says so; the `dreamer` artifact still writes into the project trail |
| Agent cut at its turn limit | a partial artifact with `Status: IN PROGRESS` (its early-write rule); the caller says so |
| `git clean -fdx`, a fresh clone, a deleted checkout | the store is gone with them, like scratch; there is no second copy — the operator's choice (ADR-043) |
| `.agents` or `.agents/memory` is a symlink, or git tracks something under it | `/init-project` stops and reports; `fyr memory add` and `show` (exit 2), `/dream` and the `dreamer` refuse the store. A symlinked `MEMORY.md` itself (the index must be the store's own file; `readLines`) is refused by `fyr memory add` and `show` only. |

## Known differences (R10)

Retired (plan `2026-10-24-fyr-agy-only.md` Phase 2): the harness targets one CLI, so there is no difference between CLIs to list. REQUIREMENTS.md R10 no longer cites this section; the limits of the one CLI (compaction window, cost, denials) are in REQUIREMENTS.md § Known limits.
