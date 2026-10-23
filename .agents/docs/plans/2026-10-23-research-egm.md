---
owner: main
date: 2026-10-23
---
# Review — Google's Elephant-Goldfish Model (egm.dev) against this harness   (2026-10-23)

Research record promoted from scratch on 2026-10-23 (operator: "go all approved"). Its items became plan `2026-10-23-session-sized-work.md` Phases 3–5.

## What was read

- https://egm.dev/ and the repository github.com/google/elephant-goldfish-model, shallow clone at `.agents/scratch/egm-review/upstream/`, HEAD `d651fe86c12dccc5272274e002674fb8eec4c547` (2026-10-22, "Fix bootstrap prompt truncation and Claude transcript lookup (#114)").
- GitHub metadata (`gh api`): created 2026-09-13, 218 stars, 19 forks, 17 open issues, 0 releases, 0 tags, pushed 2026-10-23. `plugin.json` version 0.1.0. Apache-2.0. Footer: "not an officially supported Google product".
- Every file under `skills/`, `agents/`, `.claude-plugin/`, `resources/`, `docs/{claude,getting_started,harnesses,faq}.md`, `AGENTS.md`, `AI_POLICY.md`, `plugin.json`.
- Harness side: project `AGENTS.md` rules 1–6, `skills/VENDORED.md`, `internal/harness/inventory.go` (`agentKeys`, `SlashOnly`, permission keys), `derive.go` (`toolMap`), `size_test.go` budgets (run), the 2026-09-15 research record and its G-rows, ADR at `DECISIONS.md:63` (2026-09-03) and ADR-050, `skills/{feature,bug,spec,review,research}/SKILL.md` frontmatter and the feature skill's plan template and cold-reader brief, `agents/reviewer.md`.

## What EGM is

Seven skills (`egm-brainstorm`, `egm-draft`, `egm-review`, `egm-review-loop`, `egm-resolve`, `egm-drift-report`, `egm-bootstrap`), all `disable-model-invocation: true`; nine subagent personas in `agents/` (zoom-out, comprehension, design critic, implementation readiness, codebase details, structural, style, synthesizer, bootstrap orchestrator) plus nine Claude adapters in `.claude-plugin/agents/` (`tools: [Read, Grep, Glob]`, `model: inherit`, `omitClaudeMd: true`, each reading its persona at run time); five document standards (8.4 KB) in `resources/document_standards/`; a design-doc template (`skills/egm_draft/references/design_doc.md`); one `PreToolUse` hook that auto-allows Read/Grep/Glob inside the plugin cache; manifests for Claude Code, Codex, Antigravity, ChatGPT. Prose only, no script beyond the hook, no network call. Tests: pytest over YAML scenario files (required/forbidden regexes per turn) and an opt-in live Claude probe.

The skills read `agents/`, `resources/` and `docs/claude.md` at run time through `${CLAUDE_PLUGIN_ROOT}` (21 references). `docs/harnesses.md`: "Skill-only installers that copy `skills/` alone break EGM."

## Items

### 1. The install commands are a fourth way in

`claude plugin marketplace add google/elephant-goldfish-model` and `agy plugin install …` put files on a machine through a channel the map does not draw. Project `AGENTS.md` rule 1: three ways in, nothing else; rule 3: a new path is an ADR plus a map change in one commit. Global `AGENTS.md`: a skill found elsewhere goes into the checkout's `skills/`, then `sh-agent install`, never only into a vendor directory. The plugin route would also leave each machine on whatever `main` it fetched, with no pin and no `sh-agent status` line for it.

**Recommendation:** strike the plugin install as a mechanism. Whatever is taken enters through `skills/` and `agents/` here and reaches machines in a tag. (The operator may try the plugin in a sandbox home to see it run; that is a test, not a deployment.)

### 2. Verbatim vendoring is not possible

`VENDORED.md` defines verbatim as an empty `diff -r` against the pin. EGM's skills cannot run as copied: 21 `${CLAUDE_PLUGIN_ROOT}` references, personas and standards outside the skill directory, cross-skill calls by file path. In a harness install there is no plugin root; agents are self-contained files in `~/.claude/agents/` and derived for Antigravity. The nine Claude adapters carry `omitClaudeMd` and `license` keys that `agentKeys` refuses; the hook is unnecessary (nothing lives in a plugin cache) and the installer refuses `hooks` anyway.

