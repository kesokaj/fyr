---
name: docs-ste100-80
description: "ASD-STE100 (~80%) docs authoring & audit (/docs-ste100-80): short active prose, direct steps, BLUF, safety callouts, visuals, <details>."
argument-hint: "[file or --audit]"
---

# Documentation as ASD-STE100 (up to 80%)

Use ASD-STE100 as a pragmatic target (~80% adherence), not a rigid 100% standard. Take the high-value clarity habits and stop whenever strict compliance makes engineering prose sound unnatural or robotic.

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

- **Scope living docs only:** Audit `README.md`, `SDD.md`, `TDD.md`, `AGENTS.md` (Setup/Deploy), `docs/**/*.md`, and open `spec`, `guide`, and `deploy` records (`fyr record list --kind <kind>`). Never edit `PRD.md` without explicit operator approval or audit immutable trail records (`change`, `adr`, `handoff`, `review`, `retro`, `bug`, `testing`).
- **Log findings in scratch:** Write findings (`path:line`, category, fix) to `.agents/scratch/docs-audit-<YYYY-MM-DD>.md` (date via `date +%F`; never `/tmp`).
- **Stage `brain.db` records cleanly:** Export each living record (`fyr record get <ID> --out .agents/scratch/<ID>.md`), edit in place without YAML frontmatter, and save back (`fyr record edit <ID> --body-file .agents/scratch/<ID>.md`).

## Quick Check

- Confirm commands, counts, flags, and defaults match the codebase; run `fyr check`.
- For HTML: run `node ~/.gemini/config/skills/frontend-ui-economist/scripts/check_theme.js <css>` (or `.agents/skills/frontend-ui-economist/scripts/check_theme.js`; 0 fails), `node -c` on JS, and check desktop (`1280px`) and mobile (`400px`) renders for clipping or overflow.
