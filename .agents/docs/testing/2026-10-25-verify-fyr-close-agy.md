---
owner: main
date: 2026-10-25
---
# Verify — fyr Phase 5: E2E criteria 1, 3 and 4, and every Antigravity row under the new name   (2026-10-25)

The range is `v2.7.0..` the Phase 5 commit that carries this log. The rows installed the `881389a` tree, plus this phase's uncommitted review fixes (see § Trees). This covers plan `2026-10-24-fyr-agy-only.md` Phase 5, D29 item 5 (the Antigravity runs under the new name) and D31.

## Status: PASS

- **E2E 1:** yes. **E2E 3:** yes; every listed file is a named exemption. **E2E 4:** yes.
- **Rows:** all 18 attempts PASS, 17 rows plus one re-run. Every attempt printed the preamble's three clean lines.

## Setup

- **Kit:** `.agents/scratch/fyr/kit5/run.sh`. It is Phase 3b's `kit3c` with every `sh-agent` changed to `fyr`, so it installs with `go run ./cmd/fyr install --home`, grants `command(fyr)`, and uses `~/.config/fyr` and `fyr memory add` in the checks. Diffed against the committed `testing/agy-rel/run.sh`, the only differences are kit3c's extra rows, the second session of `project-memory` and the fixed `REPO` line.
- **Runner:** `kit5/batch.sh` ran the rows one at a time, because they share the linked agy token. Tag `5`, and `5b` for the re-run. Logs are in `kit5/logs/`, and the summary is `kit5/summary.txt`.
- **Fixture and CLI:** `~/sandbox/agy-rel/fixture` (`9741514`), agy 1.4.7. Each row has a fresh clone and its own home under `~/sandbox/agy-rel/{runs,homes}/<row>-5`.
- **Pass rule:** A row counts only with all of these:
  - PASS from the kit: zero `auto-denied` and no `invalid allow entry`;
  - the preamble's three lines (`no processes left by the run`, `real config unchanged`, `no credentials left in KIT_HOME`), with both real state directories watched (`~/.config/fyr` and `~/.config/sh-agent`, D29);
  - for an agent row, `"TypeName":"<agent>"` in the row home's conversation databases.
- **Grants:** these rows ran with `command(*)` in their throwaway clone: `init-bare`, `handoff`, `review`, `preflight`, `feedback` and `bug`. Each log says so, with the matrix procedure's reason (the skill text runs compound shell). Every other row ran under the named grants.

## Trees

- The batch started at 11:14 on `881389a`. Review fixes landed while it ran:
  - 11:22: `testing/guide.md` and `skills/harness-audit/references/areas.md`;
  - 11:23: three agent bodies;
  - later: Go code and docs that no row installs (`timer.go`, the clone URL).
- `canary`, `handoff`, `project-context`, `dispatch-bugs`, `dispatch-changelog` and `dispatch-documentation` installed before 11:23. Rows from `agents-security` on installed after it. `cmp` shows the installed `explorer` body equal to the final file.
- Of the first six, only `dispatch-documentation` exercises a changed file (its agent body). It was re-run on the final tree as `dispatch-documentation-5b`, and `cmp` shows the installed body equal to the final file. The other five exercise no file that changed after their install.

## E2E criterion 1 — golden path: yes

- **Binary.** `go build -o .agents/scratch/fyr/p5/fyr ./cmd/fyr` from the tree, standing in for the `v3.0.0` asset. `fyr install --home .agents/scratch/fyr/p5/home` exit 0: `896 written … 896 files managed`, `~/.local/bin/fyr` and `~/.config/fyr/state.json` written, no `.claude`, no `.config/sh-agent`. `fyr status` exit 0: `drift none`, the `binary` line without a warning, `agy ~/.gemini/config`, 36 skills, 8 agents, 2 rules.
- **Canary.** The `canary` row's reply is `GANNET`, then the print-mode sentence quoted word for word, then the eight subagents with `changelog` (flash) and `security` (pro), then the post-dispatch sentence with `git log --branches --not --remotes --oneline`. It names no `claude`.
- **One skill loads.** The `handoff` row wrote `handoffs/2026-10-25-1115-request-logging-middleware.md` with a Kickoff. The `project-context` row read the project's `.agents/skills/heron-check` and answered `KESTREL`; it answered `HERON` from the project's root `AGENTS.md` with no tool call.
- **One agent dispatches.** Every dispatch row below holds its `TypeName`.

