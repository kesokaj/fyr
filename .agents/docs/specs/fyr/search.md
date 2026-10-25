---
owner: main
date: 2026-10-25
---
# `fyr search` and `fyr index`

Status: the project index and the global search index built 2026-09-29 (plan `plans/2026-09-29-search-index.md`, Phases 1 and 2). Requirement: R4 (one dependency, `modernc.org/sqlite`), ADR-045. Since 2026-10-25 a words search prints the brain store's record hits before the file hits (ADR-063, `specs/fyr/record.md` § Search).

## What it is

A derived SQLite index the agent queries instead of reading whole files. Git and the files in `~/.config/fyr/` stay the source of truth: the index is a cache, never committed, never sent by `report`, and deleting it loses nothing but the next query's rebuild time (about two seconds for this repository: 2.1 s for 1,351 files on 2026-10-01).

## Commands

```
fyr search [--limit N] [--kind K] [--global] <words...>
fyr search --refs <BUG-NNN|ADR-NNN|path> [--kind K] [--global]
fyr search --open [--kind K] [--global]
fyr index [--global] [--rebuild]
```

- **Records first.** A words search first prints the brain store's hits (`~/.config/fyr/brain.db`, `specs/fyr/record.md`): a `records: N hits for "…"` header, then per record `ID  kind status  date  title` and a snippet line, from the project's scope and the global scope (`global:ID`), or with `--global` from every scope (`<root>:ID`, the full scope path, for another project's). They are ranked by BM25 over title (weighted twice) and body, every word first and then any word, up to `--limit` records; the file hits that follow keep their own `--limit`. A changed `.agents/trail.jsonl` is loaded first. With no records found nothing is printed, and with no `brain.db` and no `trail.jsonl` nothing is created. A `trail.jsonl` that does not parse prints `records: not searched: <reason>` and the file hits still follow. The usage check runs before the records, so a usage error prints only its reason. `--refs <ID|path>` prints, before the file results, `records: N naming <ref>` and the records that name the ref as written — the ID's own record first, then every record whose title or body holds the path or ID as a whole name (no path character before it, nor after it unless the ref is a directory ending in `/`), each with the line that names it; `--open` prints `records: N open` and the open records (the status table's "Open when"). Both cover the project's and the global scope, every scope with `--global`, and take `--kind` and `--limit` (D30).
- **Words** are each quoted for FTS5, so `BUG-049`, `a.go` or `NOT` are searched as text, never as query syntax. Chunks holding every word rank first; the remaining slots are filled with chunks holding any of them (a chunk matching both rounds is printed once and counted once against `--limit` and the per-file cap), and the header line says how many had every word (`search: 10 hits for "…", 2 with every word (…)`). Porter stemming: `verify` finds `verifies`. Hits are ranked by BM25 with a heading weighted twice the body, at most 2 per file, `--limit` (1–50, default 10) in all.
- **Each hit** prints `path:line  kind[ ID status][  date]` and a one-line snippet (at most 200 characters, matched terms in `»«`). Control characters in indexed text, and Unicode's format characters, line separators and paragraph separators (classes Cf, Zl, Zp, and the default-ignorable characters outside them: zero-width and tag characters, direction marks, the byte-order mark, variation selectors, fillers), print as `?`, so a file cannot drive the terminal, forge a line or carry text nobody sees; the cost is that a zero-width joiner or a variation selector in an emoji sequence shows as `?` too. The line is the first line of the chunk holding a matched term.
- **`--refs X`** prints the ID's own record (`ADR-040  <path>:<line>  Superseded by ADR-041  <date>  <title>`, or `no record`), or a path's state (`tracked`, `modified`, `untracked`, `absent`, or `directory` for a trailing `/`), then `referenced by N:` and one entry per file and record that names X, newest record date first, with `×N` when it names X more than once. A path matches as written and relative to the docs root (the trail writes `plans/x.md` for `.agents/docs/plans/x.md`); no basename matching, but a bare file name that matches no path prints `hint: did you mean --refs <path>` for up to five tracked paths ending in it.
- **`--open`** lists bugs with `status: open` and plans with an unticked `- [ ]` outside fenced blocks, newest first.
- **`--kind`** is one of `bug adr plan change handoff review testing retro spec deploy trail code doc feedback proposal learning guide memory` and filters every mode by the kind of the printed path, and the record hits by the record's kind. `trail` is everything under the docs root, and every record; `code` and `doc` take no record; `feedback` and `proposal` exist only in the global search index and as global records; `learning`, `guide` and `memory` exist only as records.
- **`index`** prints `files`, `chunks`, `records`, `refs`, `changed` (by this reconcile), `bytes` (database and WAL) and `ms`. `--rebuild` deletes the database and builds it again.

Exit status: 0 with hits or without; 1 on a refusal or an error; 2 on a usage error (a flag the verb does not define, a stray argument, a `--limit` outside 1..50, an unknown `--kind`, no mode or two modes at once, `--rebuild` given to `search`; the reason on stdout, BUG-168), as every verb since 2026-10-01.

## Where the indexes live

| Index | Path | Covers |
|---|---|---|
| Project | `<checkout>/.agents/scratch/search.db` (+ `-wal`, `-shm`) | every tracked, modified and untracked-not-ignored file (except what lies under a directory holding a `.fyr-noindex` file, or one under its name before the rename, `.sh-agent-noindex`, honoured the same way (plan `2026-10-24-fyr-agy-only.md` D12; `TestOldNoIndexMarkerStillCounts`): data the project carries, such as the harness's `skills/google-catalog/catalog/`, is not the project's own text; the header of a words search and of `--open` then ends `; left out by a noindex marker: <dir>/, <dir>/` (sorted, the checkout's root as `./`; here `skills/google-catalog/catalog/`; `--refs`, `index` and `--global` print no list; `TestHeaderNamesLeftOutDirectories`), the marker counts committed, staged or not yet added, one at the checkout's root leaves the whole checkout out of the project index and of the global one, and a reconcile drops what was indexed before it appeared) under 1 MiB with no NUL byte in its first 8 KiB; symlinks, submodules and secret-shaped names (the installer's list, matched in any case: `.env`, `.env.*`, `*.env`, `*.pem`, `*.key`, token and credential files, …) skipped; a secret inside an ordinarily named file is indexed |
| Global search index | `~/.config/fyr/search.db` (+ `-wal`, `-shm`), under `--home` when given; the directory is created 0700. Both indexes' files are mode 0600 (an older 0644 file is tightened on open): they hold the text of every file read, whatever that file's own mode | `feedback.jsonl`, `retros.jsonl`, `proposals/*.md`, and the `.agents/docs` trail of every checkout that has one and has run `search` or `index` |

The global file is named "the global search index", never "cross-project index": R7 already gives that name to `retros.jsonl`. A checkout with an `.agents/docs` directory registers itself on any `search` or `index` run inside it, `--global` included, at most once a day (a third-party clone with a plain `docs/` never registers, so it cannot answer `--global` queries or feed `/retro`); `--global` then reconciles `~/.config/fyr/` and each registered checkout's docs root (as `<name>:<path>`, `name~2` for a second checkout with the same base name), and drops a registration whose root is no longer a git checkout or no longer has `.agents/docs` (a plain `docs/` is never served in its place). Run outside a checkout, `--global` still answers. The registry lives in the database, so `index --global --rebuild` drops it, with a note; each checkout returns on its next search.

## Freshness: reconcile before every answer

Every git call runs through `internal/trail.Command`: no index lock, no fsmonitor, no signature check, `GIT_NO_LAZY_FETCH=1`. None of them starts a program the checkout's config or `.gitattributes` names; `git status` is never run, because it runs clean filters (BUG-165). A partial clone (`extensions.partialClone` set, or any remote marked `promisor`) is searched only when this machine's git is 2.44 or later, which honours `GIT_NO_LAZY_FETCH`. With an older git, the project search refuses it with a message naming the fix (update git), and `--global` skips that checkout for the query with a note, because a missing object would be fetched through the clone's own `core.sshCommand`.

1. `git rev-parse --show-toplevel` from the working directory.
2. `git ls-files -s -z`: path and blob ID of every index entry.
3. Modified tracked files.
   - `git ls-files -s --debug` (read line by line, without `-z`, so a name holding a newline cannot forge another entry's lines) gives the size and mtime the index holds for each entry. A name git quotes (non-ASCII under the default `core.quotePath`, or one holding a tab, quote or backslash) is skipped, so its file is hashed on every reconcile: correct, but slower. A file whose size and mtime (to the nanosecond) still match, and which is not racily clean (written in the second the index was), keeps the index's blob ID.
   - Every other tracked file is read, and its ID is computed from its own bytes as a git blob ID. When that ID differs from the index's, the file is modified. (`git hash-object --stdin-paths` misreads names with a trailing CR or a leading quote, so it is not used.)
   - A file the checkout runs through a clean filter therefore reads as modified and is indexed from its bytes.
   - Deleted paths leave.
4. `git ls-files -z --others --exclude-standard`: untracked files, each ID computed from its bytes. A rename is a deletion and an addition.
5. Everything derived from content (chunks, records, references) is stored once per blob ID, so a branch switch re-reads only content the index has never seen. Every query joins through the current path-to-blob view, which is what keeps another branch's content out of an answer.
6. When nothing changed, no write lock is taken; the reconcile costs about 15 ms here for the project index. `--global` reconciles every registered checkout's trail before answering (the same `git ls-files` reads per checkout, limited to its `.agents/docs`), so its cost grows with the number of registered checkouts and is not the 15 ms figure. A blob no view references is deleted after 14 days unseen.

No watcher, no service, no hook (R3, R8).

## Trail records

The docs root is `.agents/docs` when it exists, else `docs`.

| Record | Read from | ID, status |
|---|---|---|
| Bug | `bugs/BUG-NNN*.md` | frontmatter `id:` (else the file name), `status:`, `date:`, first `# ` heading |
| ADR | `architecture/DECISIONS.md` headings `## ADR-NNN: <title>     (<date>) — <status>` | one record per heading; status is everything after the dash |
| Plan | `plans/*.md` | `open: N unticked` or `closed`; line of the first unticked box |
| Other trail file | any `.md` under the docs root | its docs-relative path; `date:` |

References are record IDs (`BUG-NNN`, `ADR-NNN`) and path-shaped tokens on every line of every indexed file, with a trailing `:NN`, trailing punctuation and a leading `./`, `a/` or `b/` removed.

## Failure modes

| Case | Output | Exit |
|---|---|---|
| Not inside a git checkout, or no `git` | `search: not inside a git checkout; use grep` | 1 |
| `.agents/scratch/search.db` not gitignored | `search: .agents/scratch/search.db is not gitignored; add ".agents/scratch/*" to .gitignore (/init-project does)`; nothing created | 1 |
| `.agents` or `.agents/scratch` is a symlink | `search: .agents/scratch is a symlink; refusing to write through it` | 1 |
| `search.db`, `search.db-wal` or `search.db-shm` is a symlink or any other non-regular file (either index) | `search: <path>: <file> is not a regular file; refusing to open or write through it`; nothing is written through it | 1 |
| Another process holds the write lock past 10 s | `search: index busy (another fyr is indexing); retry, or use grep`; no answer printed | 1 |
| Unreadable database, or a schema other than 1 | `note: index at <path> was unreadable (or an older schema); rebuilt`, then the answer | 0 |
| Argument errors (`--limit` out of range, unknown `--kind`, two modes at once, no mode) | `search: <what is wrong>; see fyr help` | 2 |
| Global: a registered checkout is gone, or lost its `.agents/docs` | `note: dropped <root> (no longer a git checkout)`, or `(no longer has .agents/docs)` | 0 |
| Global rebuild | `note: N project registrations dropped; each returns on its next search` | 0 |
| Global: a registered checkout's check cannot run (for example `.agents` unreadable) | `note: skipped <root> this time (<error>)`; the registration stays | 0 |
| A file edited and then restored while `search` waits for the write lock | the edit stays indexed for that blob until `index --rebuild` (known limit, plan D35) | 0 |
| A copied project directory that brings its own `search.db` | trusted as it is: an index is only as trusted as the folder it sits in, and whoever can plant one can plant `AGENTS.md` and trail files there too; after copying a project in, run `fyr index --rebuild` (known limit, the operator's choice at the v1.13.0 gate) | 0 |
| Network filesystem | not supported: SQLite's WAL needs one host's shared memory; the SQLite error is printed | 1 |

`search.db` never leaves the machine (ADR-062). `fyr uninstall` leaves `~/.config/fyr/search.db`, which is not in the install manifest; delete it by hand if wanted.

## Measured (this repository, 2026-09-29, Linux amd64)

| Measurement | Result |
|---|---|
| Full build, 1,351 files (2026-10-01; 1,080 on 2026-09-29) | 2.1 s on 2026-10-01; 1.7 s (2.2 s before the inserts were prepared once and reference parsing skipped lines that cannot match) |
| Warm `search`, nothing changed, end to end | 23–67 ms |
| Recall@10 on 41 questions from this repository's history (`testing/2026-09-29-search-recall.md`; re-measured 2026-10-25 on go1.24.4 in `testing/2026-10-25-search-recall.md`, after the "open plan" gold was re-pinned and "open bug", with no bug open, became a refs question) | 0.505 (2026-10-25; 0.633 on 2026-10-01 and 0.653 on 2026-09-29 on the old fixture), against 0.237 for one grep pass; words 0.538 against 0.115, drift 0.170 against 0.172 |
| Warm `--refs` / `--open`, end to end | 47–60 ms / 20 ms |
| Database size | 21.7 MB (10,560 chunks, 40,850 references); 24.1 MB before the unused `refs(oid)` index was dropped |
| Binary size, linux/amd64 | 13.7 MB |
