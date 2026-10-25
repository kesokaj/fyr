---
owner: explorer
date: 2026-10-25
---
# Recon — harness-audit IN-2: internal/harness (2026-10-25, commit c4885ae)

## Status: COMPLETE

Read in full: apply, state, release, zip, plan, inventory, frontmatter, migrate, sweep, source, repo, adopt, render, pathguard, owned_*. Nothing run (read-only). About 12 tool calls used.

## Summary
The installer holds its stated rules. Every destructive step in apply.go (resync, uninstall) is limited to manifest entries that `LoadState` has already filtered with `filepath.IsLocal`, skips symlinks and paths under a link, and keeps `~/.claude` files. The release path verifies the exact bytes it then installs (SHA256SUMS line, `# fyr vX.Y.Z` marker equal to the tag, canonical tag regex). The zip unpack validates every name, kind, size and duplicate before it writes. I found no high-severity defect. The defects are in the lint's asymmetry (skills, commands and rules use a two-key deny list while agents use an allow list), a "read-only" `Diff` that creates a directory, command and rule names that are never validated, and a few warts.

| ID | Sev | Where | Finding | Recommendation |
|---|---|---|---|---|
| IN1 | med | internal/harness/inventory.go:209, :348 | Skills, commands and rules refuse only the exact keys `allowed-tools` and `hooks` (a case-sensitive deny list), while agents use an allow list (`agentKeys`, :236). Any other permission-shaped key (`permissionMode`, `mcpServers`, `allowedTools`, `Allowed-Tools`, `allowed_tools`) passes in a skill, command or rule, though the AGENTS.md and ADR-056 rationale ("the next vendor key is refused by default") applies equally. Whether Antigravity honours any of them is not known from the code. | defer to operator: either an allow list for skills (name, description, disable-model-invocation, metadata, license...) or fold the key match to lower case with `-`/`_` removed. Security judgement needed. |
| IN2 | med | internal/harness/apply.go:277 (called from Diff) | `Diff` calls `ConfigDir`, which does `MkdirAll(~/.config/fyr, 0700)`, and writes a temp file there. `fyr diff` is meant to be read-only, so on a home with no state dir it creates one. Status does not. It is only reached when a file differs, so it is minor in practice. | fix: use a temp dir under an existing dir, or pipe the plan bytes via `diff -` on stdin and write nothing |
| IN3 | med | internal/harness/inventory.go:336-361 (loadDocs) | Command and rule names come from the file name and are never passed through `validName` or `identLike`, unlike skills and agents. The name is rendered in backticks into the deployed AGENTS.md (`RenderInventoryCompact`:439,446) and used as a path segment (plan.go:113,117). A name with a backtick, a newline or a leading `.` gets into the model's context. Path escape is impossible (glob gives one segment). For a checkout the author is the operator; for a zip install `zipNameSafe` (zip.go:267) allows a newline or backtick in an entry name, so a hand-downloaded zip could inject prose into the context. The zip is unsigned when it has no SHA256SUMS (the "unverified" path). Security judgement needed. | fix: `validName("command"/"rule", ...)` in `loadDocs` |
| IN4 | low | internal/harness/state.go:58-64 | `LoadState` accepts any local manifest entry, not only entries under `.gemini/config`, `.local/bin/fyr` or the old `.claude`. A hand-edited `state.json` listing `.ssh/authorized_keys` makes the next install or uninstall remove it (the manifest is user-owned, so this is the stated trust root, but there is no allowed-root check). A local entry such as `.` also reaches `os.Remove(home)` (fails if non-empty). | defer; add an allowed-prefix filter next to `IsLocal` if wanted. Security judgement needed. |
| IN5 | low | internal/harness/apply.go:225 | `Status` does `os.ReadFile` before looking at `lerr`, so a planned path that is a symlink to a FIFO or a special file blocks, and a symlink target is read before it is classed "foreign". `Diff` checks the link first. Read-only, so no damage. | fix: move the ReadFile after the link check |
| IN6 | low | internal/harness/release.go:36, :224 | The `# fyr vX.Y.Z` marker regexp is `(?m)` and matches on any line; `FindSubmatch` takes the first. A SHA256SUMS with two different markers is not refused. The doc says "at the top". The attacker model is the same as the file's own content, so there is no bypass, but the check is looser than described. | fix or amend the comment: refuse a second distinct marker |
| IN7 | low | internal/harness/release.go:150-155 | After a verified binary is written, a failed `install` returns an error with the new binary in place and the old harness deployed. The message names `install` failing but not that the binary is already replaced (`status` then shows drift). | defer: add a sentence to the error |
| IN8 | low | internal/harness/inventory.go:53-75 | Doc comments are scrambled: the `validName` comment sits over `identLike`, and its tail ("time rather than finding a file written beside their home.") is orphaned over `validName`. Violates "comments say why". | fix |
| IN9 | low | internal/harness/apply.go:51, :133, :139 | Three ways of deciding "not fyr's to remove" are repeated (symlink or underLink or claudeFile): in `saveOnError`, in the resync loop and in Uninstall. | see IN-S1 |
| IN10 | low | internal/harness/apply.go:216-253 | `Status` counts old `.claude` manifest entries still on disk as `stale` (it has no `claudeFile` check), so a home from before ADR-062 shows drift until the next install drops them from the manifest. After D32 that is the only way to clear it. Probably the intended prompt to run install; untested here. | defer; confirm with the operator |
| IN-S1 |  | internal/harness/apply.go:51,133-142,337 | One helper `fyrOwns(home, rel) (kind)` for symlink / underLink / claudeFile would replace three copies of the same condition. | operator's choice |
| IN-S2 |  | internal/harness/{source.go,repo.go,inventory.go} | Exported but used only inside the package (no non-test caller outside it): `AssetName`, `DirSource`, `EmbeddedSource`, `IsRepo`, `Keys`, `LoadAgents`, `LoadSkills`, `ParseStrict`, `ReadCapped`, `RemoveState`, `RenderInventoryCompact`, `RunningElsewhere`, `SaveState`, `SplitList`, `TightenConfigDir`. Not dead, only needlessly exported. `SecretShaped` (plan.go:135) is a one-line wrapper over `secretShaped`, used once outside (internal/search). | defer; low value |
| IN-S3 |  | internal/harness/release.go:327-376 and zip.go | Zip verification (`VerifyZipSums`, `ErrZipNoSums`, `zipNamesIn`) lives in release.go while the unpack lives in zip.go. | defer |

