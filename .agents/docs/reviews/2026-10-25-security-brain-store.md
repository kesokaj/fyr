---
owner: security
date: 2026-10-25
---
# Security review — brain-store   (2026-10-25)

Range reviewed: 9e302f1..8bd25ed -- internal/brain cmd/fyr internal/trail internal/search (plus the helpers called: internal/harness WriteAtomic, internal/search printable/cleanSnippet/ftsQuery/dsn/prepareFiles, internal/trail Command/Roots)

## Status: COMPLETE

## Verdict: FINDINGS

The worst problem: a cloned repository's committed `.agents/trail.jsonl` can exhaust memory in any `fyr record` verb, and in `fyr search <words>`, run inside the clone. `readTrail` reads up to 1 GiB into memory. Then `parseTrail` runs `bytes.Split`, which allocates a 24-byte slice header for every line before it skips blank ones. Measured on this box: a file of 100 MiB of newlines peaked at 2.5 GiB RSS, and that file packs to 101 KiB in git. At the 1 GiB cap the peak is about 25 GiB, which would trigger the OOM killer on most machines. Titles have no length limit, so a single valid record can also carry a title of hundreds of MiB. Our test with a 200 MiB title peaked at 2.5 GiB and left a 200 MiB `brain.db`. That row stays in the store of record and is printed by every `list` and `search`. The export path, the memory/feedback/proposal boundary (R8, D14) and the terminal filter held up under every trigger I tried. What remains is low severity: a check-then-use window on the export and load path (needs a concurrent local writer), match marks that stored text can spoof, and labels that blur where a record came from.

## Findings

- **[medium / confidence: high]** `internal/brain/export.go:24` (`maxTrail = 1 << 30`), `internal/brain/export.go:127` (`bytes.Split(data, []byte("\n"))`), `internal/brain/export.go:79` (whole-file `io.ReadAll`): a planted trail.jsonl can exhaust memory.
  Trigger: commit `.agents/trail.jsonl` containing N MiB of `\n` (or a single line of N MiB). Any `fyr record list|get|add|edit` or `fyr search <words>` in that checkout (a read path, `sync`, `brain.go:132`; `Search`, `search.go:62`) then reads the whole file and splits it.
  Measured with a build of 8bd25ed in `.agents/scratch/sec-brain/`: 100 MiB of newlines gave peak RSS 2,533 MiB in 2.0 s. The git pack of that commit is 100.92 KiB. The cap allows ten times that size. A file of blank lines is also a *valid* empty trail, so the rows are deleted and nothing is refused.
  Why it is reachable: the threat model (a cloned repository is untrusted) and the context, which runs `search` at every session start in any project. The `maxTrail` comment's premise ("low enough that a planted file cannot exhaust memory") does not hold, because the parse amplifies the input about 12–25×.
  Remediation:
  - Stream the parse: `bufio.Reader` line by line, or `bufio.Scanner` with `Buffer(…, maxLine)`, where `maxLine` is about `6*maxBody + 4 KiB` (the JSON `\u` escape worst case). Hash through `io.TeeReader` into `sha256.New()`, so no whole-file copy and no slice-of-lines is ever built.
  - Reject an over-long line before `json.Decode`.
  - Lower `maxTrail` to what a real trail can reach (tens of MiB), or count records (`maxRecords`).
  - stdlib only, no dependency.
  Test that fails without the fix: `TestLoadBoundedMemory` writes 64 MiB of `\n` (and, separately, one 64 MiB line) into a fixture trail. It runs `List` and asserts that `runtime.MemStats.TotalAlloc` grew by less than, say, 4× the file size, or that a line over `maxLine` is refused with its line number before decode.

- **[medium / confidence: high]** `internal/brain/export.go:166` (parseTrail title check) and `internal/brain/record.go:193` (`cleanTitle`): a title has no length limit, so it slips past the 1 MiB rule the body follows.
  Trigger: a trail line with a valid bug record and a 200 MiB `"title"`. Measured: `fyr record list` exits 0, peak RSS 2,560 MiB, and `brain.db` grows to 200 MiB. The title is FTS-indexed and printed in full by every `record list`, `list --all` and `search` hit.
  The store is a store of record and is never pruned (store.go:6), so the row stays after the clone is deleted. It also keeps appearing in `--global` searches and `list --all` from every other project.
  Remediation: cap the title in `cleanTitle` and in `parseTrail` (for example 512 bytes or 200 runes) and refuse longer ones with the line number. Optionally truncate it in `FormatLine` as `CleanSnippet` does.
  Test that fails without the fix: `TestLoadRefusals` gets a case `"long title"` with `strings.Repeat("a", 1<<20)` as title, expecting a refusal naming `line 1`. `TestBodyLimits` (or a new `TestTitleLimit`) gets `Add` with a title over the cap, expecting a usage error.

