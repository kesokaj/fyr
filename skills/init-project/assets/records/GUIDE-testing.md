---
owner: verifier
date: YYYY-MM-DD
---
# Testing guide — <project>

<!-- The one guide per project. /init-project copies this template; the verifier fills every <placeholder> on its first run and updates § 1 when the commands change; documentation maintains it when behaviour or interfaces change. The tiers and when each is owed: the testing-strategy skill. -->

## 1. Commands

The exact verification commands, the ones `AGENTS.md` § Setup lists; run each bare and read its exit status.

```bash
<build command>
<test command>        # e.g. go vet ./... && go test -race ./...
```

## 2. Tiers

| Tier | Command | When owed | What it proves |
|---|---|---|---|
| Unit | `<go test -race ./...>` | every change under `cmd/` or `internal/` | <what> |
| Integration | `<compose up + tagged tests>` | SQL, migrations, repository methods | <what> |
| API | `<httptest / curl>` | a handler, a route, a JSON shape | <what> |
| Browser e2e | `<playwright>` | a new or changed UI flow | <what> |
| Container smoke | `<docker build && docker run && curl /healthz>` | the Dockerfile, the build, `embed.go` | <what> |
| Cloud smoke | inside `/preflight` only (`deploy/gcp.md`) | a deploy flag, a secret, IAM | the revision serves |

## 3. Fixtures and environment

<!-- The compose services and how to start them; seeded data and where it comes from; env vars a test needs and their safe defaults; what is never used (the production project, real credentials). -->

## 4. What is not covered

<!-- Named, never implied: the tiers this project does not run yet and why; the paths no test reaches. A claim of coverage that names nothing here is unverified. -->

## 5. Log format

Every verification run writes `<docs-root>/testing/<date>-verify-<slug>.md` with frontmatter `owner:` (`verifier` for the verifier agent's runs, `main` for a run the main agent makes itself, such as a behavioural or sandbox run: the release gate counts a command as run only from a verifier's log), `date:`, a `Range reviewed: <range or working tree>` line, `## Status: PASS | FAIL | BLOCKED` (IN PROGRESS only while running), a `Scope:` line, the commands run with their exit status (a main-agent run gives each row as n of N against the row's own pass criterion, and its heading is PASS only when every row met it), and a `## Not examined` section naming what did not run.
