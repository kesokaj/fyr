---
name: api-auth-gcp
description: "Who may call a Go API on Cloud Run, and how the app verifies it: Identity-Aware Proxy (`--iap`) for an internal tool, Firebase Auth for a public app, OIDC identity tokens for service-to-service, Scheduler, Tasks and Pub/Sub callers, and the one Go middleware shape for each — always through Google's vetted libraries, never a hand-rolled JWT check. Use when the user asks about auth, login for an internal tool, IAP, who can call this, verifying the caller, an ID token, a 401/403 from a Cloud Run service, or a scheduler endpoint on a public service. Do NOT use for the deploy flags themselves (cloud-run-deploy), secret handling (terraform, cloud-run-deploy), gcloud login or ADC (gcloud-setup), or GKE (gke-basics)."
---

# API auth on GCP

This skill decides one thing: how a caller proves who it is to a Go service on Cloud Run,
and what the service checks before it trusts that. The answer depends on who the caller is,
and there are only four callers here.

**When NOT to use this skill:** the `gcloud run deploy` flags and the rollout
(`cloud-run-deploy`); Secret Manager and IAM in HCL (`terraform`); `gcloud auth login`,
ADC and project configuration (`gcloud-setup`); Kubernetes (`gke-basics`).

## Choose

| Caller | Mechanism | App-side code |
|---|---|---|
| People on the team using an internal tool (a dashboard, an admin UI) | **IAP on the Cloud Run service** (`--iap`, GA, no load balancer, no charge): Google's login page in front of the service, users granted `roles/iap.httpsResourceAccessor` ON THE SERVICE | a middleware that validates `x-goog-iap-jwt-assertion` on every request (§ IAP) |
| The public, signing up to a product | **Firebase Auth** (Identity Platform only when MFA or SAML/OIDC federation is required): the SPA signs in with the client SDK and sends `Authorization: Bearer <ID token>` | `VerifyIDToken` from the Firebase Admin SDK (§ Firebase) |
| Another Cloud Run service, Cloud Scheduler, Cloud Tasks, Pub/Sub push | **OIDC identity token minted by the caller's service account**; the service stays IAM-gated (`--no-allow-unauthenticated`) and the caller's SA holds `roles/run.invoker` on it | none: Cloud Run checks the token before the request reaches the container (§ Service callers) |
| Cloud Scheduler calling a route on a PUBLIC service | the same OIDC token, but Cloud Run no longer checks it | the handler validates audience, issuer, `email` and `email_verified` itself (§ A scheduler route on a public service) |

Two rules hold for every row. **Never hand-roll JWT validation** (global Hard rules): the
checks below call `google.golang.org/api/idtoken` or `firebase.google.com/go/v4/auth` and
compare claims; parsing a token or fetching keys by hand is the thing forbidden. **Never trust
a plain header as identity**: `X-Goog-Authenticated-User-Email` is set by IAP for convenience
and Google's own docs say not to rely on it; the signed assertion is the proof.

## IAP on Cloud Run (internal tools)

```bash
# the service is IAM-gated and fronted by IAP in one deploy (the other flags: cloud-run-deploy)
gcloud run deploy "${SERVICE}" --image "${IMAGE}:${TAG}" --region "${REGION}" \
  --no-allow-unauthenticated --iap

# who may log in: granted on the SERVICE, never on the project
gcloud iap web add-iam-policy-binding --resource-type=cloud-run \
  --service="${SERVICE}" --region="${REGION}" \
  --member="user:ada@example.com" --role="roles/iap.httpsResourceAccessor"
# (a Google Group works the same: --member="group:team@example.com")

# IAP's own service agent must be allowed to invoke the service once per project
gcloud run services add-iam-policy-binding "${SERVICE}" --region "${REGION}" \
  --member="serviceAccount:service-${PROJECT_NUMBER}@gcp-sa-iap.iam.gserviceaccount.com" \
  --role="roles/run.invoker"
```

What the app checks on every request, in `<project>/internal/iapauth` (the compiling sample is
[references/go-samples.md](references/go-samples.md) § IAP middleware):

1. The header `x-goog-iap-jwt-assertion` is present; a missing header is 401 (the request
   did not come through IAP — IAP was disabled, or someone reached the URL another way).
2. `idtoken.Validate(ctx, assertion, audience)` with the audience
   `/projects/PROJECT_NUMBER/locations/REGION/services/SERVICE`, read from configuration
   and **refused when empty at startup**: `Validate` skips the `aud` check when the audience is
   the empty string, so an unset variable would accept an assertion minted for any other IAP
   resource.
3. `payload.Issuer == "https://cloud.google.com/iap"`, compared by the app: `Validate`
   checks signature, expiry and audience, not the issuer.
