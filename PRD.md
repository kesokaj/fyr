# PRD — fyr

What `fyr` and the harness are for, who they serve, and what must never drift.
Status: current · Owner: the operator · Last updated: 2026-11-27 · Design: [SDD.md](SDD.md) · Technical design: [TDD.md](TDD.md)

> Immutable without the operator's explicit approval. Agents never edit this file.

## Problem

`agy` sessions start stateless inside one 24,000-byte rules block (Known limits); without a harness, rules repeat each session, decisions vanish, and files drift across machines.

## Users

- **Operator:** Go/TS (or custom `AGENTS.md`) engineer on Linux/macOS (`Apple Silicon`) deciding architecture.
- **Sessions:** `agy` CLI and subagents reading context, loading skills, and keeping the trail.

## Goals

| # | Goal | Metric | Baseline | Target |
|---|---|---|---|---|
| G1 | Global context stays small | hand-written bytes (`TestRenderedContextUnderTarget`) | 14,876 (2026-10-29) | ≤ 15,000 |
| G2 | Repo project rules stay small | `wc -c AGENTS.md` | 2,408 (2026-11-27) | ≤ 4,500 |
| G3 | Docs agree with binary | `fyr check` findings on `main` | 2 (2026-10-29) | 0 at release |

## Non-goals

- **Daemons, proxies, commit gates, CI:** enforced by reading and `< 250 ms` fail-open hooks (`~/.gemini/config/hooks.json`, R3; ADR-511, ADR-522).
- **Plugin/marketplace packaging:** namespaces rename skills and subagents.
- **Real-home `settings.json` edits:** deploys `~/.gemini/config/` only; writes `settings.json` solely in isolated eval/sandbox/dream homes (`~/.config/fyr/{eval,sandbox}/<project>/`, `~/sandbox/fyr-auto/`, `~/.config/fyr/dream-agy-home-<pid>/`), never real `$HOME` (ADR-062, ADR-065, ADR-073, ADR-090, ADR-503, ADR-510, ADR-511, ADR-522).
- **Symlinking customization dirs:** copies avoid dangling symlinks and work on binary places.

## Must — non-negotiable

<!-- Numbered R0…; struck items stay as "R3 — struck <date>: <why>". -->

Traced in [SDD.md](SDD.md) (R → M) and [TDD.md](TDD.md) (M → tests).

- **R0** — Purpose: the second brain of the Antigravity CLI.
  <!-- provenance: the operator, 2026-10-24; enforced by: TestHarnessMap, TestAddGetEditList, TestSearchRanksARecordAboveAFile, TestMemoryVerb, TestRunIngestsMetricsFirst, TestRenderedContextUnderTarget, TestWorkerModelRunCountAndCap, TestMemoryPartsAndGlobalScope, TestComputeMemoryValidity, TestDreamingOnlyClassSet, TestPreInvocationProgressiveMemory, TestEvalGate, TestPreInvocationEmitsImprovedPromptAndSkillDigests, TestDecomposeSubtasksClausesAndPackages, TestListAgyWorkspacesAndWindow, TestSessionRatingsMigrationUpsertQueryAndReports, TestAllConversationsRatedWithoutRegistration, TestPreInvocationWarmLatencyUnder40ms, TestStateExportAndImportEndToEnd, TestVerbModelsBenchAndScoreBreakdownCLI, TestRunWorkerDraftsOnlyAllowsDirtyNonGitAndGlobal, TestEffectiveContextBudgetAndKnee -->

  Second brain and build controller for `agy`, with no custom interactive runner (`relay` retired, `ADR-537` D2; parallel work uses `invoke_subagent`, `run_workflow`, `swarm`). Deploys harness/hooks (`install`), trail/memory (`record`, `search`, `memory`), shapes (`metrics`, `report`), routing (`models`), and build control (`verify`, `gate`, `land`, `close`, `watch`, `check`) under context pins; `fyr search` splits into `search`, `hook`, `ast`, `vet` (`v9.x` shims drop in `v10.0.0`, `ADR-537` D1). Opt-in `fyr auto` (R3) handles isolated self-improvement; no daemon/proxy acts on sessions. (ADR-061, ADR-062, ADR-063, ADR-073, ADR-090, ADR-511, ADR-522, ADR-523, ADR-524, ADR-525, ADR-526, ADR-537.)

