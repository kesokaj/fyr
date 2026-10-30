---
name: cloud-run-deploy
description: |
  The HOW of a Google Cloud Run deploy: build the image locally (ARM to test, AMD64 to
  deploy), push to Artifact Registry, deploy, verify, roll back. A ship, deploy, release or
  "cut vX.Y.Z" request in the user's own turn means `/preflight`, which gates and then runs
  these steps: never run them in its place. Use directly for the mechanics: an image build
  or push, a rollback, a `gcloud run` flag, a failed revision, or a deploy `/preflight`
  already gated. Do NOT use for GKE workloads (gke-basics), local-only container work
  (docker), gcloud auth problems (gcloud-setup) or Terraform-managed infrastructure
  (terraform).
---

Release pipeline: **local build (Colima) → Artifact Registry → Cloud Run**. Build
locally first, always. Artifact Registry is the primary registry; GitHub Container
Registry only when explicitly requested. Every flag below was checked against
`gcloud run deploy --help` of the Google Cloud SDK 587.0.0 (2026-10-01).

## Pre-flight

1. **Defaults:** read the project's `DEPLOY-gcp` record (`fyr record get DEPLOY-gcp`):
   project ID, region, Artifact Registry repo, service name, the service account it
   runs as, visibility, and the rollout and network rows (§ What `DEPLOY-gcp`
   records). If it is missing or empty, ask the user for the six defaults and write
   them there before anything else. Never guess a project; `gcloud config get project` and `deploy/gcp.md` are
   cross-checks, not sources.
2. **Auth:** if any gcloud/docker-push command fails with a credential error, stop and
   apply the `gcloud-setup` skill.
3. **Colima (macOS only):** `colima status || colima start`; on Linux the native daemon is
   already running.
4. **Region:** confirm once and reuse everywhere (`REGION`, e.g. `europe-north1`);
   Artifact Registry repo and Cloud Run service should live in the same region.
5. **Registry auth (once per machine/region):**
   `gcloud auth configure-docker ${REGION}-docker.pkg.dev`
