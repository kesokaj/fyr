---
owner: main
date: 2026-10-25
---
# fyr Phase 5b: the old `~/.claude` files stay     (date: 2026-10-25)

## Summary
Plan `2026-10-24-fyr-agy-only.md` Phase 5b, D32. The operator said, in their own turn: "dont remove the claude skills and context on this machine. That should still be left on dave. But in the project we will only focus on antigravity going forward." Asked how, they chose "fyr never removes them". So:
- **The crossing keeps Claude Code's files.** Every `~/.claude` entry of a pre-fyr manifest that is still on disk stays as it is: the context, the `CLAUDE.md` stub, skills, agents, commands and rules.
  - The entries leave fyr's manifest, and install says so in one line: `keep    ~/.claude: <n> files an install before fyr wrote for Claude Code; fyr never removes them, and they leave its manifest`.
  - `~/.local/bin/sh-agent` still goes by the resync.
- **Uninstall keeps them too**, even when it runs straight after the migration, over the old manifest.
- **A failed install keeps them out of the manifest** it saves (the IN2 union skips them).
- **An unclean manifest entry is judged as the path it removes:** cleaned, with the first element compared without case.
- The project itself stays Antigravity-only. No Claude Code support returns; the files are frozen at v2.7.0's text.

## Type
task (plan phase; amends D2's stated consequence and D14's sweep)

## Files changed
- `internal/harness/apply.go`:
  - `claudeFile` and `keepClaudeLine`;
  - the resync's keep and its one line;
  - `Uninstall`'s keep;
  - `saveOnError` skips `claudeFile` entries;
  - the header comment.
- `internal/harness/harness_test.go`:
  - `TestResyncShedsOldClaudeFiles` becomes `TestResyncKeepsOldClaudeFiles`. It covers uninstall over the old manifest, the install's keep and its one line, a second install that says nothing, a later uninstall, `~/.local/bin/sh-agent` removed, and two unclean entries.
  - New: `TestInstallFailureKeepsClaudeFilesOutOfTheManifest`.
- `cmd/fyr/crossing_test.go`: `TestCrossingFromV270` now asserts that the real v2.7.0 manifest's `~/.claude` files survive byte for byte, and that the keep line names them all.
- `.agents/docs/deploy/remote.md` § What `fyr install` does at the crossing: the resync bullet.
- `README.md`: the `fyr install` row names the exception.
- Trail: `reviews/2026-10-25-review-worktree-code-docs.md` and `reviews/2026-10-25-security-worktree.md`, each with `## Outcome`; `testing/2026-10-25-verify-fyr-keep-claude.md`; the plan (Phase 5b ticked). Memory `keep-claude-files-on-dave`, local and gitignored, plus one feedback-log entry.

## Agents dispatched
`reviewer` (`--code --docs`, working tree), `security` (working tree), `verifier` (last).

## Verification
- **Red, then green.**
  - The keep test failed on the shedding code: uninstall listed `remove ~/.claude/…` and install deleted the files.
  - The unclean entries were deleted before the clean-path fix.
  - The failure test found `.claude/AGENTS.md` back in the saved manifest before its fix.
- **Mutations.** Each of these fails a test:
  - dropping the resync's keep fails `TestResyncKeepsOldClaudeFiles` and `TestCrossingFromV270`;
  - dropping uninstall's keep fails `TestResyncKeepsOldClaudeFiles`;
  - dropping `saveOnError`'s exclusion fails `TestInstallFailureKeepsClaudeFilesOutOfTheManifest`.
- **A scratch crossing from a real v2.7.0 install** (`.agents/scratch/fyr/p5b/crossing.sh`, the v2.7.0 binary from Phase 4b):
  - one `fyr install` built from the tree: `keep    ~/.claude: 895 files …`;
  - `sha256sum` of all 896 files under `~/.claude` identical before and after (`settings.json` included);
  - `~/.local/bin/sh-agent` and the old `.sh-agent-noindex` marker removed (2 removed);
  - the timer replaced after the probe;
  - `status` exit 0, `drift none`.
- **Suites.** Bare full suite exit 0. Verifier PASS (`testing/2026-10-25-verify-fyr-keep-claude.md`: 665 pass, 0 fail, 5 skip, all 9 packages; the three tests RAN, none skipped; govulncheck only go1.24.4's known 15). Uninstall's keep is asserted inside `TestResyncKeepsOldClaudeFiles` (uninstall --dry-run over the old manifest, and uninstall after the installs).

## Not done
- Phase 4b report items 2 and 3, still the operator's:
  - `uninstall` and `--remove-timer` leave an old-named unit;
  - F4's `status` line.
