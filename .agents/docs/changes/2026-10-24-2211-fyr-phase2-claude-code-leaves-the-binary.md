---
owner: main
date: 2026-10-24
---
# fyr Phase 2: Claude Code leaves the binary     (date: 2026-10-24)

## Summary
`sh-agent` installs into the Antigravity CLI only (ADR-062, plan `2026-10-24-fyr-agy-only.md` Phase 2, D7, D14, D17). `Plan` and `Install` lost their vendor argument. Nothing is planned or written under `~/.claude`, and the `CLAUDE.md` stub is gone. `--vendor` is gone from every verb, so passing it is a usage error, exit 2. `~/.claude/settings.json` is never read or written: `settings.go` is deleted, so the attribution and auto-compact keys an older install wrote stay as the operator's. An install over an older manifest sheds that manifest's `~/.claude` files through the existing resync, unchanged. A symlink, or a path under a linked directory, stays, and `~/.claude` itself stays in `pruneEmptyDirs`' stop map. `memory show` prints the index only. `dream` always runs `agy`, reads skills from `~/.gemini/config/skills` and reads transcripts from Antigravity's conversations only. `metrics ingest` reads Antigravity only. `show`, `report` and the dream summary have one `agy` column. A second column, `claude (historical)`, appears only when the window holds rows an older ingest stored (D7). Nothing deletes those rows, and `export` writes them as before.

## Type
task (plan phase)

