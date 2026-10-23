---
owner: main
date: 2026-10-23
---
# Research — session-sized work: splitting tasks so a session never fills its context   (2026-10-23)

Research record promoted from scratch on 2026-10-23 (operator: "go all approved"). Its items became plan `2026-10-23-session-sized-work.md`.

## Evidence

### This box's sessions (metrics store, copy in `.agents/scratch/egm-review/metrics/copy.db`, read-only; 49 main Claude Code sessions since 2026-09-01, sandbox labels excluded)

| Measure | Value |
|---|---|
| Peak context per session, median / p75 / p90 / max (tokens) | 573,527 / 822,796 / 943,841 / 967,274 |
| Sessions whose peak passed 200K / 400K / 900K | 38 / 29 / 9 of 49 |
| Sessions with at least one compaction (total compactions) | 13 (16); max 3 in one session |
| Tool calls per session, median / p90 / max | 288 / 646 / 842 |
| Corrections logged per session, no-compaction vs compacted sessions | 0.06 vs 0.08 (3 events in all: under-logged, not a signal) |
| Escaped bugs per session, either bucket | 0.00 |
| Handoffs per session | 0.75 vs 1.15 |

Reading: on 1M-window models the sessions here run at half a million tokens for most of their life and compact at about 967K (the Claude Code default for native-1M models). The local store cannot yet show a quality difference between short and long sessions: it records corrections and escaped bugs, and both are near zero in the window.

### Published evidence

- Chroma, "Context Rot" (July 2025, 18 models incl. five Claude): "model performance consistently degrades with increasing input length"; "even a single distractor reduces performance relative to the baseline"; LongMemEval, 306 prompts: focused prompts of about 300 tokens beat full prompts of about 113K tokens "across all models", the Claude family showing the largest gap.
- Anthropic, "Effective context engineering for AI agents": context is "a finite resource with diminishing marginal returns"; degradation is "a performance gradient rather than a hard cliff"; the three long-horizon levers are compaction, structured note-taking outside the window, and sub-agents that return a condensed summary; "do the simplest thing that works".
- Claude Code docs, Best practices: "Most best practices are based on one constraint: Claude's context window fills up fast, and performance degrades as it fills"; "Use `/clear` frequently between tasks"; after two failed corrections, "`/clear` and start fresh with a more specific prompt"; after a spec is written, "start a fresh session to execute it. The new session has clean context focused entirely on implementation, and you have a written spec to reference."
- Claude Code docs, context window: what survives compaction (CLAUDE.md and rules re-injected; up to five recently modified files re-read; skill bodies capped at 5,000 tokens each, 25,000 total); `/autocompact <tokens>` sets an earlier pass (100K–1M); `/context` shows the live breakdown to the USER; nothing tells the MODEL its own usage.
- Antigravity: `agy --continue` and `agy --conversation <id>` exist (`agy --help`); no compaction control found; community reports describe compaction as aggressive and opaque with no token meter. Unverified beyond those reports.

### What the harness already has

- Global context: checkpoints "before the context gets long or the session may end (`/handoff`)"; Definition of Done step 5 "recommend a fresh session for the next task"; "progress that lives only in chat or a compaction summary is lost".
- `handoff` Step 7: "Start a new session and paste: `Read the handoff at <path> and continue the work.`" and "recommend a fresh session (`/clear` on Claude Code, a new conversation on Antigravity) for the next task". The paste line is generic: it names no phase, no approvals, no check.
- `feature` Step 4: one phase per checkpoint; an in-progress note when "the context may compact, the session may end"; nothing sizes a phase to a session or ends the session at the checkpoint.
- Sub-agents for large reads (explorer at 10+ files, verifier, reviewer): the main thread still runs a median 288 tool calls.
- `/review --resume`: the cold-read test that a fresh session can resume from the trail. This is the quality gate the feature needs and it exists.
- The learning loop already holds this correction once: feedback.jsonl 2026-10-21 19:04 (crew): "Start fresh sessions to keep the context small, and run autonomously with as few questions as possible: standing approvals in the prompt, ask only outside them or for critical failures." Today is its second occurrence; the retro cadence says a repeated correction graduates into a mechanism.

### Resume cost, measured

A fresh session's resume order on this project reads about 124 KB (roughly 30K tokens) before work: `bugs/INDEX.md` 50 KB, the active plan 27 KB, project `AGENTS.md` 18 KB, the rendered global context 16 KB, the handoff 4 KB, three changes 8 KB. Splitting a 600K session into three 200K sessions costs about 90K tokens of re-reads and saves nothing in tokens; the gain is quality, not cost. `INDEX.md` is 40% of the bill and grows with every bug.

## Stress test of the premise

