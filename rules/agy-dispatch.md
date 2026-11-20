---
trigger: model_decision
description: "Apply when invoking subagents or /fyr-swarm, picking models via fyr models, or running fyr eval."
---

# Agy dispatch

- Resolve live models via `fyr models --pick <role> [--tier pro|flash]` (`--explain <id>`, `--refresh`; `orchestrator` is interactive-only) — never hardcode IDs or static codenames. Check `agy -p "/usage"` before `/fyr-swarm` or `fyr eval`.
- Role routing: `planner`/`builder` pick active `[pro]` peers, `mechanical` picks `[flash]`, and `judge`/`reviewer`/`verifier` route cross-family when Anthropic quota is `>= 20%` weekly and `>= 30%` 5h (else Gemini `[pro]`).
- Guard: omit `Model` or pass `inherit` on `invoke_subagent` for `reviewer`, `verifier`, `security`, `dreamer` (`PreToolUse` upgrades `orchestrator`/fast turns to `"pro"`).
- Wait & progress: pass an artifact path in `.agents/scratch/agents/`, tool budget, and early-artifact duty (`"report only"` scopes source, not artifact). End the turn silently on single short waits (`agy -p` stays in turn until artifacts land); emit a 1-line progress update every ~5 minutes across long loops/waves, reserving the reply's 2-space `json` block for the final turn.
- Hang recovery & judging: pair dispatches with `schedule(DurationSeconds: 90, TimerCondition: "<child-id>")`, inspect hangs with `fyr watch --check --conv <id> --json`, and gate branches with `fyr gate --judge "<criterion>"` / `fyr land --judge "<criterion>"` (`--judge-model`).
- Log per-dispatch routing (`{"role": "REVIEWER", "scope": "<target>", "model": "<model-id>", "thinking": "HIGH", "quota": "<pct>"}`, omit empty; resolved `fyr models` ID + `thinking`). On `gone`/exhausted pool, pick next active peer and state why; both pools out -> ask via `ask_question`, never downgrade tier.
