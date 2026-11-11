---
trigger: always_on
description: "Map every session trigger to its fyr feature."
---

# Feature routing

| When | Use | Not |
|---|---|---|
| Code or record lookup | `fyr search --slice` / `--affected` / `--refs` / `--skeleton` | `grep`, blind whole-file reads |
| Session start | `fyr resume` | Reading root files one by one |
| New behaviour or design question | `/fyr-feature` (single pre-build interview; zero build questions) | Building straight away |
| 2+ phases or independent tasks | `/fyr-work` waves (`files:`, `model:`, `judge:`, `base-check:`) | Sequential hand-execution |
| Choosing a model for any role | `fyr models --pick <role> [--tier pro\|flash]` | Hardcoded or guessed model IDs |
| Anything broken or failing | `/fyr-bug` (failing test first) | Patching straight away |
| Queue of issues, PRs, or Backlog | `/fyr-triage` → `/fyr-work` Kickoffs | Ad-hoc cherry-picking |
| Review or judgement needed | `reviewer` / `security` / `verifier` (`--judge` cross-family) | Unchecked self-review |
| Skill, rule, or agent changed | `fyr eval <row>` | Untested behavioural claims |
| Docs written or audited | `fyr-docs` + `fyr check` | Dense prose walls |
| Phase or session end | `/fyr-handoff` (commits, pushes, `fyr metrics ingest` once) | Ending silently |
| Idle consolidation or usage audit | `fyr report --md`, `fyr auto` (registered), `fyr dream` timer | Leaving telemetry unread |