4. The user is `payload.Claims["email"]`; `payload.Subject` is the stable id. Log the email
   at Info if the project logs users at all; never log the assertion itself (it is a signed
   identity carrying the email: the golang skill's never-log list names it).

The SPA needs no auth code: IAP redirects to Google's login and sets the header on the way
back. A `403` after login is a missing `roles/iap.httpsResourceAccessor` binding on the
service; a `401` from the app with IAP on is a wrong audience (compare the project NUMBER,
not the project id).

## Firebase Auth (public apps)

Use when the users are not on the team: email/password, Google, phone and other
providers through the client SDK in the SPA, free to 50,000 monthly active users for the
first tier. The SPA obtains an ID token and sends it as `Authorization: Bearer <token>`; the
Go API verifies it with the Admin SDK (`firebase.google.com/go/v4`):
`auth.Client.VerifyIDToken(ctx, token)` checks signature, `aud` (the project id), `iss`
(`https://securetoken.google.com/<project id>`) and `exp`; `VerifyIDTokenAndCheckRevoked`
adds a network call and belongs on sensitive routes — every state-changing or admin route — only. The user key is `token.UID` (or custom claims); an email claim is used only when `email_verified` is true. Sample:
[references/go-samples.md](references/go-samples.md) § Firebase. Identity Platform is the
same SDKs with MFA, SAML/OIDC and multi-tenancy added; choose it only when one of those is a
requirement, since its second tier is priced per user from 50 MAU.

## Service callers (Cloud Run, Scheduler, Tasks, Pub/Sub)

The service stays `--no-allow-unauthenticated`. Each caller gets its own service account
with `roles/run.invoker` on the callee, and a token Cloud Run verifies before the container
sees the request — no app code:

```bash
gcloud run services add-iam-policy-binding "${CALLEE}" --region "${REGION}" \
  --member="serviceAccount:${CALLER_SA}" --role="roles/run.invoker"

# Scheduler → a route on the service: OIDC token, audience = the service URL
gcloud scheduler jobs create http "${APP}-tick" --location "${REGION}" --schedule "*/15 * * * *" \
  --uri "${URL}/tick" --http-method POST \
  --oidc-service-account-email "${SCHEDULER_SA}" --oidc-token-audience "${URL}"

# Scheduler → a Cloud Run JOB: an OAuth token, because the target is the Cloud Run API, not the service
gcloud scheduler jobs create http "${APP}-nightly" --location "${REGION}" --schedule "0 3 * * *" \
  --uri "https://run.googleapis.com/v2/projects/${PROJECT}/locations/${REGION}/jobs/${APP}-nightly:run" \
  --http-method POST --oauth-service-account-email "${SCHEDULER_SA}"

# Tasks → the service ("do X later for this user"); Pub/Sub push only when a second consumer exists
gcloud tasks create-http-task --queue "${QUEUE}" --location "${REGION}" --url "${URL}/work" \
  --oidc-service-account-email "${TASKS_SA}" --oidc-token-audience "${URL}"
gcloud pubsub subscriptions create "${APP}-events" --topic "${TOPIC}" --ack-deadline=600 \
  --push-endpoint="${URL}/events" --push-auth-service-account="${PUBSUB_SA}"
```

A Go service calling another: `idtoken.NewClient(ctx, audience)` returns an `*http.Client`
that attaches a fresh token for `audience` (the callee's URL) on every request; on Cloud Run
it mints the token from the instance's own service account with no key file. Sample:
[references/go-samples.md](references/go-samples.md) § Service to service. A developer's
test call is `curl -H "Authorization: Bearer $(gcloud auth print-identity-token)" "${URL}"`
(the developer needs `roles/run.invoker`; gcloud's tokens carry no audience, so this is a
test shape, never a production caller).

## A scheduler route on a public service

When `DEPLOY-gcp` says Public: yes (`--allow-unauthenticated`, or
`invoker_iam_disabled = true` in Terraform), Cloud Run checks no token, so `/tick` is callable
by anyone — and `idtoken.Validate` with the service URL as audience is NOT enough, because any
service account in any project can mint a token for that public URL. The handler checks four
things: `Validate` with the audience (the service URL), the issuer (`accounts.google.com`, with or
without the `https://` — `Validate` never compares it), then `payload.Claims["email"]` equals
the scheduler's service-account email, then `email_verified` is `true`. Sample:
[references/go-samples.md](references/go-samples.md) § Scheduler route (`tick.New` refuses an empty audience or SA at startup). The simpler
design, when the project allows it, is an IAM-gated service with no public routes at all.

## Never

- No hand-rolled JWT parsing, no key fetching by hand, no `jwt.Parse` from a third-party
  library when `idtoken` or the Admin SDK does the job (the Hard rules; stdlib first does
  not apply to a protocol check).
- No trusting `X-Goog-Authenticated-User-Email` or any email header alone: a public service
  verifies a SIGNATURE (`idtoken.Validate`, or the Admin SDK's `VerifyIDToken`), never a plain header.
- No `--allow-unauthenticated` on an API whose only gate is a header check; § A scheduler route
  above is the one public shape without an end-user login here (a public app's login is the Firebase
  row), and it holds only with all four checks (audience, issuer,
  `email`, `email_verified`).
- No empty audience in configuration: refuse to start.
- No service-account key files: Cloud Run mints tokens from the instance identity, and local
  development uses `gcloud auth application-default login` (`gcloud-setup`).

## When gcloud cannot run here

The commands above need a project and credentials. When they are denied or no project is
recorded in `DEPLOY-gcp`, write the exact commands with the project's values filled
in for the operator to run, say which binding each one creates, and stop: never guess a
project number or an audience.