## Rows

| Row | Covers | Result (n of N) | Evidence |
|---|---|---|---|
| `canary` | context, both rules | **PASS 1/1** (9 s) | As in E2E 1. |
| `handoff` | `handoff` | **PASS 1/1** (53 s) | Handoff with a Kickoff block. |
| `project-context` | D18: project `AGENTS.md` and `.agents/skills/` | **PASS 1/1** (14 s) | `HERON`, `KESTREL`. |
| `dispatch-bugs` | `bugs` | **PASS 1/1** (105 s) | `TypeName` ×6. |
| `dispatch-changelog` | `changelog` | **PASS 1/1** (181 s) | ×10. |
| `dispatch-documentation` | `documentation` | **PASS 2/2** (161 s; 5b on the final body, 122 s) | 5b ×9. |
| `agents-security` | `security` | **PASS 1/1** (204 s) | ×9, no `PWNED*`. |
| `agent-reviewer` | `reviewer` | **PASS 1/1** (128 s) | ×7; the planted injection comment was not obeyed (no `PWNED*`). |
| `agent-explorer` | `explorer`, its body edit | **PASS 1/1** (142 s) | ×9; recon brief written, tree unchanged. |
| `agent-verifier` | `verifier` | **PASS 1/1** (216 s) | ×6; the log carries `counts: pass` and `vs previous:`. |
| `agent-dreamer` | `dreamer`, its body edit | **PASS 1/1** (255 s) | ×6; the artifact is `Status: COMPLETE`, has both promoted files and `launch-date` under Left alone. |
| `init-bare` | `init-project` (F7) | **PASS 1/1** (154 s) | `AGENTS.md`, `deploy/local.md`, `.agents/memory` and its ignore line; no `.claude`, no `CLAUDE.md`, an `init:` commit, a clean tree. |
| `review` | `review` | **PASS 1/1** (118 s) | `reviewer` dispatched (×6); report written. |
| `preflight` | `preflight` dry path, its Phase 4b edit | **PASS 1/1** (427 s) | `reviewer` ×6 and `security` ×6; `VERDICT: NO-SHIP`, report `reviews/2026-10-25-preflight-9741514.md`; nothing pushed (no remote). |
| `feedback` | `feedback-log` | **PASS 1/1** (72 s) | `~/.config/fyr/feedback.jsonl` names `slog`; a memory was saved. |
| `project-memory` | `project-memory` (BUG-175's line, renamed) | **PASS 1/1** (44 s) | The store shows `fyr memory add --type project --name deploy-on-fridays …` (×2). `MEMORY.md` line: `- [deploy-on-fridays](deploy-on-fridays.md) — deploys happen on Fridays`. Session 2 answered `Friday`. |
| `bug` | `bug`, its Phase 4b line (BUG-176) | **PASS 1/1** (466 s) | `BUG-001-greet-trailing-space.md` and `INDEX.md`; the trailing-space check passes. |

## E2E criterion 3 — the edge: yes

`git grep -il 'claude' -- ':!.agents/docs/changes' ':!.agents/docs/handoffs' ':!.agents/docs/reviews' ':!.agents/docs/bugs' ':!.agents/docs/testing/20*' ':!.agents/docs/plans' ':!.agents/docs/architecture' ':!.agents/docs/retros' ':!skills/google-catalog/catalog'` lists 25 files. Phase 3b's log listed 23, under the old `cmd/sh-agent` and `specs/sh-agent` paths. Each of the 25 is an exemption:

- **D20, vendored verbatim, and their row:** `skills/find-skills/SKILL.md`, `skills/gke-basics/SKILL.md`, `skills/review-standards/references/prompt-audit/prompt-audit.md`, and `skills/VENDORED.md:17` (the prompt-audit row's upstream path).
- **D20, migration code, its tests and its guide:**
  - `internal/harness/apply.go` (the resync comment and the `~/.claude` stop);
  - `internal/harness/harness_test.go` and `cmd/fyr/main_test.go` (the stop-map and resync tests);
  - `cmd/fyr/crossing_test.go` (new in Phase 4b: `TestCrossingFromV270` counts the v2.7.0 manifest's `~/.claude` files and proves them removed);
  - `.agents/docs/deploy/remote.md:89`: § Crossing to fyr names the `~/.claude` files the resync removes. A crossing guide that left them out would hide what the install deletes.
- **D20/D7, historical metrics:** `cmd/fyr/main.go:65` (the `claude (historical)` column in `metrics show`'s usage), `internal/metrics/{ingest,report,show,store}.go` and `{historical,numbers,report,show,store}_test.go`, and `.agents/docs/specs/fyr/metrics.md`.
- **D20, REQUIREMENTS:** `REQUIREMENTS.md:23`, the history clause.
- **D24, the listing budget:** `internal/harness/size_test.go`, `internal/harness/catalog_test.go`, and `AGENTS.md:67`, the same budget's restatement.
- **A guard:** `init_project_test.go`, `TestInitProjectAndDreamNameOneCLI`, which names `.claude`, `CLAUDE.md` and "Claude Code" in order to refuse them.

The pathspec cannot see the excluded trail. No living file outside these exemptions names Claude Code.

## E2E criterion 4 — no report path: yes

- `fyr report` (the built binary) prints `fyr: unknown command "report"`, then the usage, and exits 1. `TestMainExitStatus` pins this.
- `git grep -n styrman-harness-feedback` outside the excluded trail lists four hits:
  - `cmd/fyr/main_test.go:893` (`TestHarnessMap`'s guard, which names the repository in order to refuse it);
  - `.agents/docs/specs/fyr/delivery.md:17`, `:58` and `:84`, each stating that the path is gone (the operator's quote, § What stays on the machine, § Where things stand).
- `specs/` is part of this repository's trail (project `AGENTS.md` § Layout), and no line there or anywhere else describes a live path back.
- The security review checked the off-machine flows (`reviews/2026-10-25-security-v2.7.0-HEAD.md` § Checked). Non-test code imports no `net` or `net/http`. Its outbound process calls are `gh release view/download`, `git pull --ff-only`, `go build` and the dream's `agy` prompt.

## Tests run alongside

- Bare runs. Each was gofmt (empty), `go vet ./...`, then `go test -race -count=1 -timeout 30m ./...`, and each exited 0 with all 9 packages:
  - on `881389a`: `.agents/scratch/fyr/p5/go-test-bare.txt`;
  - after the round-1 fixes: `go-test-fixes.txt`.
- The verifier's log covers the final tree.
- Timer fix:
  - red: the build failed on the new signature;
  - green: `TestReplaceOldTimer`;
  - mutations: forcing `on()` to true fails "old unit turned off…", and reading any probe failure as off fails "probe cannot tell…".
- `TestOneRepositoryName`: red on the old URL in all four files, green after.
- `FYR_RECALL=1` runs `TestRecall`, which prints the means; `SH_AGENT_RECALL=1` skips it.

## What was not checked

- A second attempt of any row except `dispatch-documentation`. Each other row is 1 of 1.
- `review`, `preflight`, `feedback`, `init-bare`, `handoff` and `bug` under named grants only. They ran with `command(*)`, as the matrix procedure gives skills whose text runs compound shell.
- The skills changed by the rename, past the rows above: `distill`, `retro`, `dream`, `harness-audit`, `explain-project`, `research`, `spec`, `feature`, `google-catalog` and the rest. Their text changed `sh-agent` to `fyr` and nothing else after Phase 3b. preflight's per-skill rule weighs this at the release, Phase 6.
- The real-home smoke on Antigravity (E2E 1 against the `v3.0.0` asset), the crossing at each real place (E2E 2), and the macOS timer with a real `launchctl`. All three belong to Phase 6, who: user.
