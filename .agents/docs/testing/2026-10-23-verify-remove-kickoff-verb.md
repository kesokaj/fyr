---
owner: main
date: 2026-10-23
---
# Verification — remove the `sh-agent kickoff` verb (plan D34, ADR-058a)   (2026-10-23)

Range reviewed: a5351b1..working tree (committed as the removal Task's checkpoint; plan `2026-10-23-session-sized-work.md` D34)

## Status: PASS

Every row below ran and passed against its own criterion. The Go checks ran bare, by the main agent, as Phase 1's did (the plan's Phase 6 dispatches the `verifier` and the `reviewer` over the whole feature); the diff was read by the main agent (`git diff --numstat`: 13 files, 23 insertions, 374 deletions, 332 of them the two deleted Go files).

## Go checks (guide § 1), bare

- `gofmt -l cmd internal` → empty; `go vet ./...` → exit 0; `go test -race -count=1 -timeout 30m ./...` → exit 0 (log `.agents/scratch/remove-kickoff/go-test-1.txt`): root 1.715s, cmd/sh-agent 110.808s, internal/dream 30.296s, internal/harness 86.973s, internal/memory 1.339s, internal/metrics 5.430s, internal/search 14.885s, internal/trail 1.206s. Go: /usr/bin/go go1.24.4 (the user-level go1.27.2 SDK named in `AGENTS.md` is not on this shell's PATH today).
- The pins the verb owed are met the other way round: `TestDeliverySpecCurrent` (every verb in `verbs` is named in the spec; the removed clause named none the table lacks), `TestUsageMatchesDocComment` (the package comment and the verb table agree), `TestUsageErrorsExit2` without its four `kickoff` rows.
- `go test -race -count=1 -v ./internal/harness -run 'TestRenderedContextFitsAntigravityBlock|TestRenderedContextUnderTarget'` → PASS: `Antigravity rules block is 18490 bytes (context 15907, rules 2583), budget 22000` (31 bytes freed by the rule's edit); `hand-written context is 14936 bytes (render 15907 with the inventory), target 15000` (unchanged).
- `go run ./cmd/sh-agent kickoff` → `sh-agent: unknown command "kickoff"`, a usage error.

## Harness self-check and scratch-home install (§ 2, § 3)

- `go run ./cmd/sh-agent list` → Skills (36), Subagents (8), Rules (2).
- `go run ./cmd/sh-agent install --home .agents/scratch/home` → `5 written, 1787 unchanged, 0 removed, 0 kept — 1792 files managed`; `status --home .agents/scratch/home` → `binary a5351b1e5059+dirty` with no warning, `drift none — 1791 files match the repo`; `.claude/rules/` and `.gemini/config/rules/` each hold `critical-thinking.md` and `session-sizing.md`.

## Behavioural runs (guide § 4): the two context rows

Kit: `.agents/scratch/session-sizing/` as Phase 1 left it, with `check.sh` changed to read the newest handoff's `## Kickoff` section with awk instead of the verb (the `skill-matrix.md` `session-sizing` row carries the same check). Fixture `~/sandbox/session-sizing/fixture` at `9741514`; rows `claude-context-21` and `agy-context-21`, each a fresh clone and a fresh home installed from this working tree (`go run -C <repo> ./cmd/sh-agent install --home <home> --vendor claude|agy`). Claude Code 2.1.309, Antigravity 1.4.6. Prompt: `prompt-context.txt` (quote the rule's first sentence, name its file, change nothing).

| CLI | row | pass criterion | result | wall |
|---|---|---|---|---|
| Claude Code | context | `A session is one Task or one plan phase` quoted, 0 commits, clone clean | **1 of 1** (named `session-sizing`; USD 0.18, 1 turn) | 8 s |
| Antigravity | context | the same | **1 of 1** (named `session-sizing.md`, quoted the sentence as a block quote; 0 auto-denied) | 25 s |

Both kit logs end with the preamble's three lines (`no processes left by the run`, `real config unchanged`, `no credentials left in KIT_HOME`) and hold no `kit: FAIL` line. The Claude Code log holds one `WARNING the credential link was replaced by a file` (BUG-172, open; the operator chose option (a) this session, fix pending); the Antigravity log none.

## What was not checked

- The `rule` and `control` arms: the rule's behaviour did not change (only the parenthesis naming the verb left its text); the Done-when named the two context rows.
- The `verifier` and `reviewer` dispatches: the plan's Phase 6 runs them over the whole feature.
- A real-home install (not in the standing approvals); the Mac.
