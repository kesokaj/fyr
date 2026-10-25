# Uptime check, notification channel, alerting policy

Three commands and one JSON file, created once per service and recorded in
the `DEPLOY-gcp` record § Operate. Flags were checked against gcloud 587
(`uptime create` and `policies create` are GA; `channels create` exists in the beta component
only, `gcloud components install beta`, or as an apt package on Linux).

## Uptime check on /healthz

```bash
URL="$(gcloud run services describe "${SERVICE}" --region "${REGION}" --format 'value(status.url)')"
HOST="${URL#https://}"
gcloud monitoring uptime create "${SERVICE}-healthz" \
  --resource-type=uptime-url --resource-labels="host=${HOST},project_id=${PROJECT}" \
  --protocol=https --path=/healthz --period=5 --timeout=10 --status-classes=2xx
```

A public service answers the probe directly. An IAM-gated service returns 403 to the probe,
which is correct and useless: probe a public `/healthz` route only when the project has one,
otherwise alert on the metrics below (the 5xx rate and `instance_count` cover a dead service).

## Notification channel (once per project)

```bash
gcloud beta monitoring channels create --type=email --display-name="operator" \
  --channel-labels="email_address=ops@example.com"
gcloud beta monitoring channels list --format 'value(name,displayName)'   # projects/P/notificationChannels/ID
```

## Alerting policy: 5xx rate

`policy.json` in the project's scratch directory, then
`gcloud monitoring policies create --policy-from-file=policy.json`. The shape is Google's
documented `conditionThreshold` form; the API validates it on create (there is no dry run),
so a rejected field is read from the error and fixed, never guessed.

```json
{
  "displayName": "SERVICE 5xx rate",
  "combiner": "OR",
  "conditions": [
    {
      "displayName": "more than five 5xx per minute for five minutes",
      "conditionThreshold": {
        "filter": "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"SERVICE\" AND metric.type=\"run.googleapis.com/request_count\" AND metric.labels.response_code_class=\"5xx\"",
        "aggregations": [
          {
            "alignmentPeriod": "60s",
            "perSeriesAligner": "ALIGN_RATE",
            "crossSeriesReducer": "REDUCE_SUM",
            "groupByFields": ["resource.labels.service_name"]
          }
        ],
        "comparison": "COMPARISON_GT",
        "thresholdValue": 0.0833,
        "duration": "300s",
        "trigger": { "count": 1 }
      }
    }
  ],
  "notificationChannels": ["projects/PROJECT/notificationChannels/CHANNEL_ID"],
  "documentation": {
    "content": "Runbook: DEPLOY-gcp § Operate — roll back first (cloud-run-deploy § Rollback), then logs read --log-filter 'severity>=ERROR'.",
    "mimeType": "text/markdown"
  }
}
```

`ALIGN_RATE` on a counter gives requests per second; 0.0833/s is five per minute. A second
policy worth having: `run.googleapis.com/container/instance_count` with
`metric.labels.state="active"` below 1 for ten minutes on a service with `--min-instances 1`
(a dead service). Replace `SERVICE`, `PROJECT` and `CHANNEL_ID` from `DEPLOY-gcp`;
the file never carries anything secret.

## What to record in `DEPLOY-gcp` § Operate

The runbook's five steps with the project's values; the uptime check name; the channel id;
the policy display names; the last good revision after every release (the `preflight` deploy
row writes it); the dependency cadence and its last run date.
