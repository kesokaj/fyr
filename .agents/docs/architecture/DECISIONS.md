# Architecture Decisions

Append-only. To reverse a decision, append a new ADR and mark the old one `Superseded by ADR-NNN` — never rewrite it. The ADRs of the previous design (a hook-driven Go harness) were removed with it in commits 09fc5cc and 20a5abd; they remain readable in git history.

Numbers are not in date order: ADR-049 sits before ADR-048, ADR-056a after ADR-057, and ADR-061 (2026-10-23) before ADR-060 (2026-10-24); ADR-059 was reserved and stays unused (ADR-062). The next ADR takes one past the highest number in this file.

## ADR-001: The harness is context, skills, and subagents — no hooks, no gates     (2026-09-03) — Accepted

**Context:** The previous harness enforced its Definition of Done with lifecycle hooks, a commit gate, scope digests, and a dispatch ledger. It was reverted the same day it was finished: the machinery cost more attention than the rules it enforced were worth for a solo operator.

**Decision:** Behaviour lives in prose: the global context (`context/AGENTS.md`), the skills, and the subagent definitions. Nothing runs on tool calls or session events. The only executable is the installer.

**Consequences:** Rules are followed because they are read, not because they are enforced. A rule that is not followed is fixed by rewriting the rule (`/retro`), not by adding a gate. Both vendors are served by identical mechanisms, since neither needs hook support.

## ADR-002: One Go binary, `styrman`, with a git resync loop     (2026-09-03) — Accepted; "stdlib only" superseded in part by ADR-045

**Context:** The operator works on several machines and wanted to push a harness change from one and pick it up on another without thinking. A bash script was considered first; it was replaced by Go so that `styrman update` (fetch, pull, rebuild, reinstall) and a manifest could be tested against a scratch home rather than by hand.

**Decision:** `cmd/styrman`, stdlib only. `install` writes an atomic copy of every managed file and records the manifest in `~/.config/styrman/state.json`; `update` fast-forwards the repo and re-runs `go run ./cmd/styrman install`, so a code change in the repo takes effect in the same update; the binary copies itself to `~/.local/bin/styrman`.

**Consequences:** Go is required on every machine (already true for the operator). Two copies of each skill exist on disk, one per vendor — a deliberate trade against symlinks, which neither CLI documents following across directories. The binary's self-copy mechanism is superseded by ADR-013.

## ADR-003: Single source; Antigravity agents are derived at install     (2026-09-03) — Accepted

**Context:** The previous layout committed generated copies (`claude/CLAUDE.md`, `antigravity/GEMINI.md`, derived agents) that needed a drift check to stay honest.

**Decision:** `context/`, `agents/` (Claude Code format), and `skills/` are the only sources. `styrman install` assembles the context (AGENTS.md + dev-setup.md + generated inventory) and translates agent frontmatter for Antigravity in memory. Nothing generated is committed.

**Consequences:** No drift is possible between committed copies. The translation table (`toolMap`, `modelMap` in `derive.go`) is the one place vendor differences live; an unmapped tool fails the install instead of shipping a name Antigravity would hang on.

## ADR-004: Uninstall is manifest-only; hand-installed skills survive     (2026-09-03) — Accepted

**Context:** The previous installer mirrored whole directories and pruned anything the repo did not have, which wiped skills installed by hand with `npx skills add -g`. The operator wants adding skills by hand to stay easy.

**Decision:** `install` records every file it writes; `uninstall` and the resync step remove only paths from that manifest. A `~/.claude/CLAUDE.md` without the styrman marker is never overwritten. `styrman adopt` copies a hand-installed skill into the repo when it should become permanent.

**Consequences:** Directories under `~/.claude/skills` may hold both styrman's and the operator's skills; `status` reports only styrman's. A skill installed by hand into a vendor directory is not versioned and is not deployed to the other vendor until adopted.

## ADR-005: The context is named AGENTS.md everywhere     (2026-09-03) — Accepted

**Context:** The operator asked for the agents.md standard. Claude Code reads `CLAUDE.md`; Antigravity reads `AGENTS.md` or `GEMINI.md`.

**Decision:** The deployed file is `~/.claude/AGENTS.md` and `~/.gemini/config/rules/AGENTS.md`. `~/.claude/CLAUDE.md` and a project's `CLAUDE.md` are the single line `@AGENTS.md`. `/init-project` creates `.claude/{skills,agents,commands}` as symlinks into `.agents/`, matching what the skills.sh CLI does on its own.

**Consequences:** One canonical name at every level. Claude Code's import adds one hop; it is documented to load user-level imports without prompting.

## ADR-006: The binary is `sh-agent`; GCP defaults are asked for, never stored     (2026-09-03) — Accepted

**Context:** The operator asked for a less repo-specific binary name and, on reviewing `context/dev-setup.md`, rejected stored GCP defaults: a placeholder region or registry name is exactly the kind of value an agent will silently assume. They also asked for zero scripts in the repo.

**Decision:** The binary, its state directory (`~/.config/sh-agent`), its env var (`SH_AGENT_REPO`), and its file marker are named `sh-agent`; the repo keeps its name. The context says work is local by default and, when the user says GCP, the agent asks for project, region, Artifact Registry repository, service name, and visibility before doing anything, then records them in the project's `AGENTS.md`. The one shell script in the repo (`mermaid-syntax/scripts/validate-mermaid.sh`) was deleted along with the old hook scripts under `~/.agents/bin`.

**Consequences:** ADR-002's name is superseded by this one; its content stands. The first GCP action in a new project costs one question instead of one wrong assumption. Skills that need helper code ship it as documentation, not as scripts.

## ADR-007: Hooks stay out — researched and rejected     (2026-09-03) — Accepted

**Context:** After the rebuild the operator considered three hooks (a deploy gate for documentation freshness, a wrong-model warning, a loop protector). The options for both vendors were researched in `.agents/docs/plans/2026-09-03-research-hooks.md`: all three are buildable as `sh-agent hook` subcommands, but the Claude side requires a managed block in `~/.claude/settings.json`, and every hook adds a way for the harness to be wrong at run time.

**Decision:** No hooks. The operator's words: "Skip the hooks. I think this was my problem before. Keep the no hooks/scripts rule." R3 (no hooks, no gates, no CI) and the zero-scripts rule stand as written; the research is kept as the record of what was weighed.

**Consequences:** The Definition of Done, the model choice, and loop avoidance remain prose in the global context, enforced by reading. If a rule is not followed, the fix is `/retro` and a better rule, never a gate. Reopening this needs a new ADR that answers the research's cost section.

## ADR-008: Bugs are the project's memory; scratch lives in the project; the trail carries progress     (2026-09-03) — Accepted

**Context:** On reviewing the global context the operator asked for: a bug subagent that numbers every failure so the same mistake is not made twice; a project-local scratch space instead of `/tmp`; explicit checkpointing so resume has something to resume from; clear steps for plans, phases, long-running work, testing, verification, and e2e. Two sources were read in full for strategy — Böckeler's harness engineering article and the elephant/goldfish repository (see `.agents/docs/plans/2026-09-03-research-harness-strategies.md`).

**Decision:** Every failure gets a `BUG-NNN` record under `.agents/docs/bugs/` with a prevention rule, indexed in `INDEX.md`, written by the new `bugs` agent and read at resume; a rule that recurs graduates into a project rule or a test. All generated and temporary files go to `.agents/scratch/` (gitignored). Work is classified as trivial, task, feature, or bug; a feature has a plan file with phases and task checkboxes, a cold-reader check of the plan by a fresh `reviewer` before any code, one phase at a time, and each phase ends in a checkpoint (tick, changes entry, commit, push). A bug gets a goldfish diagnosis on symptom and repro only and a failing test before the fix. Testing has four named tiers (unit, integration, e2e, cloud smoke) and e2e is required before "done". The Definition of Done is an ordered sequence. The harness's own state lives in `~/.config/sh-agent/`. Adopted from the sources: guides and sensors as the vocabulary, the steering loop as the retro cadence, design-before-code with an artifact-only goldfish, fix-or-rebut with a round cap, the four user gates. Rejected: docs-in-chat, per-project rewritten command templates, and hooks (ADR-007).

**Consequences:** More is written to disk per task, and it is what makes the next session and the next agent cheaper. Bug records make repeated mistakes visible and give `/retro` concrete input. The project layout gains `docs/bugs/` and `scratch/`; `/init-project` creates both. Seven agents instead of six.

## ADR-009: The context stays short; procedures are skills; the reviewer is tiered     (2026-09-03) — Accepted

**Context:** The operator asked for an honest opinion of the rebuilt harness. The deployed context had grown to 200 lines (about 5k tokens every session). Claude Code's own guidance is that bloated instruction files make the model ignore the actual rules and that workflows relevant only sometimes belong in skills; Anthropic's context-engineering guidance treats context as a finite resource with diminishing returns. Every Opus review on a routine diff was also the largest recurring cost.

**Decision:** `context/AGENTS.md` is cut to under 70 lines with every rule kept. The feature and bug procedures become the `feature` and `bug` skills, model-invocable so they load themselves when the work matches, and callable as `/feature` and `/bug`. The Definition of Done keeps its five-step order in the context; the detail lives in the agents. The embedded inventory lists names only, since both CLIs inject descriptions themselves. A compaction instruction names what must survive. The `reviewer` runs on Sonnet and reports only correctness, security, requirement, and rule findings; `/preflight` dispatches it on the strongest model. Changelog entries name the agents dispatched so cost can be counted at the first retro. The remaining improvements (pruning skills, the Mac live check) are the operator's, and no further tuning happens before real use.

**Consequences:** About 3.5k fewer tokens per session before the first prompt. Procedures now compete for nothing until they are needed. The risk moves from "rule lost in a long file" to "skill not triggered"; the description of each procedural skill is therefore written as a trigger list, and the context keeps a one-line pointer per kind of work. The split between slash-only and auto-triggering skills is superseded by ADR-014.

## ADR-010: Harness state stays outside the project and is approved, not pre-authorised; the skill matrix is a documented procedure, not a script     (2026-09-03) — Accepted, superseded in part by ADR-025

**Context:** The sandbox matrix showed that Claude Code denies headless access to `~/.config/sh-agent/` (feedback log, retro proposals, global handoffs) and prompts for it interactively. Two ways out were on the table: have `sh-agent install` add the directory to `permissions.additionalDirectories` in `~/.claude/settings.json`, or move the state into projects. The matrix itself is a bash driver, prompts, and checks under gitignored scratch, which exists on one machine only.

**Decision:** Neither escape hatch. `REQUIREMENTS.md` names editing vendor settings a non-goal and R7 fixes the state's location; the one prompt per session is accepted and documented (README, `deploy/local.md`), with the user-side setting named for those who want it gone. The matrix is recorded as documentation — `testing/skill-matrix.md` lists every scenario's prompt and check and the invocation flags — so any session on any machine can rebuild the kit in scratch; the scripts are not committed, keeping "the harness is context, skills, and subagents" true.

**Consequences:** One approval per Claude Code session that writes feedback or a proposal; nothing installed outside the customization directories. Re-running the matrix costs a few minutes of setup from the doc instead of a `git pull`; the trade is deliberate, and a Go `sh-agent sandbox` subcommand remains possible if the matrix becomes routine.

## ADR-011: GCP defaults are asked for once and recorded in the project's deploy guide     (2026-09-03) — Accepted, supersedes the "never stored" half of ADR-006

**Context:** ADR-006 rejected stored GCP defaults because a placeholder region or registry in the global context is exactly what an agent silently assumes. The operator then asked for per-project deploy guides (local, remote, GCP) that the agent both reads and maintains. Asking the same five questions in every session is friction with no safety gain once the values are the project's own.

**Decision:** The global context still stores nothing and still asks before the first GCP action. Once the user answers, the values are recorded in the project's `.agents/docs/deploy/gcp.md` (project ID, region, Artifact Registry repository, service, visibility), the project `AGENTS.md` only points there, and `cloud-run-deploy` reads that file first. ADR-006's naming decision stands; its "never stored" clause is superseded for project-level storage only.

**Consequences:** One question per project instead of one per session; the recorded values are reviewable in the project's own trail and never leak across projects. A missing or empty `gcp.md` means ask, never guess.

## ADR-012: Selective vendor deployment via `--vendor`     (2026-09-04) — Accepted

**Context:** The installer previously deployed unconditionally to both `~/.claude` and `~/.gemini/config`. When operating on a machine dedicated to Antigravity (or Claude Code only), writing unneeded vendor directories pollutes the home environment and generates unused files.

**Decision:** `sh-agent` supports `--vendor <all|antigravity|claude>` (with `agy` as an alias for `antigravity`). When a specific vendor is selected, `Plan` generates files only for that vendor's customization directory plus the harness binary (`~/.local/bin/sh-agent`). The active vendor is saved in `state.json` so `status`, `diff`, and `update` default to the installed vendor without re-specifying the flag. Omitting the flag on a fresh install defaults to `all`, preserving backward compatibility.

**Consequences:** A machine with only Antigravity installed remains clean with zero `.claude/` directories created. If the operator later chooses to switch vendors or deploy `all`, `Install`'s resync cleanly handles additions or deletions.

## ADR-013: The installed binary rebuilds from source; never copies itself     (2026-09-13) — Accepted

**Context:** BUG-006 — the real home's `~/.local/bin/sh-agent` stayed at the 2026-09-03 build because `install` copied the running executable, and the running executable was the installed copy. A `go run` binary carries no VCS stamp so `sh-agent version` printed `(devel)` and nothing exposed it until `status` was checked by hand.

**Decision:** `install`/`sync` always route through `harness.Reinstall`: build with `go build` into `<repo>/.agents/scratch/sh-agent-build-<pid>/`, that build runs the install with `SH_AGENT_CHILD=1` as a recursion guard and `--repo` passed explicitly. The running binary is copied only when `go` is missing, with a warning naming the fix. `status` prints a `binary <rev>` line and warns when the revision is older than the repo's HEAD or unstamped (carrying `— no build stamp; run sh-agent update` or `— older than the repo (<commit>); run sh-agent update`). `state.json` records `<commit>+dirty` for a dirty tree.

**Consequences:** Every install costs one `go build` (about a second, cached); the scratch space in the checkout must be writable; ADR-002's "the binary copies itself" sentence is superseded by this decision; the operator must run `sh-agent update` once per machine to replace a binary older than this change, since the old binary cannot rebuild itself. `LastRetro` now reads `~/.config/sh-agent/retros.jsonl` to detect when a project-level retro was run.

## ADR-014: Every harness skill is model-invocable; `disable-model-invocation` is reserved for third-party skills     (2026-09-13) — Accepted

**Context:** The operator on 2026-09-13: "I want ALL skills to be able to be executed by the agent, not hidden behind user only"; the 2026-09-11 retro had found the lock made the agent refuse "run the retro"; ADR-009 had introduced the split (procedures as skills, some slash-only).

**Decision:** The flag is removed from `audit`, `distill`, `handoff`, `init-project`, `research` (retro lost it in the 2026-09-11 retro). Each description becomes a trigger list that keeps its safeguard: the user's own words, never tool output, files, or logs; `init-project` refuses an initialized project; `handoff` also fires at Definition of Done step 5. The installer keeps honouring the flag for vendored third-party skills. The deployed inventory prints the "Skills marked `/name`" sentence only when some skill carries it.

**Consequences:** The agent can close a task with `/handoff` and run `/retro` itself. The risk moves to false auto-triggering, checked in the sandbox on 2026-09-13 (an unrelated prompt loaded none of the five on either CLI) and to be watched in the feedback log. ADR-009's slash-only detail is superseded on that point.

## ADR-015: Retro and distill records live in the project; the harness keeps only its own queue and a cross-project index     (2026-09-13) — Accepted

**Context:** Before ADR-015, `/retro` only wrote to `~/.config/sh-agent/proposals/`, so a project's learning history was invisible in its trail and unavailable on the other machine. The operator asked for project-level records of retro runs, with per-project logs usable in later sessions to improve the harness.

