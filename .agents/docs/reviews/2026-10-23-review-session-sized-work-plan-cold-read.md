---
owner: reviewer
date: 2026-10-23
---
# Review — session-sized-work plan, cold read   (2026-10-23)

Range reviewed: plan file .agents/docs/plans/2026-10-23-session-sized-work.md at working tree

## Status: COMPLETE

## Verdict: REQUEST CHANGES — design needs revision; implementation not ready

Worst first: the Phase 1 behavioural run and its must-fail control cannot test the rule. The handoff skill is changed in the same phase to carry `## Kickoff`, and it is installed in both the control home and the test home, so the control's handoff carries a Kickoff with or without `rules/session-sizing.md`. Next: the plan treats Antigravity as having no context data, but the code stores and reads it (agy.go:228, show.go:344). The E2E metric criterion compares a 300K-target count with the research's 200K baseline (38 of 49). The rest are smaller gaps and missed restatements. No `REQUIREMENTS.md` violation found (R3 no-hooks, the shared rules block and the 22,000 pin are respected). Pre-flight `sh-agent search --refs ... --kind bug` returned nothing for the plan file; no recorded bug reintroduced.

## Findings (gaps, question 1)

- **[high / high]** Plan lines 59, 72, 69-72 (Phase 1 behavioural run and E2E "Edge" control) — the pass signal "handoff carries `## Kickoff`" is produced by the handoff skill's template (`skills/handoff/SKILL.md:69-84`, changed in Phase 1), not by the rule. Global DoD step 5 (`context/AGENTS.md:19`) already makes a Task end with `/handoff`, with or without the rule. The control "sandbox home without the rule" still installs the new handoff skill, so it will show a Kickoff and the must-fail state is not reached (or, if it is reached, it is by the model's luck). The rule's own behaviour (END the session; a second unrelated ask gets its own Kickoff, not done in place; "continue here" keeps the session) has no check. Fix: the control removes the handoff change as well, or the verify clause tests the rule's distinct behaviours (a fixture session given two unrelated asks must write a Kickoff for the second and not do it), with the condition proven in the control state first.
- **[high / high]** Plan lines 40, 51, 75 (Phase 2 test "agy `null`", Failure modes row) — misreads existing code. Antigravity context IS recorded: `internal/metrics/store.go:49-51` (migration 2 "Antigravity's gen_metadata"), `agy.go:228-231` writes `context`, `show.go:344-347` takes `ctx := st.ctx` for every non-Claude vendor, and `show_test.go:57` already pins `B.peak_context.agy` = 42000. A required test that `sessions_over_context_target.agy` is `null` contradicts the fixture's own data, or silently discards real data. What is nil for agy today is `compactions` (show.go:544-548), not context. Decide: agy gets a number (and the Failure-modes row and the Interfaces sentence "nil for a vendor with no context data" change), or agy is nil by decision with a reason. Two implementers will diverge here.
- **[high / high]** Plan line 60 (E2E "Edge") — baseline mismatch. "38 of 49" is sessions past 200K (`plans/2026-10-23-research-session-sizing.md:16`: 38 / 29 / 9 of 49 at 200K / 400K / 900K); the metric's default target is 300K (D4), so the pre-plan rate at 300K is not 38 of 49. `--since` takes a date (`cmd/sh-agent/main.go:586` YYYY-MM-DD), the plan and its research sessions share 2026-10-23, so "since the plan start" cannot isolate this plan's sessions; `--project` is also needed. A comparison of about 7 sessions against a 49-session rate is not a yes/no criterion either. The criterion also names `session_peaks` and `sessions_over_context_target`, which are Interfaces names, although the header says "independent of how Interfaces are built". Fix: compute the 300K baseline from the same store with the shipped flag, name the isolating filter (project plus `--since` the day after, or session ids), and state what "lower" means for n of about 7.
- **[medium / high]** Plan line 67 (Phase 1 verify) — `go test -race ./internal/harness -run '...'` expects "the test log line shows the rules block under 22,000". `size_test.go:51` emits that with `t.Logf`, which `go test` prints on a PASS only with `-v`. As written the clause cannot show its expectation. Add `-v`.
- **[medium / high]** Plan line 90 (Phase 5 verify) — a canary planted in the fixture's project `CLAUDE.md` tests project-level memory only. What the cold read must not quote is the global context (`~/.claude/CLAUDE.md` stub -> `AGENTS.md`) and `~/.claude/rules/*.md`, and the plan never checks that `omitClaudeMd` covers user-level files or rules. The only in-repo mentions of the key are the research record and DECISIONS.md; the plan asserts support from a version number (2.1.271) without a source. The run must plant the canary in the sandbox home's global files and prove the condition holds (control without the key quotes it) first.
- **[medium / medium]** Plan lines 44, 90, 93 (D10 option a) — "tighten-only key" is unspecified as an interface: `agentKeys` (`inventory.go:232`) is one flat list consumed by one lint (`inventory.go:270`). Is there a second list, a flag per key? `DeriveAgy` (`derive.go:40-71`) already writes only name, description, subagent, mainAgent, model and tools, so "derive.go drops it" needs no code change, only a test; `TestDeriveDropsTightenOnlyKeys` does not exist (grep: none). Also ADR-056a and `inventory.go:225-231` and the project `AGENTS.md` agent-keys rule all say a restriction one CLI drops is refused; they are restatements to change with the ADR (the plan names AGENTS.md and inventory.go; the ADR text and the comment must be named). Resolves only when D10 is answered; Phase 5 waits, so non-blocking now.
- **[medium / high]** Plan lines 16-20 (Surfaces) — restatements missed. `README.md:278` says rules/ has "one today: critical-thinking.md"; the plan lists only the project `AGENTS.md` Layout line (`AGENTS.md:43`). `context/AGENTS.md:19` (DoD step 5) says "recommend a fresh session for the next task", while the new always-on rule says END the session: two sources, two strengths. The context has 71 bytes of headroom (D2), so the edit must be size-neutral; the plan does not decide it. `README.md:264` (review lenses list) and `skills/review/SKILL.md:3` (description listing lenses; the 19,500-byte listing budget) also enumerate the lens vocabulary (`review` standards § 7); Phase 4's docs clause names only `argument-hint` and `skill-matrix.md`. `numbers.go:65` feeds `B.peak_context.*` to the dream's numbers-only summary: the plan neither adds nor excludes the new fields.
- **[medium / medium]** Plan lines 29-33, 36 (Kickoff "Standing approvals") — the previous session (an agent) authors the approvals; the operator's paste then makes them the operator's own turn. The plan bounds them only by example ("the phase's boxes, its checkpoint commit and push, this handoff's successor"). Nothing keeps a model from listing a `who: user` box, an `Open:` D-row, a deploy, or a destructive step. Global context: "Nothing proposed runs until they approve it in their own turn". Fix: the template lists a closed set of standing approvals and states that `who: user` boxes, `Open:` rows, releases and anything outside the repo are never in it. The rule must also say what a Kickoff-less case does in an unscaffolded project (global context: no handoff there).
- **[medium / medium]** Plan lines 42, 86 (`--drift`) — no sizing. The default scope is five specs, 84,780 bytes (`.agents/docs/specs/sh-agent/`), one `reviewer` dispatch with `maxTurns: 40` (`agents/reviewer.md`), and the brief asks for every command, flag, file, field and shape per spec. A cut-off is the likely outcome, which is the very failure this plan exists to prevent. Not decided: a tool-call budget in the dispatch (Step 3 requires one below maxTurns), one dispatch per spec vs one total, and what the "named paths" are (spec files, directories, or code paths; `delivery.md` states release policy, which has no code to diff against). Also `--all`'s definition ("every lens but `--resume`", SKILL.md:27) must also exclude `--drift`; the plan says "runs alone" but does not list that line.
- **[low / high]** Plan lines 28-35 — the template block is a ```text fence nested in a ```markdown fence; as written the inner fence closes the outer one in `SKILL.md`. Use a longer outer fence. `Step 7 item 3` (summary) renumbering after "item 4 is folded" is unstated.
- **[low / high]** Plan line 69 vs D5 — Phase 1's box offers two outcomes. Phases land on main continuously, and checkout places follow main, so a skill that names rubric paths before Phase 3 points at files not installed. The default must be stated at the box: Phase 1 adds (4) and the naive-reader clause only.
- **[low / medium]** Plan lines 40, 75-76 — details an implementer will otherwise discover by failure: `metricsFlagsOutside` (`cmd/sh-agent/main.go:553`) has a hard-coded `own` set, so `context-target` must be added there and refused by `ingest` and `export`; the human `show` table (`show.go:660`) has a "peak context" row the plan does not extend; compaction count is a vendor total (`show.go:252`, `acc`), so `SessionPeak.Compactions` needs a per-session counter; replayed steps skipped by `s.seen` (show.go:316-321) affect peaks and the plan does not say per-session peaks follow the same dedupe; the usage lines at `main.go:68-69` and `508` need the flag.
- **[low / medium]** Plan lines 80, 93 — Phase 3 vendors third-party prose that a reviewer then follows as a rubric. `skills/VENDORED.md:5-8` requires a read for steps that send data out before vendoring; the plan relies on the research's remark and gives no clause or verify step. Phase 6 declares `security` not due on "prose". A single read-through box with its result is cheap.