6. **Service account and roles:** the service account the service will run as exists
   (`gcloud iam service-accounts describe NAME@PROJECT.iam.gserviceaccount.com`) and holds
   `roles/secretmanager.secretAccessor` on every secret the deploy names, plus
   `roles/cloudsql.client` when `--add-cloudsql-instances` is used; every caller of an
   IAM-gated service (another service's SA, Scheduler, Tasks, Pub/Sub) holds
   `roles/run.invoker` on it. The operator names the SA with the other defaults
   (`DEPLOY-gcp`); never fall back to the default compute account.

## Build — dual architecture

Cloud Run executes `linux/amd64`. The ARM build exists for fast local testing on
Apple Silicon; the AMD64 build is what ships. Verify both, deploy the AMD64 one.

```bash
IMAGE="${REGION}-docker.pkg.dev/${PROJECT}/${REPO}/${SERVICE}"
TAG="$(git rev-parse --short HEAD)"          # the one tag; immutable because the repository enforces it (--immutable-tags below); no :latest alias — it could never be moved to the next build

# Local test build (native ARM on Apple Silicon — fast)
docker build -t "${SERVICE}:local" .
docker run --rm -p 127.0.0.1:8080:8080 "${SERVICE}:local"   # smoke-test before anything ships; loopback only

# Deploy build (AMD64 under emulation on Colima)
docker buildx build --platform linux/amd64 -t "${IMAGE}:${TAG}" --load .
```

Notes:

- `buildx --platform linux/amd64` on Apple Silicon runs under QEMU emulation —
  slower but correct. If buildx complains about the builder, create one once:
  `docker buildx create --use`.
- Tag with the git short SHA and nothing else: the repository enforces immutable tags, so there is no `latest` alias; rollbacks and audits need a tag that never moves.
- If the Artifact Registry repo doesn't exist yet:
  `gcloud artifacts repositories create ${REPO} --repository-format=docker --location=${REGION} --immutable-tags` (tags are mutable by default; the SHA tag is an audit trail only with this)

## Push & Deploy

```bash
docker push "${IMAGE}:${TAG}"

gcloud run deploy "${SERVICE}" \
  --image "${IMAGE}:${TAG}" \
  --region "${REGION}" \
  --port 8080 \
  --service-account "${SA}" \
  --startup-probe httpGet.path=/healthz
```

There is no `--platform` flag: it is gone from the reference, and Cloud Run for Anthos
is archived. The service must listen on `$PORT` (Cloud Run injects it, default 8080)
and bind `0.0.0.0`, not localhost.

Flags — apply deliberately, not by default; a project-specific flag and its reason go in
`DEPLOY-gcp`:

| Flag | Use |
|---|---|
| `--allow-unauthenticated` | Public services only (`deploy/gcp.md` Public: yes). When the service is in Terraform, Terraform owns this setting and this flag is never passed (the `terraform` skill, item 6): the `allUsers` binding it writes outlives a later `public = false`. Omit for internal services: IAM-gated is the default, and `--ingress` does not replace it ("the `--[no-]allow-unauthenticated` flag separately controls the identities allowed to call the service"). |
| `--iap` | Identity-Aware Proxy on the service itself (GA, no load balancer): the login page for an internal tool. Users get `roles/iap.httpsResourceAccessor`; the IAP service agent needs `roles/run.invoker`. `roles/iap.httpsResourceAccessor` is granted on the service, never the project. The app still validates `x-goog-iap-jwt-assertion` with `google.golang.org/api/idtoken` — `idtoken.Validate` against a NON-EMPTY audience `/projects/N/locations/R/services/S` (the library skips the `aud` check when the audience is empty, so startup fails on an empty configured audience) plus an explicit issuer check for `https://cloud.google.com/iap`, since `Validate` does not check `iss` — never a hand-rolled JWT check (the `api-auth-gcp` skill). |
| `--ingress all\|internal\|internal-and-cloud-load-balancing` | Default `all`. `internal` admits VPC and internal Google sources only; it does not bypass IAM. |
| `--startup-probe httpGet.path=/healthz` | The default probe for every service; keys `initialDelaySeconds`, `timeoutSeconds`, `periodSeconds`, `failureThreshold`, `httpGet.port`, `tcpSocket.port`, `grpc.port`. `--liveness-probe` the same way; a failed liveness probe SIGKILLs the container, so keep `/healthz` free of downstream calls (`skills/docker/references/health-checks.md`). |
| `--network NET --subnet SUBNET --vpc-egress private-ranges-only\|all-traffic` | Direct VPC egress (a `/26` or larger subnet), first choice for reaching private IPs (Cloud SQL private IP, Memorystore). `--vpc-connector` only when cold starts over 30 s with a connector and Cloud NAT are measured to be better; `all` is deprecated, use `all-traffic`. |
| `--add-cloudsql-instances PROJECT:REGION:INSTANCE` | Mounts `/cloudsql/PROJECT:REGION:INSTANCE`; the service's SA needs `roles/cloudsql.client`. Siblings `--set-`, `--remove-`, `--clear-cloudsql-instances`. Connection code: the `data-gcp` skill; the Go shape in the golang skill's `service.md` § Database: Postgres on Cloud SQL. |
| `--set-secrets KEY=SECRET_NAME:VERSION` | Secrets via Secret Manager — the only acceptable way to pass credentials. Pin the version (`:3`), not `:latest`, for env vars: a rotated secret then ships with a deploy, not under a running revision. `/path/file=SECRET:VERSION` mounts it as a file. |
| `--set-env-vars KEY=VALUE` | Plain config. Never put secrets here. |
| `--service-account NAME@PROJECT.iam.gserviceaccount.com` | A dedicated service account per service, never the project's default compute account; grant it `roles/secretmanager.secretAccessor` on each secret `--set-secrets` names and nothing it does not use. The deployer needs `roles/iam.serviceAccountUser` on it. |
| `--cpu-boost` | Extra CPU during startup. Enabled by default on new services; `--no-cpu-boost` only with a measured reason. |
| `--min-instances N` / `--max-instances N` | Cold-start vs cost trade-off; confirm with the user before `min > 0` (idle instances bill, at a lower rate under request-based billing). `default` removes the setting. |
| `--concurrency N` | Maximum concurrent requests per instance (default 80). Lower it for CPU-bound handlers, raise it for I/O-bound ones, after a load test. |
| `--memory` / `--cpu` | Only when the defaults (512Mi / 1 CPU) are proven insufficient. |
| `--cpu-throttling` / `--no-cpu-throttling` | Request-based billing (CPU only during requests) is the default; `--no-cpu-throttling` is instance-based billing for background work in a service. Background work usually belongs in a Job instead. |
| `--execution-environment` | Leave unset: Cloud Run picks gen1/gen2 from the features used. |

## Canary rollout

When the service is in Terraform (`terraform` skill), Terraform set the image once and ignores it since: this skill owns every revision, the traffic split and the rollback, and a `terraform apply` in between changes none of them.

`/preflight`'s deploy row runs this block from the SECOND revision on; the first deploy of a
service that does not exist yet is a plain `gcloud run deploy` (there is no serving revision to
keep traffic on), and a rollback skips it. Deploy without traffic, test the tagged revision with a
token, shift a slice, promote.

```bash
gcloud run deploy "${SERVICE}" --image "${IMAGE}:${TAG}" --region "${REGION}" \
  --no-traffic --tag canary                     # new revision, 0 % traffic
# tagged URL: https://canary---${SERVICE}-<hash>.a.run.app — test it (§ Verify) before any traffic
gcloud run services update-traffic "${SERVICE}" --region "${REGION}" --to-tags canary=10
# watch logs and the 5xx rate on the canary for the agreed window
gcloud run services update-traffic "${SERVICE}" --region "${REGION}" --to-latest     # promote: 100 % to the new revision
gcloud run services update-traffic "${SERVICE}" --region "${REGION}" --remove-tags canary
```

The trap, quoted from `gcloud run deploy --help` on `--no-traffic`: "After a deployment
with this flag the LATEST revision will not receive traffic on future deployments. To
restore sending traffic to the LATEST revision by default, run the gcloud run services
update-traffic command with `--to-latest`." So `--to-latest` is the promote step AND the
reset; a canary that is abandoned instead of promoted still ends with `--to-latest` (or an
explicit `--to-revisions`), then `--remove-tags canary`. Rolling the canary back is
§ Rollback with `GOOD` = the revision that had 100 % before.

## Verify

```bash
URL=$(gcloud run services describe "${SERVICE}" --region "${REGION}" --format 'value(status.url)')
# during a canary, test the tagged URL first: gcloud run services describe ... --format 'value(status.traffic)' lists it
# public service (DEPLOY-gcp says Public: yes):
curl -fsS "${URL}/healthz"
# IAM-gated service (the default): a bare curl MUST 403 (that is the gate's proof), then the authenticated probe
curl -fsS -H "Authorization: Bearer $(gcloud auth print-identity-token)" "${URL}/healthz"
# or, for a browser or repeated checks, a local proxy with credentials attached:
gcloud run services proxy "${SERVICE}" --region "${REGION}" --port 8080 [--tag canary]
gcloud run services logs read "${SERVICE}" --region "${REGION}" --limit 50 --log-filter 'severity>=ERROR' --freshness 15m
gcloud run services describe "${SERVICE}" --region "${REGION}" --format 'value(status.traffic)'
```

The caller needs `roles/run.invoker`; a gcloud identity token has no audience claim,
which is fine for a verify step and wrong for a production caller (those use an OIDC
token with the service URL as audience). A deploy is not done until the new revision
serves the intended share of traffic and the error log is clean. Report the revision
name, the traffic split and the URL — never claim success without the probe.

## Rollback

```bash
gcloud run revisions list --service "${SERVICE}" --region "${REGION}"
gcloud run services update-traffic "${SERVICE}" --region "${REGION}" \
  --to-revisions "${GOOD_REVISION}=100"
gcloud run services update-traffic "${SERVICE}" --region "${REGION}" --remove-tags canary   # when a canary tag is still set
```

Because images are tagged by git SHA, the previous revision's image is always
recoverable; `update-traffic` is instant and does not rebuild anything. A rollback
outside a release is recorded as a `change` record and never re-tagged as a release.

## What `DEPLOY-gcp` records

The project's guide holds the six defaults (project, region, repository, service,
public yes/no, service account) and, once decided, these rows, each with its reason:

| Row | Value |
|---|---|
| Rollout | the canary tag name (`canary`), the traffic slice (`10`), the watch window, and the promote step (`--to-latest`) |
| Ingress / IAP / VPC | `--ingress` value; `--iap` yes/no and who holds `roles/iap.httpsResourceAccessor`; `--network`/`--subnet`/`--vpc-egress` or none |
| Cloud SQL | the `PROJECT:REGION:INSTANCE` passed to `--add-cloudsql-instances`, or none |
| Probes and scale | the startup probe path, `--min-instances`/`--max-instances`/`--concurrency` when set |
| Secrets | the `--set-secrets` names and pinned versions (names, never values) |

## Troubleshooting

| Symptom | Likely cause → fix |
|---|---|
| `exec format error` in Cloud Run logs | ARM image was pushed — rebuild with `--platform linux/amd64` |
| Container fails to start / `PORT` timeout | App not listening on `$PORT` on `0.0.0.0`, or crashes on boot, or the startup probe path 404s — check `gcloud run services logs read` |
| `denied: Permission ... artifactregistry.repositories.uploadArtifacts` | Missing `roles/artifactregistry.writer` or registry auth not configured — rerun `gcloud auth configure-docker`, then check IAM |
| `403` calling the service | Service is IAM-gated (no `--allow-unauthenticated`) — invoke with an identity token (§ Verify); a caller SA needs `roles/run.invoker` |
| New revision deployed, traffic unchanged | A `--no-traffic` deploy happened before; LATEST no longer receives traffic by default — `update-traffic --to-latest` (§ Canary rollout) |
| `PERMISSION_DENIED` on a secret at start | The service's SA lacks `roles/secretmanager.secretAccessor` on that secret, or the pinned version is disabled |
| Cannot reach Cloud SQL | `--add-cloudsql-instances` missing, SA lacks `roles/cloudsql.client`, or private IP without Direct VPC egress |
| Push is very slow from Mac | QEMU emulation makes AMD64 layers slow to build; the push itself is network-bound — build once, tag once, reuse layers |