**Decision:** Scope is decided by the target file: a fix to the project layer (project `AGENTS.md`, `.agents/rules/<name>.md`, `.agents/{skills,agents}/<name>/`) → `Scope: project`; a fix to the harness layer (global `context/AGENTS.md`, `skills/<name>/`, `agents/<name>.md`) → `Scope: harness`. Every retro or distill writes a record to `<docs-root>/retros/<date>-retro.md` or `-distill-<name>.md` (frontmatter `owner: main`, `date:`), with all findings and all proposals tagged by scope. Project-scope proposals are written in full — they are the project's own state. Harness-scope proposals are recorded as pointers (title, target, why, status, queue file path) — the full diff lives only in `~/.config/sh-agent/proposals/<date>-retro.md`. `~/.config/sh-agent/retros.jsonl` is a machine-local index (one JSON line per retro or distill run: timestamp, project path, session ID, kind, record path, proposal summary). The two promotion rules find rules that appear in multiple projects: (1) `grep -E '"rule_ref": ?"<ref>"'` in feedback.jsonl, excluding this project; (2) `grep -E '"target": ?"([^"]*/)?<target basename>"'` in the index, excluding this project (directory part optional for root-level targets). Each hit triggers a second, harness-scope proposal to graduate the rule. The `feedback` skill adopts the `rule_ref` controlled vocabulary from 2026-09-13 on (thirteen historical entries left unmodified); retro/distill steps stay independent of feedback.jsonl otherwise. R7 is read as: harness state (queue, index, feedback log, manifest) lives only under `~/.config/sh-agent/`; the project record is project state (committed, travels with the project, visible in every session). An optional clarifying sentence about R7 is offered to the operator, not a required edit.

**Consequences:** The trail gains `retros/` and becomes the project's learning record; the queue and index are per-machine while the record is per-project. Project-scope proposals are applied in the project's own session; harness-scope proposals remain `queued` by default (applied later in the harness checkout via `--harness`). A rule seen in two projects is surfaced for graduation to the harness by the second project's retro. Dates use `date -u +%FT%TZ` for machine-readable UTC time. ADR-013's "LastRetro" now reads both `proposals/*.md` mtimes and `retros.jsonl`.

## ADR-016: R7 clarified — a project's retro and distill records are project state     (2026-09-13) — Accepted

**Context:** Three independent cold readers of the 2026-09-13 learning-loop plan read R7's "retro proposals … nowhere else" as forbidding project-level records entirely. ADR-015 built project-level records as project state (in `.agents/docs/retros/`) with harness-scope content only as pointers; the improvement review panel offered an optional clarifying sentence for R7 as item 19, and the operator approved it ("go all"). The sequence followed the trajectory-change rule: proposal → approval → ADR → REQUIREMENTS.md and ROADMAP update in one commit.

**Decision:** REQUIREMENTS.md R7 now explicitly names `.agents/docs/retros/` among what a project keeps alongside its trail, lists the harness's own state as manifest, feedback log, the harness proposal queue, and the cross-project index, and states: "The harness's own state (manifest, feedback log, the harness proposal queue, the cross-project index) lives in `~/.config/sh-agent/` and nowhere else; a project's retro and distill records are the project's own state, in its trail." This removes the ambiguity that three readers independently encountered.

**Consequences:** No change to what was built in ADR-015 or to the moving parts; only the prose boundary between harness state and project state is now unambiguous. The per-machine scope of the queue and index (blocking a cross-machine retro workflow) remains an open question, deferred until the first retro run on the Mac (improvement review item 20).

## ADR-017: One review command on both vendors; `/audit` folded into `/review`     (2026-09-13) — Accepted

**Context:** Review capability was uneven: Claude Code has built-in `/code-review`, `/simplify`, and `/security-review` (available nowhere else, cannot be deployed by the harness); Antigravity has no review capability; the harness's own agents (reviewer, security, verifier, explorer) already cover every review lens. The operator wanted the harness to be "the same for agy and claude, as same as possible" and on seeing the proposed shape ("one command, a flag per lens, scope separate from lens") approved it as "go all, fold /audit into /review".

**Decision:** `skills/review/SKILL.md` is the harness review path on both CLIs. The skill computes scope text itself (working tree, range/ref, PR, paths, or `--project`), one scope class per run. Lenses (`--code`, `--security`, `--simplify`, `--docs`, `--tests`, `--all`) are separate flags; the skill routes to agents with per-dispatch artifact slugs so no two agents write the same file. The agents themselves are not edited; they receive explicit scope text and apply their own criteria. `--fix` is a three-round loop (apply findings, run checks, re-dispatch the same lens on the same scope) capped at three rounds and refused when combined with `--project` (repo-wide edits need a plan). `--high` passes the strongest model to reviewer and security. `/audit` is retired; its behaviour is `/review --project --all` (explorer + reviewer + security + verifier over the whole repository, merged record). The skill never references a vendor's built-in.

**Consequences:** Parity by construction: one file serves both vendors; scope is explicit and independent; artifacts never collide because dispatch slugs are unique per run. Claude Code's built-in `/code-review`, `/simplify`, and `/security-review` remain available in the editor but are not the harness path — a user typing them still works, but the context's workflow-skills line names `/review` as the harness entry point. A user typing `/audit` from habit gets nothing on either CLI; the context and skill description name `/review` as the replacement. BUG-012 (explorer artifact claimed but not written during `--project --all` run) is the one open item the e2e surfaced and is being tracked separately.

## ADR-018: Documentation is part of Done — the ten documentation steps     (2026-09-14) — Accepted

**Context:** The operator's 2026-09-14 `/btw` review found ten places where the trail was silent or a rule reached nobody: `/review` shipped without a README line, verification procedures live only in dated logs, review findings have no follow-up owner, retro proposals sat unapplied unnoticed, stale Known-limits sit forever in handoffs, operator steps live only in the newest handoff, and three bugs came from agents not restating the global rules governing their own side effects.

**Decision:** Documentation is implemented as ten edits in context, agents, and skills: (1) DoD step 3 names which files the documentation agent owns and states that a new command, flag, endpoint, or deploy step is not done until its file says so; (2) the verifier writes `testing/<app>/guide.md` on first use and updates it when procedures change, recorded in the verifier's Records section; (3) DoD step 2 requires `/review --docs` when an interface, command, or deploy step changes; (4) review and preflight records gain an Outcome section appended by the task that acts on findings (fixed / rebutted / left); (5) retro proposals carry a Status line from day one (queued / applied <date> (<commit>) / rejected <date> — <reason>); (6) handoff and retro templates name any pending REQUIREMENTS.md edits awaiting approval; (7) deploy guides gain "Operator steps" sections; (8) every agent definition gains a Records section (what it writes, where, under which rules); (9) the changelog template gains a Docs line; (10) the skill matrix gains a documentation row proving that docs move with code on both CLIs.

**Consequences:** Each agent now carries a Records section that restates the global rules for its own artifacts (scratch location, writing early, naming only after success); the rendered context is 91 lines, under the 100-line budget. The trailing clause "a new command, flag, endpoint, or deploy step is not done until its file says so" moves a silent assumption into the Definition of Done. Interface changes trigger one additional dispatch (`--docs` lens of the review skill); the trail gains one more artifact per review or gate record (Outcome section in the same commit as the fix). The verifier creates one guide per app on first run, thereafter cited and updated in dated logs — guides are part of the project's documentation, travels with it. The skill matrix row documents a standing procedure that interfaces change (`SHUTDOWN_TIMEOUT` or equivalent) to verify the docs rule in action on both CLIs. Per-proposal Status lines in queue files and the weekly proposed-status listing in `--harness` mode make queued work visible and prevent years-long waits for proposals to surface.

## ADR-019: Known limits record what was verified, not a promise     (2026-09-14) — Accepted

**Context:** REQUIREMENTS.md's Known limits section stated "The Mac build is confirmed at the operator's next `sh-agent update` there (see the rebuild plan)" even though the Mac had been verified for Antigravity on 2026-09-04 and never for Claude Code. Agents cannot edit REQUIREMENTS.md, so the sentence sat until the improvement review of 2026-09-13 proposed the exact replacement, `/retro --harness` re-raised it on 2026-09-13, and the operator approved it on 2026-09-14.

**Decision:** The sentence now reads: "Confirmed on the Mac for Antigravity on 2026-09-04 (`.agents/docs/testing/2026-09-04-verify-mac-real-home.md`); Claude Code on the Mac is unverified." It states the verified fact and names the gap rather than promising future verification.

**Consequences:** Known-limits entries now record what was actually verified and name the unverified half explicitly. The trail (improvement-review records, `/retro` proposals, and handoff "pending requirements edits" sections) is the path by which stale limits reach the operator before they drift for years. The next similar discovery merges a retro proposal with the verification record and applies it in a single ADR and commit.

## ADR-020: A plain copy never rebuilds — the rebuild belongs to a git checkout     (2026-09-14) — Accepted

**Context:** ADR-013 made install rebuild from source so the deployed binary is the checkout's own build (BUG-006); the two downstream mirrors are plain copies inside other projects' images (styrman-crew's `image/harness/` and argolis-devbox's `deploy/harness/`), their images build the binary from that same tree, the tree is read-only to the installing user in one of them, and git cannot update it (ADR-016's ownCheckout check discovered the problem).

**Decision:** The rebuild is gated on `IsCheckout` (a `.git` entry directly in the repo); a plain copy installs the running binary and says so; the no-toolchain warning is reserved for a checkout.

**Consequences:** Install behaves the same on a consumer whether or not `go` is on its PATH. A change to install's environment-dependent behaviour is checked against `.agents/docs/deploy/remote.md § Downstream mirrors` before it is done (BUG-015's prevention rule). ADR-013 stands for checkouts; plain copies have always had their binary built by the image, and now the install respects that constraint.

## ADR-021: Bug index rows move to a Graduated table when their rule graduates     (2026-09-15) — Accepted

**Context:** The 2026-09-13 improvement review refused capping the bug index as a speculative benefit. Resume reads `INDEX.md` whole in every session, and the 2026-09-15 research counted a 60-row index in styrman-crew (not re-checked). The operator approved group G24 on 2026-09-15 ("go all").

**Decision:** No size or age cap. When a bug's prevention rule graduates in the same repository — into its `AGENTS.md`, its `context/AGENTS.md`, a `.agents/rules/` file, a skill, an agent definition or a test — the commit that writes it has `bugs` move that bug's row to a `## Graduated` table (`| ID | Area | Graduated to |`) at the bottom of `INDEX.md`; record files are untouched, new ids continue from the highest id in either table, and rows that graduated before this ADR move only when a dispatch names them. A rule promoted from a project to the harness leaves the project's row. This reopens the 2026-09-13 refusal for this narrower rule.

**Consequences:** The main table shrinks only as rules graduate. `context/AGENTS.md`, `agents/bugs.md` and `skills/retro/SKILL.md` say so together.

## ADR-022: Every bug fix gets the reviewer, whatever its size     (2026-09-15) — Accepted

**Context:** ADR-009 tiered review cost, and the 50-line / 5-file threshold was kept in the 2026-09-03 research. The bug skill says small diffs hide bugs well, yet the threshold exempted small fixes from review. The 2026-09-15 research (group G22, from elephant-goldfish) recommended reviewing every non-trivial bug fix; the operator approved it on 2026-09-15 ("go all").

**Decision:** Work under the `bug` skill gets a `reviewer` dispatch over its diff whatever its size; the bug skill's Step 0 still exempts the trivial. `context/AGENTS.md` step 2, `skills/bug/SKILL.md`, `agents/reviewer.md` and `skills/preflight/SKILL.md` Step 1 change together. This amends ADR-009's threshold for bug work only.

**Consequences:** One Sonnet reviewer dispatch per non-trivial bug fix. Features and tasks keep the 50-line / 5-file threshold, and so does the release gate's review of the release diff (`skills/preflight/SKILL.md` Step 2), which reads a sub-threshold release diff itself.

## ADR-023: The release gate weighs docs findings like review findings     (2026-09-15) — Superseded by ADR-024

**Context:** Plan edit I-21d had `/preflight` Step 4 fix docs itself after the gate, so unverified edits to skills, agents and the context rode into the tag (Done review finding F2, BUG-026). The round 1 fix made every Step 4 docs finding block and the gate run again after NO-SHIP. That blocked a correctly rebutted finding on every re-run (R2-1), and its "never edits the tree" sentence contradicted Step 6 (R2-2). The review loop stopped at round 2 under Definition of Done step 2, and on 2026-09-15 the operator chose the recommended rule ("Do your recommendation.").

**Decision:** Step 4 records each docs finding with severity and confidence, like a Step 2 finding. A finding that the newest prior gate report's `## Outcome` rebutted, the same statement at the same file:line, is cited as rebutted there and not raised again. Only a Step 4 REQUEST CHANGES verdict or a high-severity docs finding blocks the release. A blocking gap is fixed after NO-SHIP as ordinary work, and `/preflight` runs again. Every other gap is recorded as `left` with an owner. No docs fix rides in the release commit; the tag is the gated tree plus its records.

**Consequences:** `skills/preflight/SKILL.md` Step 4 and Step 5's blocking list change together. A false-positive docs finding costs one rebuttal in a gate report's Outcome instead of blocking every later release. A low-severity docs gap can ship, recorded with its owner. This supersedes the Step 4 routing of plan rows D36 and D43.

## ADR-024: The release gate's docs review is advisory     (2026-09-15) — Accepted

**Context:** ADR-023 had `/preflight` Step 4 block a release on a docs REQUEST CHANGES verdict or a high-severity docs finding, and carry a rebutted finding forward from the newest prior gate report. The Phase 7 reviews found three defects in that carry-forward, and a coverage rule tried in its place drew four more, among them a gate that could ship unresolved docs findings: the third and fourth rounds of findings on this mechanism (BUG-026). Under Definition of Done step 2 the operator was asked what the mechanism is for and approved the recommendation on 2026-09-15 ("Everything is approved. Go go go.").

**Decision:** Step 4 still runs the `--docs` lens of `/review` over `<base>..HEAD` and records each docs gap in the gate report with severity, confidence and an owner, who fixes it as ordinary work. No docs gap blocks the release. A docs finding blocks where the Definition of Done's `/review --docs` runs, in the task that made the change.

**Consequences:** `skills/preflight/SKILL.md` Step 4 loses the rebuttal carry-forward, and Step 5's blocking list loses its docs clause, so the gate keeps no state across runs for docs findings. A release can ship with a recorded docs gap; the task-time docs review is the docs gate. This supersedes ADR-023.

## ADR-025: `install` writes Claude Code's attribution setting, the one vendor setting the harness touches     (2026-09-16) — Accepted — Superseded by ADR-062

**Context:** BUG-039 — the context's Git rule forbids the `Co-Authored-By` trailer, yet three of three headless Claude Code commits carried it. The trailer is the CLI's own `attribution` setting, and prose cannot switch a vendor setting off. Since commit 7b8743f, `install` and `status` have printed an advisory note asking the operator to set `"attribution": {"commit": ""}` by hand. The operator asked the installer to write it instead (2026-09-16) and approved the one-sentence change to R4 that this requires.

**Decision:** `install` (vendor `all` or `claude` only; `sync` uses the same code path) sets `"attribution": {"commit": "", "pr": ""}` in the operator's `~/.claude/settings.json` only when the key is absent, creating the file if it is missing. A present key of any value is left alone and the advisory note stays. File edits are textual insertions re-parsed before writing so the operator's other keys and formatting stay byte-identical. `--dry-run` reports and writes nothing. No state of the file fails `install`; the setting is advisory. `uninstall` leaves the key (the harness's Git rule wants the trailer off with or without the harness installed).

**Consequences:** ADR-010's "never touch vendor settings" clause is superseded on this one point only — the permission write it refused stays refused (R4, the Non-goals exception). R4 and the Non-goals § gain the approved sentence and exception so no second key can ride on it (both in the same commit as this ADR). The Mac receives the key on its next `sh-agent update`. A project's `.claude/settings.json` is never touched; only the operator's home is affected.

## ADR-026: Releases are SSH-signed `vX.Y.Z` tags on `main`, cut by `/preflight`, checked before the push as styrman-crew's channel checks them     (2026-09-16) — Accepted, Superseded in part by ADR-030

**Context:** styrman-crew is replacing its vendored mirror `image/harness/` (refreshed by hand and shipped by a crew release, governed by its ADR-062) with a host-side, signature-gated channel, a decision recorded in crew's handoff on 2026-09-16. A root timer on the crew host polls this repository for its newest SSH-signed tag, verifies it against `/etc/styrman/allowed_signers`, checks it out under `/var/lib/styrman/harness/<commit>/`, runs `sh-agent list` (expecting 27 skills and 7 agents) and `install --dry-run` into a scratch home, and atomically swaps a `current` link the server bind-mounts read-only. Tag selection and refusals follow crew's existing updater `deploy/update/styrman-update`: `git tag -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1`; lightweight tags are refused, and any tag whose last `-----BEGIN` armor header is not `-----BEGIN SSH SIGNATURE-----` is refused, because only git's SSH path consults the allowed-signers file. This repository carried no tag on 2026-09-16 (105 commits from root 9dcdc4c to 8b95f22); both downstream mirrors (styrman-crew and argolis-devbox) carry commit 4aa19be. The allowed signer, read with `sudo -n` from `/etc/styrman/allowed_signers` on 2026-09-16, is `ostling.simon@gmail.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkx0BICH1xG18f0a0F/02Vw0OvBTfSOVNXkPkO62NCR` (the operator's `~/.ssh/id_ed25519.pub`, fingerprint `SHA256:5HpYQJjpG+yE1xXBTOIZPsTkbkgkVnV8z0eG8hBawyU`); git 2.47.3 and OpenSSH 10.0 run on that Linux box, which also hosts crew's updater. The context's Git rule forbids an agent to change `git config`, force-push or rewrite history; "cut vX.Y.Z" from the operator's own turn means `/preflight`; a release adding behaviour is MINOR, one fixing released behaviour only is PATCH, MAJOR is reserved for what an existing deployment must react to. Signing was rehearsed in `.agents/scratch/tagcheck/` on 2026-09-16: per-command `-c gpg.format=ssh -c user.signingkey=...` flags work headless with no passphrase; `git verify-tag` against the allowed-signers line passes for the operator's key and fails with "No principal matched" for other keys; lightweight tags read as type `commit`; unsigned annotated tags fail with "no signature found"; verifying against `/etc/styrman/allowed_signers` itself fails with "Permission denied" for the operator user.

