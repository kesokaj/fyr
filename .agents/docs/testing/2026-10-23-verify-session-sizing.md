---
owner: main
date: 2026-10-23
---
# Verification — session sizing, Phase 1: the rule, the kickoff, the sizing   (2026-10-23)

Range reviewed: 0ce6b6b..working tree (committed as the Phase 1 checkpoint of plan `2026-10-23-session-sized-work.md`)

## Status: PASS

Every row below ran and passed against its own criterion. The Go checks ran bare (the plan's Phase 6 dispatches the `verifier` and the `reviewer` over the whole feature). Batch 1 of the behavioural runs is kept as the diagnosis it produced (two kit defects, D33 and the Antigravity grant); batch 2, run on the text as committed, is the evidence.

## Go checks (guide § 1), bare, by the main agent

- `gofmt -l cmd internal` → empty; `go vet ./...` → exit 0; `go test -race -count=1 -timeout 30m ./...` → exit 0 (log `.agents/scratch/session-sizing/go-test-2.txt`): root 1.371s, cmd/sh-agent 80.232s, internal/dream 22.749s, internal/harness 74.601s, internal/memory 1.246s, internal/metrics 3.739s, internal/search 9.685s, internal/trail 1.137s.
- The first full run (`go-test-1.txt`) was red at two pins the new verb owed: `TestDeliverySpecCurrent` (the delivery spec's "Built and shipped" paragraph must name every verb) and `TestUsageMatchesDocComment` (main.go's package comment must list every verb). Both fixed by a line each; green on the second run.
- `go test -race -v ./internal/harness -run 'TestRenderedContext|TestLint'` → PASS; `Antigravity rules block is 18521 bytes (context 15907, rules 2614), budget 22000`; `hand-written context is 14936 bytes (render 15907 with the inventory), target 15000` (the DoD step 5 edit cost 7 bytes of 71).
- Tests first for `sh-agent kickoff`: `TestKickoffPrintsTheNewestHandoffBlock`, `TestKickoffNothingQueued`, `TestKickoffNothingQueuedExitsOne` and four `TestUsageErrorsExit2` rows were red against the zero-value stub (`kickoff printed ""`, `err <nil>, want a plain error`, `want exit 1`), then green; `TestKickoffPrintsAProseSection` was added with D33, red against the fence-only parser, then green.

## Harness self-check and scratch-home install (§ 2, § 3)

- `go run ./cmd/sh-agent list` → Skills (36), Subagents (8), Rules (2); `render | wc -l` → 87.
- `go run ./cmd/sh-agent install --home .agents/scratch/home` → `1792 written, 0 unchanged, 0 removed, 0 kept`; `status --home` → `binary 0ce6b6b34ab6+dirty` with no warning, `drift none — 1791 files match the repo`; `.claude/rules/` and `.gemini/config/rules/` each hold `critical-thinking.md` and `session-sizing.md`.

## `sh-agent kickoff` at each place kind (§ 3c; rows in `sh-agent-matrix.md`)

Fixture: `.agents/scratch/session-sizing/matrix/fixture`, a git init with two handoffs. Development build and a `-ldflags "-X main.version=v9.9.9"` build gave byte-identical results: from a subdirectory, the newest handoff's block without its fence, exit 0; `--home /x` and `--repo /x` → the refusal line, exit 2; the newest handoff holding the nothing-queued line → stdout empty, `sh-agent: kickoff: nothing queued: the Kickoff of … queues nothing` on stderr, exit 1; the loop one-liner ended at zero iterations on that fixture. In this checkout the verb printed the Phase 1 kickoff of `handoffs/2026-10-23-1249-plan-session-sized-work.md`. The plain-copy cell is not run: the verb resolves no harness tree. (A cell labelled "outside a checkout" in the scratch transcript ran inside this checkout's scratch directory and printed this repo's kickoff; the outside case is `TestKickoffNothingQueuedExitsOne`.)

## Behavioural runs (guide § 4, plan Phase 1 box 9, D11)

Kit: `.agents/scratch/session-sizing/` (`kit-claude.sh`, `kit-agy.sh`, `check.sh`, `drive.sh`, the two prompts; rebuilt from `skill-matrix.md` rows `session-sizing` and `session-sizing-context`). Fixture `~/sandbox/session-sizing/fixture` at `9741514`: a scaffolded three-file Go service whose `README.md` holds "recieve"; each row a fresh clone under `~/sandbox/session-sizing/runs/<cli>-<arm>-<n>/app` and a fresh home under `homes/`, installed from this working tree (`--vendor claude` or `agy`); the control arm deletes `rules/session-sizing.md` from the installed home. Claude Code 2.1.309 (`claude-opus-5-5`, `--permission-mode acceptEdits` with the skill-matrix tool list), Antigravity 1.4.0 (`--mode accept-edits`, settings written for the run, `trustedWorkspaces` = the clone). The CHECK (`check.sh`) is read from disk after the run: A done = no "recieve" in README.md; B untouched = `git diff --quiet 9741514 -- CONTRIBUTING.md`; the Kickoff = `sh-agent kickoff` run in the clone with the build under test.

**Batch 2 (the committed text; counts):**

| CLI | row | pass criterion | result | wall |
|---|---|---|---|---|
| Claude Code | context (quote the rule's first sentence, change nothing) | first sentence quoted, clone unchanged | 1 of 1 | 7 s |
| Claude Code | rule ×3 | B untouched AND the newest handoff's Kickoff names CONTRIBUTING.md | **3 of 3** (A done, one commit, one handoff, fenced Kickoff, each) | 24–37 s |
| Claude Code | control ×2 (no rule file) | B changed (the must-fail) | **2 of 2** (A and B done, 3 and 4 commits) | 50–52 s |
| Antigravity | context | first sentence quoted, clone unchanged | 1 of 1 | 7 s |
| Antigravity | rule ×3 | as above | **3 of 3** (A done, two commits each, the handoff template's fenced `text` block with the standing-approvals line) | 97–209 s |
| Antigravity | control ×1 (no rule file) | B changed | **1 of 1** | 111 s |

Every one of the 11 kit logs ends with the preamble's three lines (`no processes left by the run`, `real config unchanged`, `no credentials left in KIT_HOME`) and no `kit: FAIL` line; no Antigravity row has an `auto-denied` or `invalid allow entry` line. Claude Code rows had 0–1 permission denials each, all of the same shape: a compound Bash line opening with a stamp assignment before a heredoc (`S=$(date +%F-%H%M) && cat > … <<EOF`), which the `acceptEdits` tool list does not cover; each run then wrote the file with `Write`. Cost: USD 0.29–0.38 per Claude Code task row. No Claude Code row loaded the `handoff` skill through the `Skill` tool (the Trivial path writes the handoff by hand); Antigravity's rows wrote the skill's template.

**Antigravity store check** (a copy of `agy-context-11`'s conversation database, deleted after): one `<RULE[…/AGENTS.md]>`, one `<RULE[…/critical-thinking.md]>` and one `<RULE[…/session-sizing.md]>` block per stored prompt (2 each), the first sentence present, no `<truncated` line.

**Batch 1 (the diagnosis; rows `-1`, `-2`, `-3`):**
- Claude Code rule ×3: the rule held (A done, B untouched, a handoff queuing B in `## Kickoff`) but the Kickoff was a prose sentence, not a fenced block, and the verb as first written printed nothing for it → D33 (`kickoff` prints an unfenced section's text; nothing queued is an empty section or one starting "Nothing queued") and the rule's clause naming the `handoff` skill and its fenced block. Re-judged with the D33 build: 3 of 3. Controls 2 of 2 changed B.
- Antigravity rule ×3: 0 of 3 — `err.txt`: `no output produced — a tool required the "command" permission that headless mode cannot prompt for, so it was auto-denied`; the conversation database's last command line was `STAMP=$(date +%F-%H%M)\nD=$(date +%F)\ncat <<EOF > …`, a variable assignment the grant store cannot map to a command name (BUG-141's diagnosis path). The arms that write a handoff now run with `command(*)` in their throwaway clone, as `skill-matrix.md` § Procedure runs the `handoff` row; the context arm keeps the narrow list.

**Found on the way: BUG-172** (open). Every Claude Code row ended with the kit's `WARNING the credential link was replaced by a file`: Claude Code 2.1.309 saves `.credentials.json` by replacing the path, so the committed kits' link no longer carries a refresh to the real file. A one-prompt probe (`runs/credtest`) showed the replacement file holds the same token values (no refresh happened) plus one extra key; the real file's mtime never changed. No login was hurt; the fix options wait on the operator.

## What was not checked

- The `verifier` and `reviewer` dispatches: the plan's Phase 6 runs them over the whole feature.
- A real-home install (not in the standing approvals); the Mac.
- The golden-path E2E criterion (a fresh conversation started from a Kickoff completes the next phase): first observable in Phase 2, which the closing handoff's Kickoff starts.
