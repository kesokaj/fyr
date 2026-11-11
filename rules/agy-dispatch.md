---
trigger: always_on
description: "Agy dispatch: read the quota and choose from fyr models before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Never hardcode a model ID: resolve live IDs from `~/.config/fyr/models.json` via `fyr models --pick <role> [--tier pro|flash]` (`fyr models --refresh` refreshes from `agy models`). Before `fyr work`, set each Kickoff's `Model:` (or `--model`; `/fyr-work PLAN-ID` copies the plan's, task lists resolve one per task); before `fyr work` or `fyr eval`, check quota via `agy -p "/usage"` (spends no turn/quota).
- Cross-family routing: `builder` prefers active Gemini Carbon (`[pro]`), `mechanical` prefers active Gemini Flash (`[flash]`), and `judge`/`reviewer`/`verifier` route cross-family to active Anthropic Opus (`[pro]`) or Sonnet (`[flash]`). When Anthropic quota in `agy -p "/usage"` is `< 20%` weekly or `< 30%` 5-hour remaining, automatically fall back to the active equivalent-tier Gemini model and log the fallback reason.
- Report role, tier, resolved model ID and remaining quota in one line per dispatch.
- Model `gone` or pool out: pick the active peer `fyr models` ranks next in the same tier (or cross-family Gemini fallback for `judge`/`reviewer`/`verifier`) and state why. Both pools out: stop and ask, never downgrade tier.