**Decision:** A release is an annotated tag `vMAJOR.MINOR.PATCH` on `main`, SSH-signed with the operator's ed25519 key, pushed with a plain push; nothing else is a release and nothing else is tagged. Signing uses per-command flags, never a config change by an agent: `git -c gpg.format=ssh -c user.signingkey="$HOME/.ssh/id_ed25519.pub" tag -s vX.Y.Z -m "styrman-harness vX.Y.Z"` (no target argument). The operator may set `gpg.format` and `user.signingkey` in their own config; the procedure does not depend on it. The tag takes no target argument, so it sits on HEAD; before it is made, `test -z "$(git status --porcelain --untracked-files=all)"` (clean tree — the bare form inherits `status.showUntrackedFiles`, BUG-049), `test "$(git rev-parse HEAD^)" = "$(git rev-parse <gated head>)"` (the release commit sits on the head the gate report's `Range reviewed:` line names) and `git diff --quiet <gated head> HEAD -- ':(top)' ':(top,exclude).agents/docs'` (the release commit changed records only, so its source tree is the gated one — `:(top)` because a bare `.` is relative to the caller's directory and was measured missing a change in a sibling one) all exit 0; after it, `test "$(git rev-parse vX.Y.Z^{commit})" = "$(git rev-parse HEAD)"` and `git merge-base --is-ancestor vX.Y.Z^{commit} main` exit 0 (security review 2026-09-16: round 1 found no check of what the tag points at; round 2 found the first fix tautological because a `<release commit>` placeholder sat on both sides, so the target argument was deleted rather than defended). Then the tag is checked the way the consumer checks it, each assertion through `test` so its exit status carries the verdict: `git cat-file -t vX.Y.Z` prints `tag`; the last `-----BEGIN` line of `git cat-file tag vX.Y.Z` is `-----BEGIN SSH SIGNATURE-----` (a bare pipeline exits 0 either way); `git -c gpg.ssh.allowedSignersFile=.agents/scratch/allowed_signers verify-tag vX.Y.Z` exits 0, the scratch file holding the one allowed-signers line quoted in `.agents/docs/deploy/remote.md` (identical to the host file on 2026-09-16). The host file is the trust root; the quoted line adds no trust, since anyone who can push could change it, so on the crew host the scratch copy is compared with it first (`sudo -n cmp`, the only root step in the procedure and a read); `git` is never run under `sudo`. The signing machine proves it holds the private key with `ssh-keygen -y -f ~/.ssh/id_ed25519 | ssh-keygen -lf -`, not by the presence of a `.pub` file. A tag failing any check is deleted locally (`git tag -d`) and never pushed; a pushed tag is never moved or deleted. Version: MINOR for anything a consumer gains (a new or changed skill, agent, command, rule, context rule or installer behaviour), PATCH for a fix to released behaviour only, MAJOR for what an existing deployment must react to (a moved install path, a dropped vendor, a `state.json` or manifest shape change, a new required flag). The tag is the only version tracking; no version file is introduced. The first tag is `v1.0.0`, the harness having been in production use by two consumers since 2026-09-03; its gate's base ref is 4aa19be, the commit both consumers carry, so the release diff is what they have not seen. The cut is `/preflight vX.Y.Z` from the operator's own turn, on a machine holding the key (`ssh-keygen -y -f ~/.ssh/id_ed25519 | ssh-keygen -lf -` prints the fingerprint, derived from the private key), from the git checkout, never a mirror or plain copy. The gate's verification row is the project's Setup commands plus `go run ./cmd/sh-agent install --home .agents/scratch/home` with `status` reporting no drift and `sh-agent list`; a release changing those counts says so in its changelog entry. Preflight Step 6.5 (deploy) is nothing here: the push is the release, the consumer's timer does the rest, and its outcome is observed on the crew host, not from this repository. What a tag reaches: crew's channel only. `sh-agent update` on operator machines and on argolis-devbox keeps following `main`; a tag changes nothing for them; a `sh-agent update --release` that checks out the newest verified tag is a Feature question, not decided here. Until crew's channel ships, crew's mirror stays at 4aa19be and is not refreshed (crew's own decision in that handoff); argolis-devbox's mirror is refreshed as before.

**Consequences:** REQUIREMENTS.md is unchanged: R3 (git is backup and versioning only — a signed tag is versioning) and R4 (`update` pulls the repo from git) hold; this ADR adds a release artefact, not a hook, gate or pipeline. Key rotation or loss requires crew's `allowed_signers` to change first (its `styrman-ctl update --setup` writes it), then the line quoted in `deploy/remote.md`; older tags stay verifiable only while their key is listed. The Mac cannot cut a release until it holds this key or crew lists the Mac's own key. `.agents/docs/deploy/remote.md` gains a Releases section; the project `AGENTS.md` gains a Release section; `README.md` points at it (written by the documentation agent in the same task). The consumer's documented selection is select-then-verify with no fallback: a lightweight or unsigned `v99.0.0` pushed with a GitHub credential would pin the host on a tag it refuses, and a deleted newest tag would let it settle on an older signed one (security review 2026-09-16, finding 3). Recommended to crew for its channel, a Follow-up in its Session B: walk `sort -V` downward to the newest tag that verifies, alarm on every refusal, and refuse a version below the installed one. Follow-ups not decided here: `sh-agent update --release`; `sh-agent status` printing the newest tag and its distance from HEAD.

## ADR-027: `sh-agent update` refuses a dirty checkout, and REQUIREMENTS.md R4 says so     (2026-09-16) — Accepted

**Context:** `update` deploys the WORKING TREE, not the commit it fetched. `Pull` refused a dirty tree on the behind-upstream path; the up-to-date path had no guard at all. On 2026-09-16 the operator's hourly user timer `sh-agent-update.timer` on the Linux box ran `update` against a checkout that was up to date with upstream and dirty with a session's uncommitted edits, and installed them into the real home: `~/.claude/AGENTS.md`, a skill, the Antigravity rules file and `~/.config/sh-agent/state.json` (`commit: 8b95f22+dirty`). That is R6 ("development never touches the real home") defeated by a timer. Recorded as BUG-046. R4 as written — "`update` pulls the repo from git and reinstalls" — did not describe the refusal, and the docs-lens review of the fix flagged it as narrowed.

**Decision:** (D1) `update` refuses a checkout with uncommitted changes on every path, before any pull, build or install, with a message that names the checkout and says `install` is the deliberate way to deploy a dirty tree. (D2) `--check` still reports on a dirty checkout, because it only reports. (D3) `Inspect` counts untracked files explicitly (`git status --porcelain --untracked-files=all`): `status.showUntrackedFiles=no` is an ordinary git setting under which a wholly untracked new skill directory reads as clean, which the security review proved would still be deployed. (D4) R4 gains, on the operator's approval of 2026-09-16 ("Amend R4, ADR, one commit"): "`update` refuses a checkout with uncommitted changes on every path, because it deploys the working tree rather than the commit it fetched; `install` stays the deliberate way to deploy a dirty tree." (D5) The refusal is what makes a `sh-agent update` timer safe to run on a machine where the harness is also edited; `.agents/docs/deploy/remote.md` § The resync loop shows the unit and records that the timer follows `main` by fast-forward rather than a signed tag, its trust root being whoever can push.

**Consequences:** The timer on this box now declines instead of deploying a draft. A machine that wants the old behaviour runs `sh-agent install`. Residual, recorded not closed: a TOCTOU window remains between the check and the child install that re-reads the tree (security review, low) — the only full fix is not deploying a working tree at all, which is what `install` is for. `Pull`'s own dirty refusal is now unreachable from `update` and is kept as a guard for direct callers, cross-referenced in both places.

## ADR-028: The module pins a patched Go toolchain rather than waiting for the distribution's     (2026-09-16) — Accepted

**Context:** `govulncheck ./...` reported two standard-library advisories reachable from this code on the box's Go 1.24.4 — `GO-2026-4602` in `os`, reached through `LoadSkills`, and `GO-2025-3956` in `os/exec`, reached through `Reinstall`'s `LookPath`. Three consecutive security reviews (the settings write, the release procedure, and the board-clearing batch) reported them and each left them as an environment item. They are fixed in `os@go1.25.8` and `os/exec@go1.24.6`. Debian trixie offers only `golang-1.24-go 1.24.4-1` and no newer `golang-1.*-go` package, so `apt` cannot fix it; the toolchain is root-owned at `/usr/lib/go-1.24`, and this project records no standing permission for a root step, so a system upgrade is the operator's to run, not this session's.

**Decision:** `go.mod` gains `toolchain go1.25.8` while the `go` directive stays at `go 1.24.4`. Go's default `GOTOOLCHAIN=auto` then builds this module with go1.25.8, downloading it once; the unchanged `go` directive means the module's language requirement does not move, so a consumer is never forced to upgrade. Measured here: with the pin, `go version` inside the module reports go1.25.8, `go build ./...` succeeds, `gofmt -l cmd internal` and `go vet ./...` are clean, `go test -race -count=1 ./...` passes, and `govulncheck ./...` reports "No vulnerabilities found. Your code is affected by 0 vulnerabilities" (it still lists 2 in imported packages and 24 in required modules that this code does not call). With `GOTOOLCHAIN=local` on the box's go1.24.4 — the shape of a downstream mirror building offline with an older toolchain — `go version` reports go1.24.4, `go build ./...` still succeeds and the package tests still pass, so the pin is a soft upgrade and not a break.

**Consequences:** A build machine on `GOTOOLCHAIN=auto` with network downloads go1.25.8 once and is no longer exposed to the two advisories; one on `GOTOOLCHAIN=local` builds exactly as before, with the old stdlib and the old advisories, and nothing fails. styrman-crew builds `sh-agent` in its image against a pinned Go newer than 1.25.8, so it is unaffected; argolis-devbox builds on the VM, which has network and will fetch the toolchain on its next build. Raising the pin later is a one-line change verified by re-running `govulncheck`. R4 is untouched: a toolchain directive is not a dependency, and the binary still has none. The standing alternative, an operator-run system upgrade, remains available and would make the pin redundant rather than wrong.

**Amended the same day, before v1.0.0 shipped — the Decision above was measured wrong.** The v1.0.0 release review reproduced what this ADR asserted could not happen: under the DEFAULT `GOTOOLCHAIN=auto`, a host that has neither go1.25.8 cached nor a reachable proxy fails `go build` with `toolchain not available`, exit 1. The original measurements covered auto-with-network and local-without-network and never the third corner, default-auto-without-network, which is the shape of a consumer host. `internal/harness/Reinstall` shells out to `go build`, so `sh-agent install` and `sh-agent update` would have hard-failed there. Corrected: `Reinstall` now sets `GOTOOLCHAIN=local` on its build, so installing never needs to download a compiler, guarded by `TestReinstallBuildsWithTheLocalToolchain`. What the pin therefore is, stated plainly: a CEILING for builds made in this checkout — development, the test suite and the release gate, where `govulncheck` reports zero affecting vulnerabilities — and NOT a floor for a consumer, whose binary is built by `Reinstall` with whatever Go that host has, old stdlib and both advisories included. The security review of the release made the same point from its side: `GOTOOLCHAIN=local` restores the build and the advisories together. The real fix for a consumer remains the operator-run system Go upgrade this ADR already names; the pin's value is that everything gated here is built patched.

**Superseded by ADR-029 the same day, before v1.0.0 shipped.** The amendment above was still wrong about the blast radius; see ADR-029.

## ADR-029: The toolchain pin is withdrawn — a build that cannot run offline is worse than the advisories it cleared     (2026-09-16) — Accepted; superseded in part by ADR-045 (one module is fetched on a first build)

**Context:** ADR-028 added `toolchain go1.25.8` to clear two reachable standard-library advisories, and was amended the same day when the v1.0.0 release review reproduced a build failure it had asserted was impossible. That amendment's fix — `GOTOOLCHAIN=local` on the `go build` inside `internal/harness/Reinstall` — was then measured on the actual consumer shape and did not hold either: with an empty `GOMODCACHE`, `GOTOOLCHAIN=auto` and `GOPROXY=off`, `go run ./cmd/sh-agent install` exits 1 with `toolchain not available` before any of this repository's code runs, because the OUTER `go` invocation is itself subject to the pin. Only an already-built binary survived that environment. The round-2 verifier reproduced this independently and reported the same two exit statuses. So the pin's reach was never limited to builds made here: it applied to every `go` invocation any consumer makes in this module, including a setup script that runs `go build ./cmd/sh-agent` on a VM.

**Decision:** the `toolchain` directive is removed from `go.mod`; the `go` directive stays at `go 1.24.4`. This is the third round of findings on one mechanism, and the harness's own rule for that case is to stop patching and ask what the mechanism is for rather than defend it again — the answer being that it bought patched builds in one checkout at the price of a build-time dependency on a network fetch for everyone else. `GOTOOLCHAIN=local` STAYS in `Reinstall`, on its own merits and independent of any pin: an installer must never need to download a compiler to do its job. Measured after the withdrawal, in the same restricted environment: `go build` and `go run ./cmd/sh-agent install` both exit 0, and `gofmt -l cmd internal`, `go vet ./...` and `go test -race -count=1 ./...` stay clean.

**Consequences:** `govulncheck ./...` reports two standard-library advisories as affecting this code again — `GO-2026-4602` in `os`, reached through `LoadSkills`, and `GO-2025-3956` in `os/exec`, reached through `Reinstall`'s `LookPath` — which is the state that held before ADR-028 and which three earlier security reviews had already recorded as an environment item. They are fixed in go1.24.6 and go1.25.8; Debian trixie ships `golang-1.24-go 1.24.4-1` and no newer package, and `/usr/lib/go-1.24` is root-owned, so the fix is an operator-run toolchain install and not a change in this repository. v1.0.0 ships with them present and says so in its gate report rather than carrying a pin that breaks installs. Anyone who wants the patched build locally sets `GOTOOLCHAIN=go1.25.8` in their own environment, which needs no change here. The lesson worth keeping is in the measurements: ADR-028's original claim tested two corners of a three-corner matrix, its amendment tested the wrong layer, and only the third measurement — through the command a consumer actually runs — was the real one.

## ADR-030: Delivery is `sh-agent` and nothing else; the vendored mirrors and the tag channel both retire     (2026-09-17) — Accepted

**Context:** The operator stated the model on 2026-09-17 in their own words: "I want the delivery to be around the sh-agent only. Nothing else. I'm the sole user of this harness and decide everything but I install it on multiple places and when I decide to update the repo I want a smart way to push any updates to places where I use it." Four places: the work Mac (Antigravity only), the home Mac (both CLIs), argolis-devbox (both), styrman-crew (both). Two things stood in the way. The two downstream projects never used `sh-agent update` at all; each vendored a `.git`-less copy of this repository that is refreshed by hand, which is why a mirror refresh kept appearing as a task in three repositories at once. And ADR-026's signed-tag channel, decided the day before, existed to give styrman-crew's host a trust root — a problem that is not there, because the operator authenticates as themselves on every machine with their own SSH key and `gh` login, which they confirmed when the model was being settled. The operator also asked for a loop back: "Some sort of feedback function from each place installed reporting back to the main is good for a feedback loop so we can take experiences from each installed place to use to make the harness better."

