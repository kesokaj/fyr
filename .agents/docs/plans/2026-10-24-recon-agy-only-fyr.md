---
owner: explorer
date: 2026-10-24
---
# Recon — agy-only-fyr   (2026-10-24)

## Status: COMPLETE

## Question
Inventory for: (1) drop Claude Code, Antigravity (`agy`) only; (2) drop `sh-agent report` and all sending to `styrman-harness-feedback`; (3) rename to `fyr` (module `github.com/kesokaj/fyr`, binary `fyr`, state `~/.config/fyr/`).

## Summary
Claude Code is woven through ~22 non-test Go files (about 150 mentions) and ~25 test files (about 340 mentions). The vendor switch is concentrated in `internal/harness/plan.go` (VendorAll/Claude/Antigravity), `settings.go`, `cmd/sh-agent/main.go`, `internal/metrics/claude.go` + `ingest.go`, `internal/memory`, and `internal/dream` (collect/dream/run). `report` is isolated: `internal/harness/report.go` (1102 lines) + `report_test.go` (1584 lines, 28 tests) + `runReport`/`report()` in main.go, BUT report.go also holds `Redact`/`RedactJSONL`, which `internal/dream/collect.go:157,254` uses, so the redactor must be moved, not deleted. The rename touches ~490 Go `sh-agent` mentions in 60 files, ~6,200 in docs (mostly historical trail) and 125 in context/README/REQUIREMENTS. The hard part is not the sed but the bridge release: the old binary `update` path hard-codes the repo, asset name `sh-agent-<os>-<arch>` and a `# styrman-harness vX` SHA256SUMS marker (release.go:24,35,51).

## 1. Claude Code surfaces in Go (non-test mention counts)
| File | Count | What it does / agy-only disposition |
|---|---|---|
| internal/harness/plan.go | 30 | Consts `VendorAll/Claude/Antigravity` (21-23), `ParseVendor` (~31-35), `ClaudeRoot`; `Plan` builds each file for `wantClaude` (73) and `wantAgy` (74): `~/.claude/{AGENTS.md,CLAUDE.md stub (KeepForeign),skills,agents,commands,rules}` vs `~/.gemini/config/{rules,skills,agents/<n>/agent.md,workflows}`. DROP all `wantClaude` branches (lines ~87-185 region: 27-30, 82-83, 96-97, 112-113, 121-122), keep agy; `--vendor` flag and `VendorAll` collapse. `claudeStub` const goes. |
| internal/harness/derive.go (63) | 7 | `DeriveAgy`: Claude-format agent -> agy `agent.md`; `modelMap`, `toolMap`, rewrites `~/.claude/skills/` (claudeSkillsDir:25). Authoring source is Claude-format `agents/*.md` with `LoadAgents` allow-list (`agentKeys`, inventory.go; keys name, description, tools, model, maxTurns, skills, effort, color). Agy-only options: (a) keep the Claude-ish authoring format and derive (smallest change, rename comments only); (b) author `agent.md` directly: delete derive.go and `toolMap` lint, rewrite 8 agent files and `TestAgentDescriptionRules`/derive tests. Needs operator decision. |
| internal/harness/settings.go (272) | 13 | Writes `~/.claude/settings.json` owned keys (`OwnedSettings`: `attribution`, memory keys, `autoCompactWindow` 300000, ADR-060); `SettingSet`, `SettingsHint`, `ReadSettings`. Called from main.go ~830 and ~1018/1059 (install, status). DELETE whole file + `settings_test.go` (12) + `cmd/sh-agent/settings_test.go`. Consequence: the `attribution` off key (Git rule) has no agy equivalent here; check agy has none. |
| internal/harness/{inventory,render,apply,adopt,frontmatter}.go | 4/2/2/1/1 | Mostly comments/skill-link paths; apply.go + pathguard.go handle `~/.claude` links and sweep. Check `sweep.go`, `pathguard.go` (test has 7) for `.claude` root guards. |
| internal/harness/state.go | - | `state.json` Vendor field ("all"/"antigravity"; state.go:56 default `VendorAll`); init-project greps `"vendor": "antigravity"` (SKILL.md:29). With one vendor the field and the grep can go; old state files still carry `claude`/`all` (migration). |
| internal/metrics/claude.go (555) + claude_test.go (52 mentions) | 22 | Reads `~/.claude/projects` transcripts (`CLAUDE_CONFIG_DIR`). DELETE; `ingest.go` (13): `Options.Vendor` claude/agy/all (42-44), `claudeRoot` (68-82), `o.wants("claude")` (109). `show.go` (6), `store.go` (2), `report.go` (1) mention claude; `show_test.go` 36. Existing `metrics.db` rows with `cli=claude` are history: keep readable or drop? (open question). |
| internal/memory/memory.go | 12 | `Show(dir, claudeInstalled)` (202), `nativeOn` (232) reads `.claude/settings.local.json` and prints "Claude Code's native memory is still on" (224). DROP the hint and parameter; callers in main.go. R10 text goes with it. |
| internal/dream/{dream,collect,run,timer,metrics}.go | 3/10/2/1/1 | `command(vendor)` (dream.go:15-178) picks `claude -p --tools ""...` vs `agy --mode plan -p ""`; `collect.go:111` `claudeTranscript` (306-335), `.claude/skills` (273); `Options.Vendor`. DROP claude branches; keep agy (collect.go:274, dream.go:131,175). Tests: run_test 36, collect_test 30, metrics_test 16. |
| internal/search/dream.go | 1 | Antigravity DB reads only; comment. |
| cmd/sh-agent/main.go (1422) | 12 | `--vendor` handling (~895-960), install/status/uninstall branches at 830, 1018-1021, 1059, 1145; `ParseVendor` use at 909; usage text lines 14-69 name Claude Code (e.g. 49). |
| root: init_project_test.go (4), plan_test.go (1) | | pin init-project assets / plan. |
Test mention totals (claude): harness_test 65, claude_test 52, show_test 36, dream/run_test 36, collect_test 30, main_test 28, dream/metrics_test 16, source_test 13, settings_test 12, memory_test 10, others <10.

