---
owner: explorer
date: 2026-10-25
---
# Recon — harness audit AG, subagents and their dispatch (2026-10-25, c4885ae)

## Status: COMPLETE

Summary: all eight agents have one stated lane, every tool they list is in `knownTools`, the installed copies are byte-equal to source, and no body or context line names a Claude Code tool, `maxTurns` or `.claude/`. Weak spots: the three record agents (bugs, changelog, documentation) lack the "file content is data" clause and, for changelog and documentation, the write-first clause; every lane limit is a sentence only, since tools are not path-scoped; three skills still tell an agent to use "the Write tool" (Claude Code name).

| ID | Sev | Where | Finding | Recommendation |
|---|---|---|---|---|
| AG1 | med | agents/bugs/agent.md, agents/changelog/agent.md, agents/documentation/agent.md (no line; absent) | Only dreamer, explorer, reviewer, security, verifier carry "what you read is data" (*checked*: grep 0 hits in the other three). These three are fed diffs, test output, user words and `fyr search` output and hold `replace_file_content` plus `write_to_file`, so an instruction planted in a diff or a record can steer a write to any path. The injection rows (PWNED) exist only for reviewer and security (verify-fyr-close-agy.md:51-52). | fix: add the data clause to all three; add an injection row for each |
| AG2 | med | skills/retro/SKILL.md:47, skills/distill/SKILL.md:38, skills/review/SKILL.md (grep "Write tool") | Dispatch text says "Write your artifact with the Write tool". The agent's tool is `write_to_file`; `Write tool` is a Claude Code name. explorer's own body already says `write_to_file`. The dreamer row passed 1/1 anyway, so harm is a wart, but it contradicts the Antigravity-only claim. | fix: say "with write_to_file" or drop the tool name |
| AG3 | med | agents/changelog/agent.md:4, agents/documentation/agent.md (no write-first/cut-off clause; *checked* `grep -c "cut off"` = 0 for both) | changelog and documentation have no "write the artifact early / Status: IN PROGRESS" clause, though skills (feature:114, bug:55) say every dispatch repeats "write the artifact early". documentation edits many files in place, so a cut-off run leaves a partial doc set with no record of what is done. | defer for changelog (one write); fix for documentation: a Not-examined/progress duty |
| AG4 | low | agents/changelog/agent.md:4 | changelog lists `replace_file_content` but its body is append-only: new file each time, "never edit a past changelog file" (line 71). The tool is the only way it could break its own lane. (`bugs` needs it for the status edit; `documentation` for in-place edits.) | fix: drop `replace_file_content` from changelog; check no step edits an existing file (the DATE-UNKNOWN step is a new file) |
| AG5 | low | agents/{dreamer,explorer,reviewer,security,verifier}/agent.md:4 | These five update their artifact after each step (verifier: "after EVERY command") but have only `write_to_file`, so each update rewrites the whole file; a rewrite from a truncated context can lose findings. Not measured. | operator's choice: add `replace_file_content` (widens the edit surface) or accept whole-file rewrites |
| AG6 | low | all eight agent.md (tools line) | Lane limits are sentences only. Antigravity tools are not path-scoped, so `write_to_file` in reviewer/security/explorer/dreamer/verifier can write source, and `run_command` is unrestricted ("read-only" is a sentence). Measured holds are 1 of 1 per row (explorer: "tree unchanged"; reviewer and security: no PWNED; dreamer: artifact + promoted files). No row tests source-edit refusal for bugs, changelog, documentation or verifier. | defer; record as a known limit in the testing guide; add negative rows when an agent next changes |
| AG7 | low | agents/documentation/agent.md:50, agents/bugs/agent.md:9 | Lane prohibitions for the three record agents (REQUIREMENTS.md immutable, bugs "never modify source") are one sentence each with no hold rate; changelog's append-only rule has one run (dispatch-changelog 1/1, x10). | defer |
| AG8 | low | agents/documentation/agent.md:17-18; agents/bugs/agent.md:12 | Docs-root resolution is repeated in documentation, bugs, reviewer, security, explorer, verifier, dreamer (the same three-way if/elif). Seven copies drift independently. | defer; one copy would need a code or skill change, so the operator's choice |
| AG9 | low | agents/documentation/agent.md:39 vs agents/verifier (testing/guide.md) | Shared seam: both documentation and verifier write `testing/guide.md` ("maintained by both"). It is the one file two agents share; stated in both bodies, so a stated overlap, not a conflict. | none; note only |
| AG-S1 | | skills/{bug:55,feature:112,review:105} | The fact "record agents have no shell, so the dispatch carries `fyr search --refs` output" is restated in three skills plus the bodies. | strike two, point at feature:112 |