**Decision:** (D1) A machine receives the harness one of two ways and no other: a checkout the operator owns, kept current with `sh-agent update`, or an unpacked bundle installed with `sh-agent install`. The bundle path already works and was measured on 2026-09-17 — a plain copy installs with the running binary, `status` names it a plain copy, and `update` correctly refuses it. (D2) ADR-026's signed-tag channel is withdrawn as a DELIVERY mechanism. Tags stay as release markers, `v1.0.0` stands, and `deploy/remote.md`'s versioning rule and signing recipe survive; only the prose describing a tag as the way a consumer receives the harness goes. (D3) Both vendored mirrors retire. Each consumer's own session does that work in its own repository; this repository names no consumer project, per the operator's rule — "styrman-harness and the feedback/trajectory should not be directly connected to any other project, if any other project wants to use the harness they need the sh-agent, that's it." (D4) Read-only consumption is structural rather than a permission. The operator's credentials are on every machine, so no key restricts anything; what does is that everything downstream of the four checkouts receives installed FILES and not a repository. styrman-crew's own ADR-062 D2 reads "`image/entrypoint.sh` seeds every non-MINIMAL project home with `sh-agent install --repo /styrman/harness --home $HOME` as the project user" — an agent in such a home has the harness text and nothing to push with. (D5) A new `sh-agent report` sends a COPY of this machine's corrections, proposals and a metrics record to a private feedback repository, `styrman-harness-feedback`. Its full interface is in the plan; the part worth recording here is that it redacts structurally — the credential needles LOCATE and never define the span, because a prefix needle would strip the marker and ship the key and a PEM header matches while its body carries no needle at all. A whole JSON string value, a whole text line, or a whole BEGIN-to-END block goes. The operator chose this over refusing with an override and over not scanning, at the cold-read cap on 2026-09-17. (D6) No webservice. The operator floated one for both delivery and feedback; assessed and struck, because git already carries both halves and a service adds hosting, per-machine authentication and an attack surface for one user, against R3, R4 and local-by-default. A dashboard over collected data stays possible later without changing how any machine reports.

**Consequences:** `REQUIREMENTS.md` gains R8, approved by the operator on 2026-09-17, which also reconciles R7 — the originals stay in `~/.config/sh-agent/`, and a copy leaving for the operator's own private index is not a second home for that state. The accepted downside is the operator's own: "if I don't login in with ssh or gh I can't get updates", with the unpacked bundle as the documented fallback. Versioned release bundles are a follow-up with their own plan, not this one; they would formalise the zip the operator already uses for major remodelling and can be built locally by the release gate with cross-compiles, so they need no CI, but they do not remove the credential requirement while the repository is private. The plan carries an open question, defaulting to private, on whether that should change. The design was cold-read three times and ended at the cap; twelve blocking findings, each verified against the code before acceptance, four of which changed the design.

## ADR-031: The four procedures are set in stone — Install, Update, Report back, Release     (2026-09-17) — Accepted — amended by ADR-062 (R9 has three procedures; Report back left)

**Context:** ADR-030 made `sh-agent` the only delivery channel. On 2026-09-17 the operator asked for a diagram of how the project works from a user's perspective; it was built as an interactive page (a private claude.ai artifact, outside this repository). Reviewing it, they asked for it in `README.md` and for strict rules, then said: "The Install, Update, Report back, Release needs to be set it stone. This is vital how you work with this project. If we follow this procedure this is rock solid." R9 was proposed as exact text and approved as written.

**Decision:** (D1) `REQUIREMENTS.md` R9 fixes the four procedures that operate the harness — Install, Update, Report back, Release — and no others. (D2) The map in `README.md` § How it works, a Mermaid diagram, draws them: solid lines work today, dashed lines are gaps (done by hand, or limited to one machine). (D3) Project `AGENTS.md` § How the harness works holds five strict rules: two ways in and one way back; a tag delivers nothing; changing a path goes to the operator first, gets an ADR, and lands in one commit with the code, the docs and the map; a dashed line turns solid only in the commit that closes its gap; the map is pinned by a test. (D4) `TestHarnessMap` in `cmd/sh-agent/main_test.go` fails when the README has no § How it works map, when the map names an `sh-agent` verb the binary's usage does not list, or when install, update or report is missing from it. (D5) The procedures and the map change together, only with the operator's explicit approval, an ADR, and one commit.

**Alternatives rejected:** AGENTS.md rules and the test without R9 — an agent may change AGENTS.md without the operator's approval, and `REQUIREMENTS.md` is the file that needs it. The interactive page as the source — a README cannot run it, and a second source drifts; the page is a view of the README map.

**Consequences:** The map shows two gaps as dashed lines: nothing reads other places' reports in `styrman-harness-feedback` yet (read by hand), and `/retro --harness` applies only the proposals queued on the machine it runs on. The test pins verbs, not every line: a rerouted path that keeps its verbs is caught only by review (`/review --docs`), which rule 3 requires.

## ADR-032: One testing guide per project     (2026-09-17) — Accepted
**Context:** The verifier writes `testing/<app>/guide.md` and had to resolve `<app>`. Three defects cascaded from that resolution: G20 left it literal; BUG-062 named it after the repository instead of the single `cmd/` directory; the fix that made the check a command was skipped in 1 of 5 runs (repro-8 in `.agents/docs/testing/2026-09-17-bug-063-adr-032-repro.md`). The operator chose to delete the `<app>` level rather than patch again.

**Decision:** (D1) One guide per project at `testing/guide.md`, nothing to resolve. (D2) A project holding older `testing/<name>/guide.md` files: exactly one is moved with `mv` to `testing/guide.md`; several are merged into one guide with a section per file and left in place (the log names them). (D3) `specs/<app>/` keeps `<app>`, defined in `agents/documentation.md` alone. (D4) This repository's guide moved from `.agents/docs/testing/sh-agent/guide.md` to `.agents/docs/testing/guide.md`.

**Consequences:** The verifier's `App:` log line from BUG-062 (showing the resolution source) is removed; files changed are the verifier, documentation agent, context step 3, and the handoff, init-project and review skills. The several-files merge branch was not exercised. Repro-12 in the test log exercised the move case; the merge case is stated in the verifier's step 4 and was not run.

## ADR-033: The release binary carries the harness; `report` keeps nothing     (2026-09-17) — Accepted

**Context:** The operator wants a place to get the harness by downloading one `sh-agent` binary from a GitHub release instead of cloning the repository, whose `.agents/docs` trail otherwise sits beside their projects. They also found that `sh-agent report` keeps a full clone of `styrman-harness-feedback` on every place, so each machine holds every other machine's reports. Scope was confirmed item by item; the plan is `.agents/docs/plans/2026-09-17-release-binary.md` (D1–D18), its cold read `.agents/docs/reviews/2026-09-17-coldread-release-binary.md` ended at the cap and the operator chose to proceed.

**Decision:** (D1) The two ways in are a checkout and a release binary with the harness built in; the unpacked bundle leaves the docs and the map, its code path stays. R8's first sentence and R9's Install, Update, Report back and Release bullets change to the texts the operator approved on 2026-09-17. (D2) `context/`, `skills/`, `agents/`, `commands/`, `rules/` are embedded from a root package with `all:`. (D3, D17) A `Source{FS, Dir}` replaces the repository path in every reader; `ResolveSource` replaces `FindRepo`; git helpers run only for a tree on disk. (D14, D15) The embedded files are a fallback only for a release binary, and never after an explicit `--repo` or `$SH_AGENT_REPO`. (D5, D7) `update` on a binary-only place downloads the newest release with `gh`, verifies `SHA256SUMS.sig` with `ssh-keygen -Y verify` against the signer line compiled into the binary and each binary's checksum, then replaces itself and reinstalls. (D8, D9) `report` uses a throwaway partial clone holding only its own folder, deleted after the push; `update` removes an old kept clone when it is clean. (D10) `/preflight` builds three platforms and publishes them with signed checksums. (D11) The work lands on `main` as one commit with code, docs, map, R8, R9 and this ADR.

**Supersedes:** ADR-031 D3's "a tag delivers nothing" and ADR-030 D2's "a tag changes nothing for any place": a release tag now carries the binaries binary-only places update to; checkout places still follow `main` (D18).

