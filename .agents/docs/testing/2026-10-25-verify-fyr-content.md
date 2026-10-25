---
owner: main
date: 2026-10-25
---
# Verify — fyr Phase 3b: skills, kits and the remaining content on Antigravity   (2026-10-25)

Range reviewed: 9174cca..the Phase 3b commit that carries this log. The rows installed the uncommitted Phase 3b tree. Plan `2026-10-24-fyr-agy-only.md` Phase 3b, D25, D26.

## Status: FAIL

Every row ran and is given as n of N. One row fails on its last attempt: `project-memory`, 0 of 2. Both failures are BUG-175, a pre-existing skill line that the grant store refuses on agy 1.4.7; Phase 3b did not cause it, and the fix waits for the operator. Every other row passes on the final tree. The first pass showed that BUG-174's recorded cause was wrong. The denial is `git log @{u}..HEAD` itself, not chaining. After the operator's D26 fix, the canary and all eight dispatch rows pass.

## Setup
- **Kits** (each a scratch copy of `testing/agy-rel/run.sh`, which sources `testing/kit-preamble.sh`):
  - `.agents/scratch/fyr/kit3b/run.sh`: Phase 3a's canary and four agent rows, plus `init-bare`, `project-memory` (two sessions), `feedback` and `project-context`.
  - `.agents/scratch/fyr/kit3b-probe/run.sh`: adds `probe-upstream`.
  - `.agents/scratch/fyr/kit3c/run.sh`: the re-runs after D26. It adds `command(govulncheck)` to the allow-list (D26, also in the committed kit), gives `feedback` the matrix's `wide` grant, and extends the canary check to the new command.
  - Per-row logs are in `logs/` beside each kit.
- **Fixture:** `~/sandbox/agy-rel/fixture` (`9741514`, the scaffolded demo service). Each row gets a fresh clone and its own home under `~/sandbox/agy-rel/{runs,homes}/<row>-3b|3c`, installed from this tree.
- **Credentials:** the agy token is linked, then removed by name; the CLI starts from `env -i` with an allowlist. Grants follow `skill-matrix.md` § Procedure. `command(*)` is used only on rows whose skill text runs compound commands (`init-bare`, `review`, `preflight`, and `feedback` in kit3c), and each such row's log line says so.
- **agy 1.4.7.** Phase 3a ran on 1.4.6.
- **A row counts only with all of these:** PASS from the kit, which means zero `auto-denied` lines and no `invalid allow entry`; the preamble's three lines; and, for an agent row, `"TypeName":"<agent>"` in the row home's conversation databases. All 30 attempts printed the preamble's three clean lines: `no processes left by the run`, `real config unchanged` and `no credentials left in KIT_HOME`.
- **Trees:**
  - kit3b rows installed the tree with D25's wording, `git log @{u}..HEAD` run separately.
  - kit3c rows installed the final tree: `git log --branches --not --remotes --oneline`, plus the `command(govulncheck)` grant.
  - Between the two, the content changed in that one context line only. `init-bare`, `handoff` and `project-context` ran on the kit3b tree only; none of them dispatches an agent, so none reaches that line.

## Rows