**Recommendation:** fork, under harness names, or take pieces (item 4). Exception: the five document standards and the design-doc template are plain files with no plugin reference and can travel verbatim as `references/` with the Apache-2.0 `LICENSE` beside them, pinned by commit in `VENDORED.md`.

### 3. Wholesale adoption costs more than it adds

Two shapes were weighed. (a) Harness-native fork: seven personas become seven agents, the standards become references, seven skills (about 95 KB of prose) rewritten for paths, scratch location and interaction rules; then, by the release rule, one Antigravity run per changed skill and agent before a tag: 16 runs, plus both CLIs' smoke. (b) Catalog shape, as `google-catalog`: the whole tree pinned as data under one router skill that substitutes the root; personas read at run time by a general-purpose subagent (EGM's own fallback for hosts without named personas). (b) keeps the text verbatim and the pin honest, but the google-catalog refresh script is bound to its two sources (a new script or a generalisation is code, so a design question), and the reviewers would be whatever general-purpose agent each CLI offers, outside `derive.go`, so not the same on both CLIs.

The method itself is already here. The 2026-09-03 ADR (`DECISIONS.md:63`) names the elephant/goldfish repository as one of its two sources: design-before-code with an artifact-only goldfish, fix-or-rebut with a round cap, the four user gates. The 2026-09-15 research (a different, earlier repository, vshvedov/elephant-goldfish at b8ebb3d) adopted G04 (Decisions section and cold-reader box), G07, G10, G11, G13, G15, G16, G21, G22, G23, G25. The `bug` skill's Step 2 is a goldfish diagnosis; the `feature` skill's Step 3 is a goldfish read of the plan. `rules/critical-thinking.md` (ADR-050) matches EGM's `egm-review` Execution Constraints 3–10 almost sentence for sentence.

**Recommendation:** defer both shapes; adopt the deltas in item 4.

### 4. What is new since 2026-09-15, worth taking

