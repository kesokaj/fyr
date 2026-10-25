---
name: terraform
description: "Terraform (or OpenTofu) for Google Cloud, applied from the operator's machine with no CI: Cloud Run v2 behind a dedicated service account, Artifact Registry, Secret Manager, Cloud SQL Postgres, Cloud Run jobs and Cloud Scheduler, state in a GCS bucket. Use when writing, reviewing or planning HCL for GCP, choosing a resource or an IAM binding, debugging state or a plan diff, or deciding what belongs in Terraform and what stays with gcloud. Do NOT use for building or pushing the image (use docker), a gcloud deploy, new revision or rollback (use cloud-run-deploy), GKE clusters or Kubernetes objects (use gke-basics), or gcloud login and ADC problems (use gcloud-setup)."
---

# Terraform for Google Cloud

Infrastructure as code for the one cloud this stack uses, written and applied by the operator from their own machine. Every HCL block in this skill and its references comes from the `hashicorp/google` provider's own documentation page, cited under it; nothing here depends on a pipeline, a pre-commit hook or an MCP server.

**When NOT to use this skill:** the container image, its build and its push (`docker`); `gcloud run deploy`, traffic, a rollback (`cloud-run-deploy`); GKE clusters and Kubernetes manifests (`gke-basics`); `gcloud auth`, ADC and project configuration (`gcloud-setup`).

## When Terraform, when gcloud

