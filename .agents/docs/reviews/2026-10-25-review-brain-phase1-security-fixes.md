---
owner: reviewer
date: 2026-10-25
---
# Review — brain-phase1-security-fixes   (2026-10-25)

Range reviewed: ca246a5..8ab2bf9 -- internal cmd (HEAD = 8ab2bf9; untracked internal/brain/import.go, import_test.go, memory.go are outside the range and were read only where they touch the same checks)

## Status: COMPLETE

## Verdict: APPROVE WITH NITS

The streamed reader is correct on every edge I could construct, and no valid file the export itself writes is refused. The worst problem is a test that cannot fail: `TestLoadRefusesAnOverlongLine` passes with the line cap deleted, so the "refused before it is decoded" claim is unpinned. Two more guards are unpinned or not discriminating (growth during read; the finding 7 test), plus one stale comment and one function with no caller.

Verification: `go test ./internal/brain` and `go test ./cmd/fyr -run 'TestRecord|TestHelpListsRecord'` pass on a `git archive HEAD` copy under `.agents/scratch/rv`. Edge cases were run there with a scratch test (deleted afterwards), through plain, one-byte, half-read and data-with-EOF readers. Mutations were applied to the copy, restored from a saved original, and each compiled.

## Findings
- **[medium / confidence: high]** `internal/brain/security_test.go:41` — `TestLoadRefusesAnOverlongLine` cannot fail on the fix it names. Mutation M1 removed both `maxLine` refusals in `readRecords`; it compiled, and the whole `./internal/brain` suite stayed green. Without the cap the 6 MiB line is decoded and refused by `parseLine` as "body is over 1 MiB", and that message also carries "line 2", which is all the test asserts. The test only proves the gather path (a line over 64 KiB) still parses. Fix: assert the cap's own text (`"over %d bytes"` with `maxLine`) and use a line whose decoded body is under 1 MiB, or one that is not JSON at all, so only the cap can refuse it. Also add a happy-path line larger than the 64 KiB buffer, up to the longest valid line. I checked 100 KB and 1 MiB-of-`<` lines by hand and both load, but no committed test does.
- **[low / confidence: high]** `internal/brain/security_test.go:53` — nothing pins `capped`'s growth guard, the case "a file that grew after its size was checked". Mutation M2 (a `capped.Read` that never fails) stays green, because `TestLoadRefusesAnOversizeFile` is already refused by the `fi.Size() > maxTrail` check in `openTrail`. Mutation M4 (the Stat check disabled) also stays green, because `capped` catches the same file. Each guard hides the other, and the stated purpose of `capped` has no test. A direct unit test of `capped` on a reader longer than `left` would pin it. By hand, 99 and 100 bytes pass and 101 is refused, so there is no off-by-one.
- **[low / confidence: high]** `internal/brain/security_test.go:140` — `TestEveryUnexportedKindStaysOut` (finding 7) passes on the pre-fix code, because the old `unexportedSQL` literal equals the map today. It is a drift pin, not a regression test of the fix, and nothing says so. Say so in its comment, or make it add a kind to the map and check that the SQL follows.
- **[low / confidence: high]** `internal/brain/export.go:163` — `parseTrail` has no caller at 8ab2bf9. `git grep` finds only its definition. It is now a `bytes.NewReader` wrapper that only the untracked `import_test.go:139` uses. Delete it, or land that caller with it.
- **[low / confidence: high]** `internal/brain/export.go:370` — the comment names `readTrail`, which this change deleted; the guard now runs in `openTrail`/`hashTrail`. Describing-text rule.
- **[low / confidence: medium]** `internal/brain/export.go:80` — every `openNonBlocking` error other than not-exist (EACCES, EMFILE, EIO) is now returned as a refusal ("the trail must be the project's own regular file"), which gives exit 1 and a misleading reason. Only ELOOP and ENXIO mean that. Refuse on those and return the rest as is.
- **[low / confidence: medium]** `internal/brain/export.go:23` — "memory follows the records" is bounded only by the 256 MiB file cap. About 1.8 million minimal valid records (roughly 150 bytes each, unique IDs) cost an estimated 600 MB or more in `out`, `seen` and the later insert, so the planted-file memory bound is a few times the cap, not one line. Not an exploit worth blocking; the comment and spec overstate it. A lower cap, or loading inside the transaction row by row, would close it.
- **[low / confidence: low]** `internal/brain/security_test.go:99` — `TestOpenNonBlockingRefusesASymlink` has no `//go:build unix` tag, but `open_other.go` follows symlinks, so the test would fail on a non-unix build. The release targets are unix (`open_other.go:7`); noted only.