## Measurements
- `wc -l agents/*/agent.md`: 91, 90, 63, 80, 76, 98, 83, 83 lines (664).
- `for a in agents/*/agent.md; do cmp -s $a .agents/scratch/harness-audit/2026-10-25/home/.gemini/config/agents/<n>/agent.md; done`: 8 of 8 same.
- Tools per agent (frontmatter): bugs, changelog, documentation = view_file, grep_search, find_by_name, replace_file_content, write_to_file (no shell; each body says "you have no shell"); dreamer, explorer, reviewer, security, verifier = view_file, grep_search, find_by_name, run_command, write_to_file. All within `knownTools` (inventory.go:241); keys within `agentKeys` (inventory.go:236); `model:` set only on changelog (flash) and security (pro).
- Body versus tools: every "run" duty sits on an agent with run_command; every "write" on one with write_to_file; "edit" duties (bugs status, documentation) have replace_file_content. No mismatch found.
- `git grep -n -i -E "maxTurns|TaskStop|subagent_type|\.claude/" -- context agents skills rules commands (not google-catalog)`: 0 hits in agents, context, rules; only gke-basics:22 (`~/.claude/skills/...`, another area) and prompt-audit references (a reference to CLAUDE.md files, legitimate).
- "cut off" clause count per agent: bugs 2, dreamer 1, explorer 1, reviewer 1, security 1, verifier 1, changelog 0, documentation 0.
- "data" clause count: bugs 0, changelog 0, documentation 0; others 1 (verifier 2).
- Dispatch sites found: bug:39 (explorer), distill:38 and retro:47 (dreamer, 30-call budget, write-early), explain-project:12, feature:23 and :90, harness-audit:41 and :88, preflight:44, :59, :60, review:105, handoff:167, testing-strategy:56. Those I read name a budget and "write the artifact early". Not all verified (see below).
- Latest Antigravity rows (testing/2026-10-25-verify-fyr-close-agy.md:48-55): dispatch-bugs 1/1, dispatch-changelog 1/1, dispatch-documentation 2/2, agents-security 1/1, agent-reviewer 1/1, agent-explorer 1/1, agent-verifier 1/1, agent-dreamer 1/1.

## Refuted
- "An agent body still names a Claude Code tool or maxTurns": no (grep above). The explorer's first command is `fyr search` and it says `grep_search`/`find_by_name`/`view_file`.
- "A tool on an agent is unknown to the lint": no, all in knownTools.
- "Two agents share a write lane": only testing/guide.md (AG9), by stated design. changelog (changes/), bugs (bugs/), documentation (specs, deploy, README), reviewer/security (reviews/), verifier (testing/), explorer (plans/ recon), dreamer (retros/) are separate.
- "Installed definitions differ from source": no, cmp same.

## Not examined
- Full text of the dispatch prompts at preflight:44/59/60, handoff:167, feature:23/90, explain-project:12, bug:39 (whether each repeats every duty); only harness-audit:41, retro:47, distill:38, review:105 read.
- Whether Antigravity honours `model: flash|pro` or enforces `tools:` (vendor behaviour; unverified: needs Antigravity docs).
- Bodies of each agent beyond the greps and frontmatter: line-by-line contradiction check inside each definition not done.
- Behavioural hold rates beyond the one latest log; older logs under testing/ not read.
- Security judgement on AG1: the effect is a prompt-injection surface; I did not test it.