| Row | Covers | Result (n of N) | Evidence |
|---|---|---|---|
| `canary` | the context, both rules, BUG-174/D26 | **PASS 2/2** | Both: `GANNET`; the print-mode sentence quoted; eight subagents, with `changelog` (flash) and `security` (pro); no `claude` in the reply. kit3b quoted "…as three separate commands, never joined on one line"; kit3c also checks `--not --remotes` in the quote. |
| `dispatch-bugs` | `bugs`, BUG-174 | **1/2** | kit3b: dispatched; BUG-001 and its INDEX row written; then the main agent's separate `git log @{u}..HEAD` was denied (step 31). kit3c: PASS (71 s). |
| `dispatch-changelog` | `changelog`, BUG-174 | **1/2** | kit3b: the entry was written, then `git log @{u}..HEAD` was denied (step 42). kit3c: PASS (124 s). |
| `dispatch-documentation` | `documentation`, BUG-174 | **1/2** | kit3b: `local.md` names PORT, then `git log @{u}..HEAD` was denied (step 33: `permission check failed for unsandboxed "git log @{u}..HEAD"`). Its chained `git log -n 5 --stat && git status …` ran (steps 5–6). kit3c: PASS (168 s). |
| `agents-security` | `security` | **1/2** | kit3b: findings written, no `PWNED*`; but the subagent's `govulncheck ./...` was denied (no grant), and so was the main agent's `@{u}` line. kit3c: PASS (151 s), with the grant. |
| `agent-reviewer` | `reviewer` | **1/2** | kit3b: report written; `git log @{u}..HEAD 2>&1 \|\| true` denied (step 24). kit3c: PASS (147 s). |
| `agent-explorer` | `explorer` | **1/2** | kit3b: brief written; `@{u}` denied (step 16). kit3c: PASS (107 s). |
| `agent-verifier` | `verifier`, its body edit | **1/2** | kit3b: the subagent chained `pwd && git status … && go version && uname -sr` (against its step 4), denied for `uname` (no grant), and the main agent's `@{u}` was denied too. kit3c: PASS (145 s); the log carries `counts:` and `vs previous:`. |
| `agent-dreamer` | `dreamer`, its body edit | **1/2** | kit3b: artifact written; `@{u}` denied (step 32). kit3c: PASS (114 s). |
| `init-bare` | `init-project` (F7: no `.claude/`) | **PASS 1/1** | On a bare service commit: `AGENTS.md`, `.agents/docs/deploy/local.md`, and `.agents/memory` at mode 700 with its `.gitignore` line. No `.claude`, no `CLAUDE.md`, no `claude` line in `.gitignore`. Commit `init: scaffold agent tree and project docs`; clean tree (153 s). |
| `handoff` | `handoff` | **PASS 1/1** | `handoffs/2026-10-25-0758-request-logging-middleware.md` written, with a Kickoff block (85 s). |
| `review` | `review` | **PASS 2/2** | `reviews/2026-10-25-review-worktree-code.md` both times (99 s, 144 s). kit3c dispatched via `invoke_subagent` (`TypeName` 6). |
| `preflight` | `preflight` dry path | **PASS 2/2** | Both: reviewer and security dispatched (`TypeName` 18 in kit3c); verdict `NO-SHIP` with the report `reviews/2026-10-25-preflight-9741514.md`; nothing pushed (no remote) (312 s, 311 s). |
| `feedback` | `feedback-log` rule 7 | **1/2** | kit3b: the skill's own logging script (multi-line, `$(date -Iseconds)`, `jq`) was denied under named grants; nothing logged. kit3c, with `command(*)` as the matrix procedure gives a skill whose text runs compound commands: PASS (94 s); `feedback.jsonl` names `slog`, and a memory was saved through `sh-agent memory add`. |
| `project-memory` | `project-memory` | **0/2** | Both: session 1's `TODAY=$(date +%F) && sh-agent memory add …` was denied, and no store was written. Session 2 answered "Friday" from elsewhere, so the recall proves nothing. BUG-175, open; the skill line predates this phase. |
| `project-context` | D18: a project's root `AGENTS.md` and a `.agents/skills/` skill | **PASS 1/1** | The reply was `HERON` (with no tool call) and `KESTREL`. The store holds `RULE[/home/kesokaj/sandbox/agy-rel/runs/project-context-3b/app/AGENTS.md]` with the canary line. The only tool call was a `read_file` of `.agents/skills/heron-check/SKILL.md`, a path known from context with no search first. Antigravity loads both natively, 1/1 (11 s). |
| `probe-upstream` | the BUG-174 diagnosis | n/a (a probe) | Each run as its own call: `git log --branches --not --remotes --oneline` RAN, `git status -sb` RAN, `git log origin/HEAD..HEAD --oneline` RAN, `git log @{u}..HEAD` DENIED. |

Totals:
- 30 attempts across 16 rows, plus the probe.
- kit3c, the final tree: 12 of 13 PASS.
- All eight dispatch rows on kit3c ran `git log --branches --not --remotes` (12–16 store mentions each) and no `@{u}`.

## E2E criterion 3

`git grep -il 'claude' -- ':!.agents/docs/changes' ':!.agents/docs/handoffs' ':!.agents/docs/reviews' ':!.agents/docs/bugs' ':!.agents/docs/testing/20*' ':!.agents/docs/plans' ':!.agents/docs/architecture' ':!.agents/docs/retros' ':!skills/google-catalog/catalog'` lists 23 files. Each is an exemption:

**D20: vendored verbatim, and their rows**
- `skills/find-skills/SKILL.md`, `skills/gke-basics/SKILL.md`, `skills/review-standards/references/prompt-audit/prompt-audit.md`: never edited in place (project `AGENTS.md`).
- `skills/VENDORED.md:17`: the prompt-audit row's upstream path (`skills/claude-api/shared/…`).

**D20: migration code and tests**
- `internal/harness/apply.go`: the resync comment and `pruneEmptyDirs`' `~/.claude` stop.
- `internal/harness/harness_test.go` (187–396) and `cmd/sh-agent/main_test.go` (334–374): the stop-map and resync tests (`TestInstallLeavesClaudeCodeAlone`, `TestResyncShedsOldClaudeFiles`).

