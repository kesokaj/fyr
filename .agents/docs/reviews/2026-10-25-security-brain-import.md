---
owner: security
date: 2026-10-25
---
# Security review — brain-import   (2026-10-25)

Range reviewed: f28e8bc..ddaa522 -- internal cmd (focus internal/brain/import.go, memory.go, cmd/fyr/record.go printImport, cmd/fyr/main.go memoryCmd)

## Status: COMPLETE

## Verdict: FINDINGS

The worst problem is that the import can write a `trail.jsonl` that the load then refuses. `docDate` and the ADR heading parser accept any `NNNN-NN-NN`, so one typo in the project's own trail becomes `updated: "2026-02-30T00:00:00Z"`, for example a frontmatter `date: 2026-13-45`, a file named `2026-02-30-x.md`, or `## ADR-001: X (2026-02-30)`. `insertNew` never parses `Updated` as RFC 3339; `parseLine` does. On the importing home the export SHA matches, so nothing reloads and the import looks fine. On every other home, every clone and every teammate, each `record`/`search` verb refuses with exit 1. I reproduced this. By then the import has told the operator to `git rm` the source tree. Second: `import --global` reads `<project>/.agents/memory` through a symlinked `.agents`. A project that was indexed while sane and later gained a link, for example from a pulled commit, therefore imports another directory's memory into its own scope, serves it through `fyr memory show`, and prints the victim's files as paths to `rm`. I reproduced this too. The removal list also names files the import reported as "left as a file". It prints paths with shell metacharacters raw. `fyr memory add` bypasses the 512-byte, no-control-character title rule that the previous review installed. Terminal output, git invocation shape and the memory/feedback/proposal export boundary held up.

## Findings

