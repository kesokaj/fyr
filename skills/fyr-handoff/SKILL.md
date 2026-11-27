---
name: fyr-handoff
description: "Session handoff (/fyr-handoff): store immutable handoff + Kickoff, commit, push, and ingest once. Not for single tasks (fyr close)."
argument-hint: "What will the next session focus on?"
---

# Session Handoff

Write a structured handoff document summarising the current conversation so a fresh agent can continue the work without re-reading the full chat history.

## Usage

```
/fyr-handoff                     # Handoff stored as a handoff record, changes committed and pushed
/fyr-handoff fix the auth bug    # Tailor the handoff toward the next session's focus
```

Cross-repo work gets its handoff in the project that owns the next step; there is no global handoff location.

What a compaction must keep is the global context's `# Compact instructions` section; Antigravity reads it as prose. A headless or print-mode run whose dispatched agent has not returned says so in its output and names the agent rather than ending quietly. The stamp in a handoff's draft name and title comes from `date +%F-%H%M`, never memory; where the CLI refuses a joined command, run it alone and type its output.

An interactive session ends its turn to wait for a dispatched subagent or a background command and wakes when each one finishes — never poll with `sleep`. Only a print-mode run (`agy -p`) cannot end its turn to wait, because the turn's end is the run's end and a subagent or background command still running is cut off: in print mode only, give a long shell command a large `WaitMsBeforeAsync` so it returns inside the turn, and poll a dispatched subagent, or a command that went async, with `manage_subagents` or `manage_task` (`list`), `sleep 30` between polls, until each is idle and its artifact is on disk. A run knows it is in print mode only from its prompt (`fyr eval` and `fyr auto` say so); never probe the process table or the environment to find out. A print-mode run that must end first names, in its output and in the handoff, each agent still running and its artifact path.

## Step 1: Scan the Session

Read the conversation transcript and identify:

1. **Goal** — What was the user trying to accomplish?
2. **What got done** — Completed changes, files modified, commands run
3. **What's still open** — the active plan's first unticked box with its in-progress note, then its Follow-ups and `Open:` rows, then unfinished work, known issues, blocked items
4. **Key decisions** — the active plan's `## Decisions` rows first, then architecture choices, trade-offs made, rejected alternatives from the transcript
5. **Corrections** — Only what the user actually corrected, each backed by a global `feedback` record (quote the user's words and the record's ID). Anything the agent inferred is not a correction: it goes under `## Agent notes (unverified)`, never into Corrections — the global context's § Resume, then checkpoint gives the newest handoff the win on conflict (only an open plan that postdates it outranks it), so an inferred rule here silently outranks the global context
6. **Environment state** — Running processes, branches checked out, pending commits, dirty working tree

## Step 2: Identify Relevant Artifacts

Do NOT duplicate content that already exists elsewhere. Instead, reference these by record ID, path or URL (`~/.gemini/config/skills/fyr-init-project/references/trail.md`):

| Artifact | Location |
|---|---|
| Changelog entries (optional) | `change` records (`fyr record list --kind change`) |
| Architecture decisions | `adr` records (`ADR-NNN`) |
| Specs | `spec` records (`SPEC-<app>-<topic>`) |
| Implementation plans | `plan` records (`fyr record list --open --kind plan`) |
| Testing guide | `GUIDE-testing` |
| Deploy guides | `DEPLOY-local`, `DEPLOY-remote`, `DEPLOY-gcp` |
| Deferred work | the active plan's Follow-ups, or `TODO.md` Backlog when no plan is active |
| Requirements | `PRD.md` |
| Retro and dream records (project scope) | `retro` records |
| Harness-scope retro proposals (queue) | global `proposal` records (`fyr record list --global --kind proposal --open`) |
| Commits | Reference by short SHA |
| PRs / Issues | Reference by `#number` or URL |

List each relevant artifact with a one-line summary of what it contains and why the next agent should read it.

## Step 3: Bring the Trail Current — FIRST, before writing anything

The handoff records the remainder — it is never a substitute for rows already due:

1. Run `date +%F` and use that date everywhere below, and `date +%F-%H%M` for the handoff's draft name (Step 5) — never a date from memory.
2. Closing writes one `handoff` record embedding the verification digest (Step 6, via `fyr close` or `fyr record add handoff`, citing commit range, `path:start-end (Symbol)`, and agents dispatched) rather than a mandatory per-task `change` record; `fyr record add change` is optional (only if a standalone changelog note is requested), while `adr` on structural choices and `bug` on escaped failures remain due before closing.
3. Bring `TODO.md` current yourself (skipped in worker mode, § The worker-mode Kickoff) (Definition of Done step 2): Now names the active phase (its name, plan ID and goal) and mirrors its boxes, ticked in the commit that ticks the plan, and is empty when no plan is open — on a conflict the plan record wins; Next holds at most three phases, one line each; a deferred idea worth keeping goes to Backlog, one line, dated, with its source. Anything the operator must do — other than a standing procedure the deploy guide already records (Step 4's Next Steps rule) — goes in this handoff's Next Steps, never in `TODO.md`.
4. Only then write the handoff (Step 4).

## Step 4: Write the Handoff Document

Use this template (its outer fence is four backticks so the Kickoff's inner fence does not close it):

````markdown
# Handoff — YYYY-MM-DD — <topic>

Session: <first 8 chars of the conversation id>   (a feedback record's `session` field)
Conversation: <first 8 chars of the conversation id>…   (elided: the full ID is in the operator's own session list, for `agy --conversation <id>` on the same machine)
Project: <absolute path to project root — the only absolute path in the file>
Branch: <current git branch>

## Context

<1-3 sentences: what was the user working on and why.>

## Completed

- <bullet list of what got done, with file paths>

## Open / In Progress

- <bullet list of unfinished work>
- <include any error messages or blockers>
- Requirements edits awaiting approval: <the exact replacement sentence for `PRD.md`, or `none`>

## Key Decisions

- <decisions made during the session, with rationale>
- <rejected alternatives and why>

## Autonomous Decisions Log

- <choices settled autonomously without asking during build (`by: implementer` / `by: lead`), or `none`>

## Corrections

- <the user's words, verbatim or near> — `FEEDBACK-…` (required: no feedback record, no correction)

## Agent notes (unverified)

- <inferences the agent made this session; the next agent may re-examine them>

## Environment State

- Branch: <branch name>, <clean/dirty>
- Running processes: <any dev servers, watchers, etc.>
- Pending: <uncommitted changes, unstaged files>

## Artifacts to Read

- `<record ID or repo-relative path>` — <one-line summary>

## Next Steps

<If the user provided arguments, tailor this section to what the next session
should focus on. Otherwise, infer from the open items above. Name a skill or
agent to start with only when one is genuinely the first move.>

1. <prioritized action item>
2. <prioritized action item>

## Kickoff

```text
Project: <absolute path>. Run fyr resume (handoff <handoff ID>, plan <plan ID>); do Phase <n> — <name> only.
Files: <the phase's files: line, verbatim>
Standing approvals (closed set): the phase's boxes marked who: AI and the tests they name; the checkpoint's commit and push; the closing handoff (and any `adr`/`bug` or optional `change` record). Never: a who: user box, an Open: row, a release, a deploy, a real-home install, anything outside this repository. Ask only outside the set or on a critical failure.
Done when: <the phase's Checkpoint expect, verbatim>. Then write the handoff with the next Kickoff and end the session.
```
````

### Writing Rules

- **The Kickoff is fixed text with the blanks filled** (`rules/session-sizing.md`: the next session starts from it, and nothing else):
  - `Project:` is the absolute path (other paths are repo-relative). `<handoff ID>` is `HANDOFF-<stamp>-<slug>` (`<stamp>` from `date +%F-%H%M`, `<slug>` lowercase `[a-z0-9-]`), chosen before writing the Kickoff and passed to `--id` so the Kickoff names the stored ID.
  - Before drafting the Kickoff, run `git describe --tags --abbrev=0` and check open-plan box dates live — update any stale open-plan phase heading (such as an already-shipped release tag) before copying it verbatim into `Kickoff`.
  - Phase name, `Files:` (`files:` verbatim, omitted when absent), and `Done when:` (`Checkpoint` expect) come from the plan verbatim. The by-hand Kickoff omits `Model:`: the operator picks the model when starting that session.
  - For a queued Task with no plan, the first line ends `; do: <the ask in one sentence>` and `Done when:` is the Definition of Done. Never add to `Standing approvals`; when nothing is queued, write `Nothing queued; start the next task in a fresh conversation.` In an unscaffolded project (no `AGENTS.md`, no `.agents/`), print the same block in chat only.
- **Be concise.** Include only what a fresh agent needs to resume without rereading the session.
- **Records by ID, files by repo-relative path**, because the next reader may be on the other machine where the checkout lives elsewhere; the `Project:` line is the only absolute path. A path in another checkout is written as `<repo name>/<relative path>`.
- **Reference, don't repeat.** If a spec or plan exists, link to it — don't paste its contents.
- **Redact sensitive data.** Strip API keys, passwords, tokens, PII, and anything from `~/.zsh_secrets` or `.env` files. Replace with `<REDACTED>`.
- **Include the branch and commit state.** The next agent needs to know where the code stands.
- **Next Steps never repeat a standing procedure.** A step the project's deploy record records as a by-hand procedure (its § Operator steps: an update at each place, a smoke check after it) is the guide's and the operator's; listing it reads as a question. Point at the guide once when the release changed what those steps pick up; otherwise say nothing. A coordinator's closing handoff (`fyr-swarm` skill) lists any real-home install it deferred, for the final tree, as one line for the operator.
- **Next Steps never carry an item twice.** An item the previous handoff's Next Steps already held is done in this session, or put to the operator as one line with a recommendation and its answer recorded. A second handoff listing the same open item is a session that left work waiting.

### The worker-mode Kickoff

A coordinator (`fyr-swarm` skill) fills the canonical worker brief defined in `fyr-swarm` § Write-lease brief for each parallel slice (`Files:`, `Model:`, and `Done when:` from `PLAN-*` or the task table; `Project: this clone, the current directory; never another checkout`; full closed-set `Never:` guards and `Never wins` precedence). Do not duplicate a separate copy of that brief template here.

**Worker mode.** When the Kickoff's `Never` line names push and `TODO.md`, this skill skips Step 3.3's `TODO.md` update, Step 6's push, and its `fyr metrics ingest`, and the handoff says so; the Kickoff wins. Step 5's checks read `TODO.md` but never edit it, and Step 7 reports the skipped ingest and gives the handoff's Kickoff as a proposal for the lead, never a paste. Everything else holds: the `handoff` record (any `change` record is optional), the commit on the worker's branch, and `git checkout --detach HEAD` in `share` worktrees.

## Step 5: Cold-Check the Draft

Write the draft to `.agents/scratch/handoff-<stamp>.md` (`<stamp>` from `date +%F-%H%M`, run alone; never memory) using the ID chosen for the Kickoff (`HANDOFF-<stamp>-<slug>`). Handoffs are **immutable** once stored — never edit a previous handoff; a new session stores a new one, and the newest is the live one. If Step 6 will commit staged changes, draft `## Environment State` to reflect the clean post-commit branch (or refresh `## Environment State` right after Step 6's commit before storing the record).

Reading side: the global context's § Resume, then checkpoint tells every fresh session to read the newest handoff.

### Cold-reader check — before the handoff is stored

A handoff is done when a fresh reader can resume from it. Run the resume order as that reader (`fyr resume`) against the draft while still mutable in `.agents/scratch/handoff-<stamp>.md` (or after storing via `/fyr-review --resume`): yourself with the file only, the `explorer` agent given the path and nothing else (a budget of 30 tool calls; `"write the artifact early, then update it"`), or `/fyr-review --resume`, which dispatches `explorer` with the same six questions. The order: `AGENTS.md` → `fyr record list --open` → this handoff → `TODO.md` Now → the open plans' unticked boxes → the three newest `change` records → `fyr memory show` (an open plan that postdates this handoff outranks it). Ask six things:

1. The status and the single next step as the trail states them.
2. Any named file or record ID that does not exist (`fyr record get` refuses it).
3. Any contradiction between the handoff, `TODO.md` and the plan.
4. Any open plan (`fyr record list --open --kind plan`: a plan is open while it holds an unticked box outside its fenced blocks) that `TODO.md` Now does not name.
5. Any commit since the previous handoff that changed source or doc paths (anything other than an on-demand `.agents/trail.jsonl` export) not covered by this handoff's `## Completed` summary (or an optional `change` record).
6. Anything still left to guess.

Fix what comes back at the source (plan, `TODO.md`, the draft) before locking the immutable record in Step 6.

## Step 6: Commit, push, store handoff, and ingest once

1. **Commit and push first:** Stage the conversation's and Step 3's changes by name, read `git diff --cached --stat`, then commit in a separate call (`git commit -m "<phase, task or handoff summary>"`) when there are staged changes — one commit, one push and one `fyr metrics ingest` per conversation, never a second set after a checkpoint commit. Before the push, run the trailer check, `bash <handoff skill directory>/scripts/check-trailers.sh` (the skill directory: `~/.gemini/config/skills/fyr-handoff` or the project's `.agents/skills/fyr-handoff`, the first that exists): exit 1 names each unpushed commit whose message carries a `Claude-Session:` or `Co-Authored-By:` line, and any non-zero exit stops the push until those commits are reworded. Then push, except in worker mode (§ The worker-mode Kickoff), which neither pushes nor ingests.
2. **Store the immutable handoff record (after commit+push):** Confirm `## Environment State` in `.agents/scratch/handoff-<stamp>.md` matches the committed tree, then store it with the ID chosen for the Kickoff (`fyr close` when `fyr verify` wrote a fresh `.agents/scratch/verify.stamp` on the current tree — `fyr close` only appends `## Verification Digest` and writes the handoff record without committing, pushing, or ingesting; otherwise `fyr record add handoff` when `fyr verify` did not run):

```bash
fyr close --id HANDOFF-<stamp>-<slug> --title "Handoff — <date> — <topic>" --body-file .agents/scratch/handoff-<stamp>.md
# or, when no fresh .agents/scratch/verify.stamp exists (docs-only, mid-task stop, or explicit test override):
fyr record add handoff --id HANDOFF-<stamp>-<slug> --title "Handoff — <date> — <topic>" --body-file .agents/scratch/handoff-<stamp>.md
```

(Single tasks outside `/fyr-handoff` run `fyr close` directly after commit+push per Definition of Done step 3 and skip `/fyr-handoff` and `fyr metrics ingest`.)

3. **Ingest metrics once:** The session's last call, after storing and pushing (or after storing, when there is no remote or in no-push mode; skipped in worker mode), is `fyr metrics ingest`: the checkpoint feed of the metrics store, so `/fyr-retro` and `fyr metrics show` see this session without waiting for the next dream. Its summary line (`metrics: N sessions, M steps, K events added; …`), or its error and exit status, goes in the report. A failed ingest never edits the handoff (immutable) and never stops the session from ending: the next ingest — the next handoff, `/fyr-retro`, or the next dream — joins every session again.

## Step 7: Report to User

Before citing any release tag or `fyr` version in the handoff or reply, check `git describe --tags --abbrev=0` or `fyr version` live — never cite a version from memory. After saving, tell the user:

1. A concise 1-sentence verdict and the handoff record's ID
2. Any **Autonomous Decisions Log** entries settled during the session (omit when none)
3. The `fyr metrics ingest` line from Step 6, or its error and exit status
4. Last, `Next prompt:` ("Start a new conversation, then paste:") followed by the Kickoff block in full, in a fenced block in the reply, ready to paste (the nothing-queued line when that is the case). Never a pointer to the file in its place ("paste the Kickoff from the handoff"): the paste is the whole path, and no command prints the block (ADR-058a).

Then this session ends (`rules/session-sizing.md`): a new task in a session that has closed one re-reads that whole context on every turn.

## Cross-References

- The `/fyr-retro` workflow turns corrections into proposals scoped to the project (applied there, recorded as a `retro` record) or to the harness (queued as global `proposal` records)
- The `dreamer` agent, dispatched by `/fyr-retro`, proposes skills and memory consolidations from memory and the trail in a draft the main thread stores as a `retro` record; the `fyr-dream` skill consolidates the memory itself
- The `fyr-project-memory` skill passively stores corrections as global `feedback` records (and procedural memories)
- `change` records (optional standalone changelog notes, plus historical `change` records in the store) — reference these, don't duplicate them