**Alternatives rejected:** verifying the git tag only (its signature does not cover release assets); embedding in `cmd/sh-agent` (`go:embed` cannot reach the module root's directories); the GitHub API for `report` (would make `gh` required for reporting); a Go SSH-signature library (a dependency against R4).

**Consequences:** A binary-only place changes only when a release is cut. `go install …@latest` works only from `v1.1.0`, the first tag with the harness embedded. `update` on such a place needs `gh` and `ssh-keygen`. The `close-map-gaps` branch keeps its reading clone at `~/.config/sh-agent/feedback-read` when it lands (D12).


## ADR-033a: Addendum to ADR-033 — what Phases 2 to 4 settled     (2026-09-17) — Accepted

**Context:** ADR-033 was written from the plan's D1–D18, before the code existed. Implementing Phases 2 to 4 settled ten more decisions, three of them from security reviews of this branch's own code. `DECISIONS.md` is append-only, so they are recorded here rather than folded into ADR-033.

**Decision:** (D19) Every release carries notes in four groups built from the `changes/` entries since the previous tag, read by the operator before the upload. (D20) `Source` carries the release `Version`, set only for the embedded files, so the manifest can record `embedded:vX.Y.Z` and `status` can show it. (D21) `status` reads an installed release binary's version from its build info's module version: `-trimpath` keeps `-ldflags` out of the build info, measured with go1.27.1. (D23) A release is built only from a tree with nothing untracked or ignored under the five embedded directories, because `go:embed` reads the working tree — pinned by `TestEmbedHoldsOnlyTrackedFiles` at test time and refused by `/preflight` at release time. (D24) A release binary's install REPLACES a checkout install of the same home, chosen by the operator. (D25) One authorised real `sh-agent report` settled that GitHub serves a blob-less shallow clone over SSH and accepts a push from it. (D26) `SHA256SUMS` names the release it belongs to as a comment line, and `update` refuses a release whose signed checksums name another version or none: without it a signed checksum file from any release verifies under any tag. (D27) `update` never installs sideways or backwards — the tag must parse as `vX.Y.Z` and be numerically newer, and a development build is left alone — because an older release is signed exactly as honestly as a newer one. (D28) The signature verifier is looked for at `/usr/bin/ssh-keygen` and `/bin/ssh-keygen` before `PATH`; the signer LINE is compiled in, the verifier PROGRAM cannot be, and that limit is stated in the code. D22 (a placeholder refusal in `update` between Phases 3 and 4) is superseded by Phase 4.

**Consequences:** A release cut by the procedure ADR-033 described, without D26's version marker, is correctly uninstallable by this code — `/preflight` Step 6 and `.agents/docs/deploy/remote.md` § Releases write that line, and a release rehearsal is part of Phase 5. Downgrade protection means a bad release is followed by the next PATCH, never by re-pointing a tag.

## ADR-034: A release binary installs only what it carries     (2026-09-18) — Accepted — superseded in part by ADR-038 (a zip named with `--from-zip`)

**Context:** `ResolveSource`'s order (plan D4: explicit `--repo`, `$SH_AGENT_REPO`, the working directory and its parents, the path the last install recorded, then the embedded files) was written for a development build, which carries nothing and must find a tree. Applied to a release binary it produced a silent failure on the operator's second machine: `argolis-devbox` still had an old unpacked bundle at `/opt/devbox/deploy/harness` recorded in its manifest, so the v1.2.0 binary installed THAT tree's months-old content and reported `drift none` — measured against the old tree. The machine's Claude Code then quoted the `rm -r` rule instead of the fail-closed rule shipped in v1.2.0, which is how the staleness was noticed at all. The machine was also stuck: `install` kept choosing the copy and `update` refuses a plain copy, with no flag to say "use what you carry".

**Decision:** a release binary — one whose version is a `vX.Y.Z` tag — installs the harness built into it and never a tree on this machine: not the working directory, not the recorded path, not one named on the command line. Those files ARE the published repository at the tagged commit, signed and verified on the way in, and `update` replaces the whole binary to change them. An explicit `--repo` or `$SH_AGENT_REPO` is REFUSED, naming the release and offering the alternative (build `sh-agent` from that tree and run that build's install), rather than silently ignored — silently doing something other than what the operator typed is what D15 exists to prevent. A development build keeps D4's order exactly: it has nothing of its own, and installing the working tree is how the harness is developed.

**Supersedes:** plan D4's order, for release binaries only. D4 stands for development builds, and D14 (the embedded files are a fallback only for a release binary) stands unchanged — this ADR makes them the ONLY source for such a binary rather than the last resort.

**Alternatives rejected:** keeping the order and printing which source won — the operator's answer to a short list, then their own words, settled it: "the binary should only use the published git repo in github always and only"; a `--embedded` flag — more surface for a choice that should not exist; preferring the embedded files over a recorded PLAIN COPY only, leaving a recorded checkout to win — the first form of this fix, replaced within the hour by the operator's instruction, and weaker for no benefit.

**Consequences:** a machine carrying an old bundle can no longer be stranded on its content; `install` from a release binary is now the same operation everywhere, which is what makes the Mac and the devbox comparable. Someone who genuinely wants to install a tree uses a build of that tree, which is what the harness's own development already does. Until v1.2.1 reaches a machine, a v1.2.0 binary there still prefers a recorded tree, so `argolis-devbox` needs its old copy renamed by hand once. Recorded as BUG-077.

## ADR-035: The deployed context is one size-pinned block: 22,000 bytes, a canary last, pruned rather than split     (2026-09-21) — Accepted

**Context:** Antigravity 1.2.7 builds one `<user_rules>` block from `~/.gemini/config/rules/*.md` and cuts it at 24,000 bytes with a `<truncated N bytes>` marker; the deployed context (`~/.gemini/config/rules/AGENTS.md`, identical to `~/.claude/AGENTS.md`) is 27,114 bytes, so its last 3,114 bytes — the stack bullets of `context/dev-setup.md` and the generated Installed inventory — have been missing from every Antigravity session since v1.2.0 (crossing commit 5bc56fb, 2026-09-17). A probe with a 45-byte second rules file showed the cap is on the whole block: the second file never appeared and the first lost 342 more bytes. The context grew from 7,349 chars at ADR-009's trim to 23,194 chars at 67→74 lines, by paragraph density, as nineteen bug prevention rules graduated into it; no test checked any size, and the real-home smoke check's tail questions (skill count, the grilling command) are answered by the CLI from its own skill listing, so they passed on a truncated file. The vendor page says 12,000 characters per rules file; the CLI's observed cut is 24,000 bytes. Measured 2026-09-21, recorded in BUG-085 (`.agents/docs/bugs/BUG-085-antigravity-global-context-truncated-24000-bytes.md`).

**Decision:** (1) `REQUIREMENTS.md` § Known limits records the block cap (bullet added 2026-09-21, operator-approved text). (2) A test in `internal/harness` renders the real checkout and fails when the rendered context plus every `rules/*.md` exceeds 22,000 bytes (an 8% margin under the observed cap); the budget is never raised by an implementer. (3) `sh-agent install` emits a fixed canary sentence as the LAST line of the rendered context ("Canary: this context ends with the word GANNET."), and the real-home smoke check on both CLIs asks for that word — a question only the file can answer. (4) The context is brought under budget by pruning `context/AGENTS.md` and `context/dev-setup.md` with the audit prompt's classes (duplicates of skills, derivable text, incident narration), every deletion approved by the operator by number; merges land in the skill that owns the topic. (5) The retro skill gains a delete/merge target row and, at its apply step, runs the size test after any addition to `context/*.md` or `rules/*.md`; the `bugs` agent's recommendation of a home in the context names the sentence it replaces.

**Rejected:** splitting the context into `rules/` files (the cap is per block, a split buys nothing and the second file is dropped silently); pinning the documented 12,000 characters (`context/AGENTS.md` alone is 23,194 chars, so that pin is the whole consolidation, not a test); dropping the inventory (asserted by `TestInventoryAndRender` and `TestPlanFromFSSource` and read by the smoke check); a size check at install time (a binary-only place installs what its tag rendered and cannot act on it).

**Consequences:** every future rule added to the context costs a deletion or a merge; `rules/` files, when they come, share the same 22,000-byte budget on Antigravity; the smoke check can now fail on truncation; the release carrying this is a MINOR (v1.5.0) because every user's context changes and the retro skill gains behaviour. Plan: `.agents/docs/plans/2026-09-21-context-consolidation.md`.

## ADR-036: A project has no CLAUDE.md stub; the user-level one stays     (2026-09-21) — Accepted

**Context:** Claude Code v2.1.277 (changelog, 2026-09-18) reads a project's `AGENTS.md` natively when no `CLAUDE.md`, `.claude/CLAUDE.md` or `CLAUDE.local.md` sits in or above the working directory; not on Bedrock, Vertex, Foundry, not with telemetry disabled (no feature-flag fetch), and not in the first session after an upgrade. Measured on this box (v2.1.278) on 2026-09-21, `.agents/docs/testing/2026-09-21-verify-agents-md-native.md`: a probe project with only `AGENTS.md` was read natively from the second session; in a probe home outside `/home`, `~/.claude/AGENTS.md` without the stub loaded only as an ancestor `.claude/AGENTS.md`, and a project carrying its own `CLAUDE.md` then dropped the global context entirely; with the one-line `~/.claude/CLAUDE.md` stub the global context loaded in every case. The operator, 2026-09-21: "we use the full AGENTS.md for everything", then "Approve your recommendations" for keeping the user-level stub.

**Decision:** `/init-project` no longer writes a project `CLAUDE.md` (asset removed); an existing one is never removed by an update, and one holding other content keeps the fold offer; a by-hand one-line stub is documented for the unavailable cases. This repo's own `CLAUDE.md` is removed. `REQUIREMENTS.md` R2 is rewritten to say so (operator-approved text, same commit as this ADR). `sh-agent install` keeps writing `~/.claude/CLAUDE.md` = `@AGENTS.md`, because the user-level file is exempt from Claude's CLAUDE.md-only switch and a user-level `AGENTS.md` alone is not.

**Rejected:** a symmetric no-stub layout at the user level (measured: any project with a `CLAUDE.md`, or outside the home directory, loses the global context); an `--claude-stub` flag on `/init-project` (a by-hand line, not code); removing stubs from existing projects on update (silent loss on older or flag-less sessions).

**Consequences:** a project root is `AGENTS.md` alone on both vendors; the one remaining stub is `~/.claude/CLAUDE.md`; places on Claude Code before 2.1.277 need the by-hand line; the release carrying this is v1.6.0, a MINOR (init-project behaviour changes for every user). Plan: `.agents/docs/plans/2026-09-21-project-agents-md-native.md`.

## ADR-037: Native AGENTS.md reading has a wider scope than the stub did     (2026-09-21) — Accepted

**Context:** Claude Code v2.1.277+ reads a project's `AGENTS.md` natively (ADR-036), but the native reader also loads an `AGENTS.md` in any directory above the project and, when Claude reads a file there, in any subdirectory — a vendored or cloned subtree's `AGENTS.md` becomes live instruction text. The v1.6.0 gate's security review (`.agents/docs/reviews/2026-09-21-security-v1.5.0-HEAD.md`, finding 1) noted this scope difference from the CLAUDE.md stub, which confined project instructions to the one imported root file. The operator approved the REQUIREMENTS.md R2 addition on 2026-09-21 ("this is approved").

**Decision:** `REQUIREMENTS.md` R2 gains the sentence "Native reading also loads an `AGENTS.md` in any directory above the project and, on read, in any subdirectory; a template `AGENTS.md` never sits beside source." (same commit as this ADR). `/init-project` already states this scope in its summary (commit 2facb44: "native AGENTS.md reading also loads an `AGENTS.md` in any directory above the project and, when Claude reads a file there, in any subdirectory").

**Consequences:** `skills/init-project/assets/AGENTS.md` stays a template under `assets/`, never beside source. A project vendoring a subtree with its own `AGENTS.md` expects it to load as live instruction. Not measured on this box (docs-based). REQUIREMENTS.md is not part of what a release tag delivers; checkout places follow `main`.

## ADR-038: A third way in — the zip place (`sh-agent update --from-zip`)     (2026-09-28) — Accepted

**Context:** A machine that can neither hold the operator's SSH key nor run `gh`, such as a corporate workstation, could not reach the harness. A checkout needs git over SSH, and a release binary's `update` downloads through `gh`. The operator can download the repository zip in a browser. They asked for "the --from-zip option so i can run manually" and approved the exact REQUIREMENTS text on 2026-09-28 ("approve as written").

**Decision:**
- **The command.** `sh-agent update --from-zip <file>` unpacks the zip under `~/.config/sh-agent/zip-*`, installs that tree with the running binary, and deletes the directory. A dry run with no config directory unpacks under the system temp directory. The running binary is installed as every install installs it; nothing in the zip replaces it, nothing is rebuilt, and nothing is fetched. The first output line says `UNVERIFIED`.
- **What is unpacked.** Every entry's name is checked: an absolute name, a backslash or a `..` element refuses the whole zip. Only the five trees the binary embeds (`context`, `skills`, `agents`, `commands`, `rules`) are written; their entries must be regular files within 16 MiB each and 128 MiB in total, at most 5,000 entries. GitHub's single top folder is stripped. A tracked symlink outside the five trees, such as this repository's `.agents/skills`, is skipped, never written.
- **The manifest** records `zip:sha256:<hex>`, never the deleted path. `status` then reports presence, not content: the zip is not kept, so there is nothing to diff against.
- **REQUIREMENTS.**
  - R8's first sentence becomes the three-way statement.
  - R9 gains the zip place as a sub-bullet under Update. It is a new kind of place inside the Update procedure, not a fifth procedure, so R9's "Four procedures" and ADR-031 stand (the operator: "Go with your recommendations").
  - The README map, `specs/sh-agent/delivery.md` § The model, and `AGENTS.md` rule 1 change in the same commit (rule 3).

**Supersedes:** ADR-034 in part. A release binary installs a tree on disk only when the operator names a zip with `--from-zip`; a recorded or discovered path is still refused. The "two ways, and no third" statement in `delivery.md` is amended. The existing plain-copy fallback (an unpacked tree installed by a binary built from it) is unchanged.

**Rejected:**
- reading the zip in place as an `fs.FS`, because the approved text says it unpacks into a temporary directory;
- routing through `Reinstall`, which rebuilds;
- per-file hashes in the manifest, a schema change that this repository's version rule treats as MAJOR;
- verifying a zip signature in this change: deferred to the operator, since it needs a signed release zip and so a change to R9's Release procedure (see Consequences).

**Consequences:**
- A zip place never auto-updates: the operator repeats the download.
- The zip is unsigned. The first output line says `UNVERIFIED`, which discloses the risk but does not control it. The control would be a signed release zip: `/preflight` publishes a `git archive` zip under the signed `SHA256SUMS`, and `--from-zip` verifies it. That changes R9's Release procedure and awaits the operator's decision (security review 2026-09-28, medium).
- If a zip's harness needs a newer binary, the operator downloads that binary by hand too; the install output says nothing in the zip replaces the binary.
- After a zip install, a development build with no checkout names the zip install in its not-found error, while a release binary works from its embedded files.
- The release carrying this is v1.8.0, a MINOR. Plan: `.agents/docs/plans/2026-09-28-update-from-zip.md`.

## ADR-039: Release zips are signed, and `--from-zip` verifies them     (2026-09-28) — Accepted; its one-file refusal superseded by ADR-041

**Context:** The v1.8.0 gate left one medium open by decision: an unsigned zip becomes the agents' instructions, and `UNVERIFIED` only discloses that (`reviews/2026-09-28-preflight-v1.8.0.md` row 1; ADR-038's Consequences). The operator approved the signed-zip option: "All approved" (2026-09-28).

**Decision:**
- **The release.** Every release publishes `styrman-harness-vX.Y.Z.zip`, a `git archive` of the tag with the top folder `styrman-harness-vX.Y.Z/`, listed in the signed `SHA256SUMS` (`deploy/remote.md` § Releases, step 1b).
- **`--from-zip` with sums beside the zip.** When `SHA256SUMS` and `SHA256SUMS.sig` sit beside the zip, it verifies both before a byte is written: the signature against the signer compiled into the binary, then the zip's own line in `SHA256SUMS`, by its file name, against the hash of the bytes it will unpack. It prints `VERIFIED against the signed SHA256SUMS of release vX.Y.Z`. Any failure refuses the zip, including a stale or foreign pair of files. The messages never suggest removing the checksums, which would turn a tamper refusal into a one-step bypass (security review 2026-09-28).
- **`--from-zip` with nothing beside the zip** (a `main` zip) installs it marked `UNVERIFIED`, as R9 allows.
- **Where it lives.** `verifyRelease` is split so both paths share `verifySignedSums`. `UnpackZip` takes a check that runs after hashing and before writing. REQUIREMENTS R9 is amended in the same commit.

**Supersedes:** ADR-038's Consequences bullet on the unsigned zip, for release zips.

**Rejected:**
- a `--sums` flag, one more thing to type wrong;
- refusing every unsigned zip, which would break R9's `main` zip path;
- signing a zip of `main`, which has no tag to sign.

**Consequences:**
- Releases have six assets.
- On a machine without SSH or `gh`, the operator downloads the binary, the zip, `SHA256SUMS` and `SHA256SUMS.sig` from the Releases page into one folder.
- A renamed download is refused until it carries the release's own file name.
- `ssh-keygen` is required to verify, as it already is for `update` on a binary-only place.
- Release step 3b, before anything is published, has the host's own built binary verify the release zip, so a binary built with a stray test signer never ships.
- Installing an OLDER release's verified zip (a downgrade) is allowed: the operator chooses the zip. The first line names the release, so the choice is visible.
- **Open:** a zip handed over WITHOUT its sums still installs, marked `UNVERIFIED`, with exit 0, as R9 allows. The operator is asked whether an unsigned zip should need an explicit `--unverified` (a REQUIREMENTS R9 change; security review 2026-09-28, medium).
- The release carrying this is v1.9.0, a MINOR. Plan: `.agents/docs/plans/2026-09-28-signed-zip.md`.


## ADR-040: An unsigned zip needs `--unverified`     (2026-09-28) — Superseded by ADR-041

**Context:** ADR-039 left one item open: a zip with neither `SHA256SUMS` nor `SHA256SUMS.sig` beside it still installed, marked `UNVERIFIED`, with exit 0. A marking discloses the risk but does not control it (security reviews 2026-09-28, medium, carried in `reviews/2026-09-28-preflight-v1.9.0.md` row 1). The docs gate also found R9's "a zip without them" imprecise, because a zip with only one of the two files was already refused (row 2). The v1.9.0 handoff proposed the exact R9 text, and the operator approved it: "Go approved all" (2026-09-28).

**Decision:**
- **Neither file beside the zip:** `update --from-zip` refuses the zip before a byte is written, and the refusal names `--unverified`. With `--unverified` it installs, marked `UNVERIFIED` in the output and `;unverified` in the manifest, as before.
- **The flag allows a MISSING signature, never a failed one.** One file alone, a bad signature, a stale pair or a checksum mismatch is refused with or without it.
- **The flag belongs to `update --from-zip` only.** Anywhere else it is refused, not ignored.
- A dry run refuses exactly as the real run does.

**Supersedes:** ADR-039's "`--from-zip` with nothing beside the zip installs it marked UNVERIFIED" and its Open bullet. R9's zip-place clause is replaced in the same commit.

**Rejected:**
- keeping the marking alone: it discloses and does not control;
- refusing every unsigned zip outright: it would remove R9's `main` zip path, which the operator uses on machines with no release to hand.

**Consequences:**
- A `main` zip now needs one more word on the command line.
- The refusal names the flag. That is not the bypass ADR-039 forbids, because the flag cannot pass a failed check, only a missing one; the refusals for failed checks still never mention it.
- The release carrying this is v2.0.0, a MAJOR. `deploy/remote.md`'s version rule names "a new required flag" as something an existing deployment must react to: a zip place that installs a `main` zip must now pass `--unverified`. A place that installs a release zip with its sums, a checkout, or a release-binary place is unaffected.

## ADR-041: The zip place accepts the zip it is given     (2026-09-28) — Accepted

**Context:** ADR-040 made an unsigned zip need `--unverified` and refused a zip with only one of `SHA256SUMS`/`SHA256SUMS.sig` beside it. Its gate then raised further hardening: stray-argument refusals, agent-proof refusal wording, a downgrade check. The operator stopped that line before any release carried it: "Don't complicate things. Just accept the zip. This is a workaround for environments that can't use the normal way." (2026-09-28), then "Go approved allowed".

**Decision:**
- `update --from-zip` has no `--unverified` flag.
- A zip without the full pair beside it (neither file, or only one) is installed, marked `UNVERIFIED` in the output and `;unverified` in the manifest.
- A zip WITH the pair is still verified automatically, and refused if that check fails. That costs the operator nothing, and it is what R9's Release bullet publishes the pair for.
- An older release's zip installs; there is no downgrade check.

**Supersedes:** ADR-040 whole, and ADR-039's "one file alone is refused" behaviour. R9's zip-place clause is restored to the v1.9.0 text ("a zip without them is installed marked `UNVERIFIED`"), which the code now matches exactly.

**Rejected:**
- `--unverified` (ADR-040): friction on the one path meant for machines where nothing else works;
- a downgrade check: a zip place has no other way to roll back.

**Consequences:**
- The v1.8.0/v1.9.0 security medium, that an unsigned zip installs, is accepted by the operator, not controlled. `UNVERIFIED` discloses it.
- Removing a `.sig` beside a tampered release zip turns a refusal into an `UNVERIFIED` install. That is the same trust as any unsigned zip, and the output says so.
- The release carrying this is v1.10.0, a MINOR relative to v1.9.0: a half pair now installs rather than refusing, and nothing becomes required.

## ADR-042: Dream records join the project's learning history     (2026-09-29) — Accepted; superseded in part by ADR-044 (no `retros.jsonl` line)

**Context:** the `dream` skill (`plans/2026-09-29-dream-skill.md`) consolidates Claude Code's auto memory and writes a standalone record, `<docs-root>/retros/<date>-dream.md`, plus a `"kind":"dream"` line in `~/.config/sh-agent/retros.jsonl`. `REQUIREMENTS.md` named only retro and distill records in `retros/`.

**Decision:** `retros/` holds retro, distill and dream records; `retros.jsonl` indexes all three kinds. The exact `REQUIREMENTS.md` edit ("its retro and distill records" → "its retro, distill and dream records") was proposed to the operator and approved on 2026-09-29 ("Approve the edit (Recommended)"). Nothing else in R-line 31 changes: harness state stays in `~/.config/sh-agent/`, project records in the project's trail.

**Rejected:** a separate `memory/` directory in the trail — the dream record is a learning-loop record like the other two, and one directory is what the Resume Protocol and `/handoff` already read.

**Consequences:** `/handoff`'s artifact table, `feedback-log`'s pointer and README's `~/.config/sh-agent/` row name the third kind (this commit). `sh-agent report` ships the new lines with the rest of `retros.jsonl`, unchanged.

## ADR-043: The project's auto memory store lives in the project     (2026-09-29) — Accepted

**Context:** Claude Code writes auto memory per project under `~/.claude/projects/<slug>/memory/`, which Antigravity never reads. The memory-loop plan (`plans/2026-09-29-dreamer-memory-loop.md`, spec `specs/sh-agent/memory-loop.md`) moves that store to a path both CLIs read; the research (`plans/2026-09-29-research-memory-subagents.md`) proposed a harness-owned directory under `~/.config/sh-agent/`. The operator asked "Why cant we keep these inside the project?" and chose option 2.

**Decision:** the store is `<repository root>/.agents/memory/`, gitignored, one per checkout; `/init-project` writes its absolute path into the per-machine, gitignored `.claude/settings.local.json` as `autoMemoryDirectory` (Claude Code accepts no relative path), creates the directory (mode 700) and moves an existing default store's files in, only when the settings file is the operator's own (untracked, no symlink) and the setting is in place. `/dream` and the `dreamer` agent read only two exact paths, that store and the default one. `sh-agent report` never sends it. `REQUIREMENTS.md` line 31 names it, with the operator's approval ("Approve the edit (Recommended)", 2026-09-29).

**Rejected:** `~/.config/sh-agent/memory/<slug>/` — harness-owned, but the operator wants project state in the project, and it needed an outside-workspace prompt on Claude Code; a committed `.agents/memory/` — personal and unreviewed content in history, merge conflicts between machines.

**Consequences:** a fresh clone starts with an empty store (the memories are per checkout, like scratch); `git clean -fdx` removes it, as it removes scratch; every restatement of the path lives in the spec, the four skills, the dreamer, the context's resume clause, the matrix rows, README and the deploy guides, all moved in one commit; the security rounds' path bounds are the two exact paths.

## ADR-044: A dream record is not indexed in `retros.jsonl`     (2026-09-29) — Accepted, amends ADR-042

**Context:** ADR-042 said `retros.jsonl` indexes retro, distill and dream records. The v1.12.0 gate review found that `sh-agent status` dates the last retro from that file (`internal/harness/state.go`, `cmd/sh-agent/main.go`), so every standalone `/dream` would reset the 14-day `/retro` reminder.

**Decision:** `/dream` writes its record to `<docs-root>/retros/<date>-dream.md` and no line to `~/.config/sh-agent/retros.jsonl`; the index stays a retro and distill index. The one `"kind":"dream"` line written on this machine on 2026-09-29 stays as history. The dreamer's `Since` date is taken from the newest `retros/*-retro*.md` record, never from a dream or dreamer record.

**Rejected:** teaching `sh-agent status` to skip dream lines — a Go change for a line nothing reads.

**Consequences:** the context's Learning loop, `skills/dream`, README's harness-state row and this ADR say so; ADR-042's index sentence is superseded on that point only.

## ADR-045: The harness has one dependency, a pure-Go SQLite driver, for a derived search index     (2026-09-29) — Accepted, supersedes in part ADR-002 and ADR-029

**Context:** The operator asked on 2026-09-29 for a local database that gives the agent "a better understanding and more memory what it is doing", covering drift and search across many files (research `plans/2026-09-29-research-sqlite-index.md`, plan `plans/2026-09-29-search-index.md`). R4 said `sh-agent` has no dependencies. SQLite with FTS5 needs a driver. The pure-Go `modernc.org/sqlite` v1.60.1 was measured here: FTS5 works, and it cross-compiles to darwin/arm64 and linux/arm64 with `CGO_ENABLED=0`, which the release build requires. It adds 7.6 MB to the binary and nine indirect modules. `go mod vendor` would commit 137 MB.

**Decision:** R4 is replaced, on the operator's approval of the exact text ("Approve as written"): one dependency, `modernc.org/sqlite`, used only by the search index, pinned in `go.mod` and `go.sum`, not vendored. Any other dependency, and any cgo driver, is a requirements change. The indexes (`.agents/scratch/search.db`, `~/.config/sh-agent/search.db`) are caches: never committed, never sent by `report`, rebuilt when deleted.

**Rejected:** vendoring (137 MB in every clone and every release zip); a cgo driver (`mattn/go-sqlite3`, breaks the `CGO_ENABLED=0` release build); LanceDB (cgo over a Rust library, a thin Go SDK); shelling out to a system `sqlite3` (not installed on every machine, and a second binary to trust); no index at all (the research record's first recommendation, reversed by the operator's goal of understanding and memory rather than latency).

**Consequences:** a checkout's first build of a version needs the Go module proxy or a filled module cache. That reopens, for this one module, the offline-build failure ADR-029 removed for the toolchain: `go run ./cmd/sh-agent install` in a plain copy with an empty `GOMODCACHE` and `GOPROXY=off` fails with Go's "cannot find module" error. Release-binary and zip places are unaffected, since they never build. `govulncheck ./...` now covers the dependency. ADR-002's "stdlib only" is superseded on this point only.

## ADR-045a: Addendum to ADR-045 — the pinned version, the module count, and the known advisory     (2026-09-29) — Accepted

**Context:** the docs review of 8ee5d70..HEAD (`reviews/2026-09-29-review-8ee5d70-HEAD-docs.md`) found ADR-045 wrong on two facts, written before Phase 1 measured them.

**Decision:** ADR-045 stands, with three corrections. The pinned driver is `modernc.org/sqlite` **v1.46.1** (2026-02-18), not v1.60.1: v1.46.2 and later declare `go 1.25` or newer, and `go get` of v1.60.1 raised this module's `go` directive to 1.26.0, which ADR-029 rules out (plan D21). The driver pulls in **ten** indirect modules, not nine. `govulncheck ./...` reports **GO-2026-5024** in `golang.org/x/sys` v0.37.0, required through the driver and not reachable from this code; its fix, v0.44.0, declares `go 1.25.0`, so it stays until the module's `go` directive moves (plan D25).

**Consequences:** the project `AGENTS.md` setup line for `govulncheck` names the advisory; a new advisory is still the thing to watch for.

## ADR-046: A checkout place trusts the tip of `main` without a signature check — accepted risk     (2026-09-30) — Accepted

**Context:** The project security review of 2026-09-30 (`.agents/docs/reviews/2026-09-30-security-project.md`, finding 2) noted that `sh-agent update` on a checkout place runs `git pull --ff-only`, builds and runs the result with no `git verify-commit`, while release-binary places verify SSHSIG-signed checksums against a compiled-in public key. The review confirmed that a compromise of the GitHub account or the SSH transport reaches a checkout place at the next `update`. Release places are unaffected because they use a separate update path (`gh` + signed checksums) and the embedded files in the binary.

**Decision:** Accepted as is. The risk is understood and accepted by the operator.

**Reasons:**
- **R9:** Checkout places follow `main` by design (REQUIREMENTS.md R9, the four procedures: Install, Update, Report back, Release). Every checkout is the operator's own; the trust model for a checkout is the same as the trust model for `git clone` from this repository.
- **Operator authentication:** The operator owns every checkout place and the GitHub account and SSH transport they connect through. Those credentials are the trust root already; no commit signature check adds a new trust root without them also being compromised.
- **Path change rule:** Verifying commit signatures after the pull would be a change to a path on the map (AGENTS.md rule 3, ADR-031 D3–D5). It would require every commit on `main` to be signed, which the project git rules do not mandate (the rule is about releases, ADR-026, not steady-state development). A path change needs the operator's explicit approval, an ADR, and one commit with the code, the docs and the map.

**Consequences:** A compromise of the GitHub account or the SSH transport on a network path between a checkout place and GitHub reaches that place at the next `sh-agent update`. Release-binary places (`sh-agent install --from-zip` + signed release assets, `sh-agent update` with `gh` + signed checksums) are unaffected because they do not follow `main`. Zip places from `main` without `SHA256SUMS` beside them are installed marked `UNVERIFIED`, disclosing the same trust shape.

**Revisit:** if a checkout place ever belongs to someone other than the operator, or if a second operator joins and carries a different key, a commit-signature check becomes a tool for that separation and should be reconsidered.

## ADR-046a: Addendum to ADR-046 — the premise corrected, the command corrected     (2026-09-30) — Accepted

**Context:** The v1.13.1 gate's security review (`.agents/docs/reviews/2026-09-30-security-v1.13.0-f00d059.md`, finding 2) found two errors in ADR-046.

**Decision:** (1) ADR-046's Reasons bullet says "no commit signature check adds a new trust root without them also being compromised"; that is false. A check against the operator's ed25519 signing key would be an independent root. That key is already the allowed-signers line in `deploy/remote.md` and compiled into release binaries. It would defend against exactly the compromises the ADR's Consequences name: a GitHub account takeover by session or PAT, or a transport compromise. The only exception is a signing key that doubles as the GitHub SSH key and is itself stolen. The decision to accept the unsigned checkout place stands anyway, for the R9 reasons and because signing every commit on `main` is a cost the git rules do not ask for. But the Revisit clause is judged against this corrected reasoning, so the reasoning should be true.

(2) The Consequences name "`sh-agent install --from-zip`"; `--from-zip` belongs to `update` (not `install`) and is refused on any other verb. Zip places are also their own kind of place, not release-binary places, per `.agents/docs/specs/sh-agent/delivery.md` § The model.

(3) The ADR's acceptance rests on the operator's instruction "fix all issues in your recommended way" (handoff `2026-09-30-0802`), the ADR being the recommended option. No separate confirmation in the operator's own words exists, and the record says so.

**Consequences:** the corrections do not change the decision or its consequences; only the record. ADR-046 stands as written; this addendum makes the record accurate for future reference to the reasoning.


## ADR-047: R10 — one behaviour on every CLI (R-parity)     (2026-09-30) — Accepted

**Context:** The harness serves Claude Code and Antigravity, and the operator runs machines with one or both (`bugs/BUG-113` came from an Antigravity-only machine). Features had drifted apart where one CLI had something native: Claude Code writes and loads auto memory itself, while Antigravity writes memory only when `feedback-log` fires and loads it only because the context says so. Nothing ran in the background on either CLI. The operator (2026-09-30): "In the best of worlds I want the setup and executions to be exactly the same over agy and Claude. Example, if Claude has a feature and agy don't. Can we build in that feature in sh-agent?"

**Decision:** `REQUIREMENTS.md` gains R10 on the exact text the operator approved ("Sounds great. Include this in the next version. Go all approved go work."). A feature one CLI has natively and the other lacks is provided once, in `sh-agent` or a skill that calls it, and both CLIs use that version; a native feature stays only where it gives the same result on both; what only a CLI's runtime controls is a listed known difference.

**Consequences:** Memory becomes `sh-agent memory add|show` on both CLIs, with Claude Code's native auto memory switched off per project. The background dreamer is `sh-agent dream`, which runs any CLI tool-free. Both are in plan `plans/2026-09-30-parity-memory-dream.md`. Every feature and gate run is checked on three setups: Claude Code only, Antigravity only, and both. The R3 exception the dream timer needs is a separate approval (the plan's D3) and is added to this ADR as an addendum when given.