## Scope (question 2)

Built beyond what `## Why` names, with a D-row asking or not:
- `--context-target N` flag with validation, on two verbs (D4 says "by: the user's approval of item 5", but the user said "you pick"; the flag is configuration for a value that never varies in the request). Gap unless the operator asked for tunability; the simplest build is a constant 300000.
- Three new `Efficiency` fields for one fact: `SessionPeaks` plus `ContextTarget` already let a reader compute `SessionsOverContextTarget`. A second shape for the same operation. `SessionPeak.Compactions` and `Date` are not requested (D6 covers privacy, not need).
- `--drift`'s four finding shapes, the `<scope>` slug rule and the "named paths" option (D7, D8 cover the default and the refusals, not the paths option or the fourth `unspecified:` shape; research-egm item 6 may; not cited).
- Phase 6 conditional security dispatch tied to D10; fine, but it is a decision recorded as a clause, not a D-row.
All other items trace to D1, D2, D3 and the operator's quoted words.

## Readiness (question 3): questions an implementer would have to ask

BLOCKING
1. What does the Phase 1 must-fail control remove, so that the absence of a Kickoff is caused by the rule and not by the handoff skill? (finding 1)
2. Does Antigravity get a number or `null` for `sessions_over_context_target`, given the code stores Antigravity's context? (finding 2)
3. What is the pre-plan baseline at 300K, and how are this plan's sessions isolated when `--since` takes a date? (finding 3)
4. (BLOCKING for D10 option a only; Phase 5 waits) What does the canary show, and does `omitClaudeMd` cover user-level files and rules? (finding 5)

