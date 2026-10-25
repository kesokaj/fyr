---
owner: verifier
date: 2026-10-25
---
# Verification — brain-phase2-ddaa522   (2026-10-25)

Range reviewed: 9e302f1..ddaa522 (clean clone `.agents/scratch/verify-ddaa522`, checked out at ddaa522; replaces the stopped 726138d run). Guide: `.agents/docs/testing/guide.md` (§ 1, § 3d).

## Status: FAIL

Scope: steps 1-5 of the dispatch ran in the clone; the .agents/kit/ scripts (behavioural tier), guide §3d matrix rows and §2/§4 sandbox runs were not named by the dispatch and did not run. Not complete.

## Commands
- 1. `gofmt -l cmd internal` → exit 0, printed nothing
- 1. `go vet ./...` → exit 0
- 1. `go test -race -timeout 30m ./...` → exit 0, 10 of 10 packages ok (fyr 1.2s, cmd/fyr 71.3s*, brain 16.8s, dream 43.2s, harness 70.8s, memory 1.3s, metrics 6.3s, redact 1.3s, search 12.0s, trail 1.2s). A first bare run (exit 0, 90 s) and a -v rerun both passed; *times from the -v run.
- 4. `go test -race -v -count=1 -timeout 30m ./...` → exit 0; counts: pass 776, fail 0, skip 6 at ddaa522 for go test -race -v -count=1 -timeout 30m ./...; vs previous: 2026-10-25-verify-*.md newest with that command (fyr-content, 0a1bd0c) pass 704→776, skip 5→6; the extra skip is `TestHelperAdd` (subprocess helper, brain, guarded by `t.Skip("helper process")` at internal/brain/brain_test.go:399, added in this range, has a reason); explained: new helper-process test. Other skips as before: TestHelperMain, TestHelperCall, TestAgyCorpusLeaves, TestRecall (env-gated), TestHelperIndex. Output: `.agents/scratch/verify-ddaa522-go-test-v.txt`.
- 2. Go: `go version go1.24.4 linux/amd64` (/usr/bin/go; ~/.local/bin/go dangles). `$(go env GOPATH)/bin/govulncheck ./...` → exit 3: 15 vulnerabilities, ALL "Standard library" (go1.24.4, fixed in go1.24.6 and later; e.g. GO-2025-3956 os/exec reached from internal/harness/repo.go:277, GO-2025-3849 database/sql reached from internal/search/search.go:418 and internal/metrics/show.go:201); plus 5 in imported packages and 42 in required modules, none called. No finding outside the standard library; the AGENTS.md remedy is a newer Go on the machine, not a pin. Output `.agents/scratch/verify-ddaa522-vuln.txt`.
- 3. `go run ./cmd/fyr install --home <clone>/.agents/scratch/home` → exit 0, "898 written, 0 unchanged, 0 removed, 0 kept". `go run ./cmd/fyr status --home …` → exit 0: `binary     ddaa52291458` (no warning), `drift none — 897 files match the repo`, repo HEAD @ ddaa522, clean. Status also prints a `leftover /home/kesokaj/.local/bin/sh-agent …` line (real home's pre-rename binary; not a binary warning, not from this change).
- 4. Brain verbs, binary = scratch home's `.local/bin/fyr`, `HOME` and `--home` = scratch home, scratch git repo `.agents/scratch/verify-ddaa522/.agents/scratch/brainrepo`:
  - `echo "body text" | fyr record add bug --title t` → `BUG-001`, exit 0; `.agents/trail.jsonl` written.
  - `fyr record list` → `BUG-001  bug  open  2026-10-25  t`.
  - `rm brain.db*` then `record list` → same line returned (rebuilt from trail.jsonl); brain.db recreated.
  - trail.jsonl with the line duplicated → exit 1: `fyr record: trail.jsonl line 2: duplicate ID BUG-001 (first at line 1); renumber one of the two lines by hand`.
  - `fyr memory add --type user --name x --description d` with empty stdin → exit 2 "the body comes on stdin and is empty" (by design); with a body → `saved MEMORY-x`; `fyr memory show` → `- x (user) — d`, exit 0.
  - Observed: in a repo that has an empty `.agents/docs/` directory, `record add`/`list` refuse with "this project's trail is still files under .agents/docs/: migrate it with `fyr record import` first". By design as read; noted for the main agent in case an empty docs dir should not count.
- 5. `fyr record import --dry-run --home <scratch>` from the clone root → exit 0. First line: `import (dry run: nothing written): adr 73, bug 177, change 90, deploy 3, guide 3, handoff 48, plan 63, retro 12, review 247, spec 6, testing 227; 949 records, 768 dropped (git history keeps them), 0 already stored`. Then 2596 per-record lines. Wrote nothing: `git status --short` in the clone empty, scratch home's `.config/fyr` holds only state.json (no brain.db).
- 7. Suppressions in the diff: one added `t.Skip("helper process")` (internal/brain/brain_test.go:399), with reason. No nolint, ts-ignore, .only, .skip.

## Root cause  (FAIL: coverage only; no test failed)
No command failed. FAIL is for changed code without a covering test, per the verifier definition.

## Coverage of changed code
Measured with `go test -cover` and `-coverpkg=./internal/brain,./internal/trail` over internal/brain and cmd/fyr; package totals: brain 81.9%, trail 69.0%, memory 91.1%, search 83.4%, cmd/fyr 89.8%.
- internal/brain/{brain,export,import,memory,record,search,store}.go — covered by brain_test.go, import_test.go, security_test.go; cmd/fyr/record.go by record_test.go; cmd/fyr/memory_test.go, internal/memory/memory.go by its tests; internal/search/{export,parse,reconcile,store}.go by search_test.go; internal/trail/trail.go by trail_test.go and brain worktree test (brain_test.go:735).
- NOT COVERED → FAIL: `internal/brain/store.go:205 isConstraint` (0.0%) — its only use, the constraint branch at `internal/brain/export.go:328`, has no test that reaches it.
- Weakly covered (reported, partial): `internal/brain/import.go:77 docsRootOf` 55.6%; `internal/trail/trail.go` `Roots` 80%, `sameDir` 71.4% (no direct test in internal/trail; reached through brain tests); `LazyFetchSafe` 0% in -coverpkg run of brain+cmd, exercised in internal/dream/collect_test.go:636.
- `internal/brain/open_other.go` (non-unix build): cannot run on Linux, no test; `.agents/kit/kit-preamble.sh`, `.agents/kit/agy-rel/run.sh`: shell kit, no automated test of the move/depth fix (internal/harness/catalog_test.go and internal/metrics/verb_test.go reference it only as strings; kit runs are the guide §4 behavioural tier, not named by the dispatch).

## Not examined
- Guide §2/§4 sandbox and behavioural runs, the kit scripts executed, §3d matrix rows, §5 real-home checks.
- Review of the diff itself (the verifier does not review); the untracked `.agents/docs/reviews/2026-10-25-security-brain-import.md` in the main tree is another agent's artifact, listed by name.
- Mutation (must-fail) controls were not asked for.

## Environment
Linux 6.12 (Debian 13) amd64, go1.24.4 (/usr/bin/go), 16 CPUs by earlier logs; no Docker involved. Main tree at start: clean; at end: untracked `.agents/docs/reviews/2026-10-25-security-brain-import.md` (another agent's), `.claude/settings.local.json`, and this log; no source, test or config path changed. The clone stayed clean through all commands.
Side effect to report: one `fyr record` help call early in step 4 ran without `--home` or `HOME` set and printed `migrated ~/.config/sh-agent → ~/.config/fyr`, i.e. it may have touched the REAL home's `~/.config` (dir `fyr` there is dated 16:01, before this run's first command at ~16:17, so the migration may predate it; not established). All later calls set both.
