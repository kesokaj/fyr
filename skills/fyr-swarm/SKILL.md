---
name: fyr-swarm
description: "Multi-agent parallel execution via run_workflow parallel() or Antigravity native /swarm with Files: write-lease briefs, prefix-based model routing, and deterministic fyr gate + fyr land waves. Do NOT use for single-track 1-6 file tasks or 1-file bug fixes."
argument-hint: "[plan-id or task slices]"
---

# Swarm — Multi-Agent Parallel Execution

Use parallel execution only when genuinely independent slices justify coordination overhead, and keep workers isolated (`workspace="share"`), write-bounded (`Files:`), and deterministically gated (`fyr gate` + `fyr land`). Note: `/fyr-work` stays available during v9 migration until Phase 6b retirement.

## Orchestration routing table

| Work shape | Tool | What `fyr` controls |
|---|---|---|
| One track, 1–6 files | Plan and execute in main session | Turn-0 capsule, read clamp, `Stop` stamp, `fyr verify`, `fyr close` |
| Width ≤ 2, or blind judgement (`reviewer`, `security`, `verifier`) | `invoke_subagent` | Prefix/subagent model routing with quota fallback (`hook_subagent.go`) |
| Width ≥ 3, independent pieces (`PLAN` wave, multi-file refactor, parallel review) | `run_workflow` (`parallel()`, `workspace="share"`, or `"inherit"` for read-only reviews) | `fyr gate` per worker branch, `fyr land` one at a time |
| Width ≥ 3, workers must negotiate interfaces mid-flight | Native `/swarm` (`workspace="share"`, `Files:` lease) | `Files:` write lease, `fyr gate`, `fyr land` |
| Hard single-surface bug or deep trade-off | Recommend `/boost` to operator (routing row only) | Normal session gates |
| Multi-day autonomous campaign | Recommend `/teamwork` or `/goal` to operator (routing row only) | Per-milestone `fyr verify` from `PLAN-*` |

## Quota check & prefix model routing

1. Run `agy -p "/usage"` before spawning any parallel wave, and emit the required 2-space `json` dispatch block (`role`, `scope`, `model`, `thinking`, `quota`).
2. `PreToolUse` (`hook_subagent.go`) routes/validates worker models from roster name prefixes:
   - `builder-<slice>` / `coder-<slice>` -> builder model (`fyr models --pick builder`, or coordinator model when omitted).
   - `reviewer-<slice>` / `judge-<slice>` / `security-<slice>` / `verifier-<slice>` -> `pro` / cross-family when Anthropic quota is `>= 20%` weekly and `>= 30%` 5h, else Gemini Pro fallback. Fast weak overrides are denied.
   - `explorer-<slice>` / `docs-<slice>` -> `flash`.
3. In native `/swarm`, switch a `waiting` worker's model with `swarm(action="model", ...)` — never `kill` and `add` a fresh conversation just to change models.

## Write-lease brief (`Files:` header)

Give every editing worker its own `workspace="share"` copy and disjoint `Files:` paths — two active workers never edit the same file. Each worker starts fresh and sees zero parent context, so make briefs self-contained:

```text
Files: internal/pkg/a.go, internal/pkg/a_test.go
Task: <exact behavior change and interfaces>
Rules: Create local branch `work/<name>`, modify ONLY paths matching `Files:`, commit cleanly with zero trailers (never push), and run `fyr verify` (or targeted `go test -race ...`).
Done: Post `[done] branch=work/<name> commit=<short-sha>` with verified command output (or return `finish` JSON in `run_workflow`).
```

For `run_workflow` scripts invoking `reviewer`, `security`, `verifier`, or `dreamer`, always pass `model="pro"` explicitly (`hook_workflow.go` `[AUTO_SUBAGENT_PRO]`) and require calling `finish` matching the script's `schema`.

## Wave gate & sequential landing (`fyr gate` -> `fyr land`)

1. **Gate every finished slice:** From the coordinator tree, check each completed worker worktree/branch against its lease:
   ```bash
   fyr gate <worker-worktree-or-branch> --lease "<comma-separated Files lease>"
   ```
   Reject slices reporting out-of-lease edits, secret-shaped filenames, missing/failing tests, or commit trailers; message the worker (or re-dispatch) rather than patching across boundaries yourself.
2. **Land sequentially — one branch at a time:** Merge passing branches onto the target branch strictly in dependency order:
   ```bash
   fyr land --lease "<comma-separated Files lease>" work/<name>
   ```
   Never run two `fyr land` commands concurrently; resolve any merge conflict cleanly on a single branch before continuing.
3. **Post-wave verify & cleanup:** Run `fyr verify` once on `main` after the wave lands. Only AFTER `fyr land` succeeds, kill finished `share` workers via `swarm(action="kill", name="<worker>")` (`kill` deletes the worker's `share` workspace permanently).
