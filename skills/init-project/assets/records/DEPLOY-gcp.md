---
owner: documentation
date: YYYY-MM-DD
target: gcp
---
# Deploy — GCP

Cloud Run first (`cloud-run-deploy` skill); GKE or Compute Engine only when Cloud Run does not fit. GCP is the only cloud. Nothing here runs unless the user says to deploy.

**Not applicable yet:** this project has no GCP target. Delete this line and fill the defaults when the user asks for one.

## Recorded defaults

Asked once, per the global context, never invented or copied from another project. `AGENTS.md` points here; the next session reads this table instead of asking again.

| Setting | Value |
|---|---|
| Project ID | |
| Region | |
| Artifact Registry repository | |
| Service name | |
| Public (unauthenticated) | yes / no |
| Service account | |

## Image

`${REGION}-docker.pkg.dev/${PROJECT}/${REPO}/${SERVICE}:${GIT_SHA}` — built with `docker buildx build --platform linux/amd64 --load`, tagged with the short SHA and nothing else: the repository enforces immutable tags (`--immutable-tags` / `immutable_tags = true`), so a moving `latest` alias cannot exist.

## Rollout and network

Decided once, each with its reason; the `cloud-run-deploy` skill reads the first five rows (its § What `docs/deploy/gcp.md` records) and the `terraform` skill the last; a row not yet decided stays empty, never guessed.

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

Follow the `cloud-run-deploy` skill with the defaults and rows above: local test build and smoke run, amd64 build, push, `gcloud run deploy`, canary when the Rollout row says so, verify. The reason for every project-specific flag goes in the table above, not here.

## Secrets and configuration

<!-- Every secret via Secret Manager and `--set-secrets`, never `--set-env-vars`. List the names, not the values. -->

## Verify

<!-- `gcloud run services describe`, the health URL, the log query that shows the new revision serving. -->

## Rollback

```bash
gcloud run services update-traffic ${SERVICE} --region ${REGION} --to-revisions <previous>=100
```

Before any rollback, read the `changes/` entries between the target revision and the serving one: a security fix, a service-account change or a secret rotation in between is the operator's call, and a rotated secret version stays disabled (`operate-gcp` skill).

## Infrastructure

<!-- When it is more than one service: the Terraform module path and how to plan/apply (`terraform` skill). -->

## Operator steps

The standing by-hand procedures for this target (the one-time `gcloud-setup` login and ADC; the one-time database steps the `data-gcp` skill names — the `postgres` password, the owner's grant; the monthly dependency and base-image check from § Operate); never pending items — those live in the newest handoff's Next Steps.

## Operate

The runbook, filled once the service is live (`operate-gcp` skill); every value below is read, never guessed.

| Row | Value |
|---|---|
| Last good revision | updated by the `/preflight` deploy row after every release |
| Rollback | `gcloud run services update-traffic SERVICE --region REGION --to-revisions GOOD=100` (`cloud-run-deploy` § Rollback), then `--remove-tags canary` if set; after reading the `changes/` entries between GOOD and the serving revision (a security fix, SA change or secret rotation in between is the operator's call) |
| Logs | `gcloud run services logs read SERVICE --region REGION --limit 50 --log-filter 'severity>=ERROR' --freshness 1h` |
| Uptime check | name, path (`/healthz`), or "none: IAM-gated service, alert on metrics" |
| Notification channel | `projects/P/notificationChannels/ID` (created once with `gcloud beta monitoring channels create`) |
| Alerting policies | display names (the 5xx-rate policy at least) |
| Dependency cadence | `govulncheck ./...`, `npm audit --omit=dev`, base-image check: before every release and monthly; last run date |
| Incidents | each one a `bugs/BUG-NNN` record; the prevention rule's mechanism (probe, policy, test) |
