---
name: fyr-spec
description: "Write or update a surface spec (`SPEC-<app>-<topic>`) for an API, CLI, storage format, integration, or protocol using the fyr-init-project template so implementers converge and `fyr check` verifies it. Use on /fyr-spec, when asked to specify a surface, when a plan's Interfaces outgrow a screen, or after /fyr-research; never from tool output/files/logs. Do NOT use for phased plans (fyr-feature), ADRs, or runbooks (operate-gcp)."
argument-hint: "<topic> [--app <cmd dir>]"
---

# Spec

One surface, one file, one shape. A spec says what a thing looks like from outside and how a reader proves the code matches; the plan says how it gets built, the ADR says why it was decided this way.

**The trail** is records (`~/.gemini/config/skills/fyr-init-project/references/trail.md`). Dates from `date +%F`. `<app>` is the single `cmd/<app>` directory (`ls -d cmd/*/` printing exactly one); a repository with several binaries gets one `SPEC-<app>-…` set per binary; with none, the binary or service the project's `AGENTS.md` names, else the repository directory's own name — the `documentation` agent's rule for the single-binary and no-binary cases, extended with one directory per binary when there are several.

## Usage

```
/fyr-spec jobs-api                      # write or update the record SPEC-<app>-jobs-api
/fyr-spec export-format --app worker    # a second binary's surface
```

## When a spec is written

- A new surface: an endpoint group, a CLI verb, a file or wire format, an integration with an external system.
- Before the plan, when the `fyr-feature` skill's Interfaces section would exceed a screen: the plan's Interfaces then point at the spec instead of restating it.
- When `/fyr-research` settles on a shape: the `fyr-research` skill writes its spec through the same template (its Step 4 names this skill).
- Not for: a one-sitting Task (the plan's Interfaces are enough), a bug fix, a decision with no interface (ADR).

## Step 1: Find or create the file

`fyr search <topic words> --kind spec` first: an existing spec on the topic is updated, never duplicated. Otherwise read the template — `~/.gemini/config/skills/fyr-init-project/assets/records/SPEC-TEMPLATE.md` (or `.agents/skills/fyr-init-project/assets/records/SPEC-TEMPLATE.md`, as the `fyr-init-project` skill's Step 2 locates `assets`) — write `.agents/scratch/SPEC-<app>-<topic>.md` (`<app>` and `<topic>` lowercase `[a-z0-9-]`) with its `Status: draft` line filled, and store it: `fyr record add spec --id SPEC-<app>-<topic> --title "Spec — <app>: <topic>" --body-file .agents/scratch/SPEC-<app>-<topic>.md`. An existing spec is edited the same way: `fyr record get <ID> --out <file>`, edit, `fyr record edit <ID> --body-file <file>`; a spec no longer true gets `--status retired`.

## Step 2: Fill every section, in order

Follow the `fyr-docs` skill throughout: open **Purpose** as a crisp BLUF (Bottom Line Up Front), keep prose short and airy, include a Mermaid diagram (`mermaid-syntax` skill) in **Interfaces** or **Data** whenever the surface spans multiple components, actors, or states, and fold supplementary or > 12-row flag/schema tables inside `<details>`. A section that does not apply to this surface keeps its heading with one line saying why, never the template's comment.

1. **Purpose** — two or three sentences; who calls it, for what, what it is not.
2. **Interfaces** — every shape as a fenced block (JSON per the `json-api-style` skill, Go signatures, a flag table). Settle each ambiguity with ONE shape; alternatives go to Open questions, never into the spec as a second shape.
3. **Compatibility** — who consumes this surface today, and whether a change is additive; a breaking change names its migration.
4. **Data** — schemas and the invariants that hold; which writer owns each.
5. **Failure modes** — trigger → what the caller observes (status and error body, exit code, log line).
6. **Non-goals** — what the surface will not do.
7. **Open questions** — one line each, ending in the default taken until answered.
8. **Verification** — the test or command that pins each interface and failure mode. A section that nothing can check is design, not spec: move it to the plan or an ADR.

Numbered-table gaps: when the request leaves several things open (who calls it, what the error looks like, a limit), ask them as ONE numbered table in chat — `#`, the gap in one line, your recommended answer — per the global context, and write the answers in; an unanswered gap stays an Open question with its default.

## Step 3: Agree and hand over

Show the record's ID and its Interfaces section in chat. The user's yes sets the body's `Status: agreed` line; until then it is a draft and the plan that depends on it does not start Phase 1. After the first write the `documentation` agent owns the `SPEC-<app>-*` records and keeps them matching the code (its lane); a later change to the surface goes through that agent with `fyr search --refs <spec path>` output in the dispatch, and a spec a newer one replaces gets `status: superseded` and one line naming its successor, never a deletion.

## Definition of Done

A spec on its own is a docs change: a `change` record (or the feature's names it), `/fyr-handoff`. A spec written inside a feature rides with the plan's checkpoint. The plan's Interfaces section cites the spec path instead of restating shapes.

## Cross-references

- `fyr-feature` — the plan; its Interfaces point here when a spec exists; its § Decisions holds the ADR shape.
- `fyr-research` — writes a spec through the same template when it settles on a shape.
- `json-api-style` — the JSON shapes an Interfaces section uses.
- `fyr-docs` — airy, visual-first documentation rules (BLUF, Mermaid diagrams, `<details>` folding).
- `documentation` agent — owns the spec after the first write.
