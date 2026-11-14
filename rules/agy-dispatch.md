---
trigger: always_on
description: "Agy dispatch: read the quota and choose from fyr models before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Never hardcode model IDs: resolve live picks via `fyr models --pick <role> [--tier pro|flash]` (`--explain <id>`, `--refresh` syncs `agy models` + `changelog`; `orchestrator` is interactive-only). Kickoff `Model: auto` / `Judge-Model: auto` picks per scope; check quota via `agy -p "/usage"` before `fyr work`/`fyr eval`.
- Role routing: `planner` prefers Gemini Argon > Carbon (`[pro]`), `builder` prefers Carbon (`[pro]`), `mechanical` prefers Hydrogen/Flash (`[flash]`), and `judge`/`reviewer`/`verifier` route cross-family to Anthropic Opus (`[pro]`) or Sonnet (`[flash]`), falling back to same-tier Gemini when Anthropic quota `< 20%` weekly or `< 30%` 5h.
- Guard: omit `Model` or pass `inherit` on `invoke_subagent` for `reviewer`, `verifier`, `security`, `dreamer`, `bugs` (`PreToolUse` blocks overrides, upgrades fast models to `"pro"`, fills omitted `swarm` `model`).
- Per dispatch, emit a 2-space `json` block (`{"role": "REVIEWER", "scope": "<target>", "tier": "PRO", "model": "<id>", "quota": "<pct>"}`, omit empty).
- Model `gone` or pool out: pick the next active same-tier peer from `fyr models` (or cross-family Gemini fallback for `judge`/`reviewer`/`verifier`) and explain why. Both pools out: stop and ask, never downgrade tier.