NON-BLOCKING (a default is stated or a reasonable implementer decides)
5. `-v` on the Phase 1 verify command (add it).
6. How tighten-only keys are represented in `agentKeys` (a second list, or a comment); decide with D10.
7. The size-neutral edit to `context/AGENTS.md:19`, and README:278, README:264, the review description, `numbers.go`.
8. A closed list of standing approvals in the Kickoff template; the Kickoff in an unscaffolded project (default: none, as the global context says).
9. `--drift` dispatch budget, one dispatch vs per spec, what "named paths" are (default: one dispatch, spec files, 30-call budget).
10. Nested fence in the template; Step 7 numbering.
11. D5 default (Phase 1 omits the paths).
12. Flag plumbing, human `show` row, per-session compaction counter, dedupe parity, usage lines.
13. Which phase serves as the single Antigravity golden-path run ("a one-box phase": Phase 3 or 4).
14. Whether the BUG-022 read is a box in Phase 3.

## Criteria (plan-level)
1. Dead code — pass: nothing to remove; Phase 5(a) states `derive.go` as a surface while it needs only a test (finding 6).
2. Flow tracing — fail: the flag does not reach `own` in main.go, the agy context path (finding 2), `numbers.go` consumers, `--all` definition.
3. Standards conformance — fail: restatements missed (README:278, README:264, context line 19, ADR-056a text); verify clauses that cannot show their expectation (finding 4); E2E names implementation.
4. Directory layout — pass: `rules/`, `skills/feature/references/`, `internal/metrics` follow `AGENTS.md` Layout.
5. Simplicity (DIY-except-security) — fail on one point: flag plus three fields for one fact (Scope). No crypto or auth in scope.
6. Decoupling — pass: seams are separate phases; Phase 2 touches only `internal/metrics` and `cmd`.

## Not examined
Whether `omitClaudeMd` exists or covers user-level files in Claude Code 2.1.309 (no source in the repo; no run made). The upstream EGM standards' content (not in the tree yet). The behavioural kit scripts in `testing/guide.md` § 4 beyond a grep of their headings. `internal/metrics/report.go` HTML code and the metrics tests beyond show_test.go:57. Whether Antigravity actually loads a second rules file (REQUIREMENTS.md:55 measured only the context plus one rule).

design needs revision; implementation not ready
