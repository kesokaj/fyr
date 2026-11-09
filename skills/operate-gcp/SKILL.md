---
name: operate-gcp
description: "Operate a Cloud Run service that is already deployed: the runbook (rollback first), reading logs and Error Reporting, the run.googleapis.com metrics, one uptime check and one alerting policy, rollback outside a release, turning an incident into a bug record, the dependency and base-image cadence, cost knobs, and the background-work shapes (Jobs, Scheduler, Tasks, Pub/Sub). Use when the user says it's slow, something is down, show me the logs, what happened in prod, set up an alert or an uptime check, roll back, write the runbook, or reports an incident. Do NOT use for a release or deploy (fyr-preflight, then cloud-run-deploy), a new deploy flag (cloud-run-deploy), the database itself (data-gcp), who may call the service (api-auth-gcp), gcloud credentials (gcloud-setup), or GKE (gke-basics)."
---

# Operating a Cloud Run service

This skill decides what to do when a deployed service misbehaves and what the project keeps
in place so that moment is short: a runbook, a log query, a probe, an alert, and the habit of
turning every incident into a numbered bug. The project's values (project, region, service,
the last good revision) come from the `DEPLOY-gcp` record (`fyr record get DEPLOY-gcp`); the runbook's home is that
file's § Operate.

**When NOT to use this skill:** shipping a new revision or cutting a version (`fyr-preflight`,
which runs `cloud-run-deploy` at its deploy row); a flag on the deploy command
(`cloud-run-deploy`); the schema, the connection or a slow query (`data-gcp`); auth and
callers (`api-auth-gcp`); a defect in the code once the incident is contained (`fyr-bug`).

## Runbook — rollback first, then look

```bash
export PROJECT=… REGION=… SERVICE=…           # from DEPLOY-gcp, never guessed
# 1. what is serving, and what served before
gcloud run revisions list --service "${SERVICE}" --region "${REGION}"
gcloud run services describe "${SERVICE}" --region "${REGION}" --format 'value(status.traffic)'
# 2. stop the bleeding: 100 % to the last good revision (the full procedure: cloud-run-deploy § Rollback) — first read
#    the change records between GOOD and the serving revision: a rollback across a security fix, a service-account
#    change or a secret rotation is the operator's call, and a rotated secret version is disabled, not re-enabled
gcloud run services update-traffic "${SERVICE}" --region "${REGION}" --to-revisions "${GOOD}=100"
# 3. what the bad revision said
gcloud run services logs read "${SERVICE}" --region "${REGION}" --limit 100 \
  --log-filter 'severity>=ERROR' --freshness 1h
# 4. Error Reporting: the service's Metrics tab groups stack traces; the log line shape it needs is golang/references/service.md § Logging
# 5. the incident note, then the bug record (below)
```

Rollback is step 2, not step 5: a service that serves the last good revision buys the time the
other steps need. A rollback outside a release is recorded as a `change` record, as what it is (traffic
moved to revision X at time T, why), never re-tagged as a release; the fix ships through
`fyr-preflight` later. When a canary tag is still set, `--remove-tags canary` after the
rollback (`cloud-run-deploy` § Canary).

## Logs

| Need | Command |
|---|---|
| The last errors | `gcloud run services logs read "${SERVICE}" --region "${REGION}" --limit 50 --log-filter 'severity>=ERROR' --freshness 1h` |
| Follow live (the beta component) | `gcloud beta run services logs tail "${SERVICE}" --region "${REGION}"` |
| One request by trace | `gcloud logging read 'resource.type="cloud_run_revision" AND resource.labels.service_name="'"${SERVICE}"'" AND trace="projects/'"${PROJECT}"'/traces/'"${TRACE_ID}"'"' --limit 50 --freshness 1h` |
| 5xx in a window | `gcloud logging read 'resource.type="cloud_run_revision" AND resource.labels.service_name="'"${SERVICE}"'" AND httpRequest.status>=500' --limit 50 --freshness 15m` |
| A revision's startup | `gcloud logging read 'resource.type="cloud_run_revision" AND resource.labels.revision_name="'"${REV}"'" AND severity>=WARNING' --limit 50 --freshness 1d` |

The service writes one JSON object per line with `severity`, `message`, `time`,
`logging.googleapis.com/trace` (`projects/PROJECT/traces/ID`) and `httpRequest` — the
contract and the Go handler are `golang/references/service.md` § Logging; that is what makes
the trace query above find one request end to end. Error Reporting needs either a stack
trace in the message or a `@type` of `type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent`
with `serviceContext.service`; the same reference carries the shape. Never log a query string,
an `Authorization`, `Cookie` or `x-goog-iap-jwt-assertion` header, a body or a DSN; a log
line that leaks one is an incident of its own. An excerpt that goes into a note or a bug record
is printed with `--format 'table(severity,timestamp,httpRequest.status,jsonPayload.message)'`,
never a raw entry (`requestUrl` carries the query, `remoteIp` and `userAgent` identify a person).

