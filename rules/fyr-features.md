---
trigger: always_on
description: "Map every session trigger to its fyr feature."
---

# Feature routing

| When | Use | Not |
|---|---|---|
| Code or record lookup | `fyr search --slice` / `--affected` / `--refs` / `--skeleton` | `grep`, whole-file reads |
| Session start (scaffolded) | `fyr resume` | Reading root files one by one |
| New behaviour or design question | `/fyr-feature` (`ask_question`) | Building straight away |
| 2+ phases or independent tasks | `/fyr-swarm` | Sequential hand-execution |
| Choosing a model for any role | `fyr models --pick <role> [--tier pro\|flash]` | Hardcoded or guessed model IDs |
| Anything broken or failing | `/fyr-bug` | Patching straight away |
| Queue of issues, PRs, or Backlog | `/fyr-triage` -> `/fyr-swarm` | Ad-hoc cherry-picking |
| Review or judgement needed | `reviewer` / `security` / `verifier` (`--judge` cross-family) | Unchecked self-review |
| Skill, rule, or agent changed | `fyr eval <row>` | Untested behavioural claims |
| Docs written or audited | `fyr-docs` + `fyr check` | Dense prose walls |
| Phase or session end | `/fyr-handoff` | Ending silently |
| Idle consolidation or usage audit | `fyr report --md`, `fyr auto`, `fyr dream` | Leaving telemetry unread |
