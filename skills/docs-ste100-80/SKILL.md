---
name: docs-ste100-80
description: "ASD-STE100 (~80%) docs authoring & audit (/docs-ste100-80): short active prose, direct steps, BLUF, safety callouts, visuals, <details>."
argument-hint: "[file or --audit]"
---

# Documentation as ASD-STE100 (up to 80%)

Use ASD-STE100 as a pragmatic target (~80% adherence) for human-facing docs (Track 1), not a rigid 100% standard. Take the high-value clarity habits and stop whenever strict compliance makes engineering prose sound unnatural or robotic.

## 5-Track Audience Matrix

| Track | Audience & files | Style & contract |
|---|---|---|
| Track 1 — Git Human Docs | Humans (git-tracked Markdown only): `README.md`, `SDD.md`, `TDD.md`, `Workflow.md`, `docs/**/*.md`, `features/**/*.md` | ASD-STE100 (~80%): short active prose, BLUF, safety callouts, visual caps (`<= 12` Mermaid nodes, `<details>` for `> 12` rows), no YAML frontmatter |
| Track 2 — Agent-Read & SQLite DBs | 100% agent-read files (`AGENTS.md`, `context/*.md`, `rules/*.md`, `skills/*/SKILL.md`, `agents/*/agent.md`) and 100% agent-only SQLite databases (`brain.db`, `search.db`, `metrics.db`: `GUIDE-*`, `DEPLOY-*`, memory, SIM, caches, telemetry) | Telegraphic Markdown / structured agent records (`<= 4,500 B` for project `AGENTS.md`); non-discoverable operational commands and boundaries only; `brain.db` `GUIDE-*`/`DEPLOY-*` keep no YAML frontmatter + `Caution`/`Warning` before destructive commands only (never STE prose) |
| Track 3 — Shared Contracts | Git contracts (`PRD.md`, `TODO.md`) + `brain.db` agent-only `SPEC-*`, `PLAN-*` | Structured Markdown + machine-projectable `SPEC-*` sections (`Purpose`, `Interfaces`, `Data`, `Failure modes`, `Verification`); never edit `PRD.md` without explicit operator approval |
| Track 4 — Ephemeral Deterministic Projections | On-demand agent capsules (not committed) | On-demand AST & contract projections (`fyr search --slice` / `--contracts` / `--skeleton`); never hand-maintained as static prose |
| Track 5 — Immutable Trail | Append-only `brain.db` history: `change`, `adr`, `handoff`, `review`, `retro`, `bug`, `testing` records | Immutable audit log; never edited or rewritten by `--audit` |

- **Strict exclusion boundary:** Apply human-prose STE (~80%) rewriting to Track 1 git-tracked living docs only. Never rewrite Track 2 (`AGENTS.md`, `context/`, `rules/`, `skills/`, `agents/`) or 100% agent-only SQLite database records (`brain.db`, `search.db`, `metrics.db`: `GUIDE-*`, `DEPLOY-*`, `SPEC-*`, `PLAN-*`, trail/memory) into human narrative prose, never hand-author Track 4 projections as checked-in summaries, and never rewrite Track 5 immutable trail records.

## Follow (~80%)

- **Short, active prose:** Aim for ~20-word procedural sentences and ~25-word explanations in active voice and simple tenses.
- **Direct procedures:** Put one clear action per step, start with imperative verbs, and state conditions before actions (`If ...`, `When ...`).
- **Plain, consistent terms:** Cut filler and marketing fluff (`seamless`, `foundational`, `zero-ops`); avoid contractions and Latin abbreviations (`for example`, not `e.g.`).
- **Safety before risk:** Place `Note` or `Caution` callouts (never consecutive or nested) before destructive or `-auto-approve` commands with exact consequences. Keep interactive and destructive commands in separate copy blocks.
- **Code-verified and visual:** Check real source files (variables, defaults, flags, outputs) before writing. Open each doc and `##` section with a short BLUF (1–3 sentences), omit manual tables of contents, replace dense architecture prose with diagrams (`mermaid-syntax`, `<= 12` nodes for Markdown; token-styled SVG for HTML), and fold large tables (`> 12` rows) behind filters or `<details>`.

