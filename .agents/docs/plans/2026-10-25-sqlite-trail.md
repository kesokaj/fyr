---
owner: main
date: 2026-10-25
---
# Plan — the brain: every record in one SQLite store, one committed export per project   (2026-10-25)

Status: scope approved 2026-10-25 ("go all approved", twice). The cold read (`reviews/2026-10-25-review-sqlite-trail-plan.md`) is answered by D5–D22; the operator's later direction (D21) moots its file-index findings. Next: the operator approves the requirements text in Phase 0. No code before that approval.

## Why
The operator, 2026-10-25:
- "We need a database of learnings that we can search really quickly instead of all files."
- "I want everything in the sqllite. plans,review,log,changelog,bugs,feedback,critique etc per project and global the agent decides".
- "Delete all closed records". "DB + committed text export". "Return text, main stores it". "Also specs and deploy docs".
- Then: "We just discussed that to many files fuck with the projects. Then we keep them in the db instead with metadata and only search for them when we need to. Fyrs purpose is the second brain of agy, it should ask fyr for help when it needs to. When it needs new skills and context, when things start drifting. Checking metrics and evals for help. dreaming when projects are idle and not used to find skills that might need to be added or created etc."

This plan is roadmap item 1 of 4. The items after it are metrics fed at every checkpoint, `fyr check` (docs freshness and drift) and `fyr eval`.

Measured problem: `.agents/docs` holds 1,636 files and 110,097 lines. `fyr search "trailer co-authored"` returns a 2026-09-16 simulation log instead of BUG-177.

## Scope
**In:**
- **One store**, `~/.config/fyr/brain.db`, holds every record with its metadata. Scope is a column: a project root, or global.
- **The `fyr record` verbs.**
- **One committed file per project**, `.agents/trail.jsonl`. It is a backup and the way records travel between machines, and nothing reads it but `fyr`.
- **`fyr search`** queries records first.
- **`fyr record import`** prunes and migrates an old `docs/` trail. `import --global` migrates `feedback.jsonl`, `proposals/`, `retros.jsonl` and the project memory stores.
- **`fyr memory`** becomes a thin alias over `memory` records (D14).
- **The dream** works only on idle projects and writes `proposal` records.
- **This repository migrated.** `.agents/docs/` and `.agents/memory/` are removed.
- **Every skill, agent, rule and the context** use the verbs.

**Out:**
- `README.md`, root `AGENTS.md`, `REQUIREMENTS.md`, `ROADMAP.md`, `TODO.md`, `skills/`, `agents/`, `context/`, `rules/` and code stay files. The search index over code and those files stays.
- `metrics.db` stays separate.
- Roadmap items 2–4.

