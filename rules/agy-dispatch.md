---
trigger: always_on
description: "Agy dispatch: read the quota and choose by tier before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Before `fyr work`, pick each worker's model by tier (`Model:` line or `--model`; `/work PLAN-ID` copies the plan's, task lists set one per task). Before `fyr work` or `fyr eval`, check quota via `agy -p "/usage"` (spends no turn/quota).
- Report tier, model and remaining quota in one line per dispatch.
- Tiers (`agy models`): [pro] argon, [flash] flash (Gemini pool); [opus], [sonnet], [gpt-oss] (Claude/GPT pool).
- Model out: pick a peer in the same tier and state why. Tier or pool out: stop and ask, never downgrade.
