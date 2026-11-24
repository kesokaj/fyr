# Deploy — GCP

<!-- Track 1: human-first documentation (docs-ste100-80). No YAML frontmatter. -->

Deploy to Cloud Run first (`cloud-run-deploy` skill); use GKE or Compute Engine only when Cloud Run does not fit. GCP is the only cloud target, and nothing here runs unless the operator asks to deploy.

**Not applicable yet:** this project has no GCP target. Delete this line and fill the defaults when the operator asks for one.

## Recorded defaults

Ask the operator once per the global context, and never invent or copy defaults from another project. `AGENTS.md` points here so future sessions read this table instead of asking again.

| Setting | Value |
|---|---|
| Project ID | |
| Region | |
| Artifact Registry repository | |
| Service name | |
| Public (unauthenticated) | yes / no |
| Service account | |

## Image

Build `${REGION}-docker.pkg.dev/${PROJECT}/${REPO}/${SERVICE}:${GIT_SHA}` with `docker buildx build --platform linux/amd64 --load` and tag it with the short Git SHA only. Artifact Registry enforces immutable tags (`--immutable-tags` / `immutable_tags = true`), so no moving `latest` alias exists.

## Rollout and network

Record each rollout and network choice once with its reason. `cloud-run-deploy` reads the first five rows (its § What `DEPLOY-gcp` records) and `terraform` reads the Terraform state row; leave undecided rows empty.

| Row | Value |
|---|---|
| Rollout | canary tag (`canary`), traffic slice (`10`), watch window, promote step (`--to-latest`) |
| Ingress / IAP / VPC | `--ingress` value; `--iap` yes/no and who holds `roles/iap.httpsResourceAccessor`; `--network`/`--subnet`/`--vpc-egress` or none |
| Cloud SQL | the `PROJECT:REGION:INSTANCE` for `--add-cloudsql-instances`, or none |
| Probes and scale | startup probe path; `--min-instances`/`--max-instances`/`--concurrency` when set |
| Secrets | `--set-secrets` names and pinned versions (names, never values) |
| Terraform state | the GCS bucket (`PROJECT-tfstate` or its suffixed name) and prefix, or none when nothing is in HCL (the `terraform` skill); when the service is in HCL, Terraform owns its shape and `cloud-run-deploy` owns revisions and traffic |
| Database roles | the path (`data-gcp` skill): socket and password with the one role `app`, or IAM authentication with `<app>-run` and `<app>-migrate` users and the one-time `GRANT CREATE` by `postgres`; the date that grant ran |

## Procedure

Follow `cloud-run-deploy` with the defaults and rows above: run the local build and smoke test, build for `linux/amd64`, push, run `gcloud run deploy`, verify canary traffic when configured, and promote. Record the reason for every project-specific flag in the table above. For a standalone static SPA without a Go backend, serve `dist/` from a minimal container on Cloud Run or sync `dist/` assets to GCS behind Cloud CDN and Load Balancing.

## Secrets and configuration

<!-- Inject every secret through Secret Manager and `--set-secrets`, never `--set-env-vars`. List secret names, never values. -->

## Verify

<!-- Record `gcloud run services describe`, the health URL, and the log query proving the new revision serves traffic. -->

## Rollback

> **Caution:** Shifting 100% of Cloud Run traffic reverts live production requests immediately. Read all `change` records between the target revision and the serving revision before rolling back; a security fix, service-account change, or secret rotation requires operator sign-off, and rotated secret versions stay disabled (`operate-gcp` skill).

```bash
gcloud run services update-traffic ${SERVICE} --region ${REGION} --to-revisions <previous>=100
```

## Infrastructure

<!-- For multi-service setups: record the Terraform module path and plan/apply commands (`terraform` skill). -->

## Operator steps

Record standing manual procedures here: one-time `gcloud-setup` login and ADC, one-time database grants (`data-gcp` skill — `postgres` password and owner grant), and the monthly dependency and base-image check from § Operate. Never store pending tasks here; put those in the newest handoff's Next Steps.

## Operate

Maintain this runbook once the service is live (`operate-gcp` skill); read every value from GCP rather than guessing.

| Row | Value |
|---|---|
| Last good revision | updated by the `/fyr-preflight` deploy row after every release |
| Rollback | `gcloud run services update-traffic SERVICE --region REGION --to-revisions GOOD=100` (`cloud-run-deploy` § Rollback), then `--remove-tags canary` if set; read `change` records between GOOD and the serving revision first (operator call on security, SA, or secret changes) |
| Logs | `gcloud run services logs read SERVICE --region REGION --limit 50 --log-filter 'severity>=ERROR' --freshness 1h` |
| Uptime check | name, path (`/healthz`), or "none: IAM-gated service, alert on metrics" |
| Notification channel | `projects/P/notificationChannels/ID` (created once with `gcloud beta monitoring channels create`) |
| Alerting policies | display names (the 5xx-rate policy at least) |
| Dependency cadence | `govulncheck ./...`, `npm audit --omit=dev`, base-image check: before every release and monthly; last run date |
| Incidents | each one a `bugs/BUG-NNN` record; the prevention rule's mechanism (probe, policy, test) |
