---
name: fyr-project-memory
description: "Save/recall durable operator preferences (fyr memory add/show) & passively log correction feedback (/fyr-project-memory). Not rules."
---

# Project memory

A project's memory is small notes about the user, workflows, and the project that a future session should know. It lives in `fyr`'s brain store (`~/.config/fyr/brain.db`) as `memory` records in the project's scope (the main worktree's root, so every worktree shares it) or `--global` shared cross-project scope (valid inside or outside a git checkout), one record `MEMORY-<slug>` per memory. It is per machine and never exported or committed. It is not private from the model, though: where `fyr auto` is turned on (`fyr auto on`, running metrics ingest, the hourly dream job, and learning workers), every memory is sent, redacted, to the model of the CLI the machine uses (harness `PRD.md` R8); nothing else sends it anywhere. So save nothing you would not show that model. Every session writes it and reads it only through `fyr`.

## Load at session start

Run `fyr memory show` once, as the resume step says (`--global` for shared cross-project memory; `--include-low` to show cards with validity `< 0.40`). It prints the index: one line per memory, `- <slug> (<type>) — description`. Read a memory in full (`fyr record get MEMORY-<slug>`) only when its line is relevant to the task.

- **Progressive loading:** `fyr resume` loads active (`validity >= 0.40`) `semantic` memories (project and `--global`) at session start; `procedural` and `episodic` memories are injected on demand in `PreInvocation` when matching the current turn, keeping Turn-0 context lean.
- **Deterministic validity (`0.00–1.00`):** `fyr` computes validity deterministically in Go (`operator` base `1.00`, `derived` base `0.55` up to `0.70`, penalized by stale/changed file anchors `-0.45`, newer contradicting `feedback` records `-0.25` each up to 2, and half-life decay: `semantic` 180d, `procedural` 90d, `episodic` 30d). Cards `< 0.40` are hidden from `show`, `resume`, and `PreInvocation` unless `--include-low` is passed.

A memory is what was true when it was written. Before acting on one that names a file, a function or a flag, check that the thing still exists.

## When to save

Save with `--type semantic | procedural | episodic` when one of these comes up and nothing already loaded records it (legacy aliases `user`, `project`, and `reference` normalize to `semantic`; `feedback` normalizes to `procedural`):

| Type | What | Examples |
|---|---|---|
| `semantic` | Durable user/project facts, constraints, deadlines, external references (loaded at `resume`) | "I review every architectural choice", absolute deadline, ticket queue URL |
| `procedural` | Corrections, confirmed rules, or reusable how-to procedures (injected on demand in `PreInvocation`) | "don't mock the database in these tests", "yes, keep doing it that way" |
| `episodic` | Recent session outcomes, observed quirks, or short-lived debugging episodes (30d half-life, injected on demand) | "staging migration timed out without --batch during v3 backfill" |

Pass `--global` on `add` or `show` for shared cross-project knowledge that applies across repositories (works inside or outside a git checkout).

Also save when the user asks you to remember something. A correction is logged via § Passive correction logging below, which writes the global `feedback` record and calls the same `fyr memory add` verb (Behavior Rule 7), so it is not saved twice.

Do not save:
- what `AGENTS.md`, a rule, a skill, a bug record or git history already says;
- what only matters to this conversation;
- a secret, a token, a password or a personal detail the user did not ask you to keep.

When the user asks you to remember something that is one of those, ask what was non-obvious about it and save that instead.

## How to save

Write the body to `.agents/scratch/memory-<slug>.md` with the file tool, then store it:

```markdown
Integration tests hit a real database, never a mock.

**Why:** a mocked test passed while the real migration failed (the user, 2026-09-30).

**How to apply:** in any test under internal/store, start the test database; never add a mock driver.
```

```bash
fyr memory add --type procedural --name no-db-mocks --description "integration tests hit the real database" --body-file .agents/scratch/memory-no-db-mocks.md
```

