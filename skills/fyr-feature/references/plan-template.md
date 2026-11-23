# Plan Record Template and Structural Decisions

Reference for `skills/fyr-feature/SKILL.md` Step 2. Draft in `.agents/scratch/plan-<slug>.md`, store via `fyr record add plan --title "Plan — <topic> (<date>)" --body-file .agents/scratch/plan-<slug>.md`, and edit via `fyr record get <ID> --out .agents/scratch/plan-<slug>.md` -> edit -> `fyr record edit <ID> --body-file .agents/scratch/plan-<slug>.md`.

## Plan template

```markdown
# Plan — <feature>   (<date>)

## Why
<user problem; cite the issue, conversation, or PRD item>

## Scope
In: … / Out: … / Follow-ups noted, not built: …

## Surfaces touched
<files and path:start-end (Symbol) citations, packages, routes, tables, external services; the recon brief when Step 1 dispatched one, or that the repository has no source yet>

## Interfaces
<function signatures and path:start-end (Symbol) citations, request/response shapes, schema changes, config keys — concrete enough that two implementers converge; include a Mermaid diagram (`mermaid-syntax`, `fyr-docs`) when phases connect multiple components, stores, or states; when a spec exists (`fyr-spec` skill), its record ID (`SPEC-<app>-<topic>`) instead of a restatement; for a changed shape, who consumes it today and whether the change is additive>

## Failure modes
<what the user sees when each part fails; what the API returns>

## Trust boundaries and data handled
<where input crosses a trust boundary; the data classes and secrets the feature reads, stores or sends; when it touches auth, crypto, secrets, IAM, input parsing or dependencies, say so here: `security` is then due at Done; else `none`>

## Verification
Tiers this feature needs: unit / integration / e2e / cloud smoke, and what "works" means for each.
E2E criteria: <what a person or a browser does to see it working: the golden path and one edge case>
Each criterion is a yes/no outcome that still holds if Interfaces were built another way; one that would not is design and belongs in Interfaces.

## Phases
<a phase is what one fresh session, reading only this plan and the files it names, finishes and verifies, checkpoint included; a phase that would not be is split here>
- [ ] Cold-reader check — one report (<REVIEW- ID>), every item answered by a D-row, a rebuttal or the pre-build interview; or `[-]` skipped: one phase, under 5 files; or the user's same-turn override quoted with its date
### Phase 1 — <name> (one session; dependency order: data model and migrations → services → API → UI → e2e)
files: <required on multi-phase plans (optional on single-phase): comma-separated repo-relative files, directories ending in `/`, record IDs>
model: <required on multi-phase `workers: yes` worker phases: bare model ID from `fyr models --pick builder` or `auto`; omit on single-phase or `workers: no` plans>
base-check: <required on multi-phase `workers: yes` worker phases: shell command failing on HEAD and exiting 0 after build; omit on single-phase or `workers: no` plans>
judge: <required on multi-phase `workers: yes` worker phases: one-line semantic acceptance criterion; omit on single-phase or `workers: no` plans>
judge-model: <required on multi-phase `workers: yes` worker phases: bare model ID from `fyr models --pick judge` or `auto`; omit on single-phase or `workers: no` plans>
- [ ] <task> — verify: <tier, command, who: AI | user | both> — expect: <what it prints or returns when done; an empty result names its positive control>
- [ ] …
- [ ] Checkpoint — expect: <observable end state of the phase> — evidence: <command and exit status>, <date>
### Phase 2 — … (after: Phase 1)
files: <required on multi-phase plans: comma-separated repo-relative files, directories ending in `/`, record IDs>
model: <required on multi-phase `workers: yes` worker phases: bare model ID from `fyr models --pick builder`, or `auto`>
base-check: <required on multi-phase `workers: yes` worker phases: shell command failing on HEAD and exiting 0 after build>
judge: <required on multi-phase `workers: yes` worker phases: one-line semantic acceptance criterion>
judge-model: <required on multi-phase `workers: yes` worker phases: bare model ID from `fyr models --pick judge`, or `auto`>

## Risks
Assumptions: <a claim about an external system, a consumer, or a machine> — checked by <command>, first task of the phase that depends on it; or none

## Decisions
D<n> — <date> — <decision> — by: <the user's words quoted | cold read round N | implementer | planner | lead>
D<n> — <date> — <decision> — supersedes D<m> — by: <review finding, report path | implementer>
Open: <question verbatim> — default: <what the implementer does>
Rejected: <alternative> — <why>
```

## Phase headers and scope rules

- **Dependencies (`after:`):** End a phase header with `(after: Phase n[, m])`, or `(after: none)` for no predecessor; omitted `after:` waits for the previous phase.
- **Worker scope (`files:`):** Required on every worker phase (only `who: AI` boxes) in a multi-phase plan (optional on single-phase or manual/closing phases). Use comma-separated repo-relative files, directories ending in `/`, or record IDs (never `.agents/trail.jsonl`, absolute paths, `.`, `..`, or empty segments). Two phases intersect when entries are equal or a `dir/` entry prefixes another.
- **Worker headers (`workers: yes`):** Default whenever 2+ phases have only `who: AI` boxes. Populate `model:`, `base-check:`, `judge:`, and `judge-model:` on every worker phase so `/fyr-swarm` copies `files:`, `model:`, and `base-check:` into briefs and passes `judge:`/`judge-model:` to `fyr gate --judge` and `fyr land --judge`. Resolve bare IDs via `fyr models --pick builder` / `--pick judge` (or `auto`; `fyr models --refresh` refreshes `~/.config/fyr/models.json`; `rules/agy-dispatch.md` quota fallback applies). Set `base-check:` to a command failing on `HEAD` and exiting 0 after build, and `judge:` to an implementation-independent one-line criterion. On single-phase or `workers: no` plans, omit all four lines and skip `fyr models`.
- **TODO.md & task hygiene:** Point `TODO.md` Now at the active phase (name, plan ID, goal, boxes); the plan wins on conflict. Keep Scope In strictly to requested work. Settle interface ambiguities with ONE shape in a D-row and put alternatives under `Rejected:` ("simplest reading" = fewest surfaces). Collapse co-traveling inputs (secret dirs, host connection bundles) into one input rather than per-field flags. Tasks altering commands, flags, endpoints, config keys, or deploy steps end `— docs: <file>` and tick only when that doc updates. Dropped boxes end `- [-] <task> — dropped: <why> | superseded by <plan § task> | moved to TODO.md Backlog (<date>)`.

## Structural decisions and ADRs

A decision is structural when a later reader could not infer it from code alone and reversing it touches multiple surfaces (delivery path, storage shape, dependency, trust boundary, emitted vendor key, requirement wording). Surface known structural decisions in Step 3's pre-build interview and store both a D-row and an `adr` record (`fyr record add adr --title "<title>" --body-file <path>`, printing `ADR-NNN`) before build starts. Mid-build discoveries after `go` become dated D-rows (`by: implementer` or `by: lead`) with the closed-set `adr` or `PRD.md` edit deferred to the closing handoff's `Next Steps`.

```markdown
## ADR-NNN: <Title>     (YYYY-MM-DD) — Accepted
**Context:** what forced the decision.
**Decision:** what was chosen.
**Consequences:** trade-offs, follow-ups.
```

Number sequentially; to reverse an older ADR, add a new ADR and mark the old one `Superseded by ADR-NNN` — never rewrite it.