- **Five document standards** (`zoom-out`, `feature-spec`, `implementation-readiness`, `heilmeier`, `re-implementation`; 8.4 KB). The cold-reader brief in `feature` Step 3 asks three questions in one paragraph; the standards give the reviewer a written rubric per question (zoom-out ↔ Scope, implementation-readiness ↔ Readiness, its "Zero-Decision Rule" is the harness's BLOCKING definition stated better). Land as `skills/feature/references/standards/` (verbatim, pinned) and one sentence in the Step 3 brief naming them.
- **The naive-reader lens** (`agents/comprehension.md`: undefined terms, acronyms, comprehension gaps). This is G27 of 2026-09-15, "later", with concrete text now. One clause in the cold-reader brief, no new agent.
- **A spec-vs-code drift lens** (item 6).

Not new, already present or rejected: the Decisions section, the no-code gate, review rounds with a cap, rebuttals needing a checkable reason, the anti-sycophancy rules, recon before the plan.

**Recommendation:** fix (adopt) the first two as one Task on `skills/feature/`; a behavioural run of the cold read on both CLIs is the tier.

### 5. The isolation gap is real and is a design question

EGM reviewers run with `omitClaudeMd: true`: no user, project or local instruction file reaches the goldfish (Claude Code 2.1.271+; this box has 2.1.309). The harness's `reviewer` cold read runs with the global context and rules loaded, because the key is not in `agentKeys` and ADR-056a refuses a restriction one CLI drops: Antigravity has no equivalent, so the derived agent would silently be less isolated than the Claude one. The gap matters most for the cold read of a plan written under the same context the reviewer then reads.

**Recommendation:** operator decides: (a) reopen ADR-056a for keys that only tighten a subagent, marking the Antigravity derivation as the documented gap (a dashed line, rule 4's honesty), or (b) accept the gap and record it in `agents/reviewer.md`. Either is an ADR; (a) is a code change in `inventory.go` and `derive.go`.

### 6. Drift report: a different lens from `--docs`

`/review --docs` is diff-scoped and `--project` lists stale references between docs (G06). EGM's `egm-drift-report` reads every spec under a directory, finds the code it names, and reports logic described but absent, models missing fields, hardcoded values where logic was planned, and architectural divergence; one report file, weekly cadence. The harness has `.agents/docs/specs/` and no sweep that checks them against code.

**Recommendation:** defer to its own Task: a `--drift` lens on `/review` over `<docs-root>/specs/` (and `plans/` with closed boxes), reviewer-dispatched, written to `reviews/`. New flag, so the review skill's docs and matrix rows change; no code.

### 7. Brainstorm swarm and the five-stage review pipeline

`egm-brainstorm` fans out six lenses; `egm-review` runs stages 5→9 with up to seven personas per stage and a synthesizer. The 2026-09-15 research struck these as G38 (three to six dispatches per use, no recorded need) and G37 (uncapped panels against the three-round cap). No incident since then shows the single cold read missing what a panel would catch.

**Recommendation:** strike, unless the operator sets a dispatch budget and names a plan where the cold read missed something a lens would have caught.

### 8. Bootstrap

`egm-bootstrap` writes a `README.md` with sentinel blocks into every directory of a tree, bottom up, and keeps them current. `explain-project` writes one `onboarding.md`; the global rule says comments say why, not what; per-directory generated prose drifts the moment code moves and is exactly what item 6 would then report.

**Recommendation:** strike.

### 9. EGM text that contradicts harness rules (to strip in any fork)

`resources/execution_instructions.md` and the skills' constraint blocks: scratch "under `/tmp/`" (harness: `.agents/scratch/`, never `/tmp`); "ONE QUESTION AT A TIME" and no "(Recommended)" prefix (the operator's protocol is one numbered table with a recommendation per row; `AskUserQuestion` puts the recommended option first); "NO SUMMARIES" (the Definition of Done ends in a report); "SILENT ENFORCEMENT / NO META-TALK: you MUST NOT mention … policies" (a concealment instruction; the harness leads with what is broken and never restates a failure as success). The anti-sycophancy items 3–10 duplicate `critical-thinking.md`.

**Recommendation:** fix in any fork; never vendor these lines. The standards and template (item 4) contain none of them (checked: no `/tmp`, no question rule, no silence rule in `resources/document_standards/` or `references/design_doc.md`).

### 10. Maturity and pin

Forty days old, 0.1.0, no release or tag, 17 open issues, a fix to Claude transcript lookup landed yesterday; Cowork and ChatGPT support carry open verification gaps (#105, #106). `VENDORED.md` requires a full 40-digit commit pin verified by `git archive <pin> <path> | tar -x` then `diff -r`, never a date; a verbatim Apache-2.0 file carries the `LICENSE` with it.

**Recommendation:** pin d651fe86c12dccc5272274e002674fb8eec4c547 for anything taken; re-pin only after reading the diff.

### 11. Budgets and CLI behaviour (no blocker)

Skill listing: 19,274 of 19,500 bytes, 226 bytes of headroom, but `TestSkillListingBudget` skips slash-only skills, so EGM-shaped skills would not count; references do not count. Antigravity block: 17,338 of 22,000; sixteen inventory names fit. Antigravity hides slash-only skills from the model (verified 2026-09-03, `verify-sandbox.md:37`). Claude's `Skill` tool refuses slash-only skills, so a skill that calls another must say "read its SKILL.md and follow it", which EGM already does. `toolMap` maps Read, Grep, Glob, Bash, Write, Edit: every EGM adapter tool derives. The harness has no slash-only skill today; the first one flips the inventory's `slashNote` sentence automatically.

### 12. Tests

EGM's unit tests are pytest over frontmatter, links and manifests, plus YAML scenarios with required and forbidden patterns per turn. The harness is Go stdlib only and tests behaviour with run kits (guide § 4). The scenario files are a usable seed for a run kit's assertions, not a tier that can run here.

**Recommendation:** defer; each adopted piece gets one behavioural run per CLI, logged under `testing/`.

## Sources

- https://egm.dev/
- https://github.com/google/elephant-goldfish-model (clone at `.agents/scratch/egm-review/upstream`)
- https://daily.dev/posts/introducing-the-elephant-goldfish-model-plugin-scaling-design-driven-ai-software-development-liy0vmgnv
- https://medium.com/@drensin/elephants-goldfish-and-the-new-golden-age-of-software-engineering-c33641a48874
- `.agents/docs/plans/2026-09-15-research-beads-elephant-goldfish-goldfish.md`, `.agents/docs/architecture/DECISIONS.md` (2026-09-03 ADR, ADR-050, ADR-056a)
