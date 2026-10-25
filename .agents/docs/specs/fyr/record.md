---
owner: main
date: 2026-10-25
---
# `fyr record` — the brain store

Status: Phase 1 built 2026-10-25 (plan `plans/2026-10-25-sqlite-trail.md`, ADR-063): the store, `add`, `get`, `edit` and `list`, the export and the load, and record hits in `fyr search`. `record import` and the `fyr memory` alias are Phase 2 and do not exist yet. Requirements: R4 (the brain store is a store of record), R7 (the trail is records), R8 (`feedback` and `proposal` are global only). Code: `internal/brain`, `cmd/fyr/record.go`.

## What it is

One SQLite store, `~/.config/fyr/brain.db` (under `--home` when given), holds every record of every project's trail with its metadata, the global records, and each project's memory. A project's records other than memory are also written to one committed file in that project, `.agents/trail.jsonl`, which is the backup and how records travel between machines through git. Nothing but `fyr` reads that file.

The store is a store of record like `metrics.db`: it migrates forward (`PRAGMA user_version`), a store a newer binary wrote is refused untouched, and `uninstall` leaves it. The directory is created 0700 and the database and its `-wal` and `-shm` 0600; a database or companion file that is a symlink or not a regular file is refused (the search index's rule, `search.PrepareDBFiles`).

## Commands

```
fyr record add <kind> --title T [--status S] [--id ID] [--global [--project ROOT]]   < body
fyr record get <id>... [--global]
fyr record edit <id> [--status S] [--title T] [--body] [--global]                     [< body]
fyr record list [--kind K] [--open] [--global [--project ROOT]] [--all]
```

- **Scope.** Without `--global` a verb works in the project scope of the git checkout holding the working directory. The scope key is the main worktree's top level, resolved by `trail.Roots` exactly as `memory.Root` resolves it (git's `--show-toplevel` in the main worktree, which must be the working directory or a parent of it; from a linked worktree the common dir's parent, and only when the common dir registers that worktree; a bare repository refused). Every worktree of a repository therefore shares one scope. `--global` selects the global scope, which needs no checkout.
- **`add`** reads the body on stdin and prints the new ID. The body must be UTF-8 text with no control character other than tab and newline (a NUL, an ESC and the `\x01`/`\x02` a search snippet marks matches with are refused), at most 1 MiB; CRLF is folded to LF, leading blank lines and trailing whitespace are dropped, and an empty body is refused. The title is one non-empty line of at most 512 bytes with no control character. `--id` stores the record under that ID instead of the next one (it must fit the kind's grammar and be free in the scope). `--project ROOT` (with `--global` only) names the project a global record is about; it is stored absolute, as the main worktree's root when ROOT is a checkout.
- **`get`** prints, for each ID in the order given, a header line in the list format (plus `  (project ROOT)` for a global record about a project) and then the body. Records are separated by a blank line. Every line goes through the printable filter of `fyr search` (`search.Printable`, BUG-152): control characters, format characters and line or paragraph separators print as `?`; tabs and newlines are kept. An ID the scope does not hold is a refusal naming it, after the found records are printed.
- **`edit`** changes the status, the title or, with `--body`, the body read from stdin. Without `--body` the body is kept (D9). At least one of the three is required. A plan's status follows its new body.
- **`list`** prints `ID  kind  status  date  title`, newest first (by date, then by last update, then by ID), with `-` for a handoff's empty status. `--kind` filters by kind and `--open` keeps the open records (table below). `--project ROOT` (with `--global` only) keeps the global records about that project: `fyr record list --global --kind proposal --project <root>` is how `/retro` lists a project's proposals. `--all` lists every scope, each line ending in `  [<scope root>]` or `  [global]`; it cannot be combined with `--global`.
- A flag the subverb does not take is a usage error (for example `record add --open`), never parsed and ignored.

## Kinds

| Kind | Scope | ID | Exported |
|---|---|---|---|
| `bug` | project or global | `BUG-NNN` | yes |
| `adr` | project or global | `ADR-NNN`, `ADR-NNNa` | yes |
| `plan` `review` `testing` `change` `handoff` `retro` `learning` `spec` `deploy` `guide` | project or global | `<PREFIX>-<YYYY-MM-DD-HHMM>-<slug>` | yes |
| `memory` | project only | `MEMORY-<YYYY-MM-DD-HHMM>-<slug>` | never (D14) |
| `feedback` `proposal` | global only | `FEEDBACK-…`, `PROPOSAL-…` | never |

The prefix is the kind in capitals, except `testing`, whose prefix is `TEST`. `add` refuses `feedback` and `proposal` in a project (R8, D10) and `memory` with `--global` (D14). The other kinds are accepted in the global scope too: a `learning`'s scope is the agent's choice (D4), and `import --global` (Phase 2) brings `retros.jsonl` there.

## IDs (D7)

- `BUG-NNN` and `ADR-NNN` are numbered per scope: the scope's highest number of that kind plus one, read inside the write's transaction, compared as numbers (`BUG-1000` follows `BUG-999`), at least three digits and at most nine. `ADR-047a` counts as 47.
- Every other kind gets `<PREFIX>-<YYYY-MM-DD-HHMM>-<slug>`, the stamp from the local clock and the slug from the title (lowercase letters and digits, every other run as `-`, at most 48 characters, `record` when nothing is left). When that ID is taken in the scope, `-2`, `-3` … is appended.
- A given `--id` must match the kind: `BUG-NNN`, `ADR-NNN[a-z]`, or the kind's prefix, a dash and a lowercase slug (`[a-z0-9][a-z0-9._-]*`, at most 128 characters). The short form (`SPEC-fyr-record`) is what Phase 2's import gives a spec.
- A duplicate ID after a merge is refused by the load, never renamed.

## Status (D6)

| Kind | Values | Open when | Starts as |
|---|---|---|---|
| plan | derived from the body: `open` while it holds a line starting `- [ ]` outside a backtick fence (`search.Unticked`, the same rule as `fyr search --open`), else `closed` | `open` | derived |
| bug | `open` `fixed` `accepted` `obsolete` `graduated` | `open` | `open` |
| adr | `accepted`, `superseded:ADR-NNN[a-z]` | never | `accepted` |
| spec, deploy, guide | `current` `retired` | `current` | `current` |
| proposal | `queued`, `applied:<commit>` (7–40 lowercase hex), `rejected` | `queued` | `queued` |
| handoff | none (the newest is the live one) | never | none |
| everything else | `closed` | never | `closed` |

`--status` on a plan or a handoff is a usage error, as is a value the kind does not take.

## Export: `.agents/trail.jsonl`

- One record per line, `{"date","id","kind","status","title","updated","body"}` with the keys in that order, JSON without HTML escaping, lines sorted by ID (prefix, then the number numerically, then the letter, else as text), and a final newline. The same rows always give the same bytes.
- `memory`, `feedback` and `proposal` records are never exported.
- Every write in a project rewrites the file inside the write's transaction: the rows are read, the file is written to a temporary file in `.agents/` and renamed over the old one (unchanged bytes are not rewritten), and its SHA-256 is stored in `meta.export_sha`, all before the commit. A write refused before the rename (a bad body, a refused load, a symlink) leaves both the store and the file as they were. When the rename lands and the commit then fails, the file holds the record and the store does not; the next call's load takes the file.
- A symlinked or non-directory `.agents`, and a `trail.jsonl` that is a symlink or not a regular file, are refused before anything is read or written. The file is created 0644; commit it.
- The export is the checkout's own file: in a linked worktree a write goes to that worktree's `.agents/trail.jsonl`, the one its branch commits, while the scope stays the main worktree's.

## Load: the file wins (D21)

- Before any `record` verb or `search` in a project, the checkout's `trail.jsonl` is hashed. When the hash differs from `meta.export_sha` (a pull, checkout or merge changed the file), the scope's exported rows are deleted and the file's lines inserted in one transaction, under the write lock, and the hash is stored. Memory rows are untouched.
- A missing file loads nothing and keeps the rows; the next write writes the file again. A lost `brain.db` is restored from the file on the next call.
- The file is refused, with its line number, for: a git conflict marker (`<<<<<<<`, `=======`, `>>>>>>>`, `|||||||` at the start of a line); a line that is not one JSON object with exactly the seven keys; a kind that is never exported or unknown; an ID, status or date the kind does not take; an `updated` that is not RFC 3339; an empty title, one over 512 bytes, or one holding a control character; a body over 1 MiB or holding a control character other than tab and newline; a line over 6 MiB plus 64 KiB (a full body with every byte escaped), refused before it is decoded; and an ID an earlier line holds (`line N: duplicate ID X (first at line M)`). A blank line is skipped. Files over 256 MiB are refused. The file is streamed line by line, so memory follows the records, never the line count, and it is opened with `O_NOFOLLOW|O_NONBLOCK`, so a symlink or FIFO swapped in after the check is refused (security review 2026-10-25).
- While the file does not parse, every `record` verb in the project refuses (reads too, since the store would answer from rows the file no longer holds), and `search` prints `records: not searched: <reason>` before its file hits. `--global` verbs are unaffected.

## Unmigrated project (D16)

In a checkout with a `docs/` or `.agents/docs/` directory and no `.agents/trail.jsonl`, every `record` verb refuses with `this project's trail is still files under <dir>/: migrate it with \`fyr record import\` first`. Nothing is created. `search` keeps answering from the old tree. `--global` verbs still work.

## Search

`fyr search <words>` prints the record hits first, then the file hits as before (`specs/fyr/search.md`). The record hits come from `records_fts` (title weighted twice the body, porter stemming): records holding every word rank first, and the remaining slots go to records holding any of them, up to `--limit` records. In a project the search covers the project's scope and the global scope; with `--global`, every scope. Each hit prints `ID  kind status  date  title` and a snippet line. The ID is prefixed `global:` for a global record and, with `--global`, `<root>:` (the full scope path) for another project's record, so two clones of one base name never print alike. A `--kind` that is a record kind filters the records. `trail` takes every record, and a file-only kind (`code`, `doc`) takes none. When there is no `brain.db` and no `trail.jsonl`, nothing is created and no records section is printed. `--refs` and `--open` print file results only (Phase 1).

## Concurrency (D20)

Every write and every load runs inside `BEGIN IMMEDIATE` on one connection, so two processes never allocate the same number or export over each other's rows. The wait is the driver's busy timeout (10 s), after which the verb refuses with `the brain store is busy`. A new store opened by two processes at once is retried for the same 10 s, because SQLite answers that race with an immediate `SQLITE_BUSY`. There is no lock file.

## Exit status

0 with or without output. 1 for a refusal: an empty or oversize body, a global-only kind in a project, `memory --global`, a duplicate `--id`, an ID not found, an unmigrated project, a symlinked export path, a `trail.jsonl` that does not parse, not a git checkout without `--global`, a newer schema or a busy store. 2 for a usage error: no or unknown subverb, a missing kind or ID, an unknown kind, a missing or multi-line `--title`, an `--id` or `--status` the kind does not take, `edit` with nothing to change, a flag the subverb does not take, `--project` without `--global`, `--all` with `--global`. The reason is printed on stdout as `fyr record: <reason>`, through the printable filter.

## Schema

```sql
records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL,
        status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
        project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, UNIQUE(scope, id));
records_fts USING fts5(title, body, content='records', content_rowid='rid', tokenize='porter unicode61');
meta(scope TEXT PRIMARY KEY, export_sha TEXT, last_dream TEXT);
```

`scope` is the main worktree's absolute root, or `''` for global. `date` is `YYYY-MM-DD`, the day the record was added. `updated` is RFC 3339 in UTC, set by every write. `rid` is the integer rowid the FTS table keys on: the plan's composite key alone would leave an implicit rowid that `VACUUM` may renumber. Triggers keep `records_fts` in step on insert, update and delete. `last_dream` is for Phase 4.
