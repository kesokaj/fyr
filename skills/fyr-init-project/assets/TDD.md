# TDD — <project>

<one line: how each part is built and tested>
Status: draft · Last updated: YYYY-MM-DD · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- The technical design: one section T1… per component M… of SDD.md — its interfaces, data, errors, configuration
     and dependencies — each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`),
     never a copy of it. TODO.md tasks name the section they build (→ T1). Kept current by the session that
     changes the code, in the same commit. -->

## T1 — <component>

Implements: M1 · Spec: SPEC-<app>-<topic> (none yet: <why>)

<!-- Interfaces (CLI, API, files), data and storage, errors and failure modes, configuration, dependencies — a
     line or two each; what the spec record already says is linked, not repeated. -->

- **Interfaces:** <one line>.
- **Errors:** <one line>.
- **Configuration:** <one line>.

## Data model

<!-- The stored entities and their keys, or "no stored data". -->

## Testing strategy

<!-- The tiers this project runs and the command behind each; the procedure is the GUIDE-testing record. -->

See `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Build, run and deploy

<!-- The commands are AGENTS.md § Setup commands; the procedures are the DEPLOY-local, DEPLOY-remote and DEPLOY-gcp records. -->

See the `DEPLOY-local` record.

## Trace

<!-- Every component of SDD.md, its section here, and the tests that verify it. A component with no row is a gap. -->

| Component | Section | Verified by |
|---|---|---|
| M1 | T1 | <TestName or command> |