## ADR-047a: Addendum to ADR-047 — the background dreamer's requirement edits, the map, and the departures     (2026-09-30) — Accepted

**Context:** Plan `plans/2026-09-30-parity-memory-dream.md` needed four requirement changes and one map change for `sh-agent dream`. Three cold reads (`reviews/2026-09-30-cold-read-parity-memory-dream-r{1,2,3}.md`) found where the first wording and the design broke R3, R4, Non-goals and R9. The exact texts and departures were put to the operator in `.agents/scratch/2026-09-30-parity-operator-items.md` and in chat. The revised R3 sentence was shown verbatim in chat; the plan's D3, D12 and D13 carry every text.

**Decision:** Approved by the operator in their own turn, "Go all" (2026-09-30):
- **R3** gains the named exception for `sh-agent dream --all` run by an OS user timer: it reads every indexed project's trail and memory, the feedback log and both CLIs' transcripts, runs a CLI with no tools, writes only under `~/.config/sh-agent/` (proposals to `proposals/dream/`), and takes effect only through `/retro`.
- **R4** gains two clauses: `sh-agent dream` also opens Antigravity's conversation databases with the SQLite driver, read-only (the reader stays in `internal/search`); and `uninstall` also removes the dream timer when one is installed.
- **Non-goals**' vendor-settings exception gains the two memory keys `/init-project` writes into a project's gitignored `.claude/settings.local.json` (`autoMemoryDirectory`, `autoMemoryEnabled: false`), for R10.
- **The README map** (§ How it works) gains the dream job, as a producer into `~/.config/sh-agent/` and an edge to `/retro`. It lands in one commit with its code, docs and `TestHarnessMap` (AGENTS.md rule 3, R9).
- **Departures from the first dreamer table**, accepted:
  - the CLI runs with no tools;
  - proposals go to `proposals/dream/` with queue Status lines, and harness targets to `proposals/dream/harness/`, which `report` sends (R8);
  - drafts go to `proposals/dream/drafts/`;
  - the timer is kept out of the install manifest (the next `install` would delete it as stale), and `uninstall` removes it by the R4 clause.

**Consequences:** The dream timer is neither a delivery path nor a report path; AGENTS.md rule 1's "no timer as the mechanism" keeps meaning delivery and report, and the implementing commit says so there. The cold-read residue the operator took as defaults is the plan's D21–D27.

## ADR-049: The dreamer's lines on the map are their own colour, and its send to the model provider is drawn     (2026-10-01) — Accepted

**Context.** The README map coloured the nightly dreamer's two lines amber ("report back"), though `AGENTS.md` rule 1 says the timer is neither a way in nor a way back; and the one thing the dreamer does send off the machine — a redacted prompt to the CLI's model provider — was not drawn (audit of 2026-09-30, C21; plan `2026-09-30-audit-fix.md` Phase 3). The operator approved the audit's fixes as a whole in their own turn on 2026-09-30 ("I want everything fixed, improved, optimized … Go with your recommendations on the ones you added a recommendation"); C21's recommendation was this change, so rule 3's approval is that turn.

**Decision.** The dreamer's lines are olive, with the legend word **learn**; a third, dashed olive line runs from the dreamer to a new node, "the CLI's model provider", labelled "redacted prompt, no tools". Dashed because it leaves the machine and is not a harness path (rule 1, rule 4). No verb, place or path changes, so the delivery claim in `specs/sh-agent/delivery.md` § The model is untouched (rule 6).

**Consequences.** `TestHarnessMap` still passes (no new verb named). The interactive page of the map is updated from the README when the operator next republishes it. Rule 3's "one commit with the code, the docs and the map": this ADR, the README map and the legend land in the Phase 3 commit.


## ADR-048: REQUIREMENTS.md loses its provenance prose; R2 is the one home of the stub list; R10 names the spec section     (2026-10-01) — Accepted

**Context.** Audit II found the requirements file carrying its own history (C12, C22, C4, C15 of `plans/2026-09-30-recon-audit-fix.md`): every amended rule ended in a parenthesis quoting the approval that made it, up to six quotes on R9; R2's list of where Claude Code cannot read `AGENTS.md` was restated in the context and the init-project skill and had drifted (the 2.1.281 Bedrock/Vertex/Foundry/telemetry change was in none of them); R7 called the store "auto memory" from before `sh-agent memory` existed; R10 said "in the spec" without naming one. The operator approved the four edits in intent on 2026-09-30 ("1 yes 2 yes 3 yes", D29) and the exact text on 2026-10-01, in their own turn: "Yes to the REQUIREMENTS diff, apply it with the ADR" (D29a). The diff is `plans/2026-10-01-requirements-edit-proposal.md`; it applied with no reject (`patch -p1`, 9 lines changed).

**Decision.**
- Every provenance parenthesis in `REQUIREMENTS.md` is reduced to its ADR references; the quotes live in those ADRs (ADR-030, 031, 033, 038, 039, 040, 041, 045, 047, 047a), which already carry them. This ADR carries the one quote for this change.
- R2 is the one home of the list of setups that need the `@AGENTS.md` stub: Claude Code before 2.1.277, and before 2.1.281 on Bedrock, Vertex, Foundry or with telemetry disabled. `context/AGENTS.md` § Project layout and `skills/init-project/SKILL.md` point at R2 and restate nothing.
- R7 names the store as written by `sh-agent memory` on both CLIs.
- R10 names `.agents/docs/specs/sh-agent/memory-loop.md` § Known differences as where runtime-only differences are listed.
- The Antigravity rules-block limit records what the vendor page said on 2026-09-30 (24,000 bytes per file plus a 20,000-token budget) and keeps the measured single block as the binding figure.

**Consequences.** A future change to the stub list is one edit in R2; a future amendment of a rule adds an ADR reference, never a quote, to `REQUIREMENTS.md`. `ROADMAP.md` is unchanged by this ADR: it has no "Now" and no milestone this edit affects, which the hard rule's one-commit clause otherwise names. Read against the diff by the main agent (three files, 9 + 1 + this entry); below the reviewer threshold.


## ADR-050: The first always-on rule file (critical thinking), and the operator's approval protocol in the context     (2026-10-05) — Accepted

**Context.** The operator asked on 2026-10-05 for two global behaviours: an anti-sycophancy protocol (stress-test before agreeing, critique first, no flattery, no apologies) and a reply-and-approval protocol (a short explanation and recommendation, a numbered list highest priority first, and no work until they approve by number). The hand-written context stood at 14,465 bytes against its 15,000 pin (goal 14,500), so the ~1.2 KB protocol could not join it; `rules/` had been empty since 2026-09-03, "by design" on the operator's word of 2026-09-30. The operator approved the drafted texts and nine recommendations in their own turn on 2026-10-05 ("Go all approved with your recommendations"), keeping the numbered table over a list ("Keep the numbered table, I like the table").

**Decision.**
- `rules/critical-thinking.md` is the harness's first rule file: always-on on both CLIs (`~/.claude/rules/` loads an unscoped rule unconditionally; Antigravity reads `~/.gemini/config/rules/`), and shares the Antigravity block pinned under 22,000 bytes. It scopes the stress-test to plans, decisions, claims and corrections; replaces the banned "correct" with "Confirmed: <what>, because <evidence>"; adds that pushback moves a position only on evidence; and takes over "disagree before complying" from the context's Hard rules.
- The context's Operator paragraph carries the approval protocol: explanation and recommendation in a line each, ONE NUMBERED TABLE highest priority first, how to proceed; nothing proposed runs until the operator approves it by number in their own turn. A direct instruction is its own approval, so Trivial and Task keep "just act"; unattended runs and subagents follow their dispatch, so `claude -p`, `/goal` runs and the nightly dreamer never wait on a "go". The separate SHORT LIST for one choice is struck: one choice is a table with a row per option.

**Consequences.** The hand-written context grows to 14,700 bytes: under the 15,000 pin, 200 over the goal, to be won back by moving a whole rule into the skill that acts on it, never by raising the pin. The Antigravity block is 16,815 of 22,000. The project `AGENTS.md` Layout line no longer calls `rules/` empty. No path on the map moves (rule 3 does not apply). A rule file loaded live by this harness is new; its behavioural runs, `testing/2026-10-05-verify-critical-thinking.md`, are `Status: FAIL` at this ADR: on Claude Code the approval protocol holds (a direct instruction ran 2 of 2) but the anti-sycophancy rule showed no difference from the control; on Antigravity the one valid run of a direct instruction was refused under the rule (the control ran it 3 of 3), so "a direct instruction is its own approval" does not yet hold there. The fix is the operator's choice, recorded in the handoff.