## Measurements
- Exported symbols with no caller outside `internal/harness` (non-test): command `for f in <exported funcs>; grep -rn "harness\.$f" --include=*.go cmd internal | grep -v _test.go`. Result: the 15 listed in IN-S2. None has zero callers, so there is no dead exported code *checked*.
- Destructive and write sites, `grep -n 'os\.\(RemoveAll\|Remove\|Rename\|Chmod\|MkdirAll\|OpenFile\)' internal/harness/*.go` excluding tests; guards read:
  - apply.go:148 (stale `os.Remove`) and :348 (uninstall): manifest entry (IsLocal-filtered, state.go:60), not a Claude file, Lstat is not a symlink, not under a link. *checked*
  - apply.go:472 `pruneEmptyDirs`: only removes empty directories, stops at home, `.claude`, `.gemini`, `.gemini/config`, `.config`, `.local`, `.local/bin`. It does not stop at `.config/fyr`; RemoveState (state.go:136) removes it when empty, which is intended. *checked*
  - apply.go:112/420 `WriteAtomic`: reached only after the symlink and underLink checks. A race between check and write is not guarded (TOCTOU, acceptable on a single-user home).
  - release.go:125 `RemoveAll(dir)`: dir is a fresh `MkdirTemp` under `~/.config/fyr`. :140 write goes through `WriteAtomic(~/.local/bin/fyr)` and does not check for a symlink at the destination (rename replaces the link rather than following it). *checked*
  - sweep.go:35 `RemoveAll`: only direct children of `~/.config/fyr` with prefix `release-` or `zip-`, real directories, older than 1 hour. *checked*
  - zip.go:102 `RemoveAll(tmp)`: its own `MkdirTemp`. :245-254 writes are under `tmp` with names validated by `zipNameSafe`, `O_EXCL`, symlink entries refused. *checked*
  - adopt.go:79/89/101/120 `RemoveAll`: staging and backup under the repo's `.agents/scratch`, name from `validName`, overlap refused with `within`. *checked*
  - repo.go:284 `RemoveAll(build dir)`: repo scratch, per pid.
  - migrate.go:52 one `Rename` of `~/.config/sh-agent` to `~/.config/fyr`, only when the new one is absent and the old is a plain directory. Refuses a symlink. *checked*
- Error values that are always nil: none found in the files read. `StateDirNotice` and `TightenConfigDir` return no error by design.
- Imports: harness imports only the standard library and `github.com/kesokaj/fyr` (the embed package); no SQLite. *checked* by reading the import blocks of all files read.
- Lint, agents: unknown tool names refused (inventory.go:308-312), `knownTools` has six names; tools key absent is allowed (what the CLI then grants is not shown in the code). Agent keys allow-listed; `subagent: true` and `mainAgent: false` forced.
- Test, from baseline: `test.txt` shows `ok internal/harness 92.9s coverage 89.8%`, exit 0.

## Refuted
- "A path can write or delete outside home": refuted for the manifest paths (IsLocal in LoadState, tested by `TestLoadStateDropsNonLocalManifestEntries`, `TestResyncNeverFollowsANonLocalManifestEntry`), for plan paths (`validName` for skills and agents; file names inside a skill come from a WalkDir under the skill dir, symlinks refused), and for the zip (zipNameSafe plus `IsLocal`, trees allow-listed).
- "A release can ship an unchecked artifact": refuted. `downloadRelease` returns the bytes it read; `verifyRelease` hashes those same bytes against the line for the exact asset name; the bytes written by `WriteAtomic` are `data`, not a re-read. The marker must equal the tag; duplicate-conflicting lines for the asset are refused; the file read is capped at 256 MiB. The `.zip` path hashes the buffer it unpacks (zip.go:55-66). Residual: SHA256SUMS comes from the same release as the binary (unsigned, ADR-053, stated).
- "Dry run skips a guard the real run applies": refuted for Install (the same Lstat, underLink and claudeFile checks run before the `dryRun` branch) and Uninstall. Exception: IN2 for Diff, which is not a dry run.
- "A refusal exits zero": every refusal here returns an error; I did not read the exit-code mapping in cmd/fyr (see Not examined).

## Not examined
- Test files (11k lines, mostly harness_test.go and catalog_test.go); only their names were used. Whether tests cover IN1-IN3 and IN10 is unknown.
- `cmd/fyr` (other dispatch): how errors map to exit codes, whether `MigrateStateDir` runs before every writing verb (only one call site seen, main.go:335), and what `diff` and `status` do about `StateDirNotice`.
- pathguard.go semantics only partly relevant (read in full, but its callers are in cmd).
- No `go test`, `go vet` or `govulncheck` run (read-only); coverage figure is the baseline's.
- Which keys Antigravity honours in skill, command and rule frontmatter (IN1), and what a missing `tools:` grants an agent, cannot be told from the code.
