---
owner: security
date: 2026-10-25
---
# Security review — worktree   (2026-10-25)

Range reviewed: working tree — changed files: .agents/docs/deploy/remote.md cmd/fyr/crossing_test.go internal/harness/apply.go internal/harness/harness_test.go; untracked, not ignored: none; read the diff with git diff HEAD -- <files> and read each untracked file whole.

## Status: COMPLETE

## Verdict: FINDINGS

Two low findings, no high or medium. The change makes it less likely that fyr deletes files: Install's resync and Uninstall now skip every old-manifest entry under `~/.claude/`, where they used to remove it. The worst gap is that the guard (`claudeFile`, `internal/harness/apply.go:371-377`) checks the prefix on the raw manifest string, while removal acts on `filepath.Join(home, rel)`, and Join cleans the path. So an entry that is not in canonical form but resolves into `~/.claude` misses the guard and is still removed. That breaks D32's promise ("fyr never removes them"). It is not a privilege boundary: only the user can write the manifest (`~/.config/fyr/state.json`), and that user can already delete those files. `LoadState`'s `filepath.IsLocal` filter (`internal/harness/state.go:58-64`) still keeps every removal inside the home, so the old containment boundary is unchanged.

**Is the boundary covered?** Yes, on the paths the brief named, for well-formed manifests: `TestResyncKeepsOldClaudeFiles` (uninstall --dry-run over the old manifest, install, a second install, a real uninstall) and `TestCrossingFromV270` (byte-for-byte keep, the symlinked entry, the keep line, sh-agent removed). Both pass under `-race`. Not covered: non-canonical entries (F1), the `saveOnError` path holding `.claude` entries (F2), a symlinked `~/.claude` directory, and a directory-valued entry.

## Findings
- **[low / confidence: high]** `internal/harness/apply.go:372` — `claudeFile` runs `strings.HasPrefix(filepath.ToSlash(rel), ".claude/")` on the uncleaned entry, but `Install` (`:137`) and `Uninstall` (`:333`) remove `filepath.Join(home, rel)`, which is cleaned. Entries that resolve into `~/.claude` but do not start with `.claude/` pass `filepath.IsLocal` and miss the guard, so the resync or uninstall removes the file:
  - `./.claude/AGENTS.md`
  - `.gemini/../.claude/CLAUDE.md`
  - `.Claude/...` on macOS's case-insensitive filesystem

  The reverse case, `.claude/../.local/bin/x`, is kept: the safe direction, but still a mismatch. fyr's own writer stores canonical `f.Rel`, so this needs a hand-edited or foreign manifest. That is why the severity is low; the cost is the D32 data-preservation guarantee, not escalation. Remediation:
  - Test the first element of `filepath.Clean(rel)`, compared with `strings.EqualFold` on darwin; or canonicalise entries once in `LoadState` next to the `IsLocal` filter.
  - Add a test case per shape above, red before the fix.
- **[low / confidence: high]** `internal/harness/apply.go:41-61` (`saveOnError`) — when a write fails, or when the sh-agent removal fails at `:147`, the manifest is saved as a union of every old entry still on disk, and that union includes the `.claude` entries. So "they leave its manifest" (`apply.go:24-25`, `remote.md:89`) does not hold on the error path, and `Status` (`:241-249`) keeps reporting them as `stale`. Removal stays safe: both removers check `claudeFile` first. This is a correctness and documentation gap, not exploitable. Remediation: skip `claudeFile` entries when building the union, and add a test with a forced write failure over a manifest that holds `.claude` entries.

## Checked
- **Secrets:** none in the diff, the tests or the doc. Output lines print only counts and relative paths.
- **Injection / path:** manifest entries are the only input. `LoadState` drops non-local entries, and removal uses `Lstat` and refuses symlinks and paths under a link (`underLink`). The one gap is canonical form in the new guard (F1). A TOCTOU between `Lstat` and `Remove` already existed, inside a single-user home; not raised.
- **Authz/authn:** not applicable. A local CLI acting on the invoking user's home.
- **Crypto:** none touched, nothing hand-rolled.
- **Dependencies:** no new module; only a stdlib `strings` import was added.
  - govulncheck ran with the shell's go1.24.4 (`/usr/bin/go`); the go1.27.2 SDK at `~/.local/bin/go` that AGENTS.md names is not on this box.
  - It reports 15 stdlib advisories the code reaches. All come from the old toolchain (e.g. GO-2025-3849, fixed in go1.24.6). None comes from this diff, and the fix is upgrading the machine's Go, never pinning it in go.mod (ADR-029).
  - It also reports 5 in imported packages and 42 in required modules that the code does not reach.
  - npm audit: no npm ecosystem in scope.
- **GCP surface:** not touched.
- **Past bugs:** BUG-128 (early return skips the manifest) and BUG-129 (installer follows symlinks) are respected. The new guard sits before the symlink check, and a symlinked `.claude` entry is kept either way.

## Not examined
- Mutation check: I did not confirm that the new tests fail with the `claudeFile` guard removed, because the review never edits source.
- The full `go test -race ./...` suite: only the two changed tests ran (`TestResyncKeepsOldClaudeFiles`, `TestCrossingFromV270`, plus the `TestInstall*`/`TestUninstall*` names matched).
- govulncheck under go1.27.2: not available on this box.
- `cmd/fyr/main.go` status and drift output for the kept entries in the error-path state of F2.
- The migration code that moves `~/.config/sh-agent`: not changed and not re-read.

## Outcome

Written by the main agent, 2026-10-25, in the Phase 5b commit of plan `2026-10-24-fyr-agy-only.md` (D32).

1. `claudeFile` tests the raw entry, not the cleaned path it removes (low) — **fixed**: `internal/harness/apply.go` `claudeFile` cleans the entry and compares its first element without case. `TestResyncKeepsOldClaudeFiles` now holds the entries `./.claude/rules/unclean-a.md` and `.gemini/../.claude/rules/unclean-b.md`, and it failed with both files deleted before the fix.
2. `saveOnError` puts the old `.claude` entries back into the manifest (low) — **fixed**: the union now skips `claudeFile` entries. `TestInstallFailureKeepsClaudeFilesOutOfTheManifest` fails with the guard removed (mutation run) and passes with it.

Round 2 skipped: no finding was medium or higher, every fix has a test that fails without it, and the verifier runs after the fixes (`review` Step 4). Not covered, as the report says: a symlinked `~/.claude` directory, which the existing `underLink` rule already keeps.
