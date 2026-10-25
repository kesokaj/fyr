---
owner: explorer
date: 2026-10-25
---
# Recon — harness audit IN-1, cmd/fyr (2026-10-25, c4885ae)

## Status: COMPLETE

Scope: cmd/fyr/main.go read in full (1345 lines); cmd/fyr tests, embed.go, go.mod, root tests, fyr-matrix.md, README command table sampled (see Not examined). No tool was run; baseline outputs under .agents/scratch/harness-audit/2026-10-25/ were read.

| ID | Sev | Where | Finding | Recommendation |
|---|---|---|---|---|
| IN1 | low | cmd/fyr/main.go:242-244, :89-95 | `fyr list -h` (or `--help`) returns `flag.ErrHelp`; `main` prints `fyr: flag: help requested` and exits 1. Help is a successful ask, and the usage block of the FlagSet is on the writer, so the extra stderr line and exit 1 are wrong. No test pins it (grep ErrHelp: one site, no test). | fix: return nil on ErrHelp (or exit 0) and pin it |
| IN2 | low | cmd/fyr/main.go:221-223 | Unknown command returns a plain error (message carries the whole usage block), so it goes to os.Stderr via main, not the caller's writer, and exits 1. Every other usage error exits 2 on the writer (usage_test.go:15-60). `testing/2026-10-25-verify-fyr-rename.md:48` already notes the 2-vs-1 contradiction ("Phase 5 should reconcile"); `TestMainExitStatus` pins 1. | operator's choice: pin 1 and fix the docs that say 2, or move to exit 2 on the writer |
| IN3 | low | cmd/fyr/main.go:754, internal/harness/repo.go:231 | `update --check` / `--dry-run` at a checkout call `harness.Behind`, which runs `git fetch --quiet` (moves remote-tracking refs, may use the network). Usage text (main.go:50) says `--dry-run ... changes nothing`; the fetch is disclosed in the usage ("git fetch") but not as an exception to "changes nothing". Not a data risk. | defer: reword to "changes nothing in the tree or the home" |
| IN4 | low | cmd/fyr/main.go:318-319 | `writesState` lists `adopt` and a plain project-local `search` / `index` as state-writing verbs, so they run the migration. `adopt` writes into the repo, and a local `search` writes `.agents/scratch/search.db` (the home is used only for --global). Over-migrating is harmless and arguably safe (rename is one atomic step, refuses symlinks, leaves both-present alone), but the table contradicts the comment at :313-315 ("writes under the state directory"). Also `dream --install-timer/--remove-timer` migrate though they write no state. | defer: say in the comment that migration is deliberately early for these |
| IN5 | med | .agents/docs/testing/fyr-matrix.md:1-86 | The verb x place matrix is a 2026-09-18 document: cells quote `sh-agent`, 260 files, `--vendor all`, the old repository name `styrman-harness.git`, 27 skills / 7 agents. The header discloses the rename but no cell was re-run under `fyr`; there are no rows for the state-dir migration (install vs status/diff/uninstall --dry-run on a home with only ~/.config/sh-agent), for `update --from-zip`, or for the D32 keep-the-old-~/.claude path, which are what Phase 4b/5b changed. The "Silent cells: None" claim is therefore a claim about the old binary. | fix: re-run the matrix with `fyr` and add the migration rows, or mark the file historical |
| IN6 | low | README.md:162 vs release-v270.txt | README names assets `fyr-linux-amd64`, `fyr-darwin-arm64`, `fyr-linux-arm64`; the newest release (v2.7.0) carries `sh-agent-*` and `styrman-harness-v2.7.0.zip`. Known (Phase 6 not done, first fyr tag is v3.0.0), recorded so the README is not read as true of a published release. | defer: Phase 6 |
| IN7 | low | cmd/fyr/main.go:1320 | `os.RemoveAll(dir)` of the unpacked zip: `dir` is the `MkdirTemp(parent, "zip-*")` result returned by `harness.UnpackZip`, never user text; parent is `~/.config/fyr` or, on a dry run with no state dir, the directory holding the zip (zip.go:~97). A dry run with no state dir therefore creates and removes a `zip-*` directory beside the operator's zip: not "writes nothing" but it is cleaned up (unless the removal fails: a warning only). | defer: document the beside-the-zip scratch in the usage text |
| IN8 | low | cmd/fyr/main.go:129-135 | `ctx` holds 27 fields, of which `dreamProject` and `metricsProject` are two `--project` flags registered by different verbs (fine: one per FlagSet), but `global` is declared twice (search and index) with the same help text. Not a defect; the shared flag is registered by two verbs, which the "one verb per flag" rule tolerates only because the text says "search, index". | strike or defer |

## Measurements