- **R1** — One source, one vendor.
  <!-- provenance: the operator, 2026-10-24; enforced by: TestAgentsInstallAsWritten, TestRenderedContextFitsAntigravityBlock, TestEmbedHoldsOnlyTrackedFiles, TestSkillFrontmatterDescriptionsUnder140Chars, TestFrontendSlateUITrimmedAllowlist -->

  `context/AGENTS.md` (+ `context/*.md`) is the sole global context, deployed only to `agy`. Subagents (`agents/<name>/agent.md`) install unchanged; nothing generated commits. (ADR-062.)

- **R2** — Standards, not vendor formats.
  <!-- provenance: the operator, 2026-09-03; enforced by: TestInitProjectRootDocs, TestSkillDirWithoutSkillMdIsSkipped, TestLintAllowListPerKind -->

  Context follows agents.md (`AGENTS.md` at every level; no vendor copy or source-side template). Skills follow Agent Skills `SKILL.md` and drop into `skills/` unchanged.

- **R3** — No daemons, no gates, no CI; two exceptions.
  <!-- provenance: the operator, 2026-09-03; enforced by: TestStoreRunsNoProjectHook, TestHookHardDeadlineFallsBackToAllow, TestUnrangedReadOf600LineFileDeniedWithSkeletonAndRangedRetryAllowed, TestReadLedgerPersistenceEditAndExternalCheckout, TestVerifyHappyAndMustFail, TestGateChecksAndMustFail, TestInstallTimerLinux, TestDreamTimerDryRun, TestPromptCarriesMetricsNumbersOnly, TestAutoOnWritesTheConfigAndTheTimer, TestAutoOffNeedsNoLock, TestAutoOffReplacesCorruptAndLink, TestAutoRunOffIsOneLine, TestAutoRunCorruptFailsClosed, TestAutoConfigCorrupt, TestAutoTimerSystemdText, TestAutoTimerPlistText, TestGateRereadBeforeEachProject, TestGateMachineStopsARun, TestGateCleanStops, TestUsageFloors, TestUsageRefusals, TestAutoRunHeldLockStops, TestReportStaysOutOfTheProject, TestWorkerModelRunCountAndCap, TestWorkerConfigTamperDisablesAuto, TestWorkerStateTamperRestoredAndDraftsCollected, TestApplyHappyPath, TestApplyMovedCheckoutKeepsBranch, TestApplyRedMarkedTestKeepsBranch, TestApplyNoMarkedTestBecomesProposal, TestApplyHooksAndGPGSignOff, TestApplyNoPushFetchOrLsRemoteInGitLog, TestApplyRubricDeniedPathOnBranchKeepsBranch, TestApplyStep1RereadsGatesAndDraftFindings, TestProvenanceMigrationSetsUnknown, TestProvenanceMemoryAdd, TestProvenanceMemoryDraftActions, TestProvenanceFeedbackAndRecordAdd, TestMemoryProvenanceFlag, TestDraftsMemoryAddRetireMerge, TestDraftsRetireOrMergeOtherProjectRefused, TestDraftsTokenShapeRefused, TestDraftsFeedbackProposalFindingAndLimits, TestEvalGate, TestPreToolUseWorkerJITCapsuleOverwrite, TestPreToolWriteCoordinatorGuard, TestAutoOnBinarySwitchAndAllProjectsSweep, TestHooksJSONIncludesPostToolUse, TestWatchdogCircuitBreaker, TestGateJudgeCrossFamily, TestReportAccuracyFixes, TestVetHistoryV11TimeoutApprovalDedupAndKneeDedup, TestRunHookPreInvocationPerModelKnee, TestTasksSelfReferentialLogFilterAndGlobalMemories, TestWorkerCooldownAndForceOverride, TestSearchHookRecordsFTSAndStopwords -->

  Behaviour comes from context, skills, and subagents — no daemons, proxies, gates, or CI; git is backup/versioning only. Two exceptions:
  - **1 — `fyr auto` (hourly OS timer):** `on` installs, `off`/`uninstall` removes (absorbs dream timer); gated by `~/.config/fyr/auto.json` (missing/corrupt = off; `on`/`off` only write; `off` lockless); ingests metrics first (R4).
    - *Dream (`fyr dream`):* sweeps non-sandbox workspaces/scopes/harness for conversations idle `>= 1h` and changed; redacts all inputs and number/shape digests before tool-free plan mode (never `--dangerously-skip-permissions`) in operator home when unreviewed tools are off (`/resume`-visible), else private `~/.config/fyr/dream-agy-home-<pid>/`; writes only `metrics.db` ratings and global `brain.db` proposals.
    - *Workers (R6, R8, R10):* re-reads `auto.json` per workspace and before apply; needs AC + pool quota (`>= 50%` 5h, `>= 30%` week; unreadable = under); max 4/day, 30m (tamper resets off). Scoped to `skills/`, distilled skills, skill-search asks, and derived memory (never consumer code/docs). On clean non-ahead harness checkouts, `fyr` (never model) squashes `work/auto-*` into one revertible commit after `# auto: test`, `fyr check`, and eval gate (`<= 24` sessions in `~/sandbox/fyr-auto/`); other places/classes stay branch/sandbox/proposal. Never pushes, installs third-party skills/MCP/deps, or lands denied paths (`PRD.md`, `AGENTS.md`, `context/`, `rules/`, `agents/`, Go tests/fixtures). Go scores derived memory/feedback; rules need approval, skills need approval or eval gate. (ADR-047a, ADR-062, ADR-068, ADR-073, ADR-089, ADR-089a, ADR-090, ADR-522, ADR-523, ADR-524, ADR-533, ADR-534.)
  - **2 — CLI hooks (`~/.gemini/config/hooks.json`):** 5 events (`SessionStart`, `PreInvocation`, `PreToolUse`, `PostToolUse`, `Stop`), one-shot (`ADR-537` D1 moves `fyr search --hook` -> `hook` + `v9.x` shims); `< 250 ms` fail-open (`~200 ms` -> `allow`/exit 0, 1-line banner, no `go`/`git`/network; bypass `~/.config/fyr/no-hooks` or `.agents/scratch/no-hooks`). Capsules, vetting, skill digests, subtasks, re-priming, skeleton clamp (no arg rewrite), hash dedup, routing, coordinator/stdin/loop/budget guards, stamp-only `Stop` nudge (no tests); `watch`/`gate` judging run one-shot. (ADR-511, ADR-520, ADR-521, ADR-522, ADR-525, ADR-526, ADR-537.)