- **[medium / confidence: high]** `internal/brain/import.go:291` (`updated := date + "T00:00:00Z"`), `import.go:414` (ADR `Updated: date + "T00:00:00Z"`), `import.go:597` (insertNew's pre-insert check omits `Updated`): the import writes a `trail.jsonl` line the load refuses, bricking the project on every other home.
  `docDate` and `adrDate` accept any `\d{4}-\d{2}-\d{2}` (`dateRe` is shape-only), so a frontmatter `date: 2026-13-45`, a file stamp `2026-02-30-x.md` or an ADR heading `(2026-02-30)` — one typo in the project's own trail — becomes `updated: "2026-02-30T00:00:00Z"`. `insertNew` checks ID, status, date shape and body size, never `time.Parse(time.RFC3339, r.Updated)`, which `parseLine` (export.go:247) requires.
  Reproduced (probe repo `.agents/scratch/sec-import/p1`, build of ddaa522): import exits 0, the trail is committed, `record list` on the same home works (the export SHA matches, so no load), and `record list --home <fresh>` prints `fyr record: trail.jsonl line 1: updated "2026-02-30T00:00:00Z" is not RFC 3339`, exit 1. Every record and search verb in that checkout on any other machine, any teammate's clone, or after `brain.db` is lost refuses until the file is hand-edited; by then the docs tree has been `git rm`'d per the import's own instructions.
  Remediation: in `insertNew`, refuse (or better, in `classify`/`splitADRs`, report as a problem and fall back to `today`) any record whose `Updated` does not parse as RFC 3339 and whose `Date` does not parse as `2006-01-02`; ideally run the exported line through `parseLine` itself before committing, so the import's check and the load's are one function.
  Test that fails without the fix: `TestImportBadDateLoads` — a fixture with `plans/2026-02-30-x.md` and an ADR heading `(2026-13-01)`; import, then open a session on a second `--home` (or delete the scope's `export_sha`) and `List`; assert no refusal (or that the import refused/reported the file before writing).

- **[medium / confidence: high]** `internal/brain/import.go:836-841` (`ImportGlobal` → `readMemoryStore(scope)`), `import.go:622-631`: the global import follows a symlinked `.agents` of an indexed project.
  `readMemoryStore` `Lstat`s only the last component, `.agents/memory`. `filepath.Join(root, ".agents", "memory")` resolves through a symlinked `.agents`, and `ImportGlobal` never calls `guardExportPath(scope)`. `fyr search` refuses such a project when it first indexes it. But the global index's project list is not re-validated, and a checkout can change after indexing: a pulled commit that replaces `.agents/` with a link, or an operator who moves things around.
  Reproduced (`.agents/scratch/sec-import/p4` → `victim/`): I indexed `p4` with a real `.agents`, replaced `.agents` with a symlink to `victim/.agents`, then ran `fyr record import --global`. Result: `record MEMORY-secret memory closed …/p4/.agents/memory/secret.md`. `fyr memory show` in `p4` prints the victim's memory, which is now stored in `p4`'s scope permanently (memory is never pruned). The printed `rm` list names `…/p4/.agents/memory/secret.md`, which deletes the victim's file through the link. If the victim is not itself an indexed project, its memory survives only in the wrong scope.
  The same unguarded read happens in the project `Import` when it runs from a linked worktree, because `guardExportPath(top)` checks this worktree and not `scope`.
  Remediation: in `readMemoryStore`, `Lstat` `root/.agents` and refuse a symlink or non-directory (or call `guardExportPath(root)`), and report it as a problem in `ImportGlobal`. Better still, open `root` with `os.OpenRoot` (Go 1.24) and read `.agents/memory` through the `Root`, which refuses any escaping link.
  Test that fails without the fix: `TestImportGlobalRefusesASymlinkedAgents`. Index a project, swap its `.agents` for a link to a second directory holding `memory/x.md` (assert `Lstat` shows a link first), run `ImportGlobal`, and assert there is no `MEMORY-x` record, a problem line naming the link, and no path under the link in `res.Memory`.

- **[medium / confidence: high]** `internal/brain/import.go:533-535` (`res.Remove` = every file read), `import.go:359`: files reported as "left as a file" are listed for `git rm`.
  `classify` reports a file outside the known trail directories as `not in a trail directory this import knows; left as a file`, but `Import` puts every tracked file under the docs root into `Remove`. The spec contradicts itself in the same way: `.agents/docs/specs/fyr/record.md:39` says "left as a file", and `:43` says "every tracked file under the docs root (to `git rm`)".
  The fallback root is `docs/`, which in a project that is not scaffolded is the project's real documentation. A `<docs-root>` argument such as `context` or `skills` lists those files too.
  Reproduced (`p5`): the dry run printed `problem docs/guide/install.md: … left as a file` and then listed `docs/guide/install.md` and `docs/overview.md` under "remove with git rm by name". An operator or agent following the instruction deletes documentation that was never imported. git history keeps it, so this is integrity rather than confidentiality, but the tool is directing the loss.
  Remediation: add to `Remove` only the files whose content became a record, plus the D8-dropped trail files and `bugs/INDEX.md`. Keep problem files out, and fix `record.md` § Import to say so.
  Test that fails without the fix: in `TestImportKeepsWhatD8Keeps` (or a new test), a fixture `docs/guide/install.md`; assert it appears in `Problems` and does not appear in `Remove`.

- **[low / confidence: medium]** `cmd/fyr/record.go:154-169` (`printImport`'s remove lists): file names are printed raw for a shell command the reader is told to run.
  `search.Printable` neutralises control characters but not shell syntax. Git tracks names such as `.agents/docs/plans/$(echo pwned).md`. The import even accepts it and slugs it to `PLAN-echo-pwned`, then prints it verbatim under "remove with git rm by name" (reproduced, `p5`). An agent that pastes the list into `git rm <names>` runs the command substitution. Names with spaces, quotes or a leading `-` break the same way.
  Remediation: refuse a tracked trail path outside `[A-Za-z0-9._/-]`, which is what IDs are built from anyway. Or print the removal as one ready command with each name single-quoted (`'` → `'\''`). Or print a `git rm --pathspec-from-file=- --pathspec-file-nul` recipe with the list NUL-separated in a scratch file.
  Test that fails without the fix: `TestRecordImport`, with a fixture `plans/$(x).md`, asserts the import refuses it, or that the printed line is quoted.

- **[low / confidence: high]** `internal/brain/memory.go:54-63,76` (`MemoryAdd` validates `--description` itself), `memory.go:121-132` (`MemoryShow`'s caps): `fyr memory add` skips the title rules from the previous review.
  The description is checked only for `\r\n\x00`, not by `cleanTitle`. It has no 512-byte cap (regresses brain-store finding 2) and no C0 refusal, so `\x01`, `\x02` and ESC are stored (regresses finding 5, the snippet-mark spoof).
  Reproduced (`p1`): `--description "$(printf 'mark\001zebra\002 hi\033[31mred')"` saved. A 40,000-byte description saved as `MEMORY-0big`, and from then on `fyr memory show` printed only `(… 5 more: …)`: the first line exceeds the 25 KB cap, so one memory hides every memory from session start. A name sorting first, such as `0…`, is enough. Who can trigger it: any session, including a prompt-injected one, that runs `memory add`.
  Remediation: run the description through `cleanTitle` (mapping its usage error to a refusal). In `MemoryShow`, truncate each line to a bound (for example `maxTitle` plus the ID) before the byte cap, so one entry cannot crowd out the rest.
  Test that fails without the fix: `TestMemoryAddTitleRules`, with a 513-byte description and a `\x01` description, both refused. `TestMemoryShowCapsLines` gets a case with one 40 KB-titled memory plus two normal ones, and both normal names must appear.

- **[low / confidence: medium]** `internal/brain/import.go:108-148` (`readDocs`), `import.go:423-450` (`outsideText`), `import.go:621-690` (`readMemoryStore`): there is a per-file cap but no total memory or time cap.
  Every tracked `.md` file under the docs root (up to 1 MiB each), every regular file under `context/`, `skills/`, `agents/` and `rules/`, and every memory file is held in memory at once. Those trees include untracked and ignored files: `outsideText` walks the filesystem, not `git ls-files`. Identical blobs dedupe in a pack, so a small clone can check out thousands of 1 MiB files. `ImportGlobal` repeats `readMemoryStore` for every indexed project and has no file-count cap.
  Not measured. The cost is bounded by the checkout's on-disk size, which the operator has already accepted at clone time, hence low.
  Remediation: walk `outsideText`'s directories via `git ls-files` (as `readDocs` does), and cap the total bytes read (for example 64 MiB) and the file count, refusing with a count past either cap.
  Test that fails without the fix: `TestImportCapsTotalRead`, with 70 files of 1 MiB under `skills/` and an assertion that the import refuses (or reports) instead of reading them all.

- **[low / confidence: high]** `internal/brain/import.go:121-138`, `426-430`, `651-656`, `800-805`: every read is `Lstat` followed by a path-based `os.ReadFile`, with no `O_NOFOLLOW`, no `O_NONBLOCK` and no `LimitReader`.
  If a file is swapped between the check and the read, three things can happen. A FIFO hangs the import. A symlink reads any file the user can read into a record body, which the project import then *exports into the committed* `.agents/trail.jsonl`. A file that grows is read unbounded (`insertNew` catches more than 1 MiB only after the read).
  This needs a concurrent writer to the checkout, the same precondition as the rebutted finding 3 of the brain-store review. Unlike that one, the fix here is cheap: the package already has `openNonBlocking` (`open_unix.go`, `O_NOFOLLOW|O_NONBLOCK`), which the previous review's finding 4 installed for exactly this.
  Remediation: open each file with `openNonBlocking`, `f.Stat()` for regular file and size, and read through `io.LimitReader(f, maxBody+1)`.
  Test that fails without the fix: a hook between `Lstat` and the read in `readDocs` that swaps a tracked plan for a symlink to a file outside the checkout. Assert a refusal (ELOOP) and that `trail.jsonl` does not hold the outside file's text.

- **[low / confidence: high]** `internal/brain/import.go:750-753` (`readLogLines` error is fatal), `import.go:785-789` (indented body unchecked), `import.go:744,825` (uniq suffix past the slug limit), `import.go:857-860`: `ImportGlobal` fails as a whole on one bad entry, contrary to its contract ("a line or file it cannot take is reported, never fatal").
  - A `feedback.jsonl` line over 1 MiB makes `bufio.Scanner` return `ErrTooLong`, so the import aborts.
  - A line under 1 MiB whose `json.Indent` output exceeds 1 MiB makes `insertNew` reject the whole transaction ("import built a record the load would refuse").
  - Two proposals whose 128-character slugs collide get `-2` appended, giving a 130-character tail, so `validID` fails and the import returns an error.
  These logs hold text that other projects' sessions wrote. The effect is that the migration cannot complete, not data loss.
  Remediation: read with a `bufio.Reader` and report an over-long line as a problem. Check each built record (`Updated`, body size, ID) per record and report it, instead of failing in `insertNew`. Leave room for the `-N` suffix in `slugOf` (truncate to 120).
  Test that fails without the fix: `TestImportGlobal`, with a 1.1 MiB feedback line, a 900 KiB line of deeply nested JSON, and two proposals with identical 140-character names. Expect three problem lines and the remaining records stored.

## Checked

- **Secrets:** none in the diff. The import logs no record content; problem lines carry paths and line numbers only. A memory body never reaches `trail.jsonl` (kind `memory` is unexported, `MEMORY-` refused on load).
- **Injection:**
  - All SQL is parameterized (`insertNew`, `MemoryAdd` and `MemoryShow` included).
  - git runs as `ls-files -z -- <relRoot>` and `status --porcelain` through the hardened `trail.Command`. `relRoot` is the fixed default or the operator's own argument, so pathspec magic and glob characters there are operator input. Adding `--literal-pathspecs` would be harmless hardening.
  - `canonicalProject` runs `git rev-parse` in a directory named by a `feedback.jsonl` line or a proposal's frontmatter, which is text other sessions wrote. That uses the hardened `Command`, and `rev-parse` runs no hooks or filters (fsmonitor is off). It costs one git spawn per line, a time cost only.
- **Path trust:**
  - `docsRootOf` refuses a symlinked docs root and anything lexically outside the checkout.
  - `readDocs` lists from git and `Lstat`s each path, so a tracked symlink is refused. A work-tree directory swapped for a link shows as dirty, which the real run refuses. The dry run skips the dirty check, but it prints only IDs and paths.
  - `guardExportPath(top)` runs before the write.
  - Gaps: findings 2 and 7.
- **Scope:**
  - A project import writes only to `trail.Roots(dir)`'s scope.
  - Memory goes to the scope that owns the store, except through the symlinked `.agents` (finding 2).
  - Global records get `scope=""`, and project records are never written there.
  - A hostile clone's committed `.agents/memory` becomes memory in *that clone's* scope only. That is no escalation over its own `AGENTS.md`.
- **Brick check:** I compared each field `insertNew` writes against `parseLine`.
  - Kind, ID, status, date shape, the title (`normalize`: controls to `?`, `oneLine`, 512-byte trim) and the body (controls to `?`, at most 1 MiB) all agree.
  - `Updated` does not (finding 1).
  - Duplicate IDs are refused before the write.
- **Terminal:** every `printImport` line, every refusal (`recordErr`) and every `memoryCmd` error goes through `search.Printable`, and `MemoryShow` lines do too. The one gap is shell syntax rather than terminal control (finding 4).
- **Authz/authn:** n/a beyond scope resolution, which is `trail.Roots` and unchanged in this range.
- **Crypto:** none touched.
- **Dependencies:**
  - `go.mod` and `go.sum` are unchanged in the range.
  - `govulncheck` (`~/.go/bin/govulncheck ./...`) ran on **go1.24.4**, because `/usr/bin/go` is first and `~/.local/bin/go` dangles, the same box state as the previous review. It reports the same 15 reachable stdlib advisories (GO-2025-3849, GO-2026-4970, …), all fixed by a newer Go, plus 5 package and 42 module advisories not reached. The range adds none.
  - No npm surface.
- **GCP surface:** none in this range.

## Not examined

- `internal/brain/import_test.go`, `cmd/fyr/record_test.go` and `cmd/fyr/memory_test.go`: I listed the test names but did not read the bodies.
- `internal/harness/catalog_test.go` and `internal/search/search_test.go` diffs: not read. The `reconcile.go` change, which excludes `.agents/trail.jsonl` from the file index, was read and holds.
- The memory/time bound (finding 6) was not measured.
- The TOCTOU windows (finding 7) are argued from the code; I did not race them.
- Finding 8's three triggers are argued from the code, not run.
- `fyr dream`'s continued reads of the old `.agents/memory` files after migration, if any, are out of this range.
- govulncheck on a Go ≥ 1.25.12: none is installed on this box.

Scratch used: `.agents/scratch/sec-import/`. It holds a build of ddaa522, the probe repos `p1`, `p2`, `p4` and `p5` (each init verified with `rev-parse --absolute-git-dir`, with `GIT_*` unset), `victim/`, and the scratch homes `home1` to `home5`. No source, test or config file was changed.

## Outcome

All eight fixed; each has a test that failed with its fix mutated out (2026-10-25, mutation runs in the main thread).

| # | Finding | Outcome | Test (mutation that failed it) |
|---|---|---|---|
| 1 | impossible date exported, bricking other homes | FIXED: `docDate` and the ADR heading take a date only when it is a real day (`realDay`), else the stem's, else the import's day; every record goes through `checkRecord` — the load's own `parseLine` for exported kinds — before anything is written, in a dry run too, and again in `insertNew` | `TestImportBadDateLoads` (realDay accepting any shape) |
| 2 | global import follows a symlinked `.agents` | FIXED: `readMemoryStore` calls `guardExportPath(root)` first; the project import passes the scope root through the same function | `TestImportGlobalRefusesASymlinkedAgents` (guard removed) |
| 3 | files left as files listed for `git rm` | FIXED: `Remove` holds only trail files; a file outside the trail's directories is a problem and stays; the spec says so in one place | `TestImportLeavesUnknownFilesOffTheRemoveList` (red before the fix) |
| 4 | remove lists printed unquoted | FIXED: `brain.ShellQuote` prints each path as one shell word | `TestShellQuote` (quoting off) |
| 5 | `memory add` skips the title rules | FIXED: the description goes through `cleanTitle` (a refusal, exit 2 as `fyr memory` always had); `memory show` cuts each line at 640 bytes | `TestMemoryAddTitleRules`, `TestMemoryShowBoundsEachLine` (each check off) |
| 6 | no total read cap | FIXED: one `budget` per import, 256 MiB and 50,000 files over the docs tree, the referencing files (now `git ls-files`, tracked only) and the memory store | `TestImportCapsTotalRead` (budget check off) |
| 7 | Lstat-then-ReadFile | FIXED: `readRegular` (`openNonBlocking`: `O_NOFOLLOW|O_NONBLOCK`, fstat regular, size cap, `LimitReader`) for every read, the logs included | `TestReadRegular` (regular-file check off; symlink and FIFO cases) |
| 8 | one bad global entry aborts the import | FIXED: logs read line by line with an over-long line reported by number; each record checked and reported, not fatal; slugs cut at 120 to leave room for `-N`; a source with a problem is not listed for removal | `TestImportGlobalBadEntriesAreProblems` (long-line check off; failed-source filter off) |