- Terraform when the deployment is more than one service, or when a resource must outlive any one deploy: the service account, the Artifact Registry repository, the secrets, the database, the scheduler. One root per app under `infra/`.
- The image, the new revision and the rollback stay with `cloud-run-deploy`: Terraform sets the image once, on the first apply, from `var.git_sha` (the git short SHA that skill produces, never computed in HCL); from then on the service's and the job's `lifecycle { ignore_changes }` leave image and traffic to gcloud, so a later `terraform apply` never resets a deploy, a canary or a rollback. Terraform owns the shape (service account, secrets, scaling, ingress, the job's definition); gcloud owns the revisions.
- `terraform plan` before every `apply`, and nothing is applied to Google unless the user asked for it in their own turn (`/preflight` is the deploy path; this skill is the HCL under it).
- The project's recorded defaults (project ID, region, repository, service name, service account, public or not) live in the project's `DEPLOY-gcp` record (`fyr record get DEPLOY-gcp`); the state bucket's name is recorded there too.

## Layout

```
infra/
├── versions.tf          # required_version, required_providers, backend "gcs"
├── main.tf              # resources, in dependency order
├── variables.tf         # project, region, app, repo, git_sha, secrets as sensitive
├── outputs.tf           # service URI, SA email, repository name
├── terraform.tfvars     # gitignored: a secret-shaped path (*.tfvars) stops a commit; secret VALUES never go here either — `TF_VAR_app_secret` from Secret Manager or a prompt at apply time
└── .terraform.lock.hcl  # committed, locked for both platforms (below)
```

`.terraform/`, `*.tfstate*`, `*.tfvars` and the plan file are ignored; the lock file is not. The plan file lives in `.agents/scratch/` because it holds every value, secrets included, in clear.

## Bootstrap, once per project

1. Enable the APIs the root uses, in HCL, so the dependency is recorded: `google_project_service` for `run.googleapis.com`, `artifactregistry.googleapis.com`, `secretmanager.googleapis.com`, `sqladmin.googleapis.com`, `cloudscheduler.googleapis.com`, `iam.googleapis.com` (`disable_on_destroy = false`; see [upgrades](references/upgrades.md)).
2. Create the state bucket by hand, since the backend needs it before the first `init` (chicken and egg):
   ```bash
   gcloud storage buckets create gs://PROJECT-tfstate --location=REGION --uniform-bucket-level-access --public-access-prevention
   gcloud storage buckets update gs://PROJECT-tfstate --versioning
   ```
   The name is a global namespace: a create that fails with "already exists" is a STOP, never a reuse — the existing bucket may be someone else's (then choose `PROJECT-tfstate-<random suffix>`). Locking needs nothing else: the GCS backend writes `<prefix>/<workspace>.tflock` into the same bucket and only the holder can delete it.
3. `terraform init`, then `terraform providers lock -platform=linux_amd64 -platform=darwin_arm64` so the lock file covers the Linux box and the Apple Silicon Mac; rerun it after any provider bump and commit the result.

## The standard app

The full blocks, each with its documentation URL, are in [references/resources-gcp.md](references/resources-gcp.md); a complete small root is in [assets/example/](assets/example/main.tf). The order is the dependency order:

1. **Runtime service account** (`google_service_account`), one per app; its `member` attribute is what IAM bindings take.
2. **Artifact Registry** repository (`format = "DOCKER"`) with a cleanup policy deleting untagged images.
3. **Secret** (`google_secret_manager_secret`, `replication { auto {} }`) with a write-only version (`secret_data_wo` plus `secret_data_wo_version`, provider 8.x) so the value never lands in state, and `roles/secretmanager.secretAccessor` for the runtime SA on that one secret.
4. **Cloud SQL Postgres** (`POSTGRES_18`, `edition = "ENTERPRISE"`), `roles/cloudsql.client` for the runtime SA at project level (the role is project-scoped).
5. **Cloud Run v2 service** with the SA, the secret as an env `value_source`, the `/cloudsql` volume, and `vpc_access` with Direct VPC egress only when a private-IP target exists.
6. **Invocation**: public via `invoker_iam_disabled = true` on the service (Google's recommended form), or IAM-gated via `google_cloud_run_v2_service_iam_member` for the one caller. `gcloud run deploy --allow-unauthenticated` reaches the same end by a different mechanism (an `allUsers` invoker binding); when the service is in HCL, Terraform owns the setting and `cloud-run-deploy` passes neither flag.
7. **Migration job** (`google_cloud_run_v2_job`, `template { template { … } }`) running the same image with `args = ["migrate"]`.
8. **Scheduler** (`google_cloud_scheduler_job`) calling the service with an `oidc_token` from its own SA, which holds `roles/run.invoker` on the service.

## Rules

- `hashicorp/google` pinned `~> 8.0`; `google-beta` only when a Preview feature is named in the plan, since every GA resource is in `google`.
- `deletion_protection` stays at its default (`true` on Cloud Run services and jobs and on Cloud SQL) in anything shared; `false` only in a throwaway environment, and said so in the plan.
- A secret never appears in `secret_data`, in `--set-env-vars`, in a `google_sql_user` password, or in state: write-only versions, or create the secret in HCL and add versions with `gcloud secrets versions add`.
- IAM by `_member` resources, never `_policy` (authoritative; wipes bindings made elsewhere) and never both on one resource. A project-level binding only for a project-scoped role such as `roles/cloudsql.client`; `roles/run.invoker` at project level would open every service.
- Direct VPC egress (`vpc_access { network_interfaces {} }`) is the default when a private-IP target exists; a Serverless VPC Access connector only for the Cloud NAT cold-start case; no `vpc_access` at all without a private target.
- Block order inside a resource: `count` or `for_each` first, then arguments, then `labels`, then `depends_on`, then `lifecycle`. `for_each` for a named set, `count` for a boolean toggle, never both on one resource (Terraform rejects it).
- Backend blocks take literal values only; everything else comes through `variables.tf`.
- Stdlib first applies here too: no community modules until the plain resources have been written once and found repetitive.

## Verify before apply

Run each bare and read its exit status (the global rule: never inside a chain that leads to a commit); the exact commands, exit codes and the `.tflint.hcl` are in [references/checks.md](references/checks.md):

1. `terraform fmt -check -recursive`
2. `terraform validate` (after `init`; `-backend=false` works offline)
3. `terraform plan -detailed-exitcode -out=.agents/scratch/tfplan` — 0 nothing to do, 1 error, 2 a diff to read
4. `tflint --init && tflint` with the google ruleset
5. `trivy config --exit-code 1 .` (without `--exit-code 1` trivy exits 0 whatever it finds, and the status read below means nothing)
6. `terraform apply .agents/scratch/tfplan`, then the project's smoke check from `deploy/gcp.md`

The results go into the project's `testing/` log as the Definition of Done asks; a `plan` that exits 2 is read in full before the apply, and a diff the plan did not predict stops the run.

## Auth

`gcloud auth application-default login` is the credential: both the GCS backend and the provider read Application Default Credentials, so one login covers state and apply. `--impersonate-service-account` (or `impersonate_service_account` in the backend) is the step up once more than one human applies: a dedicated `terraform` SA narrows the operator's live roles and unifies the audit trail. Workload Identity Federation is for workloads outside Google Cloud and for CI; with neither there is nothing to federate. Service account keys are never created. A failed login, a missing ADC or a 403 is the `gcloud-setup` skill's problem.

## Debugging state

- **Stuck lock**: the lock object is `<prefix>/<workspace>.tflock` in the state bucket. Confirm no run holds it (`gcloud storage ls gs://BUCKET/PREFIX/` and the operator's own terminals) before `terraform force-unlock <ID>`.
- **Drift**: `terraform plan` shows it; a resource created outside Terraform is adopted with an `import` block (Terraform 1.5+, inside modules since 1.16) and a plan that then shows no change.
- **Rename**: a `moved` block, never destroy and recreate.
- **Leave a resource in place while dropping it from HCL**: a `removed` block, or `lifecycle { destroy = false }` (1.16).
- **Provider major upgrade**: the checklist in [references/upgrades.md](references/upgrades.md); never bump the pin and apply in one step.