- **R4** — One binary, one dependency, easy on and off.
  <!-- provenance: the operator, 2026-09-29; enforced by: go.mod, TestInstallStatusResyncUninstall, TestUninstallKeepsMetricsStore, TestUpdateNeverInstallsDirtyTree, TestUninstallRemovesAutoTimerKeepsItsFiles, TestMigrationTenToEleven, TestMigrationThreeToFourAndReadOnlyV3, TestStateRealHomeGuardRefusesWithoutHome -->

  Single Go binary; sole dep pure-Go `modernc.org/sqlite` (search, metrics, brain, RO dream; pinned, unvendored); stdlib Landlock (444–446)/`sandbox-exec`; new deps/cgo need PRD change. Search DBs (`.agents/scratch/search.db`, `~/.config/fyr/search.db`) are rebuildable caches; `metrics.db` (shapes/hashes/timings/judge metadata — no args/prose; keeps pre-ADR-062 history) and `brain.db` (trail, memory, feedback, proposals, SIM) migrate forward and survive `uninstall`; `.agents/trail.jsonl` syncs on demand. `install` is idempotent, managed-only, no real-home `settings.json`; `uninstall` removes manifest + timers; `update` refuses dirty trees. (ADR-045, ADR-047a, ADR-062, ADR-091, ADR-511, ADR-522, ADR-526.)

