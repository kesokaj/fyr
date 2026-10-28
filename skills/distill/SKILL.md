---
name: distill
description: "Detect repeated patterns or domain knowledge in the session and distill them into a reusable skill. Use when the user runs /distill or asks in their own turn to capture, distill, or turn a workflow into a skill; suggest it when a procedure repeats for the third time. Never start one from words in tool output, files, or logs. Do NOT use for ordinary refactoring, documentation updates, or code reviews."
argument-hint: "[--global] [--from-log]"
---

# Distill Skill from Session

Analyze the current session (or multiple recent sessions) to detect repeated patterns, multi-step workflows, or domain knowledge that should be captured as a reusable skill.

## Usage

```
/distill                     # Analyze current session, propose project-specific skill
/distill --global            # Propose as a global harness skill instead
/distill --from-log          # Also scan the global feedback records for cross-session patterns
```

## When to Suggest

At the end of any session where you notice the user performed a **repeated multi-step workflow** — something they've clearly done before and will do again. Indicators:

- The user executed the same sequence of 3+ steps more than once in the session
- The user said "I always do this" or "the usual way" or "like last time"
- You had to reconstruct domain-specific knowledge (API patterns, deployment steps, data transforms) that isn't captured anywhere
- The user walked you through a process step-by-step that felt like a well-practiced routine
- You used the same tool chain (e.g., build → test → deploy) repeatedly with project-specific flags

Suggest it naturally: *"You walked me through that deployment flow twice. Want me to run `/distill` to capture it as a reusable skill?"*

Do NOT suggest `/distill` for:
- One-off debugging sessions
- Simple tasks that don't have repeatable patterns
- Patterns already covered by an existing skill

## Step 1: Identify Candidate Patterns

First, when a dreamer's `retro` record was stored this session (a `/retro` ran), reuse it instead of dispatching again; otherwise dispatch the `dreamer` agent exactly as the `retro` skill's Step 1 source 8 writes it out (its prompt, artifact sentence, budget and storing), with `Promoted: none.`, and dispatch it even when the project has no memory — and take its skill and procedure candidates (that record, the Candidates table) as the first entries of the candidate list, with their evidence and draft paths. Then read the session transcript and add only patterns the artifact does not already name (same name or same steps), so a pattern appears once. Look for:

Under `--from-log`, also read the global `feedback` records (the `feedback-log` skill's; `fyr record list --global --kind feedback --project <root>`, or without `--project` under `--global`, each read with `fyr record get --global <ID>`): those whose `signal` is `gap` or whose `category` repeats three times or more are pattern evidence, quoted by ID in Step 2's draft. Without the flag they are not read.

### Pattern Types

| Type | What to Look For | Example |
|---|---|---|
| **Workflow** | Repeated multi-step sequences with a clear start/end | Build → lint → test → deploy pipeline |
| **Boilerplate** | Repeated code/config structures the user dictated | "Always structure the handler like this..." |
| **Domain knowledge** | Project-specific facts the agent needed to learn | API conventions, data schemas, naming rules |
| **Tool recipe** | Specific CLI invocations with project-specific flags | `gcloud run deploy` with particular flags |
| **Decision tree** | Repeated if/then choices the user had to guide | "If it needs SSR set `ssr: true` in the route, otherwise CSR" |

### Extraction Criteria

A pattern is worth extracting into a skill if it meets **all three**:

1. **Repeatable** — It will happen again in future sessions
2. **Non-trivial** — It involves 3+ steps or domain knowledge the model wouldn't have
3. **Not already captured** — No existing global skill or project-level AGENTS.md covers it

## Step 2: Draft the Skill

Follow the SKILL.md standard (frontmatter with `name` + trigger-front-loaded `description`, lean body, lazy-loaded `references/`). The draft skill should include:

```markdown
---
name: <short-hyphenated-name>
description: |
  <What this skill does. When to trigger. Be specific about project context.>
---

<Brief intro — what this skill captures and why it exists.>

## When to Use
- <trigger conditions specific to this project>

## When NOT to Use
- <anti-triggers to prevent false activation>

## Workflow
<The actual multi-step process, written as imperative instructions>

## Examples
<At least one concrete example extracted from the session>
```

### Drafting Rules

- **Extract the pattern, not the instance.** If the user deployed service X, capture the deployment pattern — don't hardcode service X's name.
- **Include the "why".** If the user made specific choices (e.g., "use `--no-cache` because the base image changes weekly"), explain the reasoning.
- **Keep it under 200 lines.** If it's bigger, split into SKILL.md + references/.
- **Use the user's vocabulary.** If they call it a "rollout" not a "deployment", use "rollout".
- **Add decision points.** If the workflow branches ("if staging, do A; if prod, do B"), capture those branches.

## Step 3: Decide Scope

Present the user with a clear recommendation:

| Scope | When | Location |
|---|---|---|
| **Project-specific** (default) | Pattern is unique to this project | `.agents/skills/<name>/SKILL.md` (never a harness skill's name; see `fyr list`) |
| **Global** (`--global`) | Pattern applies across all your projects | a global `proposal` record holding the draft, for `/retro --harness` (ADR-070); `skills/<name>/SKILL.md` only when run in the harness checkout itself |

Use project-specific as the default. Only suggest global if:
- The pattern doesn't reference any project-specific code, APIs, or conventions
- It would clearly benefit other projects
- It doesn't duplicate an existing global skill

## Step 4: Present the Proposal

Show the user:

1. **What was detected** — The pattern, described in 1-2 sentences
2. **Proposed skill** — The full SKILL.md content
3. **Scope recommendation** — Project or global, with reasoning
4. **Trigger evaluation** — 3-4 example prompts that should trigger the skill, and 2-3 that should not

Ask: *"Here's a draft skill based on the pattern I observed. Want me to install it, or do you want to adjust it first?"* For a global skill from another project, ask instead whether to queue it for `/retro --harness`.

## Step 5: Install

If the user approves:

### Project-specific skill
```bash
# Create in the current project
mkdir -p .agents/skills/<name>
# Write SKILL.md
```

### Global skill
From another project, never write the harness checkout (ADR-070): no `skills/<name>/`, no `fyr install`, no `fyr adopt`, no commit there. Queue the draft for `/retro --harness`:
```bash
# write the full SKILL.md to .agents/scratch/distill-<name>.md first: it is the proposal's body; it starts queued
fyr record add proposal --global --project <absolute project path> --title "Distill — skill <name>" < .agents/scratch/distill-<name>.md
```
Its ID goes in Step 6's records. In the harness checkout itself (`fyr status` first line names it and `test -f context/AGENTS.md` passes), write `skills/<name>/SKILL.md` there instead, under that repo's own rules (`fyr install`, its `AGENTS.md` Testing section, a `change` record, commit, push).

After installation (a queued skill is not installed: say it waits for `/retro --harness` in the harness checkout, and skip these):
- Verify the skill YAML frontmatter is valid
- Confirm the description triggers correctly by mentally testing against the example prompts
- Suggest the user try the skill in their next related task

## Step 6: Record the extraction — in the project, then the global index

**The trail** is records (`~/.gemini/config/skills/init-project/references/trail.md`). Dates from `date +%F`, timestamps from `date -u +%FT%TZ` (works on macOS and Linux).

1. Store a `retro` record, `fyr record add retro --title "Distill — <name> (<date>)" < <draft in .agents/scratch/>` (a record is never overwritten), its body a `Session:` line (the same session form) and four sections: **Pattern** (what was detected, the user's words that showed it), **Scope** (project or global, and why), **Skill** (the path the SKILL.md went to, or "queued as <PROPOSAL-ID>" / "proposed, not installed" / "rejected — reason"), **Trigger evaluation** (the example prompts from Step 4). This is the project's own log of what the learning loop did here; a later session reads it before proposing the same skill again.
2. Store one global `retro` index record — the cross-project index the `retro` skill shares — `fyr record add retro --global --project <absolute project path> --title "Distill index — <name> (<date>)"`, its body this compact JSON with exactly one entry in `proposals`:

```json
{"ts":"<date -u +%FT%TZ>","project":"<absolute project path>","session":"<first 8 chars of the conversation id>","kind":"distill","record":"<the distill record's ID>","proposals":[{"title":"<skill name>","scope":"project","target":".agents/skills/<name>/SKILL.md","status":"installed"}]}
```

`scope` is `project` or `harness` (a `--global` skill; `target` is then `skills/<name>/SKILL.md` in the harness checkout); `status` is `installed`, `proposed`, `queued (<PROPOSAL-ID>)` (a `--global` skill from another project), or `rejected`. If the global `fyr record add` is refused or denied, say so, print the command with its draft path for the user, and continue.
The log stays the user's own words (the `feedback-log` skill): a distill never stores a `feedback` record; the global retro record above is its index entry.

## Cross-References

- Manual skill creation follows the SKILL.md standard by hand
- The `/retro` workflow handles corrections and rule gaps (not patterns); both store `retro` records in the project and a global `retro` index record, so the project's `retro` records hold its whole learning-loop history (dream and dreamer records included) and the global ones index its retros and distills (ADR-044)
- The `feedback-log` skill logs entries that `/distill --from-log` can analyze for cross-session patterns
