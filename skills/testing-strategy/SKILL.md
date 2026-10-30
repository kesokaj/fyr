---
name: testing-strategy
description: "Decide which tests a change owes and how the project's testing guide is structured: the test tiers for this stack (unit with -race, integration against a real dependency in compose, API with httptest or curl, browser e2e with Playwright against the Go binary, container smoke, cloud smoke inside /preflight), when each tier is owed, fixtures, what covered means, and who maintains the GUIDE-testing record. Use when the user asks for a test strategy, which tests to write, how we should test this, what coverage means here, or to write or update the testing guide; load it before planning a feature's Verification section. Do NOT use to write the test itself (golang, typescript), to run the tiers (dispatch the verifier agent), or for the release gate (preflight)."
---

# Testing strategy

Which tests a change owes, in which tier, against which fixture, and how the project records it. The test itself is the `golang` or `typescript` skill's job (`golang/references/testing.md`, `typescript/references/testing.md`); running the tiers is the `verifier` agent's; the release gate is `preflight`. This skill decides what is owed and writes it down so the next session does not re-decide.

**The trail** is records (`~/.gemini/config/skills/init-project/references/trail.md`).

## The tiers for this stack

| Tier | Command shape | What it proves | What it cannot prove |
|---|---|---|---|
| Unit | `go test -race ./...`; `vitest run --project unit` | a function or handler does what its test says, in isolation, with hand-written fakes at the boundary | that the real dependency behaves like the fake |
| Integration | `docker compose up -d db` then `go test -race -tags integration ./internal/...` against the real Postgres | the SQL, the migration and the driver settings work against the engine that ships | the HTTP surface |
| API | `httptest` in Go; `curl` against `go run ./cmd/<app>` | the wire shape (status, body per `json-api-style`, headers) | the browser |
| Browser e2e | Playwright against the Go binary that embeds the SPA (`typescript/references/testing.md` § E2E; `reuseExistingServer: false`) | the golden path and one edge case as a person sees them | load, the container |
| Container smoke | `docker build`, `docker run -p 127.0.0.1:8080:8080`, `curl /healthz` on the container you started (`docker` skill § 4) | the image starts, listens on `PORT`, answers | the cloud's IAM and network |
| Cloud smoke | inside `/preflight` only, by the `DEPLOY-gcp` record | the revision serves on Cloud Run | nothing here runs outside the gate |

One tier never stands in for another: a green unit suite says nothing about the container, and a container that answers says nothing about the SQL.

## What a change owes

| Change kind | Tiers owed |
|---|---|
| A pure function, a package with no I/O | unit |
| A handler, a route, a JSON shape | unit + API |
| SQL, a migration, a repository method | unit (fakes) + integration against compose Postgres |
| A UI route or component | unit (component test) + browser e2e for the golden path when the route is new or its flow changed |
| A Dockerfile, `.dockerignore`, the build, `embed.go` | container smoke |
| A deploy flag, a secret, IAM, the service account | cloud smoke, inside `/preflight` |
| A bug fix | the tier where the bug was observable, with the failing test first (`bug` skill Step 3) |

The plan's Verification section (the `feature` skill Step 2) names the tiers from this table and the command for each; a task's verify clause names one command and what it prints when done.

## Fixtures

- A real dependency over a mock when the engine's behaviour is the question: Postgres in compose (`skills/docker/references/setup.md` § Step 2), a `testcontainers`-free setup because compose is already there. A fake by hand (`golang/references/testing.md` § Fakes) when the question is the code around the dependency.
- Fixtures state the condition they create and the test proves it holds before the assertion that depends on it (a seeded row is read back; a denied permission is checked to be denied).
- No shared mutable fixture across tests: `t.TempDir()`, a schema per test or a transaction rolled back, a fresh `httptest.Server`.
- Never the production project, never real credentials: compose and emulators locally; the cloud only inside `/preflight`.

## What "covered" means

A test is evidence once it has failed for the right reason: written first and run bare before the code (red), then green after; a change to existing behaviour gets a must-fail mutation (`review-standards` § 2: a unique target, restored from a saved copy, the mutated code compiled and a NAMED test failed). "Covered" in a report names the tiers that ran and the tiers that did not; a claim of coverage that names nothing uncovered is read as unverified. Changed code without a covering test is a FAIL in the verifier's log, reported, never hidden.

## The project's testing guide

`GUIDE-testing` is the one guide per project, a `guide` record. Its shape is the init-project template (`init-project/assets/records/GUIDE-testing.md`): § 1 Commands (the exact commands from `AGENTS.md` § Setup), § 2 Tiers (this skill's table filled for the project), § 3 Fixtures and environment, § 4 What is not covered, § 5 Log format. `/init-project` stores the template when the project has no guide; the `verifier` drafts its placeholders on its first run and the commands when they change; `documentation` maintains it when behaviour or interfaces change. Every verification run is a `testing` record titled `Verification — <slug>`, with a `Range reviewed:` line and a `## Status:` of PASS, FAIL or BLOCKED, and names what it did not exercise.

## When NOT to use this skill

Writing the test → `golang`, `typescript`. Running the tiers → dispatch the `verifier` agent (the Definition of Done step 1). The release gate and the cloud smoke → `preflight`. Reviewing whether tests exist for a diff → `/review --tests`.