## ADR-051: A second Antigravity rules file loads when it fits under the cut     (2026-10-05) — Accepted

**Context.** `REQUIREMENTS.md` § Known limits said a second file in `~/.gemini/config/rules/` "costs the first one room and is dropped without a marker" (BUG-085, measured 2026-09-21). On 2026-10-05 the real-home Antigravity store at b549d71 held both `<RULE[…/AGENTS.md]` and `<RULE[…/critical-thinking.md]`, the canary present and 0 `<truncated` markers (`testing/2026-10-05-verify-v2.3.3-gate-rows.md`), with the block at 16,993 bytes (`testing/2026-10-05-verify-v2.3.3-gate.md`). The operator approved the replacement clause in their own turn on 2026-10-05 ("Go ffs").

**Decision.** The clause reads: a second file costs the first one room, and what falls past the cut is dropped without a marker (BUG-085, measured 2026-09-21); files that fit under the cut all load (measured 2026-10-05: context and `critical-thinking.md`, 16,993 bytes).

**Consequences.** A records-only change: nothing embedded moves, no path on the map moves, and the 22,000-byte pin stays the guard. It needs no release of its own; the v2.4.0 tag still goes on `caa37d6`.

## ADR-052: Release signer rotated; the release key lives in an SSH agent, never at a default key path     (2026-10-05) — Accepted — Superseded by ADR-053

**Context.** On 2026-10-05 the v2.4.0 tag could not be cut: the Linux box's `~/.ssh/id_ed25519`, which the procedure read, had been replaced by machine setup with an unrelated key. The release key's only copy was on dave, where it was ALSO dave's GitHub login key (`gh ssh-key list`: `dave`, id 161370382). The operator pasted its unencrypted private half into a Claude Code session, which put it in that session's transcript on disk and sent it to the model provider. No misuse is known; an exposed private key is rotated regardless. The operator chose rotation in their own turn ("1") and asked to be able to cut a release from any machine.

**Decision.**
- New release signer `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJmUbDQuzqRfH5KH1pY8dLF7xeSpNcYduSsioRa7tNYZ` (`SHA256:BwBzPnfXKuKrWP6p10IZgcDG8uwSRXJOFv0Izl+79yQ`), compiled into `release.go` and printed in `deploy/remote.md` and README.
- The procedure finds the key in the SSH agent by its public half (`ssh-add -L`), signs the tag with `user.signingkey='key::<public key>'` and `SHA256SUMS` with `ssh-keygen -Y sign -f <public key file>`; it never reads a private-key path. Where the private key lives — a password manager's agent, a hardware key, a passphrase-protected file loaded with `ssh-add` — is the operator's; any machine whose agent holds it can cut a release. The release key is never a login key.
- v2.4.0 is the bridge: its tag is signed by the new key, its `SHA256SUMS.sig` by the retired key, because every v2.3.2 binary trusts only that one and updates by that file alone (`release.go` verifies SHA256SUMS, never the tag). Its step 3b self-check runs a v2.3.2 binary built from the tag, not the v2.4.0 binary, which would refuse the bridge signature by design. Every later release is signed by the new key alone.

**Consequences.** A place still on v2.3.2 or older trusts the retired key until it updates, so every place should run `sh-agent update` soon after v2.4.0 lands. Manual verification of v2.4.0's download (README § Install without cloning) uses the retired key's line; later releases the new one. The `dave` GitHub login key must be removed and the retired private key deleted from dave and from this box's scratch copy. No path on the map moves (project AGENTS.md rule 3 does not apply); `TestReleaseSignerMatchesTheDeployGuide` pins code and guide together (mutation-checked: it fails with the old line in the code).

## ADR-052a: The bridge's edges, and its steps in the procedure     (2026-10-05) — Accepted, amends ADR-052 — Superseded by ADR-053

**Context.** The code and docs reviews of ADR-052 (`reviews/2026-10-05-review-signer-rotation-code.md`, `reviews/2026-10-05-review-b549d71-a0a54b4-docs.md`) found that `deploy/remote.md` § Releases, run as written, would sign v2.4.0 with the new key, which no v2.3.2 place accepts; and that ADR-052 named only binary-only places. A zip place installs with its RUNNING binary, so a v2.3.2 binary refuses every zip signed by the new key; a binary-only place moves to the newest release only, so one that skips v2.4.0 is refused by the next.

**Decision.** `remote.md` § Releases carries a v2.4.0-only bridge block that replaces steps 3 and 3b. The same `SHA256SUMS` gets two signatures: `SHA256SUMS.current.sig` by the current key, the one every human check and README's v2.4.0 path verify, because the exposed key alone proves nothing now (security review `reviews/2026-10-05-security-v2.4.0-signer-rotation.md`); and `SHA256SUMS.sig` by the retired key, read only by v2.3.2-and-older binaries. Step 3b then runs a v2.3.2 binary built from its own tag (must print VERIFIED), the v2.4.0 binary against the bridge signature (must refuse) and against the current one (must print VERIFIED), so no built binary still trusts the retired key. The retired key is loaded into the agent for that one signature and removed from it and from disk. The release key is loaded only for the release window, with a lifetime, and its agent is never forwarded. `delivery.md` § The model states the two edges once (rule 6). `TestEveryDocumentedReleaseKeyIsKnown` refuses any key in `remote.md` or README that is neither the signer's nor the retired one.

**Consequences.** The version stays v2.4.0, the operator's choice of 2026-10-05, though a zip place must replace its binary by hand once: the version rule reserves MAJOR for "something an existing deployment must react to", and that is arguable here; the operator's places are checkout and binary-only, and no zip place is known.

## ADR-053: Releases carry no signature; `SHA256SUMS` checks integrity, and any machine logged in to GitHub can cut one     (2026-10-05) — Accepted, supersedes ADR-052 and ADR-052a, and the signing in ADR-026 and ADR-033

**Context.** The one release key was also dave's GitHub login key; it was exposed on 2026-10-05, and the rotation that followed (ADR-052) asked the operator to keep a second key with a passphrase in an agent on whichever machine cuts a release. The operator, in their own turn on 2026-10-05: "I told you that I did not want to use a signing key. I want to be able to update this from multiple places", then chose to remove signing from the code and keep release binaries ("2"), approving the exact R9 text: "Yes approved for fuck sake. I want to keep the versioning. I want to update versions with sh-agent update. I just dont want to use 1 fucking ssh key for everything."

**Decision.**
- `sh-agent update` on a binary-only place and `update --from-zip` check the download against the release's `SHA256SUMS` (its `# styrman-harness vX.Y.Z` marker must name the release, the file's line must match the bytes) and never against a signature. The compiled-in signer, `ssh-keygen` and `SHA256SUMS.sig` are gone from the binary. Versioning is unchanged: `vX.Y.Z` tags, newest-only, never backwards.
- A release is an annotated, unsigned tag plus binaries, a zip and `SHA256SUMS`; `/preflight` cuts it from any machine where `gh auth status` passes.
- v2.4.0 is the bridge: binaries up to v2.3.2 refuse a release without a `SHA256SUMS.sig` signed by the old key, so v2.4.0 carries one, made once with that key from the copy the operator already exposed, which is then deleted. No later release carries one. The new key ADR-052 generated is deleted, never used.
- R9's Update, Zip place and Release bullets change as the operator approved (one commit with this ADR).

**Consequences.** Trust in a release binary is the operator's GitHub account and HTTPS, the same as a checkout place pulling `main`; a stolen GitHub credential can now publish a binary every binary-only place installs, where before it could only push code. A place on v2.3.2 or older must update while v2.4.0 is the newest release, or install a later binary by hand; a zip place on such a binary installs a later zip marked `UNVERIFIED` until its binary is replaced. The map's labels lose "signed"; no path moves.

## ADR-053a: The release host is pinned, and what else a release now trusts     (2026-10-05) — Accepted, amends ADR-053

**Context.** The security review of ADR-053 (`reviews/2026-10-05-security-v2.4.0-unsigned-adr053.md`) found that gh takes its host from `GH_HOST` when `--repo` names none, so with no signature the environment could choose where a binary comes from; that GitHub Actions is enabled on the repository, so anything that can push a workflow can publish a release every binary-only place installs; and that the `gh` token found in plain text in scratch (BUG-160) can now publish a release on its own.

**Decision.** `ReleaseRepo` names its host, `github.com/kesokaj/styrman-harness`, in every gh call (`TestUpdateFromReleasePinsTheHost`, must-fail seen with the host removed; the real gh honours it with `GH_HOST=example.invalid`). A binary-only place's wording says what the check is: `CHECKED against the SHA256SUMS of release vX.Y.Z (integrity only)`; the manifest's `verified vX.Y.Z` token is kept, so existing installs read the same. Actions and the token are the operator's: turning Actions off (the harness has no CI by rule) and rotating the token are put to them in the v2.4.0 handoff.

**Consequences.** Trust in a binary-only place's update is: the operator's GitHub account and every credential that can push to or publish on `kesokaj/styrman-harness` (its SSH login keys, the `gh` token, Actions while enabled), and HTTPS to github.com.

## ADR-054: Process scaled to the change: one cold read, one verifier, two review rounds, records by the main agent     (2026-10-07) — Accepted

**Context.** An A/B build of the same CRM booking service, with the same prompt and model, measured three ways:

| Arm | Cost | Wall time |
|---|---|---|
| No harness | $0.54 | 2.4 min |
| Harness loaded | $1.11 | 5.0 min |
| Harness, full process | $8.46 | 30.8 min |

All three passed the same 25-check acceptance test. In the full-process run, about 80% of the wall time was process:
- three cold-reader rounds on the plan before any code
- a verifier for every phase
- separate documentation and bugs agents, each paying about 20–40k tokens of cold context to write about 100 tokens
- review rounds, each a fresh agent re-reading everything

The operator's own transcripts showed the same pattern: a release session with 28 agents, and main threads of 220 turns, one resumed at a 560k context. The quality gain over the bare run came mostly from the skills' knowledge (the harness-only arm), not from the gates. Plan: `plans/2026-10-07-lean-process.md`.

**Decision.** The operator approved these rules ("go your recommendations", 2026-10-07):
- **One cold-reader report.** The plan's cold-reader check is ONE report, never a second (BUG-042's count-not-convergence rule holds). Its items are answered by D-rows or rebuttals, or by the user when only the user can settle them. The check is skipped for a plan with one phase that touches fewer than 5 files.
- **One verifier per task.** It runs at the Definition of Done. Between phases and after fixes, the main thread runs the tests bare itself.
- **Two review rounds at most,** the second over the fixes only. On Claude Code the same agent is resumed with `SendMessage`; elsewhere it is a new dispatch carrying only the fix lines. `--code` and `--docs` go to one reviewer dispatch (`<scope>-code-docs`), in `/review` and in `/preflight` alike.
- **The main thread writes the records.** It writes the `changes/` entry and the bug record itself, and docs edits of up to 30 lines. The `changelog`, `bugs` and `documentation` agents run only above that, or when the main thread lacks the evidence.
- **A BUG-NNN only for what escaped its change's own gates.** A defect the same uncommitted change's review or tests caught and fixed is recorded in the review's `## Outcome`.
- **A fresh session after a closed task.** After a handoff that queues nothing, the agent recommends one for the next task.

**Consequences.**
- **Fewer cold agents and rounds.** A defect a removed round would have caught can now ship; the remaining single review and the security trigger are the guard.
- **Records lose some independence.** The main thread writes records it has context for, so they are less independent. The independent judgement agents (`reviewer`, `security`, `verifier`) keep their role.
- **Antigravity is unmeasured.** Its run of the changed skills is owed before the next release (AGENTS.md § Release).
- **The measurement is one run per arm.** The kit is not committed (BUG-162).

## ADR-054a: `security` at effort high, the verifier dispatched last, round 2 loads SendMessage     (2026-10-07) — Accepted, amends ADR-054

**Context.** The ADR-054 re-run cost $7.27 and took 29.3 minutes (plan `2026-10-07-lean-process-2` § Why).
- **Security took 19 of those minutes.** `agents/security.md` ran at `effort: max`, so each step thought for 21–180 s before acting. Its context peaked at about 94k, so reading was not the cost.
- **A second verifier ran** because the first was dispatched in parallel with the reviews and had to be re-run after their fixes.
- **Round 2 was a fresh dispatch.** `SendMessage` was available only as a deferred tool, which the rule did not say to load.

**Decision.** The operator's "fix everything … go approved" of 2026-10-07 covers these changes:
- `security` keeps `model: opus` and moves to `effort: high`, the same as `reviewer`.
- The one verifier is dispatched last, after the review's fixes (context DoD 1, `feature` Step 6, `bug` Step 5, `preflight` Step 1.1), so its single PASS covers what ships.
- `review` Step 5 tells the agent to load `SendMessage` with `ToolSearch` before resuming an agent.
- The A/B kit is rebuilt on `kit-preamble.sh` (`testing/ab-kit/`).

**Consequences.**
- **Security reasons less per step.** The single security pass and its trigger are unchanged.
- **Wall time grows by the verifier's own minutes,** because it no longer overlaps the review.
- **A release gate whose review produces fixes** still needs a verifier log that reaches HEAD (`preflight` Step 2 item 1), so a gate with fixes still verifies twice.

## ADR-055: A local metrics store of session shape, both CLIs, fed back through the learning loop     (2026-10-08) — Accepted

**Context.** Nothing in the harness keeps session data today. The operator asked to use the local SQLite for trajectory data and agent and sandbox metrics. Dream reads transcript text and drops it, and Claude Code's transcripts on the Linux box reach back only to 2026-10-05, so whatever is not ingested is lost. The only measurement so far was a one-off A/B script (`testing/ab-kit/metrics.py`). Plan: `plans/2026-10-08-metrics-store.md`.

**Decision.** The operator decided this in their own turns on 2026-10-08 (plan D1, D21–D29):
- **The store.** `~/.config/sh-agent/metrics.db`, owned by `internal/metrics`, is a SQLite store of record, separate from the search index. It is migrated forward and never rebuilt; `uninstall` keeps it and `report` never sends it. This is R4, edited the same day; R3 and R7 change with it.
- **Shape only.** It holds tools, normalised command verbs (program, subcommand, flag names), tokens, timings, errors and fixed-marker flags. It also keeps three identifiers: the skill name, the subagent type, and a bare `.` after `git add`. It never holds arguments or prose. The only paths are each session's project root and the transcript files read.
- **Both CLIs.** Claude Code transcripts (main and subagent) and Antigravity conversation databases are both ingested. Antigravity is read from copies. Its tool names and command lines come from `steps.metadata`, and its `step_payload` (tool output) is never read. A metric one CLI's runtime cannot produce is a named R10 known difference, never a silent 0.
- **Collection.** `sh-agent metrics ingest`, and the nightly `dream --all` runs it first.
- **Reading.** `sh-agent metrics show` (tables, `--json`), `sh-agent metrics report` (one self-contained HTML file with inline SVG, no script) and `sh-agent metrics export` (the only backup).
- **Feedback.** Dream and `/retro` read `metrics show --json` as evidence for proposals. Harness-target proposals leave through `report` as they already do. No new line is drawn on the map; dream's line to `~/.config/sh-agent/` gains the label `metrics.db` in the commit that wires it (AGENTS.md rule 3). A dream that reads the metrics summary needs an R3 addendum, proposed to the operator before that phase.

**Consequences.**
- **A second importer of the driver.** `internal/metrics` imports `modernc.org/sqlite`, and AGENTS.md's "one package" lines change.
- **Shared helpers move.** `internal/search` exports `PrepareDBFiles`, `CopyDB`, `KindOf`, `TrailDates` (moved from dream) and `ProtoFields`.
- **A store with no second copy.** A deleted or corrupted `metrics.db` loses history; `export` is the operator's backup.
- **Antigravity numbers are weaker.** Its token fields are inferred from field numbers, with no published schema, and it reports no cost. Phase 3 confirms the tokens or records the difference.
- **A new parser feeding a persistent store** gets a security review at the Definition of Done.

## ADR-055a: Dream reads a numbers-only metrics summary     (2026-10-08) — Accepted, amends ADR-055

**Context.** ADR-055 sends feedback back through dream and `/retro` proposals. For dream to cite a metric, it has to read the metrics store, and R3 lists exactly what dream reads.

**Decision.** The operator approved this on 2026-10-08. Among the things dream reads, R3 now names "a summary of the metrics store (`sh-agent metrics show --json` for each project's window, numbers only)". The summary goes into the dream prompt through the existing redaction and 256 KB cap. When the prompt is over the cap, the summary is dropped before transcripts are.

**Consequences.** The model provider of the CLI dream runs now receives per-project metric numbers, in addition to the transcript text it already received. No identifiers beyond the three that ADR-055 names are included.

