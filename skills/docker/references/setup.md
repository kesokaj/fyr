# Docker First-Time Setup (Go service)

What a new Go project needs before its first `docker build`. The Dockerfile itself is
in `SKILL.md`.

## Step 1: `.dockerignore`

The one list; `SKILL.md` § Dockerfile rule 4 points here instead of repeating it.

```
# allowlist: closed by default — only what the build reads enters the context
*
!go.mod
!go.sum
!cmd/
!internal/
!web/
# and inside what is allowed, never these
web/node_modules
web/dist
web/.output
**/.env*
**/*.env
**/.npmrc
**/*.pem
**/*.key
**/*-key.json
**/*credentials*.json
```

An allowlist, not a denylist: `*` excludes everything, each `!` line re-admits one directory
the build reads (add one per source directory the project has, `migrations/` or `pkg/`), so a
secret OUTSIDE those directories — `key.json`, `id_ed25519`, `*.tfvars.json`, `*.tfstate.backup` at
the root or under `infra/`, `keys/`, `creds/` — stays out without being named. Inside a re-admitted
directory only the named shapes are excluded (the tail of the list), so credentials never live in
`cmd/`, `internal/` or `web/` in the first place. A denylist of
secret shapes was measured on 2026-10-01 and kept letting new shapes through (a `**/` prefix
is needed to reach subdirectories, `*.env*` and `*.env` are different patterns): the list
above is what the build needs, nothing else.

## Step 2: `docker-compose.yml` for local dependencies

The app runs on the host (`go run ./cmd/<app>`) during development; compose provides
what it talks to. Add only the services the project actually uses.

```yaml
services:
  db:
    image: postgres:18          # the major Terraform's `POSTGRES_18` creates on Cloud SQL
    ports:
      - "127.0.0.1:${DB_PORT:-5432}:5432"   # loopback only: a dev database with app/app must not face the LAN
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: app
      POSTGRES_DB: app
    volumes:
      - pgdata:/var/lib/postgresql   # postgres:18 moved PGDATA to /var/lib/postgresql/18/docker and its VOLUME to /var/lib/postgresql (image inspected 2026-10-01); the old /data path would leave the named volume empty
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pgdata:
```

Pin the image tag. Local credentials like `app`/`app` are fine in compose; real ones
never are.

## Step 3: First run

```bash
colima status || colima start        # macOS only
docker compose up -d
docker compose ps                    # db should be "healthy"
DATABASE_URL=postgres://app:app@localhost:5432/app?sslmode=disable go run ./cmd/<app>
```

Then the container smoke test from `SKILL.md` §4, and `DEPLOY-local` records
the exact commands for the next session.

## Optional: the app in compose too

Only when a second container must reach the app by name (a worker, an e2e runner):

```yaml
  app:
    build: .
    ports:
      - "127.0.0.1:${PORT:-8080}:8080"
    environment:
      DATABASE_URL: postgres://app:app@db:5432/app?sslmode=disable
    depends_on:
      db:
        condition: service_healthy
```

`build: .` uses the native architecture, which is what a local run wants; the amd64
build for Google is a separate `buildx` command, never a compose setting.