## Metrics, one probe, one alert

The metrics are `run.googleapis.com/request_count` (labels `response_code_class`, `route`),
`request_latencies`, `container/instance_count` (label `state`), `container/cpu/utilizations`,
`container/memory/utilizations`, `container/startup_latencies`,
`container/billable_instance_time`, `pending_queue/pending_requests` — sampled every 60 s,
visible two to four minutes late, on the service's Metrics tab and in Metrics Explorer. One
uptime check on `/healthz` and one alerting policy on the 5xx rate are enough for a solo
operator; an SLO (99.5 % availability, a burn-rate alert) is the next step and lives in the
console or Terraform, since gcloud has no verb for it. Commands and the policy JSON:
[references/alerts.md](references/alerts.md). The notification channel is created once
(`gcloud beta monitoring channels create --type=email …`, beta only) and its id recorded in
`DEPLOY-gcp` § Operate.

## Incident → bug record

Every incident gets a number (the global rule: a failure that reached a commit, a deploy or the user — an incident always has). While the service
serves the last good revision, write the incident note — the revision, the time window, the
log query that shows it and the counts it returns (never raw entries), the user-visible effect, what was done — into
a `bug` record (`fyr record add bug`) with a prevention rule (yourself, or the `bugs` agent when you lack the evidence). The prevention
rule becomes a mechanism when it can: a startup probe path, an alerting policy on the metric
that would have fired, a test. The code fix then runs under the `fyr-bug` skill, ships through
`fyr-preflight`, and the record is closed when the fix lands.

## Dependency and base-image cadence

The project's `AGENTS.md` names the cadence; the default is **before every release and
once a month**: `govulncheck ./...` (Go, stdlib advisories included — a stdlib finding is
fixed by raising the Go toolchain, never pinned away), `npm audit --omit=dev` in `web/`,
and a check that the Docker base images are current (`golang:<major.minor>` tracks the
patch; the distroless digest is bumped deliberately, `docker` skill rule 1). A finding that
touches a shipped artifact is a `bug` with a PATCH release; one that touches only a dev
dependency is ordinary work. Record the date and the result in the `change` record of the
release or in a dated `testing/` log.

## Cost knobs

| Knob | Default | When to move it |
|---|---|---|
| `--min-instances` | 0 | 1 when a cold start (see `container/startup_latencies`) is worse than paying for one idle instance |
| `--cpu-boost` | on for new services | leave on; it shortens cold starts for little money |
| `--concurrency` | 80 | lower when p99 latency rises with load and the service is CPU-bound; raise for I/O-bound services with small instances |
| `--cpu-throttling` (request-based billing) | on | off (instance-based) only for background work inside a service, which here is a Job instead |
| `--max-instances` | 100 | set to the number the database's `max_connections` can take (`data-gcp`) |

Each change goes through `cloud-run-deploy`'s flags table and is recorded in
`DEPLOY-gcp` with its reason; `billable_instance_time` shows what it did.

## Background work

| Shape | Use for | Command |
|---|---|---|
| Cloud Run **Job** | batch, migrations, a nightly report; the same image, a subcommand | `gcloud run jobs deploy … --args <cmd>`, `gcloud run jobs execute NAME --region R --wait` |
| **Scheduler → Job** | run a job on a schedule | `gcloud scheduler jobs create http … --uri "https://run.googleapis.com/v2/projects/P/locations/R/jobs/JOB:run" --http-method POST --oauth-service-account-email SA` (an OAuth token: the target is the Cloud Run API) |
| **Scheduler → service route** | a tick the service handles | `--oidc-service-account-email SA --oidc-token-audience URL`; the SA holds `roles/run.invoker`; on a public service the handler verifies the token itself (`api-auth-gcp`) |
| **Cloud Tasks** | "do X later for this user", rate limits, dedup, scheduled delivery | `gcloud tasks create-http-task --queue Q --url URL --oidc-service-account-email SA --oidc-token-audience URL [--schedule-time T]` |
| **Pub/Sub push** | fan-out to more than one consumer | `gcloud pubsub subscriptions create SUB --topic T --ack-deadline=600 --push-endpoint=URL/events --push-auth-service-account=SA` |

Every shape uses a dedicated service account, never the compute default, and an identity
token Cloud Run verifies; the service stays IAM-gated (`api-auth-gcp` § Service callers). A
non-2xx reply means a retry: handlers for these routes are idempotent.

## When gcloud cannot run here

The runbook's commands need credentials and a project. When they are denied, or
`DEPLOY-gcp` records no defaults, print the exact commands with the recorded values
(or the placeholders named) for the operator to run, say what each shows or changes, and
stop — never guess a revision name to roll back to.