- What the operator is not seeing: the gain they felt comes from two things at once, a fresh window AND the forced write-down of state into a prompt. The harness already forces the write-down (plan, handoff). The unmeasured part is the fresh window. Without a quality signal in the metrics the claim "fewer hallucinations" stays the operator's observation plus the literature; the plan must add the measurement, not assume it.
- Strongest counter-argument: a split mid-phase loses in-flight state the handoff cannot carry; Anthropic's own guidance ("sometimes you should let context accumulate because you're deep in one complex problem") and the resume cost both say split only at checkpoints, never on a token count.
- The model cannot see its context fill. A rule keyed on "when the context is long" is unobservable from inside; a rule keyed on a structural boundary (phase checkpoint, task done) is observable every time. Claude Code gives the OPERATOR a gauge (`/context`, status line); Antigravity gives none that was found.
- Over-splitting: Trivial and Task classes fit one session by definition; the rule is for Feature plans and for sessions that take a second unrelated ask (the "kitchen sink" pattern).
- Cross-CLI: anything that depends on `/clear`, `/autocompact` or a status line is Claude Code only; the structural rule and the kickoff prompt work on both.

## Items

### 1. Add it as its own feature plan, run before the EGM tasks

Different subject from EGM, a design question in it (where the always-on rule lives), and it changes how every later task is run. The EGM items (standards and naive-reader lens into `feature`; the `--drift` lens; the ADR-056a decision) become its first session-sized phases and its first measurement.

### 2. The always-on rule and where it lives

Rule: a session is one Task or one plan phase. At the phase checkpoint (or at Definition of Done step 5) the session writes the handoff with a kickoff prompt and ENDS; the next phase starts in a fresh conversation. A second unrelated ask in a session gets its own kickoff prompt instead of being done in place. Where: `rules/session-sizing.md` (always-on on both CLIs; the Antigravity block has 4,662 bytes of room). The hand-written context has 71 bytes of headroom, so a sentence there means removing one.

### 3. The kickoff prompt

The handoff gains `## Kickoff`: the exact prompt for the next conversation, written for a reader with no context: project path, the plan path and the ONE phase to do, the standing approvals it may act on without asking (the 2026-10-21 correction's words), what must pass before it stops (the phase's Checkpoint evidence; for an unattended run, the `/goal` condition), and the instruction to end with the next kickoff. The handoff's Step 7 report prints it in a fenced block, after "run `/clear` (Claude Code) or start a new conversation (Antigravity), then paste:". File-drop change to `skills/handoff/SKILL.md` only; no `sh-agent` verb.

### 4. Phase sizing in the feature skill

A phase is what one fresh session can finish and verify; the plan's cold reader (Step 3) gets a fourth question: "can each phase be finished by a fresh session that reads only the plan and the files it names?" A number for "finish" is not invented here: the metric in item 5 sets the target and `/retro` tunes the sizing.

### 5. Measure it

The metrics store already holds peak context per session (B3). Add one report row, "main sessions whose peak context passed the target (default 300K)", and the per-session peak in `show --json`, so `/retro` sees the trend; keep corrections and escaped bugs beside it. A small code change in `internal/metrics` (Task). Starting target 300K is a proposal, not a finding; the operator picks it.

### 6. Resume cost

`bugs/INDEX.md` (50 KB) is read in full by every fresh session. Options: resume reads only its open rows (`sh-agent search --open` already exists for plans) or a short `## Open` head the full table follows. A separate Task; it halves the cost of every split.

### 7. Claude Code knobs the operator can set today, outside the harness

`/autocompact 300k` (compaction before the gradient bites instead of at 967K), a status line with context percentage, `/clear` then `claude --resume` when a session must be revisited. Not harness changes (settings are a non-goal); recorded in `deploy/local.md` § Operator steps if the operator adopts them.

### 8. Not proposed

A token-count trigger for the model (unobservable); automatic `/clear` (no CLI exposes it to the model; hooks are refused, ADR-007); a `sh-agent next` verb (the handoff's fenced block does the job without code); per-project compaction instructions in CLAUDE.md (the project root holds `AGENTS.md` alone).

## Sources

- https://www.trychroma.com/research/context-rot
- https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- https://code.claude.com/docs/en/best-practices · https://code.claude.com/docs/en/context-window · https://code.claude.com/docs/en/model-config · https://code.claude.com/docs/en/costs
- https://discuss.ai.google.dev/t/frustrated-with-aggressive-context-compaction-before-plan-execution/167983 · https://agentpedia.codes/blog/managing-context-antigravity-sessions (community, unverified)
- `~/.config/sh-agent/feedback.jsonl` entry 2026-10-21T19:04:11+02:00; `sh-agent metrics show --since 2026-09-01 --json`; `skills/handoff/SKILL.md` Step 7; `skills/feature/SKILL.md` Step 4