## Files changed
from `git diff --numstat` (+1,172 −1,784 in the working tree, deleted files included)
- Deleted: `internal/harness/settings.go`, `internal/harness/settings_test.go`, `cmd/sh-agent/settings_test.go`, `internal/metrics/claude.go`, `internal/metrics/claude_test.go`
- `internal/harness/plan.go`: one target. `VendorAll`, `VendorClaude`, `NormalizeVendor`, `KeepForeign`, `IsOurs` and `ClaudeRoot` are gone. `VendorAntigravity` stays, so the manifest still records it.
- `internal/harness/apply.go`: `Install(src, home, files, dryRun, out)`. The foreign-file branches are gone. The stop map names `~/.claude` as a literal, with the reason beside it.
- `internal/harness/{render,state,adopt,inventory,frontmatter}.go`: the stub constant goes. The manifest's `Vendor` is written `antigravity`, and an old value is read and ignored. `adopt` no longer looks in `~/.claude/skills`. Comments changed.
- `cmd/sh-agent/main.go`: the `--vendor` flag, `resolveVendor`, `ctx.vendor`, the settings calls, the foreign-`CLAUDE.md` note and the `claude` status line are gone. `installPassArgs` forwards `--home` alone. Usage text changed.
- `internal/memory/memory.go`: `Show(dir)`. The native-memory hint and `nativeOn` are gone.
- `internal/dream/{dream,collect,run,metrics,timer}.go`: `command()` is agy-only, and the private HOME and the stream-json stdin are unconditional. `answer(stdout)`. The Claude transcript reader is gone. The summary counts sessions over every column.
- `internal/metrics`: `writer.go` is new and holds the writer helpers `agy.go` shares, moved verbatim from `claude.go`. `ingest.go` reads agy only. `show.go` has `Columns` and the `historical` column, with lazily made count maps. `numbers.go` and `report.go` follow the columns; the report's series and tables are built per summary, and Antigravity keeps colour slot 2. `store.go` and `agy.go` comments changed.
- `internal/search/dream.go`: one comment.
- Tests, new: `TestPlanAntigravityOnly`, `TestResyncShedsOldClaudeFiles`, `TestInstallLeavesClaudeCodeAlone`, `TestUpdateRefusesTheVendorFlagFirst` (BUG-171's rule, kept for the flag that no longer exists), `TestInstallPassArgsForwardsTheHomeOnly`, the six `--vendor agy` rows of `TestUsageErrorsExit2`, `TestShowHistoricalColumn`, `TestShowAgyOnly`, `TestReportAgyOnly`, `TestAgyIdentifiersAreNameShaped`, `TestIngestPrintsNoControlOrFormatCharacters`.
- Tests, moved onto Antigravity fixtures: `TestSourceLabelAndOutcome`, `TestOutcomeStampedAfterTheRun`, `TestMetricsVerb` (with a decoy `HOME`, for R6), every dream run test (`fakeAgy`), and the dream collect tests (`agyConversations`, whose schema both readers accept).
- Tests, historical: `historical_test.go`'s `historicalRows` inserts what an old ingest stored, for the show, report and numbers tests.
- Tests, deleted: the Claude transcript, settings, stub, foreign-`CLAUDE.md`, vendor-selection and native-memory-hint tests.
- Docs (the `documentation` agent): `README.md`, `specs/sh-agent/{metrics,memory-loop,dream}.md`, `deploy/{local,remote}.md`, `testing/guide.md`. `memory-loop.md` § Known differences is retired. Main agent: `AGENTS.md` lines 7, 39 and 44, and one `memory-loop.md` phrase.
- Plan: Phase 2's two work boxes are ticked. The checkpoint stays open (below).

## Deviations from the plan's box
- **The checkpoint's grep is met in part.** `git grep -il claude -- 'cmd' 'internal' '*.go'` lists 17 files. Expected among them: the stop-map entry (`apply.go`, with the resync comment of `Install`), the historical column (`main.go:65` and `internal/metrics/{ingest,report,show,store}.go` with their tests) and the stop-map tests. Not expected:
  - the Claude Code agent format: `derive.go`, `inventory.go` lines 23, 225 and 234, and `harness_test.go` lines 43, 155, 2351–2371 and 2439. Phase 3a deletes it (D3, D17).
  - the listing-budget comments: `catalog_test.go:671` and `size_test.go:90`. The 19,500-byte budget rests on Claude Code's listing; Antigravity's is about 44,000.
  - `internal/search/testdata/recall.tsv` rows 19 and 39 (D20).
  The box stays open until Phase 3a lands or the operator amends it.
- The "tests first" red was a build failure of the old tests against the new API in six packages, not a test-by-test red. Each removed symbol appears in `.agents/scratch/fyr/go-test-phase2-red.txt`.
- `State.Vendor` is still written, as `antigravity`, rather than dropped. `skills/init-project` and `skills/dream` grep for it until Phase 3b removes the check.
- `metrics.DefaultContextTarget` keeps 300,000. Its other half, the auto-compact window, went with `settings.go`, and so did `TestAutoCompactWindowIsTheContextTarget`.
- The `testing/guide.md` three-sandbox kit (§ 4, Claude Code runs) is left for Phase 3b. `memory-loop.md`'s `/init-project` rows are the skill's, also Phase 3b.

## Agents dispatched
- `documentation`: the README, specs, deploy and testing-guide edits. Its diff was read against the code by the main agent. One claim it kept ("Antigravity prompts … for paths outside the workspace") was already in the old text.
- The plan's Phase 5 holds the Definition of Done over the whole feature: the `verifier` log, `reviewer --code --docs` and `security`. This phase's code diff was read by the main agent only.

## Verification
- Full suite: `gofmt -l cmd internal` empty, `go vet ./...` ok, `go test -race -count=1 -timeout 30m ./...` exit 0, all 9 packages ok (`.agents/scratch/fyr/go-test-phase2.txt`). It ran on go1.24.4 (`~/.local/bin/go` still dangles). `govulncheck` reports the same 15 standard-library advisories as Phase 1, all fixed in later Go (`.agents/scratch/fyr/govulncheck-p2.txt`).
- Fresh scratch home (`.agents/scratch/fyr/home-p2`): `go run ./cmd/sh-agent install --home …` exit 0, 896 files, and no `.claude` directory. The installed binary's `status` exits 0 with `drift none — 895 files match the repo`, and the `binary` line has no warning. The `installed` line no longer prints a vendor.
- Crossing, local half (`.agents/scratch/fyr/home-p2-old`):
  - The `HEAD` build (from `git archive a60f83a`) installed 897 files under `.claude`, 896 of them in its manifest, and created `settings.json`.
  - This tree's install then exited 0 with `896 removed`, each reported as `remove  ~/.claude/… (no longer in the source)`.
  - `~/.claude` and `settings.json` stayed, untouched. The new manifest has vendor `antigravity` and no `.claude` entry, and `status` reports `drift none`.
- The usage rows prove that `--vendor agy` exits 2 with `flag provided but not defined: -vendor` on install, status, uninstall, dream, memory show, metrics ingest and update (checkout, dry run, zip). No checkout moved.

## Docs
Every file above. The map's node labels changed text only: no line or node was added, removed or rerouted (rule 3), and `TestHarnessMap` passes. The ADR is ADR-062.
