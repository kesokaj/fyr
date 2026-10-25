---
owner: security
date: 2026-10-25
---
# Security review — brain-import-round2   (2026-10-25)

Range reviewed: ddaa522..963467c -- internal cmd (round 2, over the fixes only), against the Outcome tables of `2026-10-25-security-brain-import.md` and `2026-10-25-review-brain-phase2-import.md`

## Status: COMPLETE

## Verdict: FINDINGS

The fixes close the round-1 triggers they name. Mutation runs on a scratch copy confirm that the date, symlinked-`.agents`, tracked-store and remove-list fixes are each pinned by a failing test. Five low findings remain, and none is high or medium. Worst: `ImportGlobal`'s `-N` collision suffix is never checked against IDs already used. A source whose record collides is skipped silently, reported as imported, and still listed for `rm`. That breaks the fix's own contract ("a source with a problem is not listed for removal"), and `~/.config/fyr` is not in git, so following the list loses the entry for good. Next: the finding-8 fix bounds `json.Indent`'s result only after computing it, which leaves an unbounded memory amplification. The finding-7 fix guards only the last path component. Three of the claimed caps and checks have no test that fails without them.

## Findings

- **[low / confidence: high]** `internal/brain/import.go:885-890` (`add`'s `used[r.ID]++` suffix), `:700-708` (`filterStored`), `:1017-1021` (`Sources`): a suffixed ID can equal a real one, and the loser is listed for removal.
  Reproduced on a scratch copy (`.agents/scratch/sec-r2/src`, probe test `zz_probe_test.go`). Proposals `FOO.md`, `foo-2.md` and `foo.md` become `PROPOSAL-foo`, `PROPOSAL-foo-2` and `PROPOSAL-foo-2` again, because the suffixed ID is never checked in `used`. `insertNew` stores the first `PROPOSAL-foo-2` and skips the second as "taken". `filterStored` matches by key, so it keeps **both** in `res.Records` (3 records, `Skipped=1`, no problem). `proposals/foo.md` is in `res.Sources`, under "remove by name once the records are checked". Its text ("lower / C") is in no record.
  Feedback lines can collide the same way: the same minute, titles "fix", "fix" and "fix 2".
  Related, and the same root cause: the project `Import` lists every trail file for `git rm` even when its record was skipped because the scope already held a different record under that ID. Git history keeps those files, so that case is integrity only (confidence: medium).
  Remediation: suffix with `for n := 2; used[id] > 0; n++` until the ID is free, and record the final ID in `used`. In `insertNew`, treat an in-batch duplicate key as an error to report. `filterStored` should follow the record that was actually stored (index, not key). A source whose record was skipped for a reason other than "already stored from this same source" gets `failed`.
  Test that fails without the fix: the probe's three-proposal fixture. Assert three distinct stored IDs, or `foo.md` reported as a problem and absent from `Sources`.

- **[low / confidence: high]** `internal/brain/import.go:938-942`: `json.Indent` runs unbounded, and the `pretty.Len() > maxBody` check comes after the work.
  Measured on the scratch copy: one 20,048-byte feedback line (`[` nested 9,999 deep, under encoding/json's 10,000 depth limit, so `Unmarshal` accepts it) indents to 200,000,057 bytes. That cost 1,337 MB of allocation and 0.44 s.
  `maxLine` (export.go:29, about 6 MiB) lets one line carry about 300 such arrays side by side. The Indent output then grows to roughly 60 GB in a single call, which means an OOM kill of `fyr record import --global`. This is computed, not run, so as not to OOM the box.
  The file is written by the operator's own sessions (`feedback-log`), so the trigger is a prompt-injected or buggy session. Hence low.
  The test's "nested" line (300,000 deep) is refused by `Unmarshal`'s depth limit and never reaches this branch. Removing `|| pretty.Len() > maxBody` passes every test (mutation m3).
  Remediation: indent only a line whose upper bound `len(l) * (1 + 2*maxDepth)` fits `maxBody`, with `maxDepth` found by one scan of the line; otherwise store the raw line. Or always store the raw line.
  Test that fails without the fix: a feedback line of depth 9,999. Assert it is stored with the raw line as its body, and under a small allocation bound (`testing.AllocsPerRun` or `MemStats` delta).

- **[low / confidence: medium]** `internal/brain/import.go:103-127` (`readRegular`), `:755-791` (`readMemoryStore`: `Lstat`, then `ReadDir(store)`, then `readRegular(store/name)`), `:220-235` (`readDocs`): finding 7's fix protects only the final path component.
  `O_NOFOLLOW` applies to the last component only. A directory (`.agents`, `.agents/memory`, `.agents/docs/plans`) swapped for a link after `guardExportPath` or `Lstat` has run is still followed. For the memory store, that imports another directory's memories into this scope and lists them for `rm`.
  The precondition is the one round 1 accepted for #7: a concurrent writer to the checkout. `TestReadRegular` pins only the final-component case.
  Remediation: `os.OpenRoot(top)` (Go 1.24, go.mod `go 1.24.4`), then read everything through the `*os.Root`, which refuses a link that escapes on any component. Note GO-2026-4970 (`os.Root.Open`, fixed in go1.25.12). The box's toolchain is go1.24.4 (see Checked), so upgrade it before relying on Root.
  Test that fails without the fix: swap `.agents/memory` for a link between `readMemoryStore`'s `Lstat` and its `ReadDir` (a test hook), and assert no record and a problem line.

- **[low / confidence: high]** `internal/brain/import.go:129-140` (`ShellQuote`), `cmd/fyr/record.go:155-158`: the quoting is for the shell, but the list is for `git rm`, where a quoted word is still a glob pathspec and still an option.
  `'-rf'`, which `TestShellQuote` asserts and the doc comment claims as protection, still reaches git as an option. `*`, `?` and `[` inside quotes are git pathspec wildcards.
  `search.Printable` runs **after** quoting and turns a control character into `?`. A quoted `a\nb.md` therefore prints as `'a?b.md'`, a git wildcard that also matches `aXb.md`.
  `shellPlain` also admits a leading `=`, which zsh's EQUALS option expands.
  None of this is reachable today: every printed path starts with the docs root and a trail directory, every file under a trail directory is already on the list, and the docs root is the operator's own argument.
  Remediation: print `git rm --` with `GIT_LITERAL_PATHSPECS=1` (or `:(literal)` per path). Better, refuse at import a tracked trail path outside `[A-Za-z0-9._/-]`, which also removes the post-quote `?` rewrite. Drop `=` from the first-character class.
  Test that fails without the fix: `printImport` output for a tracked `plans/a?b.md` beside `plans/aXb.md` names only the first, literally.

- **[low / confidence: high]** Test gaps in the Outcome's claims. Each of these mutations passed every test on the scratch copy:
  - m3: the Indent cap removed (above).
  - m7: `checkRecord` removed from `readMemoryStore`, import.go:825. That check is load-bearing for a memory file near 1 MiB: the `"type: …\n\n"` prefix pushes the body over `maxBody`, and without the check `insertNew` fails the whole global transaction, which is round-1 #8's class.
  - m8: the file-count half of `budget.take` (`b.files < 0`) removed. `TestImportCapsTotalRead` lowers bytes only, although Outcome #6 names both caps.
  - Reading only: `TestImportGlobalBadEntriesAreProblems`' two 140-character proposals import as `PROPOSAL-ppp…` and `PROPOSAL-dream-ppp…`, which never collide, so the "room for `-N`" change (slug 120) is untested.
  Remediation: one test per gap, each validated by its mutation.

## Round-1 rows checked

Security report:
- #1 FIXED, verified. `realDay` combines `time.Parse` with `dateRe`. `checkRecord` runs the exported kinds through `parseLine` itself, in the dry run and again in `insertNew`. Removing `realDay` from `splitADRs` alone fails `TestImportBadDateLoads` through the `checkRecord` refusal (m6).
- #2 FIXED, verified. Removing the guard fails `TestImportGlobalRefusesASymlinkedAgents` (m1; `git ls-files` does not stop a symlinked path by accident, m1b). The linked-worktree trigger is closed because `readMemoryStore(scope)` guards the scope.
- #3 FIXED (`leftAlone` filter); residual in finding 1.
- #4 FIXED for the shell; residual in finding 4.
- #5 FIXED. Each case of `TestMemoryAddTitleRules` is independent, because a taken name is an UPDATE, not a refusal. The 640-byte line bound holds; the 25 KB total cap still fits about 40 maximum-length lines, which is by design.
- #6 FIXED for bytes; files untested (finding 5).
- #7 FIXED for the final component; residual in finding 3.
- #8 FIXED for long lines, failed sources and slug room; residual in findings 1, 2 and 5.

Reviewer report:
- `keep` keyed on `rel` (`TestImportCustomDocsRoot`), stored keys by scope (`TestImportStoresSameIDInTwoScopes`), and the dry run's `checkRecord` (runs before `return res`): confirmed by reading.
- The tracked-store refusal is confirmed by reading and `TestImportRefusesCommittedMemory`.
- The removed `loadLocked` constraint branch is safe. The load refuses a duplicate ID first (export.go:202), and `validID` binds each exported kind to its own prefix, never MEMORY-, FEEDBACK- or PROPOSAL-.

## Checked

- Secrets: none added in the range (grep of added lines); messages carry paths only.
- Injection: `ShellQuote` and `Printable` order (finding 4). `git` is called with fixed argv and `--` (`ls-files`); no shell is involved.
- Authz/authn: not applicable. The trust boundary is another project's checkout, covered by `guardExportPath` and the tracked-store refusal.
- Crypto: none touched.
- Dependencies: go.mod and go.sum unchanged in the range. `govulncheck ./...` reports 15 standard-library advisories, all of go1.24.4: both `/usr/bin/go` **and** `~/.local/bin/go` report go1.24.4 on this box, contrary to AGENTS.md's go1.27.2 note. They include GO-2026-6604 (`os`, fixed 1.26.9), newer than AGENTS.md's known list. These are toolchain issues, not this diff; upgrade the box's Go.
- GCP surface: none touched.

## Not examined

- `internal/memory/memory.go` and `memory_test.go` deletions were not read line by line, beyond the claim that `Root`/`Store` remain.
- `cmd/fyr/record.go` outside `printImport`.
- Callers outside the diff, per the dispatch.
- The 60 GB Indent figure is computed, not run.
- govulncheck under go1.27.2: no such toolchain was found on PATH.

## Outcome

Round 2 is the last round (context § Definition of Done step 2); the fixes below were read by the main agent, each with a test that failed with its fix mutated out.

| # | Finding | Outcome |
|---|---|---|
| 1 | `-N` suffix lands on an existing ID | FIXED: the suffix is tried until it names no ID the import already gave; `TestImportGlobalSuffixNeverCollides` (FOO.md, foo-2.md, foo.md → three records; fails with a single-try suffix) |
| 2 | `json.Indent` before its cap | FIXED: the line is stored as written, never indented; `TestImportGlobalStoresTheLineAsWritten` (failed before: the body was the 261-byte indented form of a 72-byte line) |
| 3 | a directory swapped for a link between check and read | REBUTTED: it needs a process writing the checkout (or `~/.config/fyr`) concurrently as the operator's own user, who can already read and write every file the import could be steered to; the same precondition as the store review's finding 3, rebutted there. `os.Root.Rename`-free reading through `os.Root` is possible on Go 1.24 but carries GO-2026-4970 before Go 1.25.12; revisit with the module's go line |
| — | `ShellQuote` words read as options or globs by `git rm` | FIXED: the list's heading says `git rm --literal-pathspecs --`; `TestRecordImport` asserts it |
| — | test gaps | FIXED: `TestImportCapsFileCount` (fails with the file half removed); the 120-character room is pinned by `TestImportGlobalBadEntriesAreProblems`, whose two names now agree past the cut (fails with 128); the Indent cap is gone with finding 2; the `checkRecord` call in `readMemoryStore` was unreachable after `normalize` and is removed (`insertNew` still checks every record) |
| — | toolchain go1.24.4 | LEFT: the operator's step, in the handoff (`~/.local/bin/go` dangles) |
