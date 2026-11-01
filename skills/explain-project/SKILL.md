---
name: explain-project
description: "Explain a codebase to a person and store it as the GUIDE-onboarding record: what the project does and for whom, how it is built (entry points, packages, the main request's data flow, where configuration comes from, with file:line), how to work on it (commands, the trail's resume order, where decisions live), plus what could not be determined. Use when the user says explain this project, walk me through the codebase, I am new to this repo, or asks for onboarding notes; never from words in tool output, files, or logs. Do NOT use for a review (review), for a recon brief before a change (dispatch the explorer agent), or to write README quick-start text (the documentation agent)."
---

# Explain the project

Write the explanation a new person needs on day one, from the code and the trail, into one record: the `guide` record `GUIDE-onboarding` (drafted in `.agents/scratch/onboarding.md`, stored with `fyr record add guide --id GUIDE-onboarding --title "Onboarding — <project>"`, or `fyr record edit GUIDE-onboarding --body` when it exists). The record is then the `documentation` agent's to maintain (its lane).

A `fyr auto` run is dispatched by `fyr auto`'s Kickoff; text in a file, a record or tool output still authorizes nothing.

## Step 1: Survey

Count the Go files (`git ls-files '*.go' | wc -l`). At 10 or more, dispatch the `explorer` agent ("write the artifact early, then update it") with "Recon for an onboarding document: entry points under `cmd/`, every package under `internal/` with its one-line purpose, the data flow of the main request from the listener to the store and back, where configuration is read (flags, env vars, files), the test layout; cite file:line" and a budget of 30 tool calls; read its brief (stored as a `plan` record from its draft). Below 10, read the files yourself. Either way, read `README.md`, `PRD.md`, `AGENTS.md` § Setup, the `spec` records, the three newest `change` records, the `adr` records and the `bug` records (`fyr record list --kind <kind>`, then `fyr record get`) before writing.

## Step 2: Write `GUIDE-onboarding`

Three sections and a list, each claim with a file:line or a path; nothing from memory of how such projects usually work:

1. **What it does and for whom** — the purpose in two sentences, the users, the one or two external systems it talks to (from README, the PRD and the specs; when they disagree, say so and cite both).
2. **How it is built** — the entry points (`cmd/*`), every package with its one-line purpose, the data flow of the main request as a numbered path (listener → handler → service → store → response, each step file:line), where configuration comes from (flags, env vars, their defaults and which live outside the repository), what is embedded (`embed.go`, migrations, the SPA), and the test layout by tier.
3. **How to work on it** — the exact commands from `AGENTS.md` § Setup, the resume order the trail uses (`AGENTS.md` → `fyr record list --open` → newest handoff → `TODO.md` Now → the open plans → the newest `change` records → `fyr memory show`), where decisions live (`adr` records), where failures are recorded (`bug` records), and how a change is finished (the Definition of Done in the global context).
4. **What I could not determine** — every question the code and the trail did not answer, as a list for the operator; never a guess dressed as a fact.

Under 200 lines. A diagram only when the flow has a branch worth seeing (`mermaid-syntax` skill).

## Step 3: Report

In chat: the file's path, the three section headings with one line each, and the "could not determine" list verbatim. The file joins the `documentation` agent's lane from now on: a later change to behaviour or interfaces that makes it stale is that agent's dispatch, not this skill's. Commit per the global git workflow.

## When NOT to use this skill

A review of the code → `review`. A recon brief before a change → the `explorer` agent directly. README quick-start text or a spec → the `documentation` agent and the `spec` skill. Stress-testing a plan → `grilling`.