- **[low / confidence: high]** `internal/brain/export.go:287-291` (guard, then `harness.WriteAtomic`), `internal/harness/apply.go:422-451`: there is a window between the check and the use on the export write.
  `guardExportPath` checks `.agents` and `trail.jsonl` with `Lstat`. `WriteAtomic` then runs `MkdirAll(.agents)`, `CreateTemp(.agents)` and `Rename` by path. If `.agents` is swapped for a symlink in between, the temp file and the rename land in the link's target, replacing a `trail.jsonl` anywhere the user can write. When `.agents` is absent at guard time, `MkdirAll` happily follows a symlink created afterwards.
  The content is this scope's exported rows only (memory is still excluded), and the attacker needs to write to the checkout concurrently. The code comment accepts this risk, and the search index has the same pattern.
  Remediation: open the checkout with `os.OpenRoot(top)` and do the mkdir, temp create and rename through the `os.Root` methods, which refuse a link that escapes the root. Go 1.24 has `OpenRoot`/`Root.OpenFile`/`Root.Mkdir`; `Root.Rename` needs Go 1.25+, so check that against the module's `go 1.24.4` line. Or open `.agents` with `O_DIRECTORY|O_NOFOLLOW` and keep the fd for `CreateTemp`/rename (needs `unix.Renameat`, which is x/sys; the stdlib route is `os.Root`). Note GO-2026-4970 (os.Root.Open) on a Go older than 1.25.12.
  Test that fails without the fix: a hook variable in `exportLocked`, run between guard and write, that replaces `.agents` with a symlink to a second temp dir. Assert that `Add` refuses and that nothing appears in the second dir.

- **[low / confidence: high]** `internal/brain/open_unix.go:15`: the load opens trail.jsonl without `O_NOFOLLOW`.
  The comment on `guardExportPath` (export.go:30-31) says the load must never read through a link, but the open only checks `Stat().Mode().IsRegular()`, which follows the link. If `trail.jsonl` is swapped for a symlink to any regular file the user can read, between the `Lstat` and the open, that file is read and parsed. Parse errors quote only a line number and one character, and a successful parse needs a valid trail, so I see no content leak. The claim in the comment is stronger than the code.
  Remediation: `os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW` (stdlib `syscall`, unix only).
  Test that fails without the fix: a hook between `guardExportPath` and `openNonBlocking` that swaps in a symlink to a valid trail elsewhere. Expect a refusal (ELOOP), not a load.

- **[low / confidence: high]** `internal/search/search.go:658-660` (`cleanSnippet`, called from `internal/brain/search.go:110`): stored text can spoof the match marks.
  FTS5 marks matches with `char(1)`/`char(2)`. `cleanSnippet` turns *every* \x01/\x02 into `»`/`«` before `printable`, and neither `parseTrail` nor `readBody` refuses those bytes.
  Confirmed: a loaded title and body holding `\x01mark\x02` printed `»mark«` in `fyr search zebra`, a search that did not match "mark". It is cosmetic, but it lets a crafted record fake what "matched" in output an agent reads.
  Remediation: refuse C0 controls other than `\t` and `\n` in titles and bodies, in `readBody`, `cleanTitle` and `parseTrail`. Or have `cleanSnippet` map stored \x01/\x02 to `?` first and then use marks that cannot occur in stored text.
  Test that fails without the fix: a `TestLoadRefusals` case with `\u0001` in the body, expecting `line 1`. Or a `TestSearchRecords` case asserting that a hit's snippet has no `»` around an unmatched word.