## ADR-055b: R3's metrics summary is named by what it holds     (2026-10-09) — Accepted, amends ADR-055a

**Context.** ADR-055a's R3 text said dream reads "`sh-agent metrics show --json` for each project's window, numbers only". The code sends `metrics.Summary.Numbers()` for the window and the one before it. That holds fixed keys and values only, which is narrower than `show --json`: `show --json` also carries tool, skill, subagent and label names (code review `reviews/2026-10-08-review-2c0f215-166aa39-code-docs.md`, finding 6).

**Decision.** The operator approved this on 2026-10-09 ("go all approved"). R3 now names the summary by its content: "a numbers-only summary of the metrics store (its metric keys and values for each project's window and the window before, never a tool, skill, agent, label or path)".

**Consequences.** The requirement and the code say the same thing. A change that adds a name to the prompt's metrics block is now a visible requirements change.

## ADR-056: A harness agent's frontmatter is an allow-list     (2026-10-12) — Accepted

**Context.** Claude Code ships an agent file byte for byte into `~/.claude/agents/` on every place (`plan.go`), and it honours every key in the file. The agent lint refused named keys only: `hooks`, `permissionMode` and, since `3bffdcf`, `mcpServers`. The security review of those fixes (round 1, finding 5) showed `memory` and `initialPrompt` passing, and Claude Code's documented key table grows release by release (`plans/2026-09-30-research-audit-fix-formats.md`: eighteen keys on 2026-09-30). A deny-list is one key short every time a vendor adds one. Antigravity is not exposed: `derive.go` writes only `model` and `tools`.

**Decision.** The operator chose the allow-list on 2026-10-12 (deferred from the project review of 2026-10-09). `agentKeys` in `internal/harness/inventory.go` names every key a harness agent may set: `name`, `description`, `tools`, `disallowedTools`, `model`, `maxTurns`, `skills`, `effort`, `color`, the security review's list. `LoadAgents` refuses any other top-level key, so install fails. Skills, commands and rules keep their named refusals (`allowed-tools`, `hooks`): their key sets are open by the Agent Skills standard (`license`, `metadata`, `compatibility`).

**Consequences.**
- A new vendor key fails the install until a commit adds it to `agentKeys`. That commit is reviewed like any code change, and a key that grants a permission (hooks, a permission mode, an MCP server, a memory scope, a first prompt) is never added.
- `isolation` (BUG-005), `background`, `memory`, `initialPrompt` and `omitClaudeMd` are refused today. None is used by an agent in `agents/`.
- Adding an agent stays a file drop. Adding a frontmatter KEY becomes a code change, which AGENTS.md § Rules already treats as a design question.

## ADR-057: The init-project skill is the one home of the stub list     (2026-10-12) — Accepted, amends ADR-048

**Context.** ADR-048 made REQUIREMENTS R2 the one home of the list of Claude Code setups that need the one-line `@AGENTS.md` stub, and the context pointed at it ("the harness's REQUIREMENTS R2 lists them"). REQUIREMENTS.md reaches a checkout or a zip place only: a release binary embeds the context, agents, skills, commands and rules (`embed.go`), so at a release-binary place the pointer resolved to nothing (project review 2026-10-09, row 28 L2).

**Decision.** The operator approved the exact text on 2026-10-12. The list moves to `skills/init-project/SKILL.md`, the skill that writes the stub, which is installed at every place. R2 keeps the rule (a stub only where native reading is unavailable) and names the skill as the list's one home. The context says "the init-project skill lists them".

**Consequences.** The pointer resolves on both CLIs at every kind of place. A change to the Claude Code versions or setups is now one line in the skill, not a requirements edit. The rule itself still changes only through REQUIREMENTS.md.

## ADR-056a: `disallowedTools` leaves the agent allow-list     (2026-10-12) — Accepted, amends ADR-056

**Context.** ADR-056's list came from a security review's round 1 and included `disallowedTools`. The v2.7.0 gate's security review (round 2) found that `DeriveAgy` writes only `model` and `tools` into the Antigravity agent, so a harness agent that restricted itself with `disallowedTools` would run unrestricted on Antigravity. No harness agent sets it.

**Decision.** The operator chose to drop it on 2026-10-12. `agentKeys` names eight keys: `name`, `description`, `tools`, `model`, `maxTurns`, `skills`, `effort`, `color`. `disallowedTools` is refused like any unlisted key.

**Consequences.** An agent restricts itself with `tools:`, which both CLIs honour. A key comes back on the list only with a derivation that carries it to Antigravity, or a reason it needs none.

## ADR-061: sh-agent is the second brain of both CLIs, consulted at named moments     (2026-10-23) — Accepted — amended by ADR-062 (R0 names one CLI)
**Context:** the binary had grown consult verbs (search, memory, metrics, dream; kickoff planned) and the context named the moments the CLIs call them, but README, the project AGENTS.md and REQUIREMENTS still described an installer. The operator stated the purpose on 2026-10-23: "The point with styrman-harness is to be the secondary brain for agy and claude. It should keep metrics, evals, skills, context, memory, dreaming, database etc. Claude and Agy should ask sh-agent for help if they need." ADR-058 to ADR-060 are reserved by the open plan `2026-10-23-session-sized-work.md`; this ADR takes the next free number.
**Decision:** R0 records the purpose with two constraints: the brain grows in the binary and its specs, one clause per consult moment in the context (the hand-written context is at 14,929 of 15,000 bytes); and it is pull-only — "ask if they need" holds only as moments the context names, because a model cannot tell when it needs help and nothing runs between turns (R3). Evals are a later feature (ROADMAP Next); the two SQLite files are the database (R4), and no general store is added. The operator approved the R0 text verbatim on 2026-10-23 ("approved").
**Consequences:** README line 3 and AGENTS.md § What this repo is each point at R0 in one sentence. A proposal to add a consult moment is judged against R0: a verb and a clause, not a context paragraph. Supersedes nothing.

## ADR-058: A session is one Task or one plan phase; the handoff's Kickoff and `sh-agent kickoff` start the next     (2026-10-23) — Accepted
**Context:** the operator asked twice (feedback log 2026-10-21, 2026-10-23) that work be split into session-sized chunks and that each session end with the prompt for the next; a repeated correction graduates into a mechanism. `plans/2026-10-23-research-session-sizing.md` measured 49 main Claude Code sessions since 2026-09-01 on this box: median peak context 573K tokens, 38 past 200K, 31 past 300K. The model cannot observe its own context, hooks are refused (R3, ADR-007) and no CLI lets anything outside a session run `/clear`, so the one boundary the model can see is the checkpoint it already writes. The hand-written context stood 71 bytes under its 15,000 pin, so the rule could not join it.
**Decision:** (1) `rules/session-sizing.md`, the harness's second always-on rule on both CLIs (beside `critical-thinking.md`, ADR-050), says a session is one Task or one plan phase, ends at its checkpoint with a handoff whose `## Kickoff` names the next session's work, and that a second unrelated ask, whatever its size, waits for its own Kickoff unless the operator's own words keep the session open (plan D2, D11, D31). (2) The handoff template gains `## Kickoff`: a fenced, paste-ready prompt whose standing approvals are a closed set written in the template and never extended by the agent (D3, D17); one line when nothing is queued; printed in chat only in an unscaffolded project (D18). (3) `sh-agent kickoff` prints the newest handoff's block for the checkout the shell is in, exit 1 with `kickoff: nothing queued` on stderr and nothing on stdout when none, so `claude "$(sh-agent kickoff)"` or `agy -i "$(sh-agent kickoff)"` starts the next session (D28, D32: Antigravity takes an initial interactive prompt only through `-i`); it is a consult verb under R0, reads no home and no harness tree (its global flags are refused, the BUG-171 shape), and moves nothing onto a machine, so the README map does not change (project `AGENTS.md` rule 3 does not apply). The headless loop is a documented one-liner, not a verb (D29). (4) The feature skill sizes a phase to what one fresh session finishes and verifies from the plan alone, its cold read asks a fourth question (sizing), and its checkpoint sentence ends the session (D9). (5) The context target is 300,000 tokens as a flag default on `sh-agent metrics show|report`, tuned by `/retro` from the metric, never a pin (D4, D15, D27); the metric itself is the plan's Phase 2, and the plan runs under its own rule from Phase 1 on.
**Consequences:** every phase boundary costs a resume (about 30K tokens; accepted, the plan's Follow-ups name the fix: `bugs/INDEX.md` is read whole). Definition of Done step 5 ends the session instead of recommending it. A CLI that ignores the rule shows in the § 4 behavioural run as n of N, never hidden (ADR-050 records one Antigravity run where the first rule did not hold). The rule shares the 22,000-byte Antigravity block (`TestRenderedContextFitsAntigravityBlock`). ADR-059 (reviewer isolation, D10) and ADR-060 (the auto-compact backstop, D25) stay reserved by the plan.

## ADR-058a: The `sh-agent kickoff` verb is removed; the paste is the whole path     (2026-10-23) — Accepted, amends ADR-058
**Context:** ADR-058 (3) added `sh-agent kickoff`, which printed the newest handoff's `## Kickoff` block so one shell command started the next session. The Phase 1 report named that command, and the operator's shell ran the installed release binary v2.7.0, which has no such verb (feedback log 2026-10-23T15:46:12+02:00: a command named to the operator is verified by the shell that will run it). A verb in the binary and a Kickoff in the handoff skew whenever a place is behind the release, the paste has no such gap, and the verb saved one paste. The operator chose removal over fixing the skew in the handoff skill ("The kickoff skill is shit. I think we should remove it entirely and focus on all other stuff.", "go approved 2", 2026-10-23; plan `2026-10-23-session-sized-work.md` D34).
**Decision:** ADR-058 (3) is withdrawn: no `sh-agent` verb reads the handoff; the handoff skill's Step 7 gives the Kickoff block to paste and nothing else; the headless loop one-liner, the `agy -i` form and the prose fallback (plan D28, D29, D32, D33) go with it, and the plan's D3 ("no verb") stands again. `cmd/sh-agent/kickoff.go`, its tests, the verb-table entry, the `local` field and its global-flag refusal, the four usage rows, the README verbs row, the delivery spec's clause and the `sh-agent-matrix.md` row are removed in the one commit with this ADR; the skill-matrix `session-sizing` check reads the newest handoff's `## Kickoff` section with awk instead. ADR-058 (1), (2), (4) and (5) — the rule, the handoff's `## Kickoff`, the phase sizing and the context target — stand unchanged; the rule's text only loses the parenthesis that named the verb.
**Consequences:** the verb was never in a tag, so no place loses a command and the README map is as it was (ADR-058 drew no line for it). The verb-table comment returns to its pre-ADR-058 wording; a later consult verb under R0 re-states the "draws no line" reading when it comes. The `/review --resume` lens and the rule's behavioural row still judge the Kickoff by reading the handoff, as the kit's `check.sh` now does.

## ADR-060: `install` writes Claude Code's `autoCompactWindow`, the harness's auto-compact backstop     (2026-10-24) — Accepted — Superseded by ADR-062

**Context:** The session-sizing rule (ADR-058) ends a session at a structural boundary because the model cannot observe its own context; on this box 33 of 54 main Claude Code sessions since 2026-09-01 passed 300,000 tokens (plan `2026-10-23-session-sized-work.md`, Phase 2's metric). The operator declined to run the knobs by hand (2026-10-23: "I dont want to run compact myself. I want something in this harness to monitor this and trigger"). Hooks are refused (R3); no CLI accepts `/compact` or `/clear` from outside a session; a harness process watching transcripts could observe but never trigger. Claude Code's own compaction runs at the window its settings name: the top-level `autoCompactWindow` key (any settings file, 100,000 to 1,000,000 tokens), overridden for one model by the value `/autocompact` saves under `modelSettings` (v2.1.288 and later), by `--autocompact` for one launch, and by `CLAUDE_CODE_AUTO_COMPACT_WINDOW` over all of them (vendor docs, model-config § Set the auto-compact window, read 2026-10-24; the installed 2.1.310 binary carries both keys and the variable). Antigravity exposes no compaction control that was found (research-session-sizing, 2026-10-23).

**Decision:** `sh-agent install` (vendor `all` or `claude`) writes `"autoCompactWindow": 300000` into `~/.claude/settings.json` only when the top-level key is absent, through the same writer as the attribution key (ADR-025): a textual insertion re-parsed before it lands, every other byte kept, never through a symlink or a linked `~/.claude`, advisory (no state of the file fails an install); `--dry-run` prints and touches nothing; `status` prints one note when the key is absent. A present value of any kind is the operator's. A `/autocompact` value under `modelSettings`, the flag and the variable take precedence by the CLI's own rules, and the harness never reads or writes them. The window equals the metric's context target (plan D27: one number, pinned equal by a test), so the report's over-target count and the CLI's compaction point are the same line. `REQUIREMENTS.md` § Non-goals names the key as the third and last vendor-setting exception, in the operator's approved words (plan D25, "D25 approved as written", 2026-10-23); this ADR, that edit and the `ROADMAP.md` line land in one commit. Antigravity's gap is a row in `specs/sh-agent/memory-loop.md` § Known differences.

**Consequences:** A fresh Claude Code install compacts at 300,000 tokens unless the operator set a window, instead of at the 1M model's 967K default. Compaction is a backstop, not the mechanism: the rule still ends a session at its checkpoint, because a compaction summary loses what the trail does not hold (context § Checkpoints). Phase 6 of the plan dispatches `security` (D22: a write into the operator's home and JSON parsing). The deployed value at a real place changes at that place's next `sh-agent update` after a release. No delivery path moves, so the README map is unchanged (project `AGENTS.md` rule 3).

## ADR-062: fyr — the Antigravity CLI only, no report path, renamed     (2026-10-24) — Accepted

**Context:** the operator redirected the harness in their own turn on 2026-10-24: "I want to remove claude from styrman harness fully. Only focus styrman harness on agy and I want to rename it to something else", then "lets go with fyr. I also want to rename the styrman-harness to fyr and then remove the report back to styrman-harness-feedback repo. This should be removed also." The plan `plans/2026-10-24-fyr-agy-only.md` (recon `plans/2026-10-24-recon-agy-only-fyr.md`, cold read `reviews/2026-10-24-review-cold-read-fyr-agy-only.md`) was approved with comments ("go approved with my comments"), and the `REQUIREMENTS.md` replacement set, its D9, was approved as written ("go all approved", 2026-10-24).

**Decision:** (1) The harness installs into the Antigravity CLI and no other: every Claude Code path leaves the binary, the skills, the agents, the context, the rules, the test kits and the living docs; agents are authored natively as `agents/<name>/agent.md`, byte-equal to today's derived output, and derivation goes (plan D2, D3, D17). (2) `report` and the `styrman-harness-feedback` path leave entirely; nothing replaces them; the redactor stays for the dream, in its own package; harness-target proposals stay on the machine for the operator's own hands; the feedback repository is deleted by the operator (D4). (3) The project is renamed `fyr` in place: repository and module `github.com/kesokaj/fyr`, binary `fyr`, state `~/.config/fyr/`, unit `fyr-dream`, marker `.fyr-noindex` with `.sh-agent-noindex` still honoured, env `FYR_*` (D1, D12, D15). (4) One release, `v3.0.0`, ships `fyr` only — no bridge, no shim; each installed place is reinstalled by hand once, and `fyr install` migrates `~/.config/sh-agent` and lets the existing resync remove the old manifest's files (D5a, D13, D14). (5) `REQUIREMENTS.md` is rewritten for one CLI and the new name in the approved words: the title, R0–R5, R7–R10, Known limits and Non-goals; R9 has three procedures; R10 is one behaviour on every place; Non-goals forbid every vendor setting. The historical trail keeps the old names (D8).

**Consequences:** supersedes ADR-025 (the `attribution` key) and ADR-060 (`autoCompactWindow`): `install` writes no vendor setting, and the two keys stay in the operator's `~/.claude/settings.json` until removed by hand; supersedes the report half of ADR-047a. Amends ADR-030, ADR-033 and ADR-038 (the binary's name; ADR-033's report clause goes), ADR-047 (R10 now spans places and operating systems, not CLIs; memory-loop.md § Known differences is retired and its one lasting row is a Known limit), ADR-058 and ADR-058a (the Kickoff wording that names `claude`), and ADR-061 (R0 names one CLI). ADR-059, reserved for reviewer isolation by plan `2026-10-23-session-sized-work.md`, stays unused: that plan closed at Phase 2b (its D35). The requirements now describe the target: until the plan's Phase 6 the binary in the tree and at every place is still `sh-agent`, and each phase brings the code to the text. A Claude Code on a crossed machine loses the harness files the manifest owned (D2). The README map changes in Phase 1, when the report line leaves, in the one commit with its code (project `AGENTS.md` rule 3).
