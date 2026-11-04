# Work: a task list with no plan

`/work <task>; <task>…`, typed by the operator in their own turn, runs each task as a worker with no plan, no cold read and no D-row (`PLAN-2026-11-02-1806-fyr-speed` item 10, D4, D6). A Task already runs with no plan (context § Work); the safety comes from the same verb, the same `Files:` check and the same merge steps as a plan's run. The SKILL.md rules hold unless this file says otherwise: the `--home` rule, the standing approvals, the closed set, a safety FAIL, the names' alphabet, and loop steps 5–7 and 9.

## 1. Read the list

- Split the list on `;`. Each task is one sentence; a line break inside a task is joined with a space, because a Kickoff line starting `Files:` or `Model:` is read by the verb.
- **Classify each task** (context § Work). A Task or a Trivial change qualifies.
- **A task with a design question goes to the feature skill.** It is never dispatched, and this session never starts it (`rules/session-sizing.md`). A design question is a new surface whose shape is undecided, a choice between named options, a schema, an interface another part depends on, or work that would outgrow one sitting. The table names its row `feature`, and the lead's handoff gives its Kickoff in the queued-Task form (`handoff` § Writing Rules, `; do: /feature <the task>`). The other tasks still run.
- A task that needs an item of the closed set is not dispatched either; its row says which item.

## 2. Make the project ready

- The project is scaffolded (`AGENTS.md` and `.agents/`): a PASS needs the worker's handoff record. Without one, stop and offer `/init-project`.
- The tree is clean. A dirty tree is shown above the table with the commit it needs first, by path; that commit runs after the go, before the first wave.

## 3. Propose each task

The lead reads only enough to fill these fields (`fyr search`, `git ls-files`, a file's head), and implements nothing:

- **Name:** a slug of the task's words, matching `^[a-z0-9][a-z0-9-]{0,62}$`, unique in the list, and used by no branch yet (`git branch --list 'work/<name>*'`). The Kickoff file is `<name>.md`, and the branch is `work/<name>`, with `-r2` on the second attempt.
- **Files:** the narrowest comma-separated scope that fits, in the form the verb reads (`fyr help work`): each file the task changes, its test included, or a directory ending in `/` where the files are not known yet. Never the repository's root, a glob, or `.agents/trail.jsonl`.
- **Done when:** the operator's own words for the task when they gave some; otherwise `committed with a covering test, and <the project's full test command from AGENTS.md> passes in the clone`.
- **Model:** one model ID per task from `agy models`, chosen by tier (the `agy-dispatch` rule): a `[flash]` ID for a mechanical change, a `[pro]` ID for one that needs judgement. The ID, never the tier's name, becomes the Kickoff's `Model:` line, since the verb refuses a line `agy models` does not list; `fyr work` runs with no `--model`.
- **Runner:** what `fyr work` will use. Today that is `host`, the verb's default; name it.
- **Wave:** go through the tasks in the order given. A task joins the first wave whose tasks' `Files:` it does not intersect (two entries intersect when they are equal, or when one is a directory ending in `/` that is a prefix of the other), and that holds fewer than the verb's `--max-workers` (8 by default). A later wave runs after every merge of the wave before it, on the new HEAD.

## 4. One table, then the go

Print one table, ordered by wave, with these columns: `#`, the task in one line, name, `Files:`, Done when, model, runner and wave. A row routed to the feature skill, or to the closed set, says so in the name column and has no other fields.

Nothing runs until the operator's go in their own turn (D4). The go is waived only when the same `/work` turn says so in words, for example "no go needed" or "go waived". A waiver never covers a worker that runs on host because docker was not ready (D5): that table always waits for the go.

## 5. Compose and run each wave

1. **Compose each Kickoff** from the handoff skill's plan-less worker-mode Kickoff (`handoff` § The worker-mode Kickoff), filling its fixed text and changing none of it. Write it to `.agents/scratch/work/kickoffs/<name>[-r2].md`.
2. **Run the wave** with loop steps 5 and 6: one `fyr work` call with every Kickoff of the wave, after the quota read and one line per dispatch.
3. **Merge each PASS** with loop step 7, unchanged, except that the commit subject is `work: <name> (work/<name>)`.
4. **Re-dispatch** as loop step 7 says, once per task, as `-r2`. A FAIL naming `changed outside its Files`, and a red squash naming a file outside the scope, block the task: the lead never widens a scope. The handoff proposes the wider scope to the operator, and the task's branch stays.
5. **A worker's question** is answered as loop step 7 says. With no plan to hold a D-row, each answer goes in the lead's handoff under `## Key Decisions`, `by: lead`.
6. **Store the bug drafts** with loop step 9.
7. **End the wave:** commit by name, run `check-trailers.sh`, and push when the project has a remote, so the tree is clean before the next wave. There is no plan box or `TODO.md` line to tick.

A task is blocked when it needs the closed set, failed twice, was refused by the verb, or merged with a handoff whose `## Completed` section prints nothing (loop step 8's command). Blocked tasks are never re-dispatched, and the other tasks still run.

## 6. After the last merge

Run the Definition of Done once over the whole range, from the first wave's base to HEAD, as for any Task:

- `reviewer` over the range's diff when it has 50+ lines or 5+ files, and `security` when a changed path needs one;
- the docs check;
- the `verifier` last.

The workers' change records are the range's change records. The lead stores the review and testing records its dispatches drafted, and edits no source, tests or docs: a finding to fix becomes one fix-up worker inside the task's own `Files:` (`<name>-fix`), dispatched and merged as in section 5. A finding outside every task's scope is a closed-set item.

## 7. The lead's handoff

The lead's handoff (the handoff skill) ends the session. Per task it gives the result as one line:

- the merged commit and its worker's `## Completed` evidence, written as loop step 8 writes evidence (one line, no plan-line prefix), or the handoff ID when it cannot be written so;
- or the blocked reason and the branch that stays.

Its Next Steps name every task routed to the feature skill, with its Kickoff; every wider scope proposed; every closed-set item; every bug record stored, by ID; and every unmerged branch.

A task list never resumes. A stopped task run is restarted by a new `/work` list built from the lead's handoff, after its unmerged branches are dealt with.
