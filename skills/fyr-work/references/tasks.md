# Work: a task list with no plan

`/fyr-work <task>; <task>…`, typed by the operator in their own turn, runs each task as a worker with no plan, no cold read and no D-row (`PLAN-2026-11-02-1806-fyr-speed` item 10, D4, D6). A Task already runs with no plan (context § Work); the safety comes from the same verb, the same `Files:` check and the same merge steps as a plan's run. The SKILL.md rules hold unless this file says otherwise: the `--home` rule, the standing approvals, the closed set, a safety FAIL, the names' alphabet, and loop steps 5–7 and 9.

## 1. Read the list

- Split the list on `;`. Each task is one sentence; a line break inside a task is joined with a space, because a Kickoff line starting `Files:` or `Model:` is read by the verb.
- **Classify each task** (context § Work). A Task or a Trivial change qualifies.
- **A task with a design question goes to the `fyr-feature` skill.** It is never dispatched, and this session never starts it (`rules/session-sizing.md`). A design question is a new surface whose shape is undecided, a choice between named options, a schema, an interface another part depends on, or work that would outgrow one sitting. The table names its row `fyr-feature`, and the lead's handoff gives its Kickoff in the queued-Task form (`fyr-handoff` § Writing Rules, `; do: /fyr-feature <the task>`). The other tasks still run.
- A task that needs an item of the closed set is not dispatched either; its row says which item.

## 2. Make the project ready

- The project is scaffolded (`AGENTS.md` and `.agents/`): a PASS needs the worker's handoff record. Without one, stop and offer `/fyr-init-project`.
- The tree is clean. A dirty tree is shown above the table with the commit it needs first, by path; that commit runs after the go, before the first wave.

## 3. Propose each task