- **[low / confidence: medium]** `internal/brain/search.go:126-134` (`label`), `internal/brain/brain.go:132-146` (any read loads): search output does not say clearly where a record came from.
  Running `fyr search <words>` (or any `record` read) in an untrusted clone loads its trail into the machine-wide store permanently. With `--global` from any other project, those records print under `filepath.Base(scope)+":"`, so a clone at `~/x/styrman-harness` prints exactly like the real project.
  In a linked worktree of a trusted repository that has an untrusted branch checked out, the load replaces the *trusted scope's* exported rows with the branch's file. Until the main worktree next runs a verb, cross-project views attribute the branch's records to the trusted repository. This follows D21 and the shared scope. The security cost is misattributed text in output an agent reads.
  Remediation: print the full scope path in `search --global` labels, or disambiguate basenames that collide. Consider a `record forget --scope` (Phase 2) so a deleted clone's rows can be removed.
  Test that fails without the fix: two scopes with the same basename, one record each. `Search(Global)` labels must differ.

- **[low / confidence: medium]** `internal/brain/record.go:39-42`: the export filter `unexportedSQL` and the Go map `unexported` are two hand-kept copies of the R8/D14 boundary.
  Only `memory` is pinned by a test (brain_test.go:481-493). A future kind added to the map but not to the SQL string would be exported to a project's committed file.
  Remediation: build the SQL `IN` list from the map at init, or add a test that, for every key of `unexported`, inserts a row of that kind directly into a project scope and asserts that `exportLocked` leaves it out.
  Test: as described in the remediation; it fails today only if the copies drift, which is the point.

- **[low / confidence: low]** `cmd/fyr/main.go:100` and `internal/brain/export.go:64-80`: errors that are not refusals print without the filter.
  A non-refusal error from the brain package (an `os` error from `Lstat`/`open`/`WriteAtomic`, which carries the checkout path) prints raw as `fyr: <err>` on stderr. Refusals and `recordHits` go through `Printable`. A checkout path holding escape bytes (possible from a hand-extracted archive, not from `git clone`) would reach the terminal.
  Remediation: in `runRecord`'s `recordErr`, pass the default branch through `Printable` as well.
  Test: a `record list` run in a checkout whose directory name contains `\x1b`, with `.agents` unreadable (0000), asserting no ESC byte in the output.

## Checked

- **Secrets:** none in the diff. No secret is logged. The store directory is 0700 and the files 0600 (`PrepareDBFiles`), with symlinked or non-regular companions refused.
- **Injection:**
  - All SQL is parameterized. The only concatenations are the constants `unexportedSQL`, `openSQL` and `recordCols`.
  - FTS input is quoted by `FTSQuery`.
  - No record ID or title reaches a path or a shell.
  - git runs through `trail.Command`, with fsmonitor disabled and `GIT_NO_LAZY_FETCH` set.
- **Parsing (`parseTrail`):**
  - Each line is decoded as one object with `DisallowUnknownFields`, all seven keys required and trailing values refused.
  - Kind, ID, status and date are checked against bounded regexps, the body is capped at 1 MiB and must hold no NUL, and duplicates are refused.
  - Exported kinds cannot carry a `MEMORY-` ID, and memory, feedback and proposal kinds are refused, so a file cannot insert, overwrite or delete memory rows. The DELETE excludes the unexported kinds, and the `isConstraint` branch is unreachable.
  - The global scope `''` cannot be targeted from a file, because the scope comes from `trail.Roots`, not from the file.
  - `export_sha` is the SHA-256 of the exact bytes parsed, under BEGIN IMMEDIATE, so I found no desync that loses a write. A commit failure after the rename is healed by the next load (file wins).
  - Gaps: memory amplification and the title length (findings 1–2).
- **Terminal:** confirmed empirically. A loaded record with ESC/CSI/OSC-8, BEL, U+202E, U+2028, C1 U+009B, U+200B and a tag character printed every one of them as `?` in `record list`, `record get`, `list --all` and `search` hits and snippets. `get` keeps tabs, and CR prints as `?`. The only leak is the \x01/\x02 mark spoof (finding 5).
- **Authz/authn:**
  - The scope is resolved by `trail.Roots`: a linked worktree must be registered by the common dir, and the toplevel must be the working directory or a parent of it.
  - `feedback`/`proposal` are refused in a project and `memory` in the global scope.
  - `get`/`edit` are confined to the session's scope.