## What I checked and found sound
- `readRecords`: a last line with no newline is parsed; a trailing newline gives one extra empty slice that is skipped. CRLF is handled, and `\r\r\n` is also accepted (the old code trimmed one `\r`). A line of exactly `maxLine` bytes including its newline is accepted and `maxLine+1` is refused, with or without a trailing newline. Line numbers stay correct across lines over 64 KiB and across blank lines (a bad line at 4 after a 100 KB line at 2 reports line 4). A line of exactly 64 KiB, +1 and +2 (the `ErrBufferFull` boundary) parses under every reader flavour. The reused `long` buffer is safe, because the JSON strings are copied before the next read.
- `maxLine = 6*maxBody + 64<<10` parses as intended (shift binds tighter than `+`). The export escapes at most 2x (U+2028) with HTML escaping off, so no line the export writes comes near it. A 1 MiB body of `<` and a 1 MiB body of U+2028 both round-trip.
- `loadLocked` hashes the bytes it parses through a `TeeReader`, so the stored SHA matches the loaded content even if the file changed between the two passes. `exportLocked`'s `current != sum(data)` is equal to the old `!bytes.Equal`. The non-UTF-8 and NUL checks moved into `bodyText`/`titleText` with no gap: NUL, `\r` and `\n` in a title are still refused (`\r\n` in `cleanTitle`, the rest in `titleText`).
- Nothing the export writes is now refused: titles reach the store only through `cleanTitle` or the import's `normalize`; bodies only through `readBody` or `normalize`; `Update` uses the same two. A title with a tab, or a body with a lone `\r` or ESC, was storable before this range and is refused now. `fyr record` is on main only and in no tag, so I found no deployed data to strand. A developer's own `brain.db` could hold such a row, and its next export would then be a `trail.jsonl` the same checkout refuses to load. That cannot happen to a row written through a checked path from here on.
- `recordErr` collapsing the error to `errors.New` loses the type and the wrap chain only for errors that were not usage or refusal and reached `main` as a plain error anyway.
- `label`: `scope == ""` is the global scope, so the first case is unchanged. The full path is printed through `FormatHit`'s `search.Printable`.
- Mutation results: removing the streaming (`io.ReadAll` plus `bytes.Split`, M3) fails `TestLoadBoundedMemory` (959 MiB allocated against a 16 MiB bar). `recordErr` returning `err` unfiltered fails `TestRecordErrFiltersOtherErrors`. `TestTitleLimit`, `TestControlCharactersRefused`, `TestGlobalLabelsTellScopesApart` and `TestRecordGetIsPrintable` assert behaviour the prior code lacked; I read them and did not mutate them one by one.

## Criteria
1. Dead code — fail (low): `parseTrail` has no caller in the range.
2. Flow tracing — pass with notes: sync, load and export are consistent, errors propagate, and handles close on every path. One new test cannot fail (the overlong line) and one guard (growth during read) is unpinned.
3. Standards conformance — pass with one nit: the stale `readTrail` comment at `export.go:370`. The spec `record.md` lines 25, 76 and 85 match the code (512-byte title, 6 MiB + 64 KiB line, 256 MiB, full-path label).
4. Directory layout — pass: no new files except `security_test.go`, beside the code it covers.
5. Simplicity (DIY-except-security) — pass: stdlib only, no new dependency, and nothing hand-rolled beyond a `crypto/sha256` checksum.
6. Decoupling — pass: `openTrail`, `hashTrail` and `readRecords` have narrow, separate jobs, and `parseLine` is separable from the reader.

## Not examined
- The untracked `import.go`, `import_test.go` and `memory.go`, and the modified `internal/search` files in the working tree (outside the range); `import.go` was read only around `insertNew`, `normalize` and `oneLine`.
- Mutations of each remaining test one by one (`TestTitleLimit`, `TestControlCharactersRefused`, `TestGlobalLabelsTellScopesApart`) and `TestRecordGetIsPrintable`'s two assertions on their own.
- The full `go test -race ./cmd/fyr` (about 13 minutes on this box); only the record tests ran, without `-race`.
- `govulncheck`, and non-unix builds.

## Outcome

| # | Finding | Outcome |
|---|---|---|
| 1 | `TestLoadRefusesAnOverlongLine` cannot fail on its fix | FIXED: it asserts `line 2: over`, the cap's own text; new `TestLoadTakesALongValidLine` loads a 300 KiB valid line (CRLF, no final newline) and checks line numbering after it. Mutation: both `maxLine` refusals off, the test fails. |
| 2 | the growth guard and the size check hide each other | FIXED: the size check's test asserts the refusal names the file's path (the growth guard's does not), and `TestCappedReaderRefusesGrowth` tests `capped` alone at and one byte past the cap. The redundant pre-read check in `capped.Read` is gone (it read at most one byte past the cap, so the post-read check covers a repeat call). Mutations: size check off, `TestLoadRefusesAnOversizeFile` fails; growth check off, `TestCappedReaderRefusesGrowth` fails. |
| 3 | `TestEveryUnexportedKindStaysOut` is a drift pin only | ACCEPTED as such: that is its job (security finding 7); its mutation (SQL cut to one kind) failed it, recorded in the security report's Outcome. |
| nit | `parseTrail` has no caller | KEPT: Phase 2's import test parses the export back through it. |
| nit | comment names the deleted `readTrail` | FIXED (`hashTrail`). |
| nit | every open error becomes a misleading refusal | FIXED: the refusal is `<path>: <error>`, without the claim about the file's kind. |
