---
owner: verifier
date: 2026-10-25
---
# Verification — fyr-keep-claude   (2026-10-25)

Range reviewed: f8cc874..working tree (5 modified files: remote.md, README.md, cmd/fyr/crossing_test.go, internal/harness/apply.go, internal/harness/harness_test.go)

## Status: PASS

Scope: complete for the owed items 1-3 (unit, scratch-home install, coverage). Guide: .agents/docs/testing/guide.md (§1, §3).

## Commands
- 1. `pwd` /home/kesokaj/workspace/styrman-harness; git status as in range line (+2 untracked reviews under .agents/docs/reviews/)
- 4. `gofmt -l cmd internal` → exit 0, printed nothing
- 4. `go vet ./...` → exit 0
- 4. `go test -race -v -count=1 -timeout 30m ./...` → exit 0; 9 of 9 packages ok (cmd/fyr 64.3 s, internal/harness 63.9 s); counts: pass 665, fail 0, skip 5 at f8cc874+dirty for go test -race -v -count=1 -timeout 30m ./...; vs previous: none with -v counts (2026-10-25-verify-fyr-crossing-local.md recorded packages only). Skips (all pre-existing environment skips): TestHelperMain, TestHelperCall, TestAgyCorpusLeaves, TestRecall, TestHelperIndex. Output: .agents/scratch/verify-keep/go-test-1.txt
- 7. Covering tests, all `--- PASS` (not SKIP) in -v output: TestCrossingFromV270 (1.15s), TestResyncKeepsOldClaudeFiles (0.00s), TestInstallFailureKeepsClaudeFilesOutOfTheManifest (0.00s)
- 4. `/home/kesokaj/.go/bin/govulncheck ./...` → exit 3; "Your code is affected by 15 vulnerabilities from the Go standard library" (go1.24.4: GO-2026-6604, 6600, 6599, 6091, 4982, 4980, 4970, 4865, 4603, 4602, 4601, 4341, GO-2025-4010, 3956, 3849; os, os/exec, net/url, html/template, database/sql) = the known set; no module finding reached by code (5 in imported packages and 42 in required modules, not called). Output: .agents/scratch/verify-keep/vuln.txt
- 3. `go run ./cmd/fyr install --home .agents/scratch/verify-keep/home` → exit 0; "896 written, 0 unchanged, 0 removed, 0 kept"
- 3. `go run ./cmd/fyr status --home .agents/scratch/verify-keep/home` → exit 0; `binary f8cc874deac1+dirty` (no warning); `drift none — 895 files match the repo`
- 3. `ls <home>/.claude` → does not exist; home holds only .config .gemini .local
- 7. Suppressions added: internal/harness/harness_test.go (TestInstallFailure...) `t.Skip("root ignores directory modes")` has a reason; not hit (test passed, non-root). No nolint/ts-ignore/.only.

## Coverage of changed code
- internal/harness/apply.go (claudeFile, keepClaudeLine, resync keep, Uninstall keep, saveOnError exclusion) — covered by TestResyncKeepsOldClaudeFiles, TestInstallFailureKeepsClaudeFilesOutOfTheManifest (harness_test.go) and TestCrossingFromV270 (cmd/fyr/crossing_test.go). Not separately measured with -cover; Uninstall's keep is not named by an owed test and I did not map it to one.
- cmd/fyr/crossing_test.go, internal/harness/harness_test.go — are tests.
- README.md, .agents/docs/deploy/remote.md — docs, no test.

## Not examined
- Per-line coverage (`-cover`) of apply.go; whether Uninstall's keep branch has its own test.
- Docs wording of README.md / remote.md changes (reviewer's lane); guide §3a, §3c-3g, §4, §5 (not owed).
- Two untracked review files under .agents/docs/reviews/ (another agent's artifacts) were present at start and end.

## Environment
Linux 6.12 amd64, go1.24.4, govulncheck at /home/kesokaj/.go/bin. Tree unchanged between step 1 and the end (only this log added under .agents/docs/testing/; scratch under .agents/scratch/verify-keep/). Real home untouched.

Status: PASS