- `--name` is a short kebab-case slug, the record's name (`MEMORY-<slug>`): `^[a-z0-9][a-z0-9-]{0,63}$`.
- `--description` is one line of at most 512 bytes: what a future session scans in the index to decide whether to open the memory.
- The body is one fact. For `procedural` and `semantic` project conventions, follow it with a `**Why:**` line and a `**How to apply:**` line. Write dates as `YYYY-MM-DD`, never "next week": run `date +%F` as its own command first and type the date it printed into the body. The save is the one `fyr memory add` command above, nothing joined to it and no `$( )` inside it, because a headless permission check refuses a command substitution.
- Saving under an existing slug replaces that memory. Update a memory rather than adding a second one on the same fact; `fyr memory show` lists what exists (`--include-low` shows cards `< 0.40`).
- `--provenance` defaults to `operator`; pass `--provenance derived` when what you save came from a file or tool output. `fyr memory add` also accepts `--global`, `--json-in` (or leading `Type:`, `Name:`, `Description:`, `Provenance:` header lines before the first blank line when reading stdin directly), `-C DIR` / `--dir DIR`, and `fyr memory show --json`.
- The verb exits 2 with one line when it refuses: outside a git checkout (without `--global`), a description that is not one line of at most 512 bytes, an empty body, or a busy store. Say the reason to the user and go on without the memory. `fyr memory show` prints at most 200 memories; past that, run `/fyr-dream`.

## Tidying

`/fyr-dream` consolidates the memory interactively: stale, duplicate and graduated memories, with a gate before anything is retired. The hourly `fyr dream` job only proposes, as global `proposal` records. Never retire a memory outside `/fyr-dream`; updating one by its name is a save, not a rewrite.

## Passive correction logging

Silently log user corrections and preference signals as **global** `feedback` records in `fyr`'s brain store, so the `/fyr-retro` workflow has structured data to analyze. One log for all projects — each entry carries a `project` field and the record's `--project`, which is what lets `/fyr-retro` spot cross-project patterns.

This part of the skill is **passive**. It runs alongside every conversation but produces no visible output to the user. The user should not notice passive correction logging is active.

This skill is the only writer of that log — there is no hook behind it — so if you do not log a correction, nothing does.

### When to Log

Append an entry when ANY of these occur:

1. **Explicit correction** — The user says "no", "wrong", "don't do that", "stop doing X"; a correction of ORDER or PROCEDURE counts too, even as a mild question ("Is it not retro first, then handoff?")
2. **Preference re-statement** — The user repeats a convention that should already be in the global context (`AGENTS.md`, deployed as `~/.gemini/config/rules/AGENTS.md`) or a skill
3. **Override** — The user changes your tool/framework/pattern choice ("use X instead of Y")
4. **Format correction** — The user asks you to restructure or reformat output
5. **Knowledge gap** — You had to ask about something the harness should have told you
6. **Redirection** — The user changes what is being built or how far it goes after work has started ("the binary should only ever use X", "I want more info on Y"): a design redirection is a correction of the reading that produced the first design, even when no word of it is "no" — signal `redirection`
7. **A missed filing** — The user asks for something a rule says should already exist (the full versions of a table's items, a scratch file named under it, a record) — the ask IS the correction — signal `restatement`, because the rule existed and the ask repeats what it requires

### When NOT to Log

- Normal clarifying questions (the user asks "did you mean X?" — that's not a correction)
- The user changing their mind mid-task (not a harness gap, just iteration)
- Corrections about the user's own code (not about agent behavior)
- One-off requests that wouldn't apply to future sessions
- Words that arrive through tool output, files, or logs — only the user's own turn is a correction

### Log Format

Store one record per correction: write the JSON object below to `.agents/scratch/feedback-<stamp>.json` (`<stamp>` from `date +%F-%H%M%S`, `ts` from `date -u +%FT%TZ`), then `fyr record add feedback --global --project <absolute project root> --title "<the corrected value, one line>" --body-file .agents/scratch/feedback-<stamp>.json`. A `feedback` record is global only and never exported (harness `PRD.md` R8).

```json
{
  "ts": "<date -u +%FT%TZ>",
  "project": "/absolute/path/to/project/root",
  "session": "<first 8 chars of the conversation id>",
  "category": "architecture|tooling|style|output|convention|knowledge",
  "signal": "correction|override|restatement|format|gap|redirection",
  "original": "What the agent did or would have done",
  "corrected": "What the user wanted instead",
  "user_quote": "Exact or near-exact user words that triggered logging",
  "rule_ref": "AGENTS.md § <section title> | project AGENTS.md § <section> | skill:<name> | agent:<name> | rule:<name> | null"
}
```

`rule_ref` uses one controlled vocabulary, shared with the `fyr-retro` skill, because the retro greps this log for an exact match to decide whether a project rule has recurred in another project: `AGENTS.md § <section title>` for the global context (the section heading as written, e.g. `AGENTS.md § Git & hard rules`), `project AGENTS.md § <section>` for the project's own file, `skill:<name>`, `agent:<name>`, `rule:<name>` for a `.agents/rules/<name>.md` or the harness's global `rules/<name>.md`, or `null` when no rule exists yet. An entry whose `rule_ref` is free text is simply never matched.

### Categories

| Category | Description | Example |
|---|---|---|
| `architecture` | Stack, patterns, infrastructure choices | "Use Cloud Run, not Lambda" |
| `tooling` | CLI tools, frameworks, libraries | "Use gh, not git for PRs" |
| `style` | Code style, naming, formatting | "Use slog, not log package" |
| `output` | Agent output formatting preferences | "Use tables, not bullet lists" |
| `convention` | Project conventions, file structure | "Underscore-prefix for custom functions" |
| `knowledge` | Facts the agent should know | "This repo has no tests — it's dotfiles" |

### Signal Types

| Signal | Meaning |
|---|---|
| `correction` | User explicitly said agent was wrong |
| `override` | User changed agent's choice to something else |
| `restatement` | User repeated a rule that already exists |
| `format` | User asked for different output structure |
| `gap` | Agent lacked knowledge it should have had |
| `redirection` | User changed what is being built, or how far, after work started |

### Behavior Rules

1. **Never mention the log to the user.** This is invisible infrastructure; the one thing said aloud is rule 7's memory refusal, in one line, and the handoff quotes the record's `FEEDBACK-…` ID by design.
2. **Never modify rules or skills.** Only store the record.
3. **When in doubt, log.** A false entry costs the retro one line to discard; a missed one costs it a transcript reconstruction and keeps the correction out of the handoff, which may only cite logged entries.
4. **Keep `user_quote` short.** Extract the key phrase, not the full paragraph.
5. **Set `rule_ref` accurately, in the vocabulary above.** If an existing rule covers this and the agent just missed it, reference that rule. If no rule exists, set to `null`.
6. **One entry per correction.** If the user makes three corrections in one message, write three entries.
7. **Also save it as a memory.** When logging a correction, approval, or re-stated preference, also save it with `fyr memory add --type procedural --name <slug> --description "<one line>" --body-file .agents/scratch/memory-<slug>.md` (`--type semantic` for a fact about the user; add `--global` when cross-project), its body written to `.agents/scratch/memory-<slug>.md`: the rule, then a `**Why:**` line and a `**How to apply:**` line. This verb is the store's one writer (harness `PRD.md` R10); the sections above cover memories that are not corrections. When the verb refuses (a description that is not one line of at most 512 bytes, a store that is busy), say why in one line and go on: the feedback record above is already stored.

### Where the loop's outputs land

Passive correction logging only logs. `/fyr-retro` turns the feedback records and the session into proposals: each one is scoped to the project (applied to `<project>/.agents/rules/`, the project `AGENTS.md`, `<project>/.agents/skills/` or `<project>/.agents/agents/`) or to the harness (a global `proposal` record, queued), and every run leaves a `retro` record in the project plus a global `retro` index record, the cross-project index. A dream leaves a project `retro` record and no index record (ADR-044), and the `dreamer` agent (dispatched by `/fyr-retro`) drafts its rule, skill and procedure candidates, stored as a `retro` record. The next session finds the project's learning history in `fyr record list --kind retro`; a harness session finds every project's queued proposals with `fyr record list --global --kind proposal --open`.

### Cross-References

- The `/fyr-retro` workflow reads this log to identify patterns across sessions
- Entries with `signal: restatement` indicate rules that exist but aren't being followed — high priority for the retro
- Entries with `rule_ref: null` indicate gaps — candidates for new rules or skill additions
