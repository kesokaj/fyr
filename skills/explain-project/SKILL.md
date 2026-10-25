---
name: explain-project
description: "Explain a codebase to a person and write <docs-root>/onboarding.md: what the project does and for whom, how it is built (entry points, packages, the main request's data flow, where configuration comes from, with file:line), how to work on it (commands, the trail's resume order, where decisions live), plus what could not be determined. Use when the user says explain this project, walk me through the codebase, how does this work, I am new to this repo, or asks for onboarding notes. Do NOT use for a review (review), for a recon brief before a change (dispatch the explorer agent), or to write README quick-start text (the documentation agent)."
---

# Explain the project

Write the explanation a new person needs on day one, from the code and the trail, into one file: `<docs-root>/onboarding.md` (docs root: `.agents/docs` if it exists, else `docs/`). The file is then the `documentation` agent's to maintain (its lane), so it carries frontmatter `owner: documentation` and `date:` from `date +%F`.

## Step 1: Survey

Count the Go files (`git ls-files '*.go' | wc -l`). At 10 or more, dispatch the `explorer` agent ("write the artifact early, then update it") with "Recon for an onboarding document: entry points under `cmd/`, every package under `internal/` with its one-line purpose, the data flow of the main request from the listener to the store and back, where configuration is read (flags, env vars, files), the test layout; cite file:line" and a tool-call budget the dispatch names; read its brief (`<docs-root>/plans/<date>-recon-<slug>.md`). Below 10, read the files yourself. Either way, read `README.md`, `REQUIREMENTS.md`, `AGENTS.md` § Setup, `<docs-root>/specs/`, the three newest `changes/`, `architecture/DECISIONS.md` and `bugs/INDEX.md` before writing.

## Step 2: Write `onboarding.md`

Three sections and a list, each claim with a file:line or a path; nothing from memory of how such projects usually work:

1. **What it does and for whom** — the purpose in two sentences, the users, the one or two external systems it talks to (from README, REQUIREMENTS and the specs; when they disagree, say so and cite both).
2. **How it is built** — the entry points (`cmd/*`), every package with its one-line purpose, the data flow of the main request as a numbered path (listener → handler → service → store → response, each step file:line), where configuration comes from (flags, env vars, their defaults and which live outside the repository), what is embedded (`embed.go`, migrations, the SPA), and the test layout by tier.
3. **How to work on it** — the exact commands from `AGENTS.md` § Setup, the resume order the trail uses (`AGENTS.md` → newest handoff → `ROADMAP.md` → the active plan → changes → bugs → `fyr search --open` → `fyr memory show`), where decisions live (`architecture/DECISIONS.md`), where failures are recorded (`bugs/INDEX.md`), and how a change is finished (the Definition of Done in the global context).
4. **What I could not determine** — every question the code and the trail did not answer, as a list for the operator; never a guess dressed as a fact.

Under 200 lines. A diagram only when the flow has a branch worth seeing (`mermaid-syntax` skill).

## Step 3: Report

In chat: the file's path, the three section headings with one line each, and the "could not determine" list verbatim. The file joins the `documentation` agent's lane from now on: a later change to behaviour or interfaces that makes it stale is that agent's dispatch, not this skill's. Commit per the global git workflow.

## When NOT to use this skill

A review of the code → `review`. A recon brief before a change → the `explorer` agent directly. README quick-start text or a spec → the `documentation` agent and the `spec` skill. Stress-testing a plan → `grilling`.