## Stop Before Strictness (the relaxed ~20%)

- **No strict STE dictionary:** Keep normal software and cloud verbs (`create`, `deploy`, `delete`, `enable`, `configure`, `override`, `default`, `optional`), exact code/vendor names, and quoted error, log, or UI strings verbatim; update inbound `#heading` links when renaming headings.
- **Clarity over word-counting:** Do not force awkward rewrites just to eliminate every `-ing` word or shave one word off a natural sentence.
- **Ergonomic formats:** Keep concise non-sentence table cells, diagram labels, and standard Markdown or clean HTML (`frontend-ui-economist` **Airy** theme default; tokens-only CSS; opaque copy buttons; safe `.textContent`-only JS; `<= 400` LOC per UI file).

## Auditing a Docset (`--audit`)

- **Scope Track 1 git human docs for STE; safety/structure only on `brain.db`:** Audit git-tracked Track 1 files (`README.md`, `SDD.md`, `TDD.md`, `Workflow.md`, `docs/**/*.md`, `features/**/*.md`) for ASD-STE100 (~80%) prose. Treat all `brain.db` records (`GUIDE-*`, `DEPLOY-*`, `SPEC-*`) as 100% agent-only: check `guide`/`deploy` only for accuracy, no YAML frontmatter, and `Caution`/`Warning` callouts before destructive commands, and check `spec` for `spec-structure` (5 required headings + 4-column failure table). Exclude Track 2 agent files from human-prose STE rewriting (check `AGENTS.md` for `<= 4,500 B` budget and Setup/Deploy command parity only). Never edit `PRD.md` without explicit operator approval or audit Track 5 immutable trail records (`change`, `adr`, `handoff`, `review`, `retro`, `bug`, `testing`).
- **Log findings in scratch:** Write findings (`path:line`, category, fix) to `.agents/scratch/docs-audit-<YYYY-MM-DD>.md` (date via `date +%F`; never `/tmp`).
- **Stage `brain.db` records cleanly:** Export each living record (`fyr record get <ID> --out .agents/scratch/<ID>.md`), edit in place without YAML frontmatter, and save back (`fyr record edit <ID> --body-file .agents/scratch/<ID>.md`).

## Quick Check

- **Drift & deterministic rules (`fyr check`):** Run `fyr check` to verify commands, flags, paths, anchors, `SPEC-*` freshness/links, `PRD.md`/`SDD.md`/`TDD.md` traces, context/rules budgets, and skill frontmatter, plus three hard ADR-535/ADR-536 rules: `docs-style` (5 hard checks on Track 1 git human docs: no YAML frontmatter on root docs, `Caution`/`Warning` before destructive commands, `<= 12` Mermaid nodes, `<details>` folding for `> 12`-row tables, no banned fluff/Latin `seamless`/`foundational`/`zero-ops`/`e.g.`/`i.e.`/`etc.`; plus agent-safety checks only — no YAML frontmatter and `Caution`/`Warning` before destructive commands — on 100% agent-only `brain.db` `current` `guide`/`deploy` records), `budget` (project root `AGENTS.md <= 4,500 B` and no `> 15`-line box-drawing tree dump), and `spec-structure` (5 required `##` headings: `Purpose`, `Interfaces`, `Data`, `Failure modes`, `Verification`; plus an advisory note when `## Failure modes` lacks a 4-column table).
- **Advisory notes (`docs-style-note`, `>= 80%` STE):** `fyr check` emits non-failing `docs-style-note` hints on Track 1 git human docs only (never `brain.db` records) when a file drops below `>= 80%` STE sentence-length compliance (`<= 25` words prose, `<= 20` words list items) or when prose uses a discouraged term in optional `<repo>/.agents/glossary.json`.
- **HTML checks:** Run `node ~/.gemini/config/skills/frontend-ui-economist/scripts/check_theme.js <css>` (or `.agents/skills/frontend-ui-economist/scripts/check_theme.js`; 0 fails), `node -c` on JS, and check desktop (`1280px`) and mobile (`400px`) renders for clipping or overflow.