## Surfaces touched
- `internal/brain` (new): the store, the record verbs' logic, export and load, import, ID allocation, path trust for the export.
- `internal/search` (records ranked first; the global index drops its `~/.config/fyr` file view, which the brain replaces).
- `internal/memory` (alias over the brain), `internal/dream` (reads records; idle rule; writes proposals), `internal/trail` (`DocsRoot` callers).
- `cmd/fyr/main.go` (verb `record`, usage, help).
- The tests that read `.agents/docs` by path (`cmd/fyr`, `internal/{harness,search,trail,dream}`, the root package's doc tests).
- The kit moves to `.agents/kit/` (D17).
- `context/AGENTS.md`, `rules/session-sizing.md`.
- Every skill whose text names `docs/`, `<docs-root>`, `.agents/docs`, `feedback.jsonl`, `retros.jsonl`, `proposals/` or `.agents/memory` (D19's grep).
- All eight agents, `README.md` (verbs, layout; no map line moves, D18), root `AGENTS.md`, `REQUIREMENTS.md` (Phase 0), `ROADMAP.md`, ADR-063.

## Interfaces
**Store** `~/.config/fyr/brain.db` (mode 0600; the directory 0700):
```sql
records(scope TEXT NOT NULL,          -- absolute main-worktree root, or '' for global
        id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL,
        date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
        project TEXT NOT NULL DEFAULT '', -- for a global record about a project (feedback, proposal)
        updated TEXT NOT NULL,        -- RFC 3339, set by every write
        PRIMARY KEY(scope, id));
records_fts USING fts5(title, body, content='records', tokenize='porter unicode61');
meta(scope TEXT PRIMARY KEY, export_sha TEXT, last_dream TEXT);
```
The scope root is resolved the way `memory.Root` resolves it today (`internal/memory/memory.go`).

**Kinds** (lowercase, as `search --kind`):
- Project scope: `plan review testing change handoff bug adr retro learning spec deploy guide memory`.
- Global only: `feedback proposal`. `record add` refuses those two in a project (D10).
- `memory` is project-scoped but never exported (D14).

**IDs (D7):**
- `BUG-NNN` and `ADR-NNN[a-z]` are numbered per scope, allocated as max+1 inside the write's transaction, and compared numerically.
- Every other kind is `<KIND>-<YYYY-MM-DD-HHMM>-<slug>`.

**Status (D6):** the same table as before.

| Kind | Status values | Open when | Starts as |
|---|---|---|---|
| plan | derived from the body | unticked `- [ ]` outside fences | open |
| bug | `open \| fixed \| accepted \| obsolete \| graduated` | `open` | open |
| adr | `accepted \| superseded:<ADR>` | n/a | accepted |
| spec, deploy, guide | `current \| retired` | `current` | current |
| proposal | `queued \| applied:<commit> \| rejected` | `queued` | queued |
| handoff | none; the newest is the live one | | |
| everything else | `closed` | never | closed |

**Verbs** (exit 0 with or without output, 1 refusal, 2 usage; `--global` selects the global scope):
- `fyr record add <kind> --title T [--status S] [--id ID] [--project ROOT] [--global] < body`: prints the ID. Refuses an empty body or a body over 1 MiB.
- `fyr record get <id>... [--global]`: prints a header line and the body, through `printable` (`internal/search/store.go:128`).
- `fyr record edit <id> [--status S] [--title T] [--body] [--global]`: `--body` reads stdin; without it, only metadata changes (D9).
- `fyr record list [--kind K] [--open] [--project ROOT] [--all] [--global]`: `ID  kind  status  date  title`, newest first. `--all` covers every scope (the cross-project view).
- `fyr record import [--dry-run] [<docs-root>]` and `import --global [--dry-run]`.

**Export** `.agents/trail.jsonl`:
- One record per line: `{"date","id","kind","status","title","updated","body"}` with keys in that order, lines sorted by ID (numbers numerically), and a final newline.
- `memory`, `feedback` and `proposal` records are never exported.
- Every project write rewrites it (temp file plus rename, refusing a symlinked `.agents` or `trail.jsonl`) and stores its SHA-256 in `meta.export_sha`.

**Load (D21):** before any `record` or `search` call in a project, if the file's SHA-256 differs from `meta.export_sha` (a pull, checkout or merge changed it), the project's exported rows are replaced by the file's content in one transaction. The file wins. Every `fyr` write exports at once, so the database and the file agree after each write. A file that does not parse, or that holds a duplicate ID or a git conflict marker, is refused with its line number, and writes refuse until it parses.

**Search:** `fyr search` prints record hits (`ID  kind status  date  title` and a snippet) from `records_fts` for the project and global scopes, then file hits from the index as today. `--global` searches every scope.

**Unmigrated project (D16):** if `docs/` or `.agents/docs/` exists and `.agents/trail.jsonl` does not, every `record` verb except `import` refuses and prints the command.

**Agents (D12):**
- An agent writes its draft early to `.agents/scratch/agents/<agent>-<stamp>.md` and replies with the artifact and that path.
- The main thread stores it with `fyr record add <kind> --title … < <path>`.

**Dream (D15, D22):**
- The nightly timer dreams a project only when it has been idle for at least 24 hours (no commit, no `updated` record, no ingested session in that window), and has records or sessions newer than `meta.last_dream`.
- It reads records, not files, and writes `proposal` records (global, `project` set, `queued`).
- `/retro` lists them with `fyr record list --global --kind proposal --project <root>` and sets their status.

## Failure modes
- **A duplicate ID after two boxes each add `BUG-NNN`:** load refuses with both line numbers, and the operator renumbers one line by hand.
- **`trail.jsonl` conflicts in git:** load refuses on the marker. It is resolved as text, with one record per line.
- **`brain.db` is lost:** project records come back from each project's `trail.jsonl` on the next call in it. Memory, feedback and proposals are lost, as they are today with a lost `~/.config/fyr`. Stated in R7.
- **A symlinked export path:** refused.
- **An agent cut off before it replies:** its scratch draft survives.
- **`import` on a dirty tree:** refused.

## Verification
- **Unit:**
  - schema and verbs, exit codes, `edit` without `--body` keeping the body, an empty body refused, a body over 1 MiB refused;
  - global-only kinds refused in a project;
  - IDs: max+1, `ADR-047a`, past 999, two processes allocating at once getting distinct IDs;
  - export: byte-stable for the same rows, sorted, `memory` and `feedback` excluded;
  - load: a changed file replaces rows; a duplicate ID, a conflict marker or bad JSON refused with its line number;
  - a symlinked `.agents` refused (the symlink proven to exist first);
  - `printable` applied by `get`;
  - search ranking a record above a file;
  - the unmigrated refusal;
  - import on a fixture tree: an open plan kept, a closed plan dropped, a closed bug trimmed to its rule and kept as `BUG-NNN`, a referenced record kept (D8), ADRs one record each, `--dry-run` writing nothing;
  - the `fyr memory` alias round trip;
  - the dream's idle rule (a project touched within 24 h skipped, an idle one with new records dreamed, an idle one with nothing new skipped).
- **Integration:**
  - `fyr record import --dry-run` on this repository prints counts per kind;
  - after the import, `fyr search "trailer co-authored"` prints BUG-177 first;
  - deleting `brain.db` and running `fyr record list --open` restores the project records from `trail.jsonl`.
- **Install:** a scratch home, then `status` with no warning.
- **Behavioural (Antigravity kit, n of N):** `feature` (plan stored as a record), `bug`, `review` (the reviewer's draft stored), `handoff`, `project-memory` (alias).
- **E2E criteria:**
  - A fresh clone on a scratch home with no `brain.db` resumes in a new Antigravity session from `fyr record list --open` and `get`.
  - A second clone sees a record added and pushed from the first after `git pull`.
- **Size:** both context pins and the listing budget pass.

## Phases
- [x] Cold-reader check: `reviews/2026-10-25-review-sqlite-trail-plan.md`, answered by D5–D22

### Phase 0 — Requirements and ADR
- [x] The operator approves the exact requirements text below, in their own turn — who: user — expect: the approval quoted as a D-row
  - Approved 2026-10-25: "approve requirements as written" (D23). Applied word for word, except one comma after "since their last dream" in R3, so that the clause that follows still attaches to the timer.
  - **R0, replacing the paragraph:**
    > `fyr` is the second brain of the Antigravity CLI. It keeps what a session cannot hold on its own: the context, skills, rules and agents (`install`); every record of every project's trail, with its metadata, in one store the CLI searches when it needs to (`record`, `search`), and the project memory in the same store (`memory`); the shape of every session (`metrics`); and the consolidation of idle projects (`dream`). The CLI asks it for help at the moments the context names: resume, before touching an area, at a phase end, when a skill or context seems missing, and when work starts drifting. It grows to evaluate projects and catch drift in the binary and its specs, never in the context, whose hand-written part stays under its size pin. The brain is consulted, never consulting: nothing in `fyr` acts on the CLI between its turns (R3), and the CLI learns only what it asks for. (ADR-061, ADR-062, ADR-063.)
  - **R3, in the dream exception:**
    > "run nightly by an OS user timer" → "run nightly by an OS user timer, over the projects idle for at least a day that have something new since their last dream"
    >
    > "reads every indexed project's trail and memory, the feedback log" → "reads those projects' records and memory and the global feedback records"
    >
    > "writes only under `~/.config/fyr/`, its proposals to `proposals/dream/`" → "writes only under `~/.config/fyr/`, its proposals as global `proposal` records"
  - **R4, the dependency sentence:**
    > "used only by the search index and the metrics store" → "used only by the search index, the metrics store and the brain store"
    >
    > After the metrics-store sentence, add: "The brain store (`~/.config/fyr/brain.db`) is a store of record like the metrics store: project records are also exported to each project's `.agents/trail.jsonl`, which git holds and from which they are restored; memory, feedback and proposal records exist only in the store; `uninstall` leaves it."
  - **R7, replacing the paragraph:**
    > Every project keeps its scratch in `.agents/scratch/` (gitignored) and its trail as records in the brain store: plans with task checkboxes, reviews, test logs, changes, handoffs, numbered bugs, ADRs, retros, learnings, specs, deploy and testing guides. They are written and read through `fyr record`, searched with `fyr search`, and exported to one committed file, `.agents/trail.jsonl`, so a zero-context session on any machine can resume and does not repeat a mistake. Its memory is `memory` records in the same store, local to the machine and never exported. The harness's own state (manifest, the brain store, the global search index, the metrics store) lives in `~/.config/fyr/` and nowhere else. (ADR-015, ADR-063.)
  - **R8, after "no verb sends the machine's corrections, proposals, memory or metrics anywhere":**
    > "; `feedback` and `proposal` records are global only, `memory` records are never exported, and `fyr record` refuses the first two in a project"
  - **Known limits and Non-goals:** the three `.agents/docs/...` paths become `TEST-2026-09-03-verify-real-home`, `TEST-2026-09-04-verify-mac-real-home` and `PLAN-2026-09-03-research-hooks`, read with `fyr record get`. D8 keeps them.
- [x] ADR-063: the brain store. It names the schema, the export, the load rule, the scopes, the agent rule, the dream's idle rule and the rejected alternatives — who: AI — expect: present
- [x] `REQUIREMENTS.md`, `ROADMAP.md` (Now: this plan; Next: items 2–4) and the ADR, in one commit — who: AI — expect: `git show --stat` lists exactly those three files — done: 0aaaf0a

### Phase 1 — The store and the verbs
- [ ] Tests first (§ Unit, all but import, memory alias and dream) — who: AI — expect: red, then green
- [ ] `internal/brain`, `cmd/fyr` `record add|get|edit|list`, export and load, and records-first search — who: AI — expect: green — docs: `specs/fyr/record.md` (imported in Phase 2 as `SPEC-fyr-record`), `specs/fyr/search.md`, the README verb list
- [ ] Checkpoint: full suite, a scratch install with `status`, and `security` over the export path trust, the load parser and `get`'s output — expect: exit 0, `## Outcome` written

### Phase 2 — Import, memory alias, this repository's migration
- [ ] Tests first (§ Unit: import and the memory alias) — who: AI — expect: red, then green
- [ ] `fyr record import`, `import --global`, and `fyr memory add|show` over `memory` records — who: AI — expect: green — docs: `SPEC-fyr-record`, `SPEC-fyr-memory-loop`
- [ ] The kit moves to `.agents/kit/`, with its path updated in the tests and docs — who: AI — expect: `grep -rn "agy-rel/run.sh" --include=*.go --include=*.md . | grep -v "\.agents/kit"` prints nothing
- [ ] Import this repository: dry run, read the counts, the real run, `git rm` by name from the printed list — who: AI — expect: `git ls-files .agents/docs | wc -l` prints 0; `fyr record list --open --kind plan` names the plans that have an unticked box
- [ ] The tests that read `.agents/docs` read records or the export — who: AI — expect: full suite exit 0
- [ ] Checkpoint: full suite; `fyr search "trailer co-authored"` prints BUG-177 first; `brain.db` deleted, then restored by `record list` — expect: all three shown

### Phase 3 — Skills, agents, context, docs
- [ ] The context: resume is `fyr record list --open`, then the newest handoff, then `get` on the open plans; the ask-for-help moments named in R0 (a missing skill or context, drift) point at `fyr search`/`record`; the Definition of Done names record kinds; the `docs/` sentence goes. Both pins stay green — who: AI — expect: pins pass; `grep -nE "docs/|<docs-root>|\.agents/docs" context/AGENTS.md` prints nothing
- [ ] Every skill uses `fyr record`; `feedback-log` writes `feedback` records; `retro` reads `proposal` records; `init-project` scaffolds only `.agents/trail.jsonl` — who: AI — expect: D19's grep prints nothing outside a migration note (must-fail first: record the count now)
- [ ] All eight agents use D12's scratch-draft rule — who: AI — expect: `grep -L "\.agents/scratch/agents/" agents/*/agent.md` prints nothing
- [ ] README, root `AGENTS.md` and `REQUIREMENTS.md`'s paths become record IDs — who: AI — expect: `grep -rn "\.agents/docs" README.md AGENTS.md REQUIREMENTS.md` prints nothing
- [ ] Checkpoint: `/review --docs`, a reviewer, the Antigravity rows (feature, bug, review, handoff, project-memory) reported n of N, then the verifier — expect: all pass

### Phase 4 — The dream on idle projects
- [ ] Tests first (§ Unit: the idle rule), then the code: idle selection, records as input, `proposal` records as output — who: AI — expect: red, then green; `fyr dream --all --dry-run` names which projects it would dream and why — docs: `SPEC-fyr-dream`, `skills/dream`
- [ ] Checkpoint: `security` over the dream change (what leaves the machine is unchanged), then the verifier — expect: pass

### Phase 5 — E2E and close
- [ ] E2E criteria 1 and 2 on fresh clones in scratch homes — who: AI (verifier) — expect: PASS, stored as a `testing` record
- [ ] `change` and `handoff` records, then commit and push — who: AI — expect: `fyr record list --open --kind plan` names only the next roadmap plan

## Risks
**Assumptions:**
- **Antigravity's main thread runs `fyr` from a skill.** Checked: `testing/2026-10-25-verify-fyr-e2e-c4885ae.md:24` (`project-memory` ran `fyr memory add`, PASS).
- **The kept records fit comfortably in one `trail.jsonl`.** Checked by Phase 2's dry-run counts before anything is removed.

## Decisions
- **D1 — An SQLite store plus a committed export.** Superseded in its location by D21 — by: the operator ("DB + committed text export").
- **D2 — Agents return text and the main thread stores it, for all eight agents** — by: the operator; "all eight" is the AI's extension.
- **D3 — Everything under `.agents/docs` moves, specs and guides included. Closed records are dropped (D8)** — by: the operator.
- **D4 — `feedback` and `proposal` are global; the agent picks a `learning`'s scope** — by: the operator ("the agent decides"); R8 limit from cold read round 1.
- **D5 — One file per record.** Superseded by D21 — by: the operator, then reversed by the operator.
- **D6 — Status per kind (Interfaces table)** — by: cold read round 1.
- **D7 — IDs.** `BUG-NNN` and `ADR-NNN[a-z]` keep their numbers, allocated in a transaction; the other kinds use `<KIND>-<date>-<slug>`. A duplicate after a merge is refused, never renamed — by: cold read round 1.
- **D8 — What import keeps.**
  - Open plans, the newest handoff, every ADR, open bugs, specs and guides.
  - The closure of what those, `REQUIREMENTS.md`, `AGENTS.md`, `README.md`, `context/`, `skills/`, `agents/` and `rules/` reference.
  - Closed bugs, trimmed to their title and prevention rule. They keep `BUG-NNN` and are the learnings.

  Everything else is dropped; git history keeps it — by: cold read round 1.
- **D9 — `edit --body` reads stdin; `add` refuses an empty body** — by: cold read round 1.
- **D10 — `record add` refuses `feedback` and `proposal` in a project** — by: cold read round 1.
- **D11 — Lowercase kinds, matching `search --kind`; `learning` and `guide` added; `EVAL` and `CRIT` struck (a critique is a `review`)** — by: cold read round 1.
- **D12 — An agent's draft goes to `.agents/scratch/agents/`, and the main thread stores it from that path** — by: cold read round 1, within D2.
- **D13 — Import removes nothing; the main thread runs `git rm` by name** — by: the global context's rule.
- **D14 — Memory is `memory` records in the brain, project-scoped and never exported. `fyr memory add|show` stays as an alias.**
  - The store's trust model is replaced by the store living under `~/.config/fyr/`.
  - Supersedes the earlier "memory unchanged".
  - By: the operator ("keep them in the db instead"; "too many files").
- **D15 — The dream writes `proposal` records** — by: cold read round 1.
- **D16 — In an unmigrated project, `record` refuses everything but `import`, and search keeps the old tree** — by: cold read round 1.
- **D17 — The kit moves to `.agents/kit/`. A script is not a record** — by: cold read round 1.
- **D18 — No map line moves.** `record` joins the README verb list and `TestHarnessMap`, and records stay on the machine apart from the project's own git — by: cold read round 1.
- **D19 — The restatement grep covers `docs/`, `<docs-root>`, `.agents/docs`, `feedback.jsonl`, `retros.jsonl`, `proposals/` and `.agents/memory`** — by: cold read round 1.
- **D20 — Writes are transactions in the store, plus temp-file-and-rename for the export.** There is no lock file — by: cold read round 1.
- **D21 — 2026-10-25 — Location: one store, `~/.config/fyr/brain.db`, for every scope. Each project carries one file, `.agents/trail.jsonl`.**
  - The file wins on load when git changed it; every write exports at once.
  - This moots the cold read's file-index findings: the per-file oid keying, the 1 MiB cap on one file, and the escape grammar.
  - By: the operator ("to many files … keep them in the db instead with metadata"; "go all approved" over the seven-row design).
- **D22 — The dream works on projects idle for 24 hours or more that have something new since their last dream** — by: the operator ("dreaming when projects are idle and not used").
- **Rejected options:**
  - **A `.db` per project committed to git:** a binary nobody can review.
  - **A database with no export:** a lost disk loses the trail, and other boxes never see it.
  - **One file per record (D5):** the many files the operator asked to remove.
  - **JSONL as the only store:** not searchable without loading it in full.
- **D23 — 2026-10-25 — The requirements text in Phase 0 is approved and applied word for word, plus that one comma** — by: the operator ("approve requirements as written").
- **D24 — Phase 1's build decisions, recorded at its checkpoint** — by: the AI (the Phase 1 agent proposed them; the spec `specs/fyr/record.md` states each):
  - A linked worktree writes its own checkout's `trail.jsonl`, the one its branch commits; the scope stays the main worktree's.
  - A missing `trail.jsonl` loads nothing and keeps the rows; the next write writes the file again.
  - The export is written inside the write's transaction, before the commit, so the file and `meta.export_sha` describe one state.
  - While `trail.jsonl` does not parse, reads refuse too: the store would answer from rows the file no longer holds.
  - The FTS table keys on an integer `rid`, not the composite key, whose implicit rowid `VACUUM` may renumber.
  - The global scope refuses only `memory`; every other non-global-only kind is accepted there.
  - `--project` needs `--global`.
  - Record hits appear only in a words search; `--refs` and `--open` print file results.
  - Opening a new store retries `SQLITE_BUSY` for 10 s, the busy timeout, because the WAL switch race does not wait on it.
- **D25 — An ID given with `--id` may be the kind's prefix and any lowercase slug (`SPEC-fyr-record`, `TEST-2026-09-03-verify-real-home`); an ID `fyr` generates always carries the stamp.** The import and R7's Known-limits IDs need the short form; the stamp only makes a generated ID unique — by: the AI, settling the handoff's first contradiction.
- **D26 — Global-only kinds are exactly `feedback` and `proposal` (D10).** `memory` is project-only (D14); every other kind may be global, a `learning` by the agent's choice (D4) and a `retro` by `import --global` — by: the AI, settling the second contradiction.
- **D27 — The unmigrated refusal names `fyr record import` before Phase 2 builds it.** The refusal states the one way out, and Phase 2 makes the command exist; no release is cut between the two (no tag is approved) — by: the AI, settling the third contradiction.