- **Crypto:** `crypto/sha256` is used as a change-detection checksum, which is stdlib used as designed. No hand-rolled primitive.
- **Dependencies:**
  - No new module; `modernc.org/sqlite` was already allowed for `internal/brain` by AGENTS.md (ADR-063).
  - `govulncheck` (`~/.go/bin/govulncheck ./...`) ran on **go1.24.4**, not the go1.27.2 that AGENTS.md documents. `~/.local/bin/go` is a dangling symlink to `~/sdk/go1.27.1`, which does not exist, so the shell falls back to `/usr/bin/go`.
  - Findings on go1.24.4: 15 standard-library advisories that the code reaches. One is GO-2025-3849 (database/sql `Rows.Scan`, a path the brain store uses) and one is GO-2026-4970 (os.Root.Open). All are fixed by upgrading the box's Go, not by code.
  - Not reached: 5 package and 42 module advisories.
  - There is no web/npm surface in this range.
- **GCP surface:** none in this range.

## Not examined

- The `internal/search` file-search path beyond the helpers named, and `search.Validate`.
- `internal/brain/brain_test.go` and `cmd/fyr/record_test.go`: grepped, not read.
- Phase 2 verbs (`record import`, the memory alias), which do not exist yet.
- The non-unix `openNonBlocking`: read, not run.
- The race findings (3, 4) are argued from the code; I did not reproduce them with a racing writer.
- The ~25 GiB figure at the 1 GiB cap is extrapolated from the 100 MiB measurement, not run.
- govulncheck was not run on a Go ≥ 1.25.12, because none is installed on this box.
- The `trail.Roots` core.worktree-to-a-parent case for a checkout delivered as an archive with its own `.git`: earlier audits covered it, and I did not re-review it.

Scratch used: `.agents/scratch/sec-brain/` (a probe repo verified with `rev-parse --absolute-git-dir`, a fyr build, an RSS script, a scratch `--home`); no source, test or config was changed.

## Outcome

Every finding fixed but 3, which is rebutted; each fix has a test that fails without it (run red first, 2026-10-25).

| # | Finding | Outcome | Test |
|---|---|---|---|
| 1 | trail.jsonl read whole, `bytes.Split` amplifies | FIXED: the load streams (`readRecords` over a 64 KiB `bufio.Reader`, `ReadSlice`; blank lines cost nothing), the hash is streamed (`hashTrail`, `io.TeeReader` for the parse pass), a line over `maxLine` (6 MiB + 64 KiB) is refused before decode, the cap is 256 MiB and enforced while reading (`capped`), the export compares hashes instead of re-reading the file | `TestLoadBoundedMemory` (32 MiB of `\n`: 1,151 MiB allocated before, under 16 MiB after), `TestLoadRefusesAnOverlongLine`, `TestLoadRefusesAnOversizeFile` |
| 2 | no title cap | FIXED: 512 bytes in `cleanTitle` (usage error) and the load (refusal with its line); the import trims to it | `TestTitleLimit` |
| 3 | guard-then-`WriteAtomic` window on the export | REBUTTED: the race needs a process that can write the checkout concurrently, i.e. the operator's own user, who can already write any file the export could reach; what is written is this scope's own exported rows (memory, feedback and proposal never, finding 7). The search index carries the same accepted pattern. `os.Root.Rename` needs Go 1.25 and the module line is 1.24.4. Revisit when the module's `go` line reaches 1.25. | n/a |
| 4 | load opened without `O_NOFOLLOW` | FIXED: `O_RDONLY|O_NONBLOCK|O_NOFOLLOW` | `TestOpenNonBlockingRefusesASymlink` (fixture proven a symlink first) |
| 5 | `\x01`/`\x02` spoof match marks | FIXED: a C0 control other than tab and newline (and DEL) is refused in a body (`readBody`), a title (`cleanTitle`) and the load (`parseLine`); the import maps them to `?` | `TestControlCharactersRefused`; `TestRecordGetIsPrintable` now asserts the ESC refusal and keeps the output filter for C1, U+2028 and bidi |
| 6 | `--global` labels show the base name | FIXED: another project's hits are labelled with the full scope root. `record forget` not added: out of the plan's scope | `TestGlobalLabelsTellScopesApart` |
| 7 | `unexported` map and SQL kept by hand | FIXED: `unexportedSQL` is built from the map | `TestEveryUnexportedKindStaysOut` (mutation: SQL cut to one kind, the test failed on the leaked proposal row; restored, green) |
| 8 | non-refusal errors print unfiltered | FIXED: `recordErr` passes every other error through `Printable` | `TestRecordErrFiltersOtherErrors` |

Spec `specs/fyr/record.md` and `specs/fyr/search.md` updated in the same change. The govulncheck note (go1.24.4 because `~/.local/bin/go` dangles) is the operator's step already in the handoff's Next Steps.