**D20/D7: historical-metrics code, tests and spec**
- `cmd/sh-agent/main.go:65`: `metrics show`'s usage, which names the `claude (historical)` column.
- `internal/metrics/{ingest,report,show,store}.go` and `{historical,numbers,report,show,store}_test.go`.
- `.agents/docs/specs/sh-agent/metrics.md`: 28 lines, every one about the historical rows, their column, vocabulary or chart colour (the documentation agent's list).

**D20: REQUIREMENTS.md's history clause**
- `REQUIREMENTS.md:23`: "including the Claude Code sessions ingested before ADR-062, kept as history".

**D24: the listing budget, until Antigravity's listing is measured**
- `internal/harness/size_test.go:90`, `internal/harness/catalog_test.go:671`.
- `AGENTS.md:67`, the same budget's reason ("past Claude Code's budget a skill is a bare name nothing triggers"). D24 names the two comments, and this is their restatement in the project rules. It is kept for the same reason and revisited with them, not reworded to pass the grep.

**A guard**
- `init_project_test.go`: `TestInitProjectAndDreamNameOneCLI` names `.claude`, `CLAUDE.md` and "Claude Code" in order to refuse them (Phase 1's `TestHarnessMap` precedent).

Not exempt and edited:
- `internal/search/testdata/recall.tsv` row 39, now "install edits no vendor setting", with the same five gold paths, which the test stats.
- `.agents/docs/deploy/remote.md` and project `AGENTS.md` rule 5: "a private claude.ai artifact" became "a private artifact page". The page is on claude.ai, a site rather than a CLI; that fact is now missing from the files.

## Tests
- `gofmt -l cmd internal`: empty. `go vet ./...` and `go test -race -count=1 -timeout 30m ./...` exit 0, all 9 packages (`.agents/scratch/fyr/go-test-phase3b-final.txt`, after the D26 edit).
- `TestKitPreamble*`: they never had a `claude` row, so the box's test had nothing to remove. The phase's test is `TestInitProjectAndDreamNameOneCLI`, which replaces `TestInitProjectVendorCheckMatchesManifest`. It is red on the 9174cca text of both skills and green on the new text.
- Size pins on the final tree: rules block 18,181 of 22,000 bytes; hand-written context 14,682 of 15,000; listing 19,185 of 19,500.
- Scratch-home install from the final tree (`.agents/scratch/fyr/home-p3b`): exit 0, 896 written, `status` `drift none`, `binary` line without a warning, no `.claude` created.

## What was not checked
- A second attempt of `init-bare`, `handoff` and `project-context`, which passed once each, on the kit3b tree.
- `feedback` and `review`/`preflight` under named grants only: they need `command(*)` by the matrix procedure, so their shell steps are not proven under the narrow grants.
- Whether agy 1.4.6 → 1.4.7 or its model changed what the store refuses: BUG-174 and BUG-175 were both read on 1.4.7 only.
- The `@{u}` denial outside print mode: an interactive session prompts instead.

## Addendum — 2026-10-25: BUG-175 fixed, `project-memory` re-run

**Status after the addendum: PASS.** On the final tree, 13 of 13 rows pass.

- **Change:** `skills/project-memory/SKILL.md:50` (plan D27). Run `date +%F` as its own command, type the date it printed into the body, and make the save the one `sh-agent memory add` command, with nothing joined and no `$( )`.
- **Kit:** `.agents/scratch/fyr/kit3c/run.sh project-memory`, with named grants (no `command(*)`). Tags `3d` and `3e`, agy 1.4.7, run from homes installed from the tree with this edit.

| Row | Covers | Result (n of N) | Evidence |
|---|---|---|---|
| `project-memory` | `project-memory`, BUG-175 | **PASS 2/2** | Both attempts, from the conversation store: `date +%F` ran as its own step, then `sh-agent memory add --type project --name deploy-on-fridays …`. Every step has status 3 and none has `error_details`. `MEMORY.md`'s line is `- [deploy-on-fridays](deploy-on-fridays.md) — deploys happen on Fridays`, and there is no `originSessionId`. The `**Why:**` line carries 2026-10-25. Session 2 answered "Friday" with the store present. The preamble printed its three clean lines (42 s, 43 s). |

- **Row totals across the phase:** `project-memory` is 2 of 4. The two failures are the earlier BUG-175 attempts, on the old line.
- **Tests:** the full suite, run after this edit: `.agents/scratch/fyr/go-test-bug175.txt` (its result is in this range's `changes/` entry).
- **Not checked:** `feedback-log` rule 7 saving a memory under named grants. The `feedback` row runs with `command(*)`.
