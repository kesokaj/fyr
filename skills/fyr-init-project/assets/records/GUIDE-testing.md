# Testing guide — <project>

<!-- Agent-only brain.db record (Telegraphic/Structured Markdown; no YAML frontmatter; keep Caution before destructive commands).
     The one guide per project. /fyr-init-project copies this template; `fyr verify` runs § 1 formatting,
     vet, race tests, and `fyr check`; the `verifier` fills/runs live e2e and smoke tiers; `documentation`
     maintains it when behaviour or interfaces change. The tiers and when each is owed live in testing-strategy. -->

Run these verification commands and test tiers before claiming a change works. Keep this guide aligned with `AGENTS.md` § Setup commands.

## 1. Commands

Run each verification command bare from the repository root and check its exit status.

```bash
<build command>
<test command>        # verify: go vet ./... && go test -race ./...
```

## 2. Tiers

Match each changed surface to the required test tiers below.

| Tier | Command | When owed | What it proves |
|---|---|---|---|
| Unit | `<go test -race ./...>` | every change under `cmd/` or `internal/` | <what> |
| Integration | `<compose up + tagged tests>` | SQL, migrations, repository methods | <what> |
| API | `<httptest / curl>` | a handler, a route, a JSON shape | <what> |
| Browser e2e | `<playwright>` | a new or changed UI flow | <what> |
| Container smoke | `<docker build && docker run && curl /healthz>` | the Dockerfile, the build, `embed.go` | <what> |
| Cloud smoke | inside `/fyr-preflight` only (`DEPLOY-gcp`) | a deploy flag, a secret, IAM | the revision serves |

## 3. Fixtures and environment

<!-- Compose services and start commands; seeded test data and its source; safe default env vars; what tests never touch (production projects, real credentials). -->

## 4. What is not covered

<!-- Name every tier this project does not run yet and every uncovered path. A coverage claim that omits gaps here is unverified. -->

## 5. Log format

Record every e2e verification run as a `testing` record titled `Verification — <slug>` from the `verifier` draft (or the main agent's own log for `fyr verify`, behavioural, or sandbox runs). The release gate accepts an e2e or smoke command only from a `verifier` log or `fyr verify` digest.

Include these required fields in every log:

- `Range reviewed: <range or working tree>` and `Scope:`
- `## Status: PASS | FAIL | BLOCKED` (`IN PROGRESS` only while running)
- Every command run with its exit status (for main-agent runs, report each row as `n of N` against its pass criterion; set `PASS` only when every row passes)
- `## Not examined` naming every tier or path that did not run
