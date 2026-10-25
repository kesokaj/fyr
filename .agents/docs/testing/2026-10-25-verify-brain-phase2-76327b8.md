---
owner: verifier
date: 2026-10-25
---
# Verification — brain-phase2-76327b8   (2026-10-25)

Range reviewed: 9e302f1..76327b8 (clean clone .agents/scratch/verify-76327b8, HEAD 76327b8, never the main tree). Supersedes 2026-10-25-verify-brain-phase2-ddaa522.md. Guide: .agents/docs/testing/guide.md (exists, not changed).

## Status: FAIL

Scope: every command the dispatch lists ran (steps 1-5). Not complete: govulncheck ran on go1.24.4 only (see Not examined), and one changed function has no coverage. The suite itself is green.

## Commands
- 1. `gofmt -l cmd internal` → exit 0, printed nothing
- 1. `go vet ./...` → exit 0
- 4. `go test -race -v -count=1 -timeout 30m ./...` → exit 0 (1m31s); counts: pass 767, fail 0, skip 6 at 76327b8 for go test -race -v -count=1 -timeout 30m ./...; vs previous: none comparable (the ddaa522 log ran a different range state; counts not compared). Packages all `ok`: fyr (root), cmd/fyr 85s, internal/brain, dream, harness, memory, metrics, redact, search, trail. Skips: TestHelperMain, TestHelperAdd, TestHelperCall, TestHelperIndex (helper-process re-exec), TestAgyCorpusLeaves, TestRecall (explained: corpus/box dependent, not touched by Phase 2 files).
- 2. `govulncheck ./...` (by full GOPATH path) on go1.24.4 (/usr/bin/go; the user-level go1.27.2 named in AGENTS.md is not present on this box: ~/.local/bin/go is dangling) → reports 15 standard-library vulnerabilities reached by the code (e.g. GO-2025-3849 database/sql, fixed in go1.24.6; traces internal/search/search.go:418, internal/metrics/show.go:201), 5 in imported packages and 42 in required modules not called. AGENTS.md: these are Go-version advisories, fix is upgrading the machine's Go, not a code finding. Nothing in internal/brain was named in the trace tail I read (only the tail of output was read).
- 3. `go run ./cmd/fyr install --home <clone>/.agents/scratch/home` → exit 0, "898 written, 0 unchanged, 0 removed". `status --home` same → `binary     76327b879546`, no warning on the binary line. status also printed `leftover /home/kesokaj/.local/bin/sh-agent ...` (operator's real-home pre-rename leftover; not from this run).
- 4. Brain verbs, built binary, scratch repo + --home (.agents/scratch/{repo,home2}):
  - `echo body | record add bug --title t` → `BUG-001`
  - `record list` → `BUG-001  bug  open  2026-10-25  t`
  - brain.db deleted, `record list` → same row, brain.db recreated from .agents/trail.jsonl
  - trail.jsonl with the line duplicated → exit 1: `fyr record: trail.jsonl line 2: duplicate ID BUG-001 (first at line 1); renumber one of the two lines by hand`
  - `echo mem body | memory add --type user --name x --description d` → `saved MEMORY-x`; `memory show` → `- x (user) — d`
- 5. From the clone root, `record import --dry-run --home <scratch home3>` → exit 0; first line: `import (dry run: nothing written): adr 73, bug 177, change 90, deploy 3, guide 3, handoff 48, plan 63, retro 12, review 247, spec 6, testing 227; 949 records, 772 dropped (git history keeps them), 0 already stored`. Clone `git status --short --untracked-files=all` before and after: empty. No brain.db under the scratch home.
- 7. Suppressions in the diff: no added nolint, lint:ignore, ts-ignore, .only, .skip. Added `t.Skip` calls (internal/brain/brain_test.go:399, import_security_test.go:150, internal/memory/memory_test.go:171) each carry a reason string.

## Coverage of changed code
Measured with `go test -coverpkg=./internal/brain,./internal/memory,./internal/trail ./...` (whole suite): every function in those three packages is hit except one. Per-package: brain 83.3%, memory 81.0%, trail 69.0%, search 83.4% (own tests only). Changed shell files under .agents/kit/ have no Go test (covered by the guide's behavioural kit runs, not run here).
- internal/brain/import.go:858 `utf8Text` — NOT COVERED, and unreferenced anywhere in the repo (dead code left by the round-2 edits) → FAIL. Reported, not patched: either delete it or use and test it.
- Functions that showed 0% in a per-package run (brain.ImportSummary, FormatHit, Kinds, trail.Roots, sameDir, LazyFetchSafe, memory Error) are covered by cmd/fyr and internal/dream tests in the whole-suite run.

## Root cause
Only the coverage finding: `utf8Text` in internal/brain/import.go:858 is a leftover helper with no caller and no test. Tests, vet, gofmt, install, the brain end-to-end and the import dry run all pass.

## Not examined
- govulncheck on a current Go (go1.27.2 absent); only the tail of its output was read, so a finding in internal/brain beyond the shown trace is not ruled out.
- Guide § 3d matrix rows, § 3e/§ 3g real-data checks, § 4 Antigravity sandbox and behavioural runs, § 5 real-home install, the .agents/kit/ scripts run.
- Vs-previous counts for pass/skip: no earlier log with the identical command was compared.
- Main tree at the end showed other agents' paths: ` M .agents/docs/plans/2026-10-25-sqlite-trail.md`, `?? .agents/docs/changes/2026-10-25-1639-brain-phase1-checkpoint-phase2-code.md` (docs, listed as seen); no source, test or config path moved.
- Main tree later also showed source edits by another agent (` M cmd/fyr/record_test.go`, ` M internal/brain/search.go`, `?? internal/brain/refs_test.go`); this run tested the clone at 76327b8, so the verdict is for that commit only, not for those edits.

## Environment
Linux 6.12.113 amd64, go1.24.4 (/usr/bin/go), no Docker used. Clone at 76327b8. An earlier `~/.config/fyr` already existed on the box; every fyr call here carried --home.
