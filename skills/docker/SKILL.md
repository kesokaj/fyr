---
name: docker
description: |
  Container-based development with Docker via Colima. Use when running containerized commands,
  building images, managing docker-compose services, writing Dockerfiles, debugging container issues,
  or working with volumes and networking. Activate for any docker, docker-compose, container,
  or Dockerfile mention — even simple "build the container" requests.
  Do NOT use for Kubernetes orchestration (use gke-basics) or Terraform infrastructure (use terraform).
---

# Docker & Container Development

Local container work for a Go service (`cmd/<app>/` + `internal/`, see the `golang`
skill). Docker runs via **Colima** on macOS (Apple Silicon); Linux boxes use the plain
Docker daemon and skip every Colima step.

**When NOT to use this skill:** pushing or deploying (`cloud-run-deploy`), Kubernetes
(`gke-basics`), infrastructure-as-code (`terraform`).

## Environment

```bash
# macOS only: Colima must be up before any docker command
colima status || colima start
# both platforms
docker info && docker compose version
```

## Core Rules

### 1. Colima first (macOS)
`colima status || colima start` before every Docker operation on the Mac. On Linux,
`docker info` is the check.

### 2. One architecture rule
Native build for the local smoke test; `linux/amd64` for anything that goes to Google.
An arm64 image on Cloud Run fails with `exec format error`.
```bash
docker build -t <app>:dev .                                        # local smoke test only
docker buildx build --platform linux/amd64 --load -t <app>:<sha> .  # everything bound for GCP
```
Tagging, Artifact Registry, and the deploy itself live in `cloud-run-deploy`.

### 3. No TTY in agent contexts
`docker exec <container> <cmd>` — never `-it`, it fails with "not a TTY".

### 4. Smoke test before anything leaves the machine
```bash
cid=$(docker run --rm -d -p 127.0.0.1:8080:8080 <app>:dev)   # bind to loopback: the box's other interfaces see nothing
docker ps --filter id=$cid --format '{{.Status}}'   # the container you started is the thing under test
curl -fsS http://localhost:8080/healthz              # a reply from something else on :8080 is not evidence
docker logs $cid; docker stop $cid
```
Probe the container you started, not whatever answers on the port: a stale container, another project's service, or a tunnel all return 200 and prove nothing (`context/AGENTS.md`, the instrumentation rule).

## Dockerfile: Go multi-stage

```dockerfile
# Stage 1 (only when web/ exists): the SPA, built where Node lives and nowhere else.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build                        # vite build && tsc -b → dist/client (the frontend skill)

# Stage 2: the Go binary; the tag is the go.mod go line's MAJOR.MINOR (go 1.27.1 → golang:1.27)
FROM golang:<major.minor of the go.mod go line> AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/client ./web/dist/client   # embedded by web/embed.go at go build; omit without web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app ./cmd/<app>

# Stage 3: the runtime — no shell, no Node, one static binary
FROM gcr.io/distroless/static-debian12@sha256:<digest>   # rule 1: distroless has no version tag; the digest is the pin
COPY --from=build /app /app
USER nonroot
EXPOSE 8080
CMD ["/app"]
```

**Key rules:**
1. Pin the base images, never `:latest`: the Go stage is `golang:<major.minor of the go.mod go line>` (read `go.mod`: `go 1.27.1` → `golang:1.27`, which tracks the current patch and its stdlib fixes; a three-part tag (major.minor.patch) freezes a patch with known advisories; a minor written in this skill goes stale; `cloud.google.com/go/cloudsqlconn` needs 1.26 or later), the runtime `gcr.io/distroless/static-debian12@sha256:<digest>` — distroless publishes no version tags, so a bare name is an implicit `:latest` and the digest is its only pin (`docker buildx imagetools inspect gcr.io/distroless/static-debian12` prints it; bump it deliberately). When the whole build must be reproducible byte for byte, pin the Go and Node images by digest too.
2. `CGO_ENABLED=0` so the binary is static and runs on `distroless/static`; if cgo is
   unavoidable, switch to `distroless/base-debian12` and say why.
3. Copy `go.mod`/`go.sum` and `go mod download` before the source, so the module layer caches.
4. `.dockerignore`: the one list is [First-Time Setup](references/setup.md) § Step 1; copy it, do not retype it.
5. No secrets in any layer: no `COPY .env`, no build args carrying tokens. A secret the BUILD
   needs (a private module proxy token) comes in through `RUN --mount=type=secret,id=netrc,target=/root/.netrc`
   with `--secret id=netrc,src=$HOME/.netrc` on the build command: mounted for that `RUN` only,
   never a layer. Everything else arrives at runtime (Secret Manager on Cloud Run, `env_file` locally).
6. Health check through the app's `/healthz`. Distroless has no shell or curl, so the
   check is the binary itself (`/app -healthcheck`) or the platform's HTTP probe —
   see [Health Checks](references/health-checks.md).
7. `USER nonroot` stays; bind a port above 1024 (`PORT`, default 8080).

If the Go binary also serves the `web/` frontend, the `node:24-alpine` stage above (the
only Node image here) runs `npm ci && npm run build` in `web/`, and the Go stage copies
`web/dist/client` into the build context BEFORE `go build`, because the frontend skill
embeds that directory with `go:embed` at compile time; nothing is copied next to the
binary, and the runtime image stays distroless. `web/dist/` is gitignored and in
`.dockerignore` (the build makes it, the context never carries it), never committed.

## Docker Compose (local dependencies)

Compose runs what the app needs locally (Postgres, Redis, an emulator); the app itself
usually runs with `go run ./cmd/<app>` on the host for fast iteration, in a container
only for the smoke test. Pattern in [First-Time Setup](references/setup.md).

```bash
docker compose up -d           # start dependencies
docker compose down -v         # stop and drop volumes (fresh DB)
docker compose logs -f <svc>   # follow one service
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
```

## Troubleshooting

| Problem | Fix |
|---|---|
| `Cannot connect to the Docker daemon` | macOS: `colima start`. Linux needs a root step: run `sudo -n systemctl start docker` yourself only when the operator asked for it AND the project's `DEPLOY-local.md` § Operator steps records the standing permission, and record the command plus its verification in the change entry; otherwise hand that exact command back to them (`context/dev-setup.md`) |
| `exec format error` on Cloud Run | arm64 image was pushed — rebuild with `--platform linux/amd64 --load` |
| buildx complains about the builder | `docker buildx create --use` once, then retry |
| amd64 build slow on Apple Silicon | Expected: QEMU emulation. `CGO_ENABLED=0` keeps it a pure Go cross-compile in practice |
| `no such file or directory` on start | Binary linked against cgo on `distroless/static` — set `CGO_ENABLED=0` or use `base-debian12` |
| Port already in use | `lsof -i :<port>` then stop it or change `PORT` |
| Container has no shell for debugging | Run the same binary in the `golang:<major.minor>` build image or use `docker cp` / logs; do not add a shell to the runtime image |
| Compose DB keeps old schema | `docker compose down -v` before `up -d` |

## Reference Library

- [First-Time Setup](references/setup.md) — `.dockerignore`, compose file for local dependencies, first run
- [Health Checks](references/health-checks.md) — `/healthz` handler, `-healthcheck` flag for distroless, compose healthcheck
