---
owner: main
date: 2026-10-25
---
# fyr Phase 3b: skills, kits and the remaining content name one CLI; BUG-174 fixed     (date: 2026-10-25)

## Summary
The skills, the two remaining agent bodies, the kits and the living docs now describe one CLI, the Antigravity CLI (ADR-062, plan `2026-10-24-fyr-agy-only.md` Phase 3b). E2E criterion 3's grep lists only named exemptions. `/init-project` writes nothing under `.claude/`: no links, no settings keys, no memory migration. Its memory-store step is now Step 5. The Claude Code kits `ab-kit/` and `seed-security/` are deleted. BUG-174 is fixed, after a corrected diagnosis (plan D26). The context's post-dispatch check runs `git status --short --untracked-files=all`, `git log --branches --not --remotes --oneline` and `git tag` as three separate commands, because agy's grant store refuses `git log @{u}..HEAD` itself. D18 is answered yes: Antigravity loads a project's root `AGENTS.md` and its `.agents/skills/`. One row stays red: `project-memory`, BUG-175, open.

## Type
task (plan phase)

## Files changed
From `git status` and `git diff HEAD --shortstat` over the working tree:
- Skills:
  - `init-project` (the `.claude/` step, the vendor check, Step 5b's settings keys and migration, the stub paragraph and the `.gitignore` lines are gone) and its `assets/AGENTS.md`.
  - `dream` (one store count; no old default path).
  - `handoff`, `feedback-log`, `project-memory`, `review`, `preflight` (`.agents/agents/` shadow check, `agy --model pro`).
  - `google-catalog`, `bug`, `distill`, `feature`, `spec`, `retro`, `explain-project`, `research`, `harness-audit` and `references/areas.md`, and `VENDORED.md`'s adopt note.
  - Every "below the agent's `maxTurns`" phrase is now "a tool-call budget the dispatch names" (D17), and "both CLIs" wording is gone.
- Agents: the `dreamer` body (paths) and the `verifier` body (the timeout clause).
- Context: the post-dispatch check (D25 wording, D26 command).
- Go:
  - `init_project_test.go`: `TestInitProjectAndDreamNameOneCLI` replaces `TestInitProjectVendorCheckMatchesManifest`.
  - One usage string, `cmd/sh-agent/main.go:61` ("every session writes memory this way").
  - Comment-only edits in `internal/dream/dream.go`, `internal/harness/{inventory,catalog_test,pathguard_test,size_test}.go` and `cmd/sh-agent/memory_test.go`.
- Kits: `testing/ab-kit/` (4 files) and `testing/seed-security/run.sh` deleted by name; `testing/agy-rel/run.sh` gains `command(govulncheck)` (D26).
- Docs:
  - `README.md`: the install paragraph, the metrics row, and the agents row, which still said "Claude Code's format" after Phase 3a.
  - `AGENTS.md`: lines 8, 18, 48, 52, 67, 74 and 78.
  - `deploy/{local,remote}.md`, `specs/sh-agent/{delivery,dream,memory-loop,metrics}.md`, `testing/{guide,skill-matrix,sh-agent-matrix}.md`.
  - `.gitignore`: the two `.claude` lines.
- Fixture: `internal/search/testdata/recall.tsv` row 39, now "install edits no vendor setting", with the same gold paths.
- Trail: BUG-174 (fixed, with an addendum), BUG-175 (new, open), both INDEX rows, plan D26 and ticks, `testing/2026-10-25-verify-fyr-content.md`.

## Deviations from the plan's box
- **`TestKitPreamble*` had no `claude` row.** The box's premise did not hold. The phase's red-then-green test is the init-project/dream guard instead.
- **BUG-174's fix took two steps.** Option (a), D25, split the three commands as approved. The first behavioural pass showed that `git log @{u}..HEAD` alone is denied, while chained lines of granted commands run. The operator chose D26 (`--branches --not --remotes`) and a `govulncheck` grant in the kits. Then every dispatch row was re-run.
- **Beyond the box:**
  - "both CLIs" wording in living files and Go comments: the grep for `claude` does not see it, but the box's goal does.
  - README's agents row, stale since Phase 3a.
  - "a private claude.ai artifact" became "a private artifact page" (project `AGENTS.md` rule 5, `deploy/remote.md`). The page is on claude.ai, a site rather than the CLI. The log names this.
- **`AGENTS.md:67` keeps the listing budget's reason**, which names Claude Code's listing. It is kept with D24's two comments, not as D20's: the log names it.
- **`feedback`'s second attempt used `command(*)`.** That is the matrix procedure for a skill whose own text runs compound commands. The first, under named grants, failed on the skill's own `jq` script.

## Agents dispatched
- `documentation` ×2, in parallel:
  - `testing/guide.md`, `skill-matrix.md`, `sh-agent-matrix.md`;
  - `deploy/{local,remote}.md` and `specs/sh-agent/{delivery,dream,memory-loop,metrics}.md`.
- The main agent read both diffs. It corrected `memory-loop.md`'s layout line (the frontmatter is `metadata.type`, not `type`) and reworded the two retired-row pointers in `skill-matrix.md`.
- No reviewer or security dispatch. The main agent read its own diff, and Phase 5 holds the `reviewer --code --docs` and `security` dispatches over the whole feature, as in Phases 2 and 3a.

## Verification
- Full suite: `gofmt -l cmd internal` empty, `go vet ./...` ok, `go test -race -count=1 -timeout 30m ./...` exit 0, all 9 packages (`.agents/scratch/fyr/go-test-phase3b-final.txt`).
- Size pins: 18,181 of 22,000; 14,682 of 15,000; 19,185 of 19,500.
- Scratch home `.agents/scratch/fyr/home-p3b`: install exit 0, 896 files, `drift none`, no binary warning, no `.claude`.
- Behavioural: `testing/2026-10-25-verify-fyr-content.md` (Status FAIL; 12 of 13 PASS on the final tree; `project-memory` 0/2, BUG-175).
