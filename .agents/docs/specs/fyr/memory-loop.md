---
owner: main
date: 2026-10-24
---
# The memory loop — one store, and the `dreamer` agent

Status: built 2026-09-29 (plan `plans/2026-09-29-dreamer-memory-loop.md`); writing and loading moved to `fyr memory` on 2026-09-30 (R10, plan `plans/2026-09-30-parity-memory-dream.md`). The nightly background job is `specs/fyr/dream.md`.

## The store

- **Where:** `memory` records in the brain store, `~/.config/fyr/brain.db`, in the project's scope (the main worktree's root, so every worktree shares one; `specs/fyr/record.md` § Memory). Never exported to `.agents/trail.jsonl`, never committed, local to the machine (brain plan D14, ADR-063). Since brain plan Phase 2 (2026-10-25) it replaces the old per-checkout `.agents/memory/` directory, which `fyr record import` (project) and `import --global` (every indexed project) migrate.
- **Shape:** one record per memory, ID `MEMORY-<name>`, the description as its title, the body `type: <user|feedback|project|reference>`, a blank line, then the text.
- **Who writes:** `fyr memory add --type T --name SLUG --description TEXT` (body on stdin; the same name replaces), called by the `project-memory` skill and by `feedback-log` rule 7. Serialised by the store's write transaction.
- **Who reads:** every session runs `fyr memory show` at the end of the resume order; it prints one line per memory, capped at 200 lines and 25 KB. `fyr record get MEMORY-<name>` prints one in full. `/dream` consolidates them; the `dreamer` agent reads them.
- **Lost:** with `brain.db`. Memory has no second copy, as before (R4, R7).

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
| `git clean -fdx`, a fresh clone, a deleted checkout | the memory stays: it is in `~/.config/fyr/brain.db`, keyed by the checkout's root, not in the checkout |
| `brain.db` lost | the memory is lost with it; there is no second copy (R4) |
| an old `.agents/memory` that is a symlink | `fyr record import` refuses it; nothing else reads that directory any more |

## Known differences (R10)

Retired (plan `2026-10-24-fyr-agy-only.md` Phase 2): the harness targets one CLI, so there is no difference between CLIs to list. REQUIREMENTS.md R10 no longer cites this section; the limits of the one CLI (compaction window, cost, denials) are in REQUIREMENTS.md § Known limits.
