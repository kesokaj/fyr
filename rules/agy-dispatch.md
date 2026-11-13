---
trigger: always_on
description: "Agy dispatch: read the quota and choose from fyr models before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Never hardcode a model ID: resolve live IDs via `fyr models --pick <role> [--tier pro|flash]` (`--explain <id>` shows profile; `--refresh` syncs `agy models` + `changelog`; `orchestrator` is interactive-only). Kickoff `Model: auto` / `Judge-Model: auto` auto-picks per scope; before `fyr work` or `fyr eval`, check quota via `agy -p "/usage"`.
- Role routing: `planner` prefers Gemini Argon > Carbon (`[pro]`), `builder` prefers Carbon (`[pro]`), `mechanical` prefers Hydrogen/Flash (`[flash]`), and `judge`/`reviewer`/`verifier` route cross-family to Anthropic Opus (`[pro]`) or Sonnet (`[flash]`), falling back to same-tier Gemini when Anthropic quota is `< 20%` weekly or `< 30%` 5h.
- Subagent & swarm guard: omit `Model` or pass `inherit` on `invoke_subagent` for `reviewer`, `verifier`, `security`, `dreamer`, and `bugs` (`PreToolUse` blocks explicit overrides, upgrades fast session models to `"pro"`, and fills omitted `swarm` `model`).
- Report role, tier, resolved model ID and remaining quota in one line per dispatch.
- Model `gone` or pool out: pick the next active same-tier peer from `fyr models` (or cross-family Gemini fallback for `judge`) and state why. Both pools out: stop and ask, never downgrade tier.
