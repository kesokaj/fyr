---
trigger: always_on
description: "Agy dispatch: read the quota and choose by tier before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Before scheduling `fyr work`, choose each worker's model by tier: a `Model:` line, else `--model`. The `/work` lead copies the plan's and adds none. Before that and before every `fyr eval`, read the quota: `agy -p "/usage"` (no turn, no quota spent).
- State the tier, the model and the quota left in one line per dispatch.
- Tiers (IDs: `agy models`): [pro] argon, [flash] flash (Gemini pool); [opus], [sonnet], [gpt-oss] (Claude and GPT pool).
- A model out: a similar model in the same tier, named with why. A tier out: stop and ask, never a weaker tier. Quota is per pool, so a pool out is every tier in it.
