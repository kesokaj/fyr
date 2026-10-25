---
owner: verifier
date: 2026-10-25
---
# Verification — fyr-close   (2026-10-25)

Range reviewed: v2.7.0..881389a plus uncommitted working tree (14 modified files)

## Status: FAIL

Scope: tiers 1, 2, 3, 5 ran; tier 4 ran as unit tests plus search timing, metrics real-data check was vacuous (see Not examined); not complete (coverage gap). Run kit/behavioural tiers (guide 4) not asked. Guide: .agents/docs/testing/guide.md (exists, read).

## Commands
- 1. orient: pwd /home/kesokaj/workspace/styrman-harness, HEAD 881389a, dirty tree as dispatched (cmd/fyr, internal/dream, internal/harness/repo.go + docs). Untracked: reviews/2026-10-25-*.md x2, testing/2026-10-25-verify-fyr-close-agy.md (another artifact, seen not touched).
- 4. `gofmt -l cmd internal` → exit 0, printed nothing.
- 4. `go vet ./...` → exit 0, no output.
- 4. `go test -race -v -count=1 -timeout 30m ./...` (output .agents/scratch/verify-fyr-close/go-test-1.txt) → exit 0; counts: pass 663, fail 0, skip 5 at 881389a+dirty for go test -race -v -count=1 -timeout 30m ./...; vs previous: none (the earlier fyr-close-agy log records no go-test counts). Skips: TestHelperMain, TestHelperCall, TestHelperIndex (subprocess helpers), TestAgyCorpusLeaves and TestRecall (env-gated FYR_CORPUS / FYR_RECALL). All 9 packages ok (cmd/fyr 107.8 s, harness 102.9 s, dream 25.4 s).
- 4. `$(go env GOPATH)/bin/govulncheck ./...` → exit 3: "Your code is affected by 15 vulnerabilities from the Go standard library" (GO-2026-6604, 6600, 6599, 6091, 4982, 4980, 4970, 4865, 4603, 4602, 4601, 4341, GO-2025-4010, 3956, 3849; os, html/template, net/url, os/exec, database/sql @go1.24.4) = the known 15 of this toolchain. None in a module: "5 vulnerabilities in packages you import and 42 in modules you require, but your code doesn't appear to call these". No NEW advisory, no module finding reached.
- 3. Scratch-home install: `go build -o .agents/scratch/verify-fyr-close/fyr ./cmd/fyr` exit 0; `fyr install --home .agents/scratch/verify-fyr-close/home` → exit 0, "896 written, 0 unchanged"; `fyr status --home …/home` → exit 0, `binary 881389a74dde+dirty` (no warning), `drift none — 895 files match the repo`, "repo has 36 skills, 8 agents, 0 commands, 2 rules". Home holds .config/fyr/state.json, .gemini, .local; no .claude, no .config/sh-agent.
- 3a. Plain copy (no .git, chmod a-w): `fyr install --repo …/mirror --home …/home2` → exit 0, "plain copy, not its own git checkout: the running binary is installed as is", 896 written; mirror gained no .agents/; `fyr status` → exit 0, "plain copy … update is refused", `drift none — 895 files match`. `install --repo . --home …/home3 --dry-run` → "[dry-run] the binary would be rebuilt from /home/kesokaj/workspace/styrman-harness".
- 3d. `fyr index --rebuild --home …/s` → 3.38 s (1868 files; limit 10 s); warm `fyr search … marker rename` → 0.074 s (limit 100 ms), reports the noindex marker. Unit tests of search/cmd/fyr are inside the passing full run (not repeated: same flags as the full run plus -v -count=1).
- 3g. Metrics unit/canary tests (internal/metrics, trail, dream, search, cmd/fyr) are in the passing full run. Real-data check from a copy: this box's ~/.gemini/antigravity-cli/conversations holds 1 database (384 KB); copied to scratch, `fyr metrics ingest --home …/mh` twice → "0 sessions, 0 steps, 0 events added; 0 lines skipped; 0 sources failed" both times; `metrics show --json --all-labels` has empty tool_calls_by_tool. The copy was deleted. The independent type-132 count and step_verbs shape check could not be made (nothing ingested): not exercised.
- 7. Suppressions in the diff (`//nolint`, `t.Skip`, `.only(`, `ts-ignore` …): checked by reading the Go diffs above; none added in cmd/fyr, internal/dream, internal/harness/repo.go (the test diffs were scanned by grep for Skip: none).
- 8. `git status --porcelain --untracked-files=all` after the run is identical to step 1: tree did not move. Untracked artifacts seen: .agents/docs/reviews/2026-10-25-review-v2.7.0-HEAD-code-docs.md, …-security-v2.7.0-HEAD.md, .agents/docs/testing/2026-10-25-verify-fyr-close-agy.md (other agents' records).

## Root cause
No test failed. FAIL is for a coverage gap only (step 7): see below.

## Coverage of changed code
- internal/dream/timer.go (`timerUnit.on`, probe/off fields, `ReplaceOldTimer` third return) → covered by internal/dream/timer_test.go TestReplaceOldTimer: subtests "old unit turned off: its files go, no fyr timer" (systemctl disabled, masked; launchctl "Could not find service"; dry-run path), "probe cannot tell: replaced as on", and the `replaced` assertions in the existing subtests.
- cmd/fyr/main.go adopt message (clone URL) → covered by cmd/fyr/main_test.go:816 (exact string) and TestOneRepositoryName.
- cmd/fyr/main.go:829-832, the two NEW install messages ("[dry-run] the dream timer %s is turned off: …" and "removed the dream timer %s, which was turned off; no fyr timer installed …") → NOT COVERED → FAIL. grep of cmd/fyr/*_test.go finds only absence assertions of "removed the dream timer" (memory_test.go:202, :254); no test drives installFiles with a turned-off old unit and reads either line, so the switch cases `old != "" && !replaced` (dry-run and real) are unexercised at the install level (the dream-level behaviour is covered).
- internal/harness/repo.go errPlainCopy text → no test reads the message; harness_test.go:687-713 only uses errors.Is. The new URL in it is not pinned by TestOneRepositoryName's file list in practice either? It is: the test reads internal/harness/repo.go and requires `git@github.com:fyr.git`-shaped URL there, so the text is pinned by that file-content check (partial: the check is on the file, not on the error value). Reported as covered indirectly.
- New TestOneRepositoryName → ran and passed in the full run (reads README.md, deploy/remote.md, main.go, repo.go).

## Not examined
- Metrics real-data equality (type-132 count vs B.tool_calls_by_tool) and step_verbs name-shape: the box has one small conversation database that ingests to 0 sessions; nothing to compare.
- Guide 3g browser report (light/dark/400 px screenshots); `metrics report` not run.
- FYR_RECALL recall measurement and TestAgyCorpusLeaves (env-gated, skipped; no change to the reader in the dirty files).
- Guide 3c verb × place matrix, guide 4 sandbox/behavioural runs of the three edited agent.md files (documentation, dreamer, explorer), guide 5 real-home checks: not asked by the dispatch; the agent changes are not behaviourally verified here.
- Must-fail control of the canary tests (no change to ingester/Verb in the dirty files).
- Real `--install-timer` or any real service manager: not run (R6).

## Environment
Linux 6.12.113 amd64, 16 CPUs, go1.24.4, govulncheck from GOPATH/bin, no Docker use. All installs under .agents/scratch/verify-fyr-close/ (home, home2, mirror, s; the real $HOME untouched except reading ~/.gemini/antigravity-cli/conversations for the copy, since deleted).

Status: FAIL
