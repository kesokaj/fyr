---
trigger: model_decision
description: "Apply only when actively invoking a subagent or swarm, picking a model via fyr models, or running fyr eval (skip when no subagent is dispatched)."
---

# Agy dispatch

- Never hardcode model IDs: resolve live picks via `fyr models --pick <role> [--tier pro|flash]` (`--explain <id>`, `--refresh`; `orchestrator` is interactive-only). Kickoff `Model: auto` / `Judge-Model: auto` picks per scope; check quota via `agy -p "/usage"` before `/fyr-swarm`/`fyr eval`.
- Role routing: `planner` prefers Gemini Argon > Carbon (`[pro]`), `builder` prefers Carbon (`[pro]`), `mechanical` prefers Hydrogen/Flash (`[flash]`), and `judge`/`reviewer`/`verifier` route cross-family to Anthropic Opus (`[pro]`) or Sonnet (`[flash]`), falling back to Gemini if Anthropic `< 20%` weekly or `< 30%` 5h.
- Guard: omit `Model` or pass `inherit` on `invoke_subagent` for `reviewer`, `verifier`, `security`, `dreamer` (`PreToolUse` enforces it and `needsGuardedProUpgrade` automatically upgrades guarded subagents on `orchestrator`/fast models to `"pro"`).
- Dispatch & wait discipline: pass an artifact path in `.agents/scratch/agents/`, tool budget, and early-artifact duty; stop once written (`"change nothing"` / `"report only"` scopes source, not the artifact). Never emit wait notes while awaiting work; end the turn to await a subagent or background command — only a print-mode run (`agy -p`) stays in the turn until every agent's artifact is on disk (`fyr-handoff` § Usage).
- Hang recovery & gate judging: pair child dispatches with `schedule(DurationSeconds: 90, TimerCondition: "<child-id>")` and run `fyr watch --check --conv <id> --json` on silent hangs; use `fyr gate --judge "<criterion>"` / `fyr land --judge "<criterion>"` (`--judge-model`) for isolated cross-family diff judging.
- Per dispatch, emit a 2-space `json` block (`{"role": "REVIEWER", "scope": "<target>", "model": "Claude Opus 5.5", "thinking": "HIGH", "quota": "<pct>"}`, omit empty; exact `model` name + `thinking`, never `tier` or `agy:pro`).
- Model `gone` or pool out: pick next active peer from `fyr models` (or Gemini fallback for judges) and state why; both pools out: stop and ask, never downgrade tier.
