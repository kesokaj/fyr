# Deploy — local

<!-- Track 1: human-first documentation (docs-ste100-80). No YAML frontmatter. -->

Build, configure, and verify this project on your local workstation. `/fyr-init-project` fills this record from setup answers, the Definition of Done docs step keeps it current, and `verifier` follows it for local smoke tests.

## Prerequisites

<!-- Toolchain and versions (Go 1.24+, Node …), Colima/Docker if a Dockerfile exists, and one-time machine tools. -->

## Build

```bash
<the build command from the setup answers>
```

## Configure

Set runtime environment variables below before starting the app. Never store secrets in code defaults; name where each secret comes from.

<!-- One row per setting the code reads. No code yet, or no settings: delete the placeholder row and keep the empty table. -->

| Variable | Default | Purpose |
|---|---|---|
| `<VARIABLE>` | `<default>` | `<what it sets>` |

## Run

```bash
<the run command from the setup answers>
```

## Test

```bash
<the test command from the setup answers>
```

## Verify

<!-- Smoke check proving the process works: health endpoint, CLI call, or startup log line with real ports and flags once implemented. -->

## Container (when there is a Dockerfile)

<!-- Start Colima (`colima status || colima start`), then build and run natively for local testing; keep linux/amd64 release builds in DEPLOY-gcp. -->

## Database & migrations

<!-- Local Postgres / compose service (delete these comments and fill when the project uses a database; otherwise keep "No database yet."). -->
<!-- Start: `docker compose up -d db` (`docker/references/setup.md` § Step 2); check health: `docker compose ps`. -->
<!-- Migrate: run migrations before starting the app, for example `DATABASE_URL=postgres://localhost:5432/app?sslmode=disable go run ./cmd/<app> migrate` or `golang-migrate`. -->

## Dependency cadence

<!-- Run `govulncheck ./...`, `npm audit --omit=dev`, and base-image checks before every release and monthly; record the last run date. -->

## Operator steps

<!-- Standing manual procedures on this workstation, never pending tasks (those live in the newest handoff). Record any standing root-step permission ("the agent may run `sudo -n systemctl start docker` here when asked") here. -->

## Stop and clean up

Stop local processes and remove temporary containers when finished.

> **Caution:** Running `docker compose down -v` permanently deletes local database volumes and all local test data. Run it only when you intend to reset local state.

```bash
docker compose down -v
```
