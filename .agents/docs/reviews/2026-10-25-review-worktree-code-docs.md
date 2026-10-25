---
owner: reviewer
date: 2026-10-25
---
# Review — worktree-code-docs   (2026-10-25)

Range reviewed: working tree — changed files: .agents/docs/deploy/remote.md cmd/fyr/crossing_test.go internal/harness/apply.go internal/harness/harness_test.go; untracked, not ignored: none; read with `git diff HEAD -- <files>`

## Status: COMPLETE

## Verdict: APPROVE WITH NITS

The code is sound. `claudeFile` (Lstat, `.claude/` prefix) is checked before the removal rule in `Install` and in `Uninstall`. A `~/.claude` entry that is still on disk, symlinks included, is counted and never touched. An entry already gone falls through to the old rule, which skips it because the Lstat fails. Install prints one keep line and drops the entry from the new manifest. I found no failure path. One docs restatement is stale: `README.md:42` still says install removes whatever a previous install wrote that the repository no longer has. That is now untrue for the old `~/.claude` files, and the plan's Phase 5b task names this README line.

## Findings
- **[low / confidence: high]** `README.md:42` — says `fyr install` "removes files a previous install wrote that the repository no longer has". After this diff the old manifest's `~/.claude/...` entries are exempt: they are kept and leave the manifest. The Phase 5b task in plan `2026-10-24-fyr-agy-only.md` (line 86) names "README.md where it says what install removes". Fix: add "(except Claude Code's files an install before fyr wrote under `~/.claude`, which it keeps)" and point at `deploy/remote.md` § Crossing. The sentence at `README.md:44` ("Removes exactly what it installed") stays true, because those entries leave the manifest.

## Docs lens
- Restatements checked by grep over README, AGENTS.md, context, rules, agents, specs, testing guide, deploy docs and `*_test.go`: `deploy/remote.md:89` was updated and matches the code. That covers the keep-line text, the `{agents,skills,commands,rules}` list, the symlink rule, `~/.local/bin/sh-agent` still removed, and the `settings.json` note. `local.md:111` ("uninstall removes exactly the files the manifest lists") still holds, since the entries leave the manifest.
- Stale tests: none. `TestInstallLeavesClaudeCodeAlone` (`cmd/fyr/main_test.go`) uses a fresh home with no old manifest, so it is unaffected. The old `TestResyncShedsOldClaudeFiles` is renamed and reversed. Its symlink-under-resync case moved to `TestCrossingFromV270` (F2, the first Claude entry replaced by a symlink).
- The plan's Phase 5b boxes are still unticked and the plan has no `changes/` entry for this work. Those are records for the author's close-out, not findings.
- Unverified: I did not read the delivery spec's prose on `uninstall` or the testing guide for a removal claim beyond the grep above, which found none.

## Criteria
1. Dead code — pass. The new helpers `claudeFile` and `keepClaudeLine` each have two callers.
2. Flow tracing — pass. Install, dry-run, `saveOnError`, Uninstall and the crossing path all agree. `saveOnError` re-lists still-present old entries, `.claude` ones included, which is harmless because uninstall skips them. Both targeted test runs pass: `go test ./internal/harness -run 'TestResyncKeepsOldClaudeFiles|TestInstall|TestUninstall'` and `go test ./cmd/fyr -run TestCrossingFromV270`. The new tests can fail: they compare file bytes, the keep line, and a second install that must stay silent. I did not run the mutation protocol.
3. Standards conformance — one stale restatement (`README.md:42`). The comments on `Install` and the tests were updated with the behavior.
4. Directory layout — pass. No new files.
5. Simplicity — pass. Stdlib only, no security primitives touched.
6. Decoupling — pass. The change is local to `apply.go`.

## Not examined
- The full `go test -race ./...` suite, `gofmt`, `go vet` and `govulncheck` (the verifier's job). Only the targeted tests above were run.
- The mutation protocol for the new tests.
- `skills/`, `context/` and `agents/` prose beyond the restatement grep.
- Whether the plan's Phase 5b boxes and the `changes/` entry exist (records, out of scope).

## Outcome

Written by the main agent, 2026-10-25, in the Phase 5b commit of plan `2026-10-24-fyr-agy-only.md` (D32).

1. `README.md:42` says install removes every file the repository no longer has — **fixed**: the row now names the exception (the Claude Code files an install before fyr wrote under `~/.claude` are kept and no longer managed) and points at `deploy/remote.md` § Crossing to fyr. It is a docs line, with no test; the code it describes is pinned by `TestResyncKeepsOldClaudeFiles`.

Round 2 skipped: the one fix is a docs line, and the verifier runs after it (`review` Step 4).