- Verb table: `verbs` map has 15 keys (version, uninstall, memory, dream, metrics, search, index, install, sync, status, diff, update, list, render, adopt) plus `help` / `-h` / `--help` in run(). Usage text covers all 15 (sync as an alias on the install line); README table (lines 40-51) covers all but `help`, with `sync` under install. No verb missing from either. Measured by reading main.go:44-74, :161-217 and README.md:40-51.
- Exported functions with no caller outside tests: none in cmd/fyr (package main, no exports). Not measured for internal/ (other dispatch).
- Duplicate helpers in cmd/fyr: `TestMain` is identical in cmd/fyr/testmain_test.go and testmain_test.go (root), 21 lines each, by necessity (two packages). `reorder` + `hasFlag` are two argument scanners; `hasFlag` is used only to give `--from-zip` on a wrong verb a better message.
- Errors always nil: none seen in main.go; `selfExecutable`, `runVersion` (returns nil by design).
- Writes/deletes in main.go, with guards: `os.RemoveAll(dir)` at :1320 (temp dir from UnpackZip, see IN7); all other filesystem mutation goes through `harness.Install`, `Uninstall`, `MkdirAllTracked`/`RemoveCreated`, `SweepStaleDirs`, `MigrateStateDir` (one `os.Rename`, refuses a symlinked old dir, leaves both-present alone), `dream.*Timer`, `metrics.*`, `memory.Add`, `search.*` — those are the internal half; `os.Lstat`/`os.Stat`/`os.ReadFile` in main.go are reads. main.go itself has one delete and no direct writes.
- Home confinement: `c.home` is made absolute (main.go:268) and every path in main.go is `filepath.Join(home, ...)`; `knownCheckout` refuses a discovered checkout other than the installed one. Whether Install/Uninstall follow links inside the home is the harness half's question (not examined).
- Tests: baseline test.txt exit 0; cover.txt cmd/fyr 90.5% of statements, root package no statements. Only `TestVersionVerb` calls `run()` without `--home` and it is a pure print; no cmd/fyr test touches the real HOME (the `t.Setenv("HOME", ...)` at main_test.go:1096, 1392, 1418 and metrics_test.go:74 point at temp dirs).
- Dependencies: git grep of Go imports outside the module and stdlib finds only `modernc.org/sqlite` (one import line). go.mod requires sqlite plus its indirect set; no `toolchain` directive (matches ADR-029). Which package imports sqlite was not checked in this half.
- Command: `git grep -hn '^\s*"[a-z0-9.]*\.[a-z]*/' -- '*.go'` (path-like strings in the output are test fixtures, not imports).

## Refuted

- "A read-only mode skips a guard the real run resolves": `update --check/--dry-run` runs the same dirty-tree refusal as the real run (main.go:1041, above the checkOnly return); `install --dry-run` resolves `knownCheckout` and `ResolveSource` as the real run does; `uninstall --dry-run` probes the timer and the legacy-timer notice as the real run. Not refuted for internal/harness.Install (other half).
- "The state-dir migration runs in a read-only verb": it does not. `writesState` (main.go:316-328) returns false for `--dry-run`, `update --check`, `status`, `diff`, `list`, `render`, `memory show`, `metrics show|report|export`, `version`, and those print `StateDirNotice` on stderr instead (main.go:333-341). `metrics report` cannot create `~/.config/fyr` ahead of a migration: `existingMetricsStore` Lstats the store first and exits 1 when it is missing, so a report cannot run before a migration has put the store where it expects it (a home with only the old dir gets "no metrics store").
- "`--from-zip` can fall through to a normal update": refused before the FlagSet for other verbs (:229), and for `update` an empty or flag-looking value, `--check` and `--repo` are exit 2 (:283-291); the decision is made on the parsed flag, not the raw args.
- "Refusals do not reach the caller's writer": every usage refusal in main.go that exits 2 prints to `out` (:230, :252, :286, :289, :411, :463, :479 ...). The exceptions are IN1 and IN2.
- "A release can ship an artifact its own verification did not check": not decidable from cmd/fyr; the zip path always hashes the bytes it unpacks and passes them to `VerifyZipSums` first (zip.go:60-66). The release publishing path is the harness / preflight half.

## Not examined

- cmd/fyr/*_test.go bodies beyond usage_test.go:1-80 and crossing_test.go:1-35, and the headers of the test files (counts only: main_test 1500 lines/34 top-level funcs); zip_test.go, migrate_test.go, delivery_test.go, memory/metrics/search tests not read.
- embed.go read (10 lines, the embed pinned both ways by embed_test.go:15,54 by name); embed_test.go, plan_test.go, statedir_test.go, init_project_test.go bodies not read.
- fyr-matrix.md lines 40-86 read truncated to 260 columns.
- README.md read only at the command table and the install sections' grep hits.
- The baseline outputs install.txt, status.txt, diff.txt, uninstall-dry.txt, gofmt.txt, vet.txt, govulncheck.txt were opened only to their first lines (status, uninstall) or not at all.
- All of internal/harness (Install, Uninstall, UnpackZip's link handling, UpdateFromRelease, Reinstall), internal/dream timer writes, which package imports sqlite, whether `fyr dream` with neither --all nor --project errors with exit 2.
- Behaviour was not run; nothing here is measured on a binary.