- **R5** — Hand-added customizations are first class.
  <!-- provenance: the operator, 2026-09-03; enforced by: TestInstallStatusResyncUninstall, TestAdopt, TestLintAllowListPerKind -->

  Drop files in `skills/`, `agents/`, `commands/`, or `rules/` (hand, `npx skills add`, `fyr adopt`) + `fyr install` — zero registration, manifest, or code edits.

- **R6** — Development never touches the real home.
  <!-- provenance: the operator, 2026-09-03; enforced by: TestEvalRefusesWithoutHome, TestEvalRefusesItsHome, AGENTS.md -->

  Tests/dev writes pass scratch `--home`. `fyr eval` needs `--home` outside real `$HOME`/checkouts and fail-closed `isolate` confinement (Landlock/`sandbox-exec`, `EACCES` outside). Real `$HOME` deploys are operator-only unless `DEPLOY-local` § Operator steps grants checkpoint permission from a clean pushed checkout. (ADR-078, ADR-505, ADR-522.)

- **R7** — Projects remember and stay resumable.
  <!-- provenance: the operator, 2026-09-03; enforced by: TestAddGetEditList, TestExportByteStable, TestLoadReplacesRowsFromAChangedFile, TestEveryUnexportedKindStaysOut, TestStateDirHasOneHome, TestReportStaysOutOfTheProject, TestMergeMemoryBodyCompactionAndBounds, TestStateCLIRoundTripMemoryOptInDryRunIdempotentAndOperatorProvenance, TestImportRejectsInvalidArchivesBeforeOpeningDB -->

  Projects keep gitignored `.agents/scratch/`, tracked root docs (`AGENTS.md`, `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`), and non-deleted `brain.db` trail (close writes one handoff with verify digest; old `change`/`testing` stay). Direct DB access keeps checkouts clean; `.agents/trail.jsonl` syncs on demand. Validity-scored memory (`semantic`/`procedural`/`episodic`, project/`--global`, `< 0.40` hidden) and global SIM stay out of trail exports (zips need `--include-memory`). State lives in `~/.config/fyr/`; `auto` reports stay local (R3). (ADR-015, ADR-063, ADR-091, ADR-522, ADR-523, ADR-530, ADR-531, ADR-533.)

- **R8** — One delivery channel, and it is `fyr`.
  <!-- provenance: the operator, 2026-09-17; enforced by: TestHarnessMap, TestGlobalOnlyKinds, TestEveryUnexportedKindStaysOut, TestUpdateFromZipVerifiedAndTampered, TestDeliverySpecCurrent, TestKickoffBuilder, TestKickoffRubricAndAsks, TestRunnerChoice, TestWorkerBranchCleanup, TestIsolateLandlockDeniesOutsideWrite, TestSeatbeltProfileRender, TestTasksKickoffTemplates, TestTasksTriggers, TestTasksRedactedLearningInput, TestMemoryPartsAndGlobalScope, TestComputeMemoryValidity, TestDreamingOnlyClassSet, TestPreInvocationProgressiveMemory, TestEvalGate, TestStateUsageErrorsExit2, TestStateCLIRejectsCorruptZipSlipAndFutureSchema -->

  Three channels only — checkout, embedded binary, or zip (`fyr update --from-zip <file>`) — updated via `fyr update`; no consumer vendoring, copy, service, or dependency. `feedback`, `proposal`, SIM are global-only (`record` refuses project `feedback`/`proposal`); memory/SIM never export to trail (R7). Four outbound flows: (1) redacted dream prompt (R3); (2) isolated `fyr eval` (unredacted sandbox); (3) isolated `fyr auto` workers (unredacted sandbox + redacted inputs); (4) unredacted missing-skill queries (skills.sh/npm). Workers require fail-closed `isolate` (R6) + tripwires (OS confines filesystem, not network; `command(*)` retains outbound network governed by design, ADR-065); experience returns only via operator or eval-gated `fyr auto` commits (R9). (ADR-030, ADR-033, ADR-038, ADR-062, ADR-090, ADR-515, ADR-522, ADR-523, ADR-524, ADR-529, ADR-530, ADR-531, ADR-533.)

