---
trigger: always_on
description: "Agy dispatch: read the quota and choose by tier before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Before `fyr work`, pick each worker's model by tier (`Model:` line or `--model`; `/fyr-work PLAN-ID` copies the plan's, task lists set one per task). Before `fyr work` or `fyr eval`, check quota via `agy -p "/usage"` (spends no turn/quota).
- Cross-family routing: route builders/workers to Gemini (`[pro]` `gemini-4-argon`, `[flash]` `gemini-3.8-flash`) and verifiers/reviewers/judges to Anthropic (`[opus]`/`[pro]` `claude-opus-5-5`, `[sonnet]`/`[flash]` `claude-sonnet-4-6`). When Anthropic pool quota in `agy -p "/usage"` is `< 20%` weekly or `< 30%` 5-hour remaining, automatically fall back to the equivalent-tier Gemini model (`gemini-4-argon` / `gemini-3.8-flash`) and log the fallback reason.
- Report role, tier, model and remaining quota in one line per dispatch.
- Tiers (`agy models`): `[pro]` argon, `[flash]` flash (Gemini pool); `[opus]`, `[sonnet]`, `[gpt-oss]` (Claude/GPT pool).
- Model out: pick a peer in the same tier (or cross-family Gemini fallback for judge/verifier) and state why. Both pools out: stop and ask, never downgrade tier.
