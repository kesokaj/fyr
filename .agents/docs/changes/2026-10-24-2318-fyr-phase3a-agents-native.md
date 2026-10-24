---
owner: main
date: 2026-10-24
---
# fyr Phase 3a: agents native, the lint, the context and rules     (date: 2026-10-24)

## Summary
Agents are authored as `agents/<name>/agent.md` in Antigravity's format and installed as written; `derive.go` is gone (ADR-062, plan `2026-10-24-fyr-agy-only.md` Phase 3a, D3, D17, F6). Each of the eight files is `DeriveAgy`'s output at the phase's base commit `a2e6a5a`, generated into `.agents/scratch/fyr/derived/` before the derivation was deleted and compared with `cmp`. The lint keeps both allow-lists: `agentKeys` is now the derived keys (`name`, `description`, `subagent`, `mainAgent`, `model`, `tools`), so an old-format key such as `maxTurns` or `effort` is refused; `knownTools` is the six names the old `toolMap` produced; a harness agent sets `subagent: true` and `mainAgent: false`; a flat `agents/<name>.md`, an agent directory with no `agent.md`, and a symlinked `agent.md` are refused with the file named. The inventory prints an agent's `model` key as written, and nothing when it is absent. The global context and the two rules name one CLI: the `/goal` and `claude -p` clause, the `.claude/*` links, the `CLAUDE.md` stub and both `TaskStop` mentions are gone, and "a budget below the agent's `maxTurns`" is now "a tool-call budget" (D17). `# Compact instructions` is kept as prose. BUG-173: the committed `agy-rel` kit still passed `--vendor agy`, a usage error since Phase 2, and is fixed.

## Type
task (plan phase)

## Files changed
From `git diff HEAD -M --numstat` over the working tree:
- Agents: `agents/<name>.md` → `agents/<name>/agent.md` for all eight, each the derived file byte for byte. Git sees them as renames (−3/+4 lines each: the format keys).
- Deleted: `internal/harness/derive.go`; `yamlQuote` in `frontmatter.go` (its only caller was `DeriveAgy`).
- `internal/harness/inventory.go`: `LoadAgents` reads `agents/<name>/agent.md` through `readRegular` (a symlinked `agent.md` is refused). New: `agentKeys`, `knownTools`, the subagent and mainAgent check, and the refusals of a flat file and a missing `agent.md`. `RenderInventory` and `RenderInventoryCompact` print the model as written.
- `internal/harness/plan.go`: an agent is copied from `a.Raw`.
- Tests: `agents_test.go` is new. `harness_test.go`, `pathguard_test.go`, `source_test.go` and `zip_test.go` moved to native fixtures. `TestDeriveAgy`, `TestDeriveAgyRealReviewer` and `TestYamlQuoteEscapesControlCharacters` are deleted.
- `context/AGENTS.md`: lines 3, 15, 31, 35, 39 and 64.
- Docs: `AGENTS.md`, `README.md`, `specs/sh-agent/delivery.md` (§ Antigravity derivation becomes § Agents), `specs/sh-agent/memory-loop.md`, `testing/guide.md`.
- Fixture: `internal/search/testdata/recall.tsv` gold paths.
- Kit: `testing/agy-rel/run.sh` (BUG-173).
- Trail: BUG-173, BUG-174 and the INDEX rows; `testing/2026-10-24-verify-fyr-agents.md`; the plan's ticks.

## Deviations from the plan's box
- **Byte-equality is a one-time `cmp`, not a standing test.** The derived files live in gitignored scratch, so a test that read them would fail on every other checkout. `TestCheckoutAgentsAreNative` and the lint pin the shape that stays: the keys, `subagent`/`mainAgent`, the known tools, and the reviewer's skill path.
- **No standing check of an agent body's skill path.** The derivation rewrote `~/.claude/skills/` in bodies. Nothing is rewritten now, and a lint for that string would name the old CLI in the code. The reviewer's Antigravity path is pinned by `TestCheckoutAgentsAreNative`, and every other agent names no skill path.
- **Lint wording.** The lint and its tests name "the format before ADR-062", never the old CLI, so Phase 2's checkpoint grep lists only its expected hits plus D24's exemptions. That box is ticked.
- **Two agent bodies still name the old CLI** (`dreamer`: `~/.claude/AGENTS.md`; `verifier`: "Claude Code's maximum"). They are kept byte-equal by D17, and Phase 3b edits them.
- **`recall.tsv` was edited before Phase 4a.** `TestRecallQuestionsAreWellFormed` stats every gold path, and the moved agents and the deleted `derive.go` broke it. Row 39 waits for Phase 4a.
- **The behavioural log's status is FAIL.** Every row is stated as n of N, and the cause is BUG-174, which predates this phase (base-tree control 0/2). Its fix is the operator's call.

## Agents dispatched
None. The main agent read the diff itself (about 200 lines in 27 files). The plan's Phase 5 holds the `reviewer --code --docs` and `security` dispatches over the whole feature, as Phase 2 did.

## Verification
- Full suite: `gofmt -l cmd internal` empty, `go vet ./...` ok, `go test -race -count=1 -timeout 30m ./...` exit 0, all 9 packages (`.agents/scratch/fyr/go-test-phase3a-final.txt`, go1.24.4). Red first: `.agents/scratch/fyr/go-test-phase3a-red.txt`.
- Scratch homes `.agents/scratch/fyr/home-p3a` and `home-p3a-final`: install exit 0, 896 files, `drift none`, no binary warning, no `.claude`; each installed agent is `cmp`-equal to its source.
- Behavioural: `testing/2026-10-24-verify-fyr-agents.md`.

## Docs
Every file above. No path on the README map changed (rule 3).
