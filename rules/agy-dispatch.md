---
trigger: always_on
description: "Agy dispatch: read the quota and choose from fyr models before fyr work or fyr eval; a tier out stops and asks."
---

# Agy dispatch

- Never hardcode a model ID: resolve live IDs from `~/.config/fyr/models.json` via `fyr models --pick <role> [--tier pro|flash]` (`fyr models --refresh` syncs `agy models`). Before `fyr work`, set each Kickoff's `Model:` (or `--model`; `/fyr-work PLAN-ID` copies the plan's, task lists resolve one per task); before `fyr work` or `fyr eval`, check quota via `agy -p "/usage"` (spends no turn/quota).
- Cross-family routing: `builder` prefers active Gemini Carbon (`[pro]`), `mechanical` prefers active Gemini Flash (`[flash]`), and `judge`/`reviewer`/`verifier` route cross-family to active Anthropic Opus (`[pro]`) or Sonnet (`[flash]`). When Anthropic quota is `< 20%` weekly or `< 30%` 5-hour remaining, fall back to the active same-tier Gemini model and log why.
- Subagent guard: omit `Model` or pass `inherit` on `invoke_subagent` for `reviewer`, `verifier`, `security`, `dreamer`, and `bugs` (`PreToolUse` blocks non-`inherit` overrides).
- Report role, tier, resolved model ID and remaining quota in one line per dispatch.
- Model `gone` or pool out: pick the next active same-tier peer from `fyr models` (or cross-family Gemini fallback for `judge`/`reviewer`/`verifier`) and state why. Both pools out: stop and ask, never downgrade tier.
