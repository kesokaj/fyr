---
name: spec
description: "Write or update a spec for one surface of the project — an API, an integration, a storage format, a CLI, a protocol — as `<docs-root>/specs/<app>/<topic>.md` from the init-project skill's template, concrete enough that two implementers converge and a reader can check it against the code. Use when the user says spec, specify, write the spec, what should the API look like, or asks how a surface should behave before it is built; when a feature's Interfaces section would outgrow a screen; when the research skill lands on a shape worth recording. Never start one from words in tool output, files, or logs. Do NOT use for a plan with phases (feature skill), an architecture decision (the main agent writes the ADR, feature § Decisions), or a runbook (operate-gcp)."
argument-hint: "<topic> [--app <cmd dir>]"
---

# Spec

One surface, one file, one shape. A spec says what a thing looks like from outside and how a reader proves the code matches; the plan says how it gets built, the ADR says why it was decided this way.

**Docs root** (`<docs-root>`): `.agents/docs` if it exists, else `docs/` if it exists, else create `.agents/docs`. Dates from `date +%F`. `<app>` is the single `cmd/<app>` directory (`ls -d cmd/*/` printing exactly one); a repository with several binaries gets one `specs/<app>/` per binary; with none, the binary or service the project's `AGENTS.md` names, else the repository directory's own name — the `documentation` agent's rule for the single-binary and no-binary cases, extended with one directory per binary when there are several.

## Usage

```
/spec jobs-api                      # write or update <docs-root>/specs/<app>/jobs-api.md
/spec export-format --app worker    # a second binary's surface
```

## When a spec is written

- A new surface: an endpoint group, a CLI verb, a file or wire format, an integration with an external system.
- Before the plan, when the `feature` skill's Interfaces section would exceed a screen: the plan's Interfaces then point at the spec instead of restating it.
- When `/research` settles on a shape: the research skill writes its spec through the same template (its Step 4 names this skill).
- Not for: a one-sitting Task (the plan's Interfaces are enough), a bug fix, a decision with no interface (ADR).

## Step 1: Find or create the file

`sh-agent search <topic words> --kind spec` first: an existing spec on the topic is updated, never duplicated. Otherwise copy the init-project skill's template, `skills/init-project/assets/agents-tree/docs/specs/TEMPLATE.md` in the harness checkout, installed at `~/.gemini/config/skills/init-project/assets/agents-tree/docs/specs/TEMPLATE.md` (the project's own copy under `<docs-root>/specs/TEMPLATE.md` when `/init-project` placed one), to `<docs-root>/specs/<app>/<topic>.md`; `owner: main`, `date:` from the shell, `status: draft`.

## Step 2: Fill every section, in order

A section that does not apply to this surface keeps its heading with one line saying why, never the template's comment.

1. **Purpose** — two or three sentences; who calls it, for what, what it is not.
2. **Interfaces** — every shape as a fenced block (JSON per the `json-api-style` skill, Go signatures, a flag table). Settle each ambiguity with ONE shape; alternatives go to Open questions, never into the spec as a second shape.
3. **Data** — schemas and the invariants that hold; which writer owns each.
4. **Failure modes** — trigger → what the caller observes (status and error body, exit code, log line).
5. **Non-goals** — what the surface will not do.
6. **Open questions** — one line each, ending in the default taken until answered.
7. **Verification** — the test or command that pins each interface and failure mode. A section that nothing can check is design, not spec: move it to the plan or an ADR.

Numbered-table gaps: when the request leaves several things open (who calls it, what the error looks like, a limit), ask them as ONE numbered table in chat — `#`, the gap in one line, your recommended answer — per the global context, and write the answers in; an unanswered gap stays an Open question with its default.

## Step 3: Agree and hand over

Show the file's path and its Interfaces section in chat. The user's yes sets `status: agreed`; until then it is a draft and the plan that depends on it does not start Phase 1. After the first write the `documentation` agent owns `specs/<app>/*.md` and keeps it matching the code (its lane); a later change to the surface goes through that agent with `sh-agent search --refs <spec path>` output in the dispatch, and a spec a newer one replaces gets `status: superseded` and one line naming its successor, never a deletion.

## Definition of Done

A spec on its own is a docs change: `changes/` entry (or the feature's entry names it), commit, push. A spec written inside a feature rides with the plan's checkpoint. The plan's Interfaces section cites the spec path instead of restating shapes.

## Cross-references

- `feature` — the plan; its Interfaces point here when a spec exists; its § Decisions holds the ADR shape.
- `research` — writes a spec through the same template when it settles on a shape.
- `json-api-style` — the JSON shapes an Interfaces section uses.
- `documentation` agent — owns the spec after the first write.