## 2. Report surfaces
Remove:
- internal/harness/report.go (1102): `FeedbackURL` (27, `git@github.com:kesokaj/styrman-harness-feedback.git`), `Collect` (386; collects feedback.jsonl, retros.jsonl, `proposals/` and `proposals/dream/harness/` 423-459), `BuildRecord` (505), `ResolveFeedback`, throwaway clone (`cloneThrowaway`, `sweepInterruptedClones` 595, `RemoveKeptClone` 699, `verifyFeedback` 800), `SendReport` (835), `writeAndPush`, `commitAndPush`, `pushError`.
- KEEP/move: `Redact` (321), `RedactJSONL` (330) and the needle machinery (`needleIn`, `redactLines`, `redactDocument`, `marker`, `blockEnd`, `decoded`, lines ~27-380); dream uses them (internal/dream/collect.go:157,254). Needles are pinned by `TestReportRedactShapes` in report_test.go (AGENTS.md rule on needle kinds, BUG-110). Also `Alive` (629) may be used elsewhere (check).
- internal/harness/report_test.go (1584 lines, 28 funcs): split; keep redaction tests, delete clone/push/Collect tests. `audit_test.go` mentions feedback repo.
- cmd/sh-agent/main.go: verb entry `"report"` (158-161, flags `--feedback-repo`), `runReport` (373), `report()` (852 SendReport), `uninstall` calls `RemoveKeptClone(home, FeedbackURL)` (1127), usage lines 14, 58, 225 (comment); `TestHarnessMap` in cmd/sh-agent/main_test.go:1399 (NOT the root package) requires install, update, report on the README map -> must change to drop report; delivery_test.go, usage_test.go, main_test.go mention report.
- State that report sent but others own: `~/.config/sh-agent/feedback.jsonl` (read by internal/dream/collect.go:232 and internal/metrics/events.go:257; written by `feedback-log` skill), `retros.jsonl`, `proposals/` (state.go:140-149), `reports/` dir is the metrics HTML (metrics report, unrelated; keep). Decide if feedback-log/retro `--harness` proposal queue still has a destination: with no `report` the harness-target proposals and `/retro --harness` (reads queue in the harness checkout) have no transport. `retro`, `feedback-log`, `distill`, `dream` skills, dream.md spec need rewording.
- Docs pinning it (not exhaustive, non-historical): README.md (map in § How it works line 5; mentions 84, 223), AGENTS.md rules 1,3,4,5 (rule 1 "one way back"), REQUIREMENTS R0/R7/R8/R9 (below), .agents/docs/specs/sh-agent/{delivery,dream,memory-loop,search}.md, deploy/{local,remote}.md (remote.md:63), skills/project-memory/SKILL.md, ADR-030/033/038 (append-only: supersede with a new ADR), BUG-053/055/056/060/094/110 (history). `styrman-harness-feedback` appears in 55 doc mentions/42 files, 4 in README/context/AGENTS/REQ files, 1 in Go.