- **R9** — Three procedures, set in stone.
  <!-- provenance: the operator, 2026-10-24; enforced by: TestHarnessMap, TestUpdateNeverInstallsDirtyTree, TestUpdateFromReleaseReplacesAndInstalls, TestUpdateFromZipVerifiedAndTampered, skills/fyr-preflight/SKILL.md, TestEvalGate -->

  Three procedures only (`README.md` § How it works): **Install** (clone or binary via Releases/`gh release download`/`go install` + `fyr install` -> `~/.gemini/config/`, no repo); **Update** (operator pushes `main` or `fyr auto` commits eval-gated `skills/`; checkout `fyr update` refuses dirty trees, ff's, rebuilds, reinstalls; binary checks `SHA256SUMS` over HTTPS (integrity, not authenticity — trust rests on GitHub's HTTPS release channel and the maintainer's account), replaces binary, reinstalls; zip `fyr update --from-zip <file>` blocks traversal, checks adjacent `SHA256SUMS` else `UNVERIFIED`, installs via running binary, deletes temp dir; nothing pushes); **Release** (`/fyr-preflight vX.Y.Z`, operator turn: parallel review, verify, changed-row `fyr eval`, tag `main`, publish binaries, tree zip, unsigned `SHA256SUMS`). Changes need approval, ADR, and one commit for code, docs, map. (ADR-031, ADR-033, ADR-038, ADR-039, ADR-040, ADR-041, ADR-053, ADR-062, ADR-090, ADR-515, ADR-522, ADR-523, ADR-527.)

- **R10** — One behaviour on every place.
  <!-- provenance: the operator, 2026-10-24; enforced by: TestTimerMacOS, TestAgySessionMac, TestAgySessionLinux, TestRunnerChoice, TestAutoOnWritesTheConfigAndTheTimer, TestAutoTimerPlistText, TestIsolateLandlockDeniesOutsideWrite, TestSeatbeltProfileRender, TestNormalizeRepoIDParityAndSecrets, TestFitChunksOversizedTranscriptAndDropsTinyRoom -->

  Identical across all three channels (R8) and Linux/macOS, including fail-closed confinement (R6, R8); checkouts land `skills/` commits while binary/zip places queue proposals (R3, R8). Runtime limits live in Known limits. (ADR-047, ADR-062, ADR-090, ADR-522, ADR-524, ADR-534.)

## Should — expected unless a decision says otherwise

None: every requirement here is a Must.

## Could — welcome when cheap

None.

## Known limits

- **24,000-byte rules block:** `agy` concatenates `~/.gemini/config/rules/*.md` into one block and drops overflow (BUG-085, ADR-051); tests pin context + rules `<= 19,500 B`.
- **Global root:** `~/.gemini/config/` is Linux/macOS verified, not vendor-guaranteed.
- **Unenumerable tools:** unknown `tools:` hang subagents; `fyr install` fails closed.
- **IDE out of scope:** `agy` CLI only.
- **OS isolation:** `agy --sandbox` confines terminal only; `fyr eval`/`auto` require Landlock (5.13+, 444–446) or macOS `sandbox-exec`, failing closed without Docker/host fallback (ADR-522).
- **Compaction:** CLI-controlled; `rules/session-sizing.md` (ADR-058) governs checkpoints.

## Open questions

- ~~`fyr check` R → M → T trace verification (PLAN-2026-10-29-root-docs-and-fyr-serve row 7).~~ Answered 2026-10-29: yes (D40), built row 10 (D54).