The lead reads only enough to fill these fields (`fyr search`, `git ls-files`, a file's head), and implements nothing:

- **Name:** a slug of the task's words, matching `^[a-z0-9][a-z0-9-]{0,62}$`, unique in the list, and used by no branch yet (`git branch --list 'work/<name>*'`). The Kickoff file is `<name>.md`, and the branch is `work/<name>`, with `-r2` on the second attempt.
- **Files:** the narrowest comma-separated scope that fits, in the form the verb reads (`fyr help work`): each file the task changes, its test included, or a directory ending in `/` where the files are not known yet. Never the repository's root, a glob, or `.agents/trail.jsonl`.
- **Done when:** the operator's own words for the task when they gave some; otherwise `committed with a covering test, and <the project's full test command from AGENTS.md> passes in the clone`.
- **Model:** one model ID per task from `agy models`, chosen by tier (the `agy-dispatch` rule): a `[flash]` ID for a mechanical change, a `[pro]` ID for one that needs judgement. The ID, never the tier's name, becomes the Kickoff's `Model:` line, since the verb refuses a line `agy models` does not list; `fyr work` runs with no `--model`.
- **Runner:** what `fyr work --check` prints, run once for the whole list from the project's root: `runner docker`, `runner docker (fyr-work:local will be built first)`, or `runner host (<reason>)`; the runner is the word after `runner`. The verb's default, `--runner auto`, takes docker when its docker preflight passes and the host otherwise, saying why (D5). `--check` builds no image: a missing or stale default image that a run builds first reads as docker and says so, and the wave's `--runner docker` builds it before its workers start; a build that fails refuses that run, saying why (§ 5 step 2). A refusal (exit 2) would refuse the run too: quote it, and run nothing.
- **Wave:** go through the tasks in the order given. A task joins the first wave whose tasks' `Files:` it does not intersect (two entries intersect when they are equal, or when one is a directory ending in `/` that is a prefix of the other), and that holds fewer than the verb's `--max-workers` (8 by default). A later wave runs after every merge of the wave before it, on the new HEAD.

## 4. One table, then the go

Print one table, ordered by wave, with these columns: `#`, the task in one line, name, `Files:`, Done when, model, runner and wave. A row routed to the feature skill, or to the closed set, says so in the name column and has no other fields. Under a `host` runner, quote the reason `--check` printed on one line in backticks, as data.

Nothing runs until the operator's go in their own turn (D4), which settles the table's scope and runner (`host` fallback included) before build starts (`ADR-517`, superseding `ADR-507`'s mid-build wait-point). The go is waived only when the same `/fyr-work` turn says so in words, for example "no go needed" or "go waived": when `--check` reported `host` and the invocation turn also pre-approved `host` fallback in words, the waves run on `--runner host` and the closing handoff quotes `<reason>` in backticks; when `go` was waived without pre-approving `host`, never stop mid-run to ask — run no host workers and defer the tasks with `<reason>` quoted in backticks to the closing handoff's `Next Steps`.

## 5. Compose and run each wave

1. **Compose each Kickoff** from the `fyr-handoff` skill's plan-less worker-mode Kickoff (`fyr-handoff` § The worker-mode Kickoff), filling its fixed text and changing none of it. Write it to `.agents/scratch/work/kickoffs/<name>[-r2].md`.
2. **Run the wave** with loop steps 5 and 6: one `fyr work --runner <the table's runner>` call with every Kickoff of the wave, after the quota read and one line per dispatch. Naming the runner keeps the run on the one the go was given for: `--runner docker` refuses when docker is no longer ready instead of falling back to host, and that refusal blocks every task still to run.
3. **Merge each PASS** with loop step 7, unchanged, except that the commit subject is `work: <name> (work/<name>)`.
4. **Re-dispatch** as loop step 7 says, once per task, as `-r2`. A FAIL naming `changed outside its Files`, and a red squash naming a file outside the scope, block the task: the lead never widens a scope. The handoff proposes the wider scope to the operator, and the task's branch stays.
5. **A worker's question** is answered as loop step 7 says. With no plan to hold a D-row, each answer goes in the lead's handoff under `## Key Decisions`, `by: lead`.
6. **Store the bug drafts** with loop step 9.
7. **End the wave:** commit by name, run `check-trailers.sh`, and push when the project has a remote, so the tree is clean before the next wave. There is no plan box or `TODO.md` line to tick. When another wave remains, check `fyr metrics ingest` and `fyr metrics trajectory --project . --limit 1` before chaining: on `KNEE_EXCEEDED` (`>= 180,000` tokens) or CLI compaction, stop chaining before the next wave and list the remaining tasks as a new `/fyr-work` list in the closing handoff's `Next Steps`.

A task is blocked when it needs the closed set, failed twice, was refused by the verb, or merged with a handoff whose `## Completed` section prints nothing (loop step 8's command). Blocked tasks are never re-dispatched, and the other tasks still run.

## 6. After the last merge

Run the Definition of Done once over the whole range, from the first wave's base to HEAD, as for any Task:

- `reviewer` over the range's diff when it has 50+ lines or 5+ files, and `security` when a changed path needs one;
- the docs check;
- the `verifier` last.

The workers' change records are the range's change records. The lead stores the review and testing records its dispatches drafted, and edits no source, tests or docs: a finding to fix becomes one fix-up worker inside the task's own `Files:` (`<name>-fix`), dispatched and merged as in section 5. When the review loop hits its two-round cap, the lead never stops to ask: in-scope trade-offs go under `## Key Decisions` (`by: lead`) and in the review's `## Outcome`, while any finding outside every task's scope or needing the closed set goes to the closing handoff's `Next Steps`.

## 7. The lead's handoff

The lead's handoff (the `fyr-handoff` skill) ends the session. Per task it gives the result as one line:

- the merged commit and its worker's `## Completed` evidence, written as loop step 8 writes evidence (one line, no plan-line prefix), or the handoff ID when it cannot be written so;
- or the blocked reason and the branch that stays.

Its Next Steps name every task routed to the `fyr-feature` skill, with its Kickoff; every wider scope proposed; the `--check` host `<reason>` quoted in backticks when waves ran on `host` or were deferred for unapproved host fallback; every closed-set item; every bug record stored, by ID; and every unmerged branch.

A task list never resumes. A stopped task run is restarted by a new `/fyr-work` list built from the lead's handoff, after its unmerged branches are dealt with.