## 3. Rename surfaces (git grep counts, mentions/files)
| Pattern | Go | skills (non-catalog) | context,agents,rules,README,AGENTS,REQ,ROADMAP | .agents/docs |
|---|---|---|---|---|
| `styrman` | 95/39 | 0 | 23/4 | 1084/501 |
| `styrman-harness` | 80/34 | 0 | 21/4 | 744/417 |
| `kesokaj/styrman-harness` | 49/30 | 0 | 3/1 | 240/126 |
| `styrman-harness-feedback` | 1/1 | 0 | 4/3 | 55/42 |
| `sh-agent` | 491/60 | 88/20 | 125/11 | 6187/1247 |
| `.config/sh-agent` | 54/21 | 27/7 | 24/4 | 789/353 |
| `sh-agent-noindex` | 20/4 | 1/1 | 1/1 | 52/28 |
| `STYRMAN_` | 0 | 0 | 0 | 20/11 (docs only) |
| `SH_AGENT_*` env | 55/11 | 1/1 | 1/1 | 205/113 |
Env vars in Go: `SH_AGENT_REPO` x40 (the repo path), `SH_AGENT_FAKE_MARKER` 6, `SH_AGENT_CORPUS` 3, `SH_AGENT_HELPER_ARGS` 3, `SH_AGENT_RECALL` 3, `SH_AGENT_CHILD` 1. No `STYRMAN_` in code.
Anchors:
- go.mod:1 `module github.com/kesokaj/styrman-harness`; root `embed.go` is package `styrman` (`//go:embed all:context all:skills all:agents all:commands all:rules`); imported as `styrman "github.com/kesokaj/styrman-harness"` at internal/harness/source.go:12 (alias line 43). 30 Go files import the module path. Dir `cmd/sh-agent` -> `cmd/fyr` (build paths in remote.md:131, install's `go build ./cmd/sh-agent`, pathhint).
- internal/harness/release.go: `ReleaseRepo = "github.com/kesokaj/styrman-harness"` (24), `sumsVersionMarker` regexp `# styrman-harness vX.Y.Z` (35), `AssetName` `sh-agent-<goos>-<goarch>` (49-51), uses `gh release view --repo` (58) and `gh release download`. repo.go:218 error message names clone URL; `repo.go` origin checks (repo_origin_test.go) use the repo identity. zip.go: zip prefix `styrman-harness-vX/` (remote.md:135).
- State dir `~/.config/sh-agent/` in 21 Go files (state.json, metrics.db store.go:58, dream-state.json, search.db, reports/, feedback.jsonl, retros.jsonl, proposals/). Also systemd units `sh-agent-update.service` (remote.md:80-83) and dream timer unit names (internal/dream/timer.go). `.sh-agent-noindex` marker (internal/search). Migration of existing state dir needed for current places (move vs fresh).
- Release/preflight: skills/preflight/SKILL.md (2 claude, plus sh-agent mentions), deploy/remote.md (§ Releases lines 108-184: tag message, 3 binaries, zip, SHA256SUMS, `gh release create/upload --repo`, ssh-keygen signing for the bridge at 165-172, which is historical).
- Test kits: `.agents/docs/testing/kit-preamble.sh` (14 claude mentions; the file is named kit-preamble, not sh-agent, but sources `sh-agent` paths), ab-kit/run.sh, agy-rel/run.sh, seed-security/run.sh, guide.md (16 claude), sh-agent-matrix.md (8 claude, filename carries `sh-agent`).

## 4. Skills, agents, context, rules mentioning Claude Code / `~/.claude` (grep -i claude)
init-project/SKILL.md 26 (stub list, Step 5 `.claude/*` symlinks, Step 5b settings.local.json memory keys, Step 6 gitignore `.claude/worktrees/`, `.claude/settings.local.json`, Antigravity-only branch Step 1 line 26-32: the whole "Claude Code or not" section collapses); review-standards/references/prompt-audit/prompt-audit.md 23; review/SKILL.md 9; dream/SKILL.md 5; project-memory/SKILL.md 3; handoff/SKILL.md 3; context/AGENTS.md 3 (lines 31 unattended `/goal` Claude Code vs `agy -p`; 39 global vs project `.claude/*` linking; 64 root layout CLAUDE.md stub); skills/VENDORED.md 2; preflight 2; feedback-log 2; 1 each: spec, retro, init-project/assets/AGENTS.md, harness-audit/references/areas.md, google-catalog, gke-basics, find-skills, feature, distill, bug; agents/verifier.md 1, reviewer.md 1, dreamer.md 1. rules/: no match (critical-thinking, session-sizing). Per file edits are small; the `# Compact instructions` clause (ADR-060/D25) in context and the `autoCompactWindow` text have no agy equivalent.
Note global ~/.claude/CLAUDE.md `@AGENTS.md` stub + the project `CLAUDE.md` stub are written by plan.go (`claudeStub`, KeepForeign) only.

## 5. REQUIREMENTS.md exact sentences (line numbers)
- :5 R0 title "the second brain of both CLIs"; :7 "`sh-agent` is the one place Claude Code and the Antigravity CLI keep what a session cannot hold on its own: ..." ; also "A CLI consults it at the moments the context names" (ADR-061).
- :9 R1 title "One source, two vendors"; :11 "`context/AGENTS.md` ... is the single global context, deployed to Claude Code and the Antigravity CLI. Subagents are authored once in Claude Code format under `agents/`; the Antigravity flavour is derived at install time."
- :13-15 R2 "A project has no `CLAUDE.md`: Claude Code 2.1.277+ reads `AGENTS.md` natively, and a one-line `@AGENTS.md` stub is written by hand only where it cannot. The init-project skill is the one home of that list ..." (whole stub list goes).
- :19 R3 names `sh-agent dream --all`, `--install-timer`, `--remove-timer`; :23 R4 "`sh-agent` is a single Go binary", `modernc.org/sqlite`; R4 also holds the `attribution` key write (see :68).
- :27 R5 "maps each directory to its place under both vendors". :35 R7 "written by `sh-agent memory` on both CLIs; ... never sent by `sh-agent report`"; "The harness's own state (manifest, feedback log, the harness proposal queue, ...) lives in `~/.config/sh-agent/`".
- :37 R8 title "One delivery channel, and it is `sh-agent`"; :39 "a release's `sh-agent` binary ... Experience flows back the one way too: `sh-agent report` sends a COPY of this machine's corrections, proposals and a metrics record to the private feedback repository. The originals stay in `~/.config/sh-agent/` ... (ADR-030, ADR-033, ADR-038.)"; :45 "a place either clones `styrman-harness` ... downloads a release's `sh-agent` binary"; :48 "**Report back:** each place runs `sh-agent report`, which sends a redacted copy ... to `styrman-harness-feedback` ..." (delete the bullet); :46-49 Update/Release bullets name `sh-agent update`, `SHA256SUMS`.
- :53 R10 title "One behaviour on every CLI (R-parity)"; :55 "Every harness feature is set up and runs the same on Claude Code, on Antigravity, and on a machine with both. ..." (whole R10 collapses or is deleted; memory-loop.md § Known differences cited).
- :59-62 Antigravity facts: "global rules are one 24,000-byte block", global rules path `~/.gemini/config/`, tool names cannot be enumerated, "Antigravity IDE is out of scope".
- Non-goals :66-69: ":68 Editing `settings.json` or any vendor setting ... R4's `attribution` key ... the two memory keys ... `.claude/settings.local.json` ... and, on Claude Code, the top-level `autoCompactWindow` key ... Antigravity has no equivalent (ADR-060)" -> delete the exceptions; :67 "Plugin or marketplace packaging on either vendor"; :69 "Symlinking the vendor directories..."; :66 hooks "on either vendor".
REQUIREMENTS is immutable without approval: propose exact edits -> ADR -> update with ROADMAP in one commit (needs a new ADR superseding ADR-030/033/038/047/060).

## Flows
- Install: main.go `install` -> `harness.LoadSource` (source.go: embedded `styrman.Files` or checkout) -> `Plan(vendor)` (plan.go) -> `Install` (apply.go, pathguard/sweep) -> for Claude: `SettingSet` per `OwnedSettings` + `SettingsHint`; state.json manifest (state.go). agy-only: keep `~/.gemini/config/*`, derive agents, 22,000-byte rendered-context pin (size_test.go) stays; Claude-specific `TestRenderedContextUnderTarget` 15,000 stays.
- Update: checkout = git fetch/pull --ff-only + rebuild + reinstall (repo.go); release binary = `UpdateFromRelease` (release.go:69) `gh release view --repo ReleaseRepo` -> asset `sh-agent-<os>-<arch>` + SHA256SUMS with `# styrman-harness vX` marker -> replace binary -> exec `<dest> install`; zip = `--from-zip` (zip.go).
- Report: `report` -> `Collect(home)` -> `BuildRecord` -> `SendReport` -> clone `styrman-harness-feedback` throwaway -> push; also invoked by uninstall cleanup (main.go:1127).
- Metrics ingest: `metrics ingest --vendor` -> `ingestClaude` (claude.go reads ~/.claude/projects) and `ingestAgy` (agy.go) -> store.go SQLite `~/.config/sh-agent/metrics.db`; events.go reads feedback.jsonl for corrections.
- Dream: `dream` -> `collect` (claude transcripts + agy via internal/search/dream.go DB reads) -> `command(vendor)` runs `claude -p` or `agy ... -p` -> proposals under `~/.config/sh-agent/proposals/dream/{harness,...}` (harness-target ones were what `report` sent).

## Conventions to match
Tests first, `go test -race`; stdlib-only Go (one SQLite dep); ADR for any map/path change landing in ONE commit with code, docs and README map (AGENTS.md rules 3-5); `TestHarnessMap` pins README map vs binary verbs; delivery claim has one home (delivery.md § The model); `changes/` entries append-only, ADRs append-only; docs trail is historical: do NOT rewrite old plans/changes/reviews/bugs on rename (about 6,000 of the 6,187 doc `sh-agent` hits live there); embed pinned both ways to git (embed_test.go); size pins (22,000 / 15,000 bytes) and skill listing budget 19,500.

## Risks
1. BUG-bridge: places on old binary run `sh-agent update` which asks `gh release view --repo github.com/kesokaj/styrman-harness`. If the GitHub repo is renamed, `gh` follows the redirect for API reads, but the old binary then looks for an asset named `sh-agent-<os>-<arch>` and a `# styrman-harness vX` marker in SHA256SUMS; a release carrying only `fyr-*` assets makes every old place fail the update (and "nothing replaced" is the safe failure). A bridge release must ship BOTH names (a `sh-agent-*` binary that is really the new fyr build + a SHA256SUMS with the old marker), then the new code must migrate `~/.config/sh-agent` -> `~/.config/fyr`, the PATH binary name, the systemd update/dream timer unit names, and the old `~/.claude` files it wrote (uninstall for claude). AGENTS.md says a release after a bridge release waits for the operator's word that every place crossed it (BUG-161).
2. `gh` redirect on rename does not cover `git@github.com:kesokaj/styrman-harness.git` checkout remotes forever in all cases (GitHub redirects git too until a new repo takes the old name; do not create a new `styrman-harness`). Checkout places' `repo_origin_test.go` identity checks may reject a renamed origin.
3. Module path change: 30 Go files import the path (embed.go package `styrman` alias at source.go:12); root-package tests (embed_test, plan_test, init_project_test) reference it. `go:embed` itself has no path dependency, mechanical.
4. report.go holds the shared redactor (dream uses it); deleting the file breaks `internal/dream` and its redaction pins.
5. Removing `report` leaves harness-target proposals, `retros.jsonl`, and `feedback-log`/`retro --harness` flows with no transport; `/retro --harness` and queue dirs (`~/.config/sh-agent/proposals/`) become dead or must be redefined. Also R9's "improve the harness from reports" loop disappears (needs operator design call).
6. Dropping Claude Code removes the harness's own development CLI: the repo is developed in Claude Code today (this session; the global CLAUDE.md is Claude's `~/.claude/CLAUDE.md` -> @AGENTS.md). After the change nothing deploys into `~/.claude`, so the operator's dev sessions would lose the harness unless they keep an old binary. The behavioural tests (ab-kit, seed-security, kit-preamble) run `claude -p` today; agy-only means agy runs only. The reviewer/verifier subagents here are Claude Code subagents.
7. Antigravity limits become binding: 24,000-byte global rules block (context pin 22,000 incl. inventory), no `autoCompactWindow`/compaction control (ADR-060 clause moves nowhere), no settings keys, no native memory switch (R10 memory hint), tool-name hang risk (toolMap), IDE unsupported. `.claude/` project symlinks (init-project Step 5) removed: project discovery for agy is `.agents/` natively.
8. Existing data: metrics.db rows `cli=claude`, state.json vendor values, `~/.claude/*` files left behind by old installs (uninstall sweep must still remove or the new binary must say so).
9. Mechanical rename of docs: sed over trail would falsify history; and `sh-agent` appears in skill text that models read (88 mentions in 20 skills + `~/.config/sh-agent` 27 in 7 skills) which must change in lock-step with the binary.
10. `sh-agent-matrix.md` and `kit-preamble.sh` assert fixtures (BUG-087..123); renaming silently breaks run kits unless the preamble greps are updated.

## Open questions
- Authoring format: keep Claude-format `agents/*.md` + derive, or switch to native `agent.md`? (affects derive.go, 8 agents, lint, REQUIREMENTS R1.)
- Bridge: how many places exist and are they all reachable to run one transitional `sh-agent update`? Ship dual-named assets for one release? Rename repo in place (redirect) or new repo?
- Does a `--home` / `--vendor` flag survive (agy only), or is vendor removed and state.json versioned for migration?
- Metrics: keep reading old claude rows? Keep `metrics ingest --vendor`?
- What replaces `report` as the way experience returns (nothing, manual, or `fyr export`)? Fate of `feedback.jsonl`, `retros.jsonl`, `/retro --harness`, `proposals/dream/harness`.
- Is the `attribution` off setting needed on agy (git commit trailers)? Not examined for agy settings.
- Whether `agy` supports commands/workflows dir names and `agent.md` keys unchanged (existing derive covers it today).
- Is `github.com/kesokaj/fyr` the same repo renamed or a new one (affects ReleaseRepo and old-binary behaviour)?

## Estimated change size
| Area | Files | Approx lines (delete+edit) |
|---|---|---|
| Delete Claude: plan.go, settings.go(+tests), derive comments, memory, main.go branches | ~10 + 5 tests | 1,200 (settings 272+tests ~300, plan ~80, main ~150, memory ~40, harness_test ~65 mentions) |
| Delete metrics/claude.go + ingest/show edits | 5 + 3 tests | 800 (claude.go 555 + claude_test ~600 + edits) |
| Dream: drop claude branch | 6 + 5 tests | 250 |
| Delete report (keep redactor move) | report.go, report_test.go, main.go, audit_test, TestHarnessMap | ~1,300 deleted + ~1,600 test lines (net after keeping redaction tests ~ -2,000) |
| Rename Go: module path, cmd dir, state dir, release asset/marker, env vars | ~60 Go files | ~700 mentions, mechanical, plus bridge logic ~100 new |
| Skills/agents/context/rules text | ~25 files | ~150 lines edited (init-project 26 mentions largest) |
| README/AGENTS.md/REQUIREMENTS/ROADMAP/delivery.md/deploy guides/specs | ~15 files | ~400 lines + 1 ADR (superseding 030/033/038/047/060) |
| Test kits (kit-preamble, ab-kit, agy-rel, seed-security, guide, matrix) | 7 | ~150 |
| Historical trail (plans, changes, reviews, bugs, testing) | ~1,200 files | untouched by design; add one change entry, ADR, handoff |

## Not examined
Per-line contents of pathguard.go/sweep.go/apply.go for `.claude` guards; internal/dream/timer.go unit names; search store paths beyond grep counts; skills' exact lines outside init-project/context; guide.md § 4/5 line detail; whether `Alive` in report.go is used outside report; the other agy behaviours (settings equivalents); ADR-030/033/038 text; internal/search marker behaviour; GitHub-side facts (redirect semantics are from general knowledge, not tested).
