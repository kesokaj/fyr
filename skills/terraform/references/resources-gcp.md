# GCP resources — the standard app in HCL

Every block below is the minimal form from the `hashicorp/google` provider documentation (the `website/docs/r/<name>.html.markdown` pages, https://registry.terraform.io/providers/hashicorp/google/latest/docs), checked against provider 8.x on 2026-09-30. `var.project`, `var.region`, `var.app`, `var.repo` and `var.git_sha` are the values the project's `DEPLOY-gcp` records; `var.db_url` is `sensitive = true` and `ephemeral = true` (write-only values must not reach the plan file).

## versions.tf and the GCS backend

```hcl
terraform {
  required_version = "~> 1.16"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.0"
    }
  }
  backend "gcs" {
    bucket = "PROJECT-tfstate" # literal: backend blocks take no variables
    prefix = "APP"
  }
}

provider "google" {
  project = var.project
  region  = var.region
}
```

- The backend authenticates with ADC; `impersonate_service_account`, `access_token`, `credentials` and `kms_encryption_key` (CMEK) are the options. https://developer.hashicorp.com/terraform/language/backend/gcs
- Locking is built in and always on: the backend creates `<prefix>/<workspace>.tflock` in the same bucket under a does-not-exist precondition and deletes it with a generation match, so only the holder releases it. No lock table exists or is needed.
- The bucket is created once by hand with versioning, uniform bucket-level access and public-access prevention (the bootstrap commands in SKILL.md), as Google's state guide describes. https://docs.cloud.google.com/docs/terraform/resource-management/store-state

## APIs

```hcl
resource "google_project_service" "apis" {
  for_each           = toset(["run.googleapis.com", "artifactregistry.googleapis.com", "secretmanager.googleapis.com", "sqladmin.googleapis.com", "cloudscheduler.googleapis.com", "compute.googleapis.com", "iam.googleapis.com"])
  service            = each.value
  disable_on_destroy = false # since provider 7.0 the default is already false; set it so the intent is visible
}
```

## Runtime service account

```hcl
resource "google_service_account" "run" {
  account_id   = "${var.app}-run"
  display_name = "${var.app} Cloud Run runtime"
}
```

`google_service_account.run.member` is `serviceAccount:<email>`, the form every IAM `_member` resource takes. https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/google_service_account

## Artifact Registry

```hcl
resource "google_artifact_registry_repository" "app" {
  location               = var.region
  repository_id          = var.repo
  format                 = "DOCKER"
  cleanup_policy_dry_run = false
  docker_config {
    immutable_tags = true # Artifact Registry tags are mutable by default; the SHA tag is an audit trail only when this is set
  }
  cleanup_policies {
    id     = "delete-untagged"
    action = "DELETE"
    condition {
      tag_state = "UNTAGGED"
    }
  }
}
```

Required: `format`, `repository_id`; `location` is always set here so the image path `${var.region}-docker.pkg.dev/${var.project}/${var.repo}/…` is stable. https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/artifact_registry_repository

## Secret, write-only version, access

```hcl
resource "google_secret_manager_secret" "db_url" {
  secret_id = "${var.app}-database-url"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "db_url" {
  secret                 = google_secret_manager_secret.db_url.id
  secret_data_wo         = var.db_url # write-only: never written to state; the variable is `sensitive = true` AND `ephemeral = true`, or the plan file holds it in clear
  secret_data_wo_version = "1"        # a string, required with secret_data_wo (provider 8.0)
}

resource "google_secret_manager_secret_iam_member" "run_reads_db_url" {
  project   = google_secret_manager_secret.db_url.project
  secret_id = google_secret_manager_secret.db_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.run.member
}
```

- `secret_id` and `replication` are the only required arguments of the secret. `secret_data` (plaintext in state) and `secret_data_wo` are mutually exclusive; write-only arguments need Terraform 1.11 or later. `deletion_policy` is one of `DELETE`, `DISABLE`, `ABANDON`, `PREVENT`.
- Bump `secret_data_wo_version` to rotate: the provider writes a new version only when that string changes.
- The alternative that keeps values out of HCL entirely: create only the secret here and add versions with `gcloud secrets versions add NAME --data-file=-`; the service then pins the version NUMBER that command prints (`version = "3"`), never a floating alias.
- https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/secret_manager_secret , `…/secret_manager_secret_version` , `…/secret_manager_secret_iam`

## Cloud SQL Postgres

```hcl
resource "google_sql_database_instance" "db" {
  name                = "${var.app}-db"
  database_version    = "POSTGRES_18" # the default for new instances; POSTGRES_17 the previous line
  region              = var.region
  deletion_protection = true
  settings {
    tier    = "db-f1-micro" # ENTERPRISE tiers; ENTERPRISE_PLUS needs performance-optimized tiers
    edition = "ENTERPRISE"  # PG 16+ defaults to ENTERPRISE_PLUS without this
    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true # a destructive migration or an injection can be undone
    }
    ip_configuration {
      ipv4_enabled = true             # the /cloudsql socket works over public IP, no VPC needed
      ssl_mode     = "ENCRYPTED_ONLY" # the API default also accepts plaintext; the connector always encrypts
      # no authorized_networks: clients reach the instance through the connector or the socket only
    }
  }
}

resource "google_sql_database" "app" {
  name     = var.app
  instance = google_sql_database_instance.db.name
}

resource "google_project_iam_member" "run_sql_client" {
  project = var.project
  role    = "roles/cloudsql.client"
  member  = google_service_account.run.member
}
```

- A `google_sql_user` password lands in state: prefer IAM database authentication (`database_flags { name = "cloudsql.iam_authentication", value = "on" }`, one `google_sql_user` of `type = "CLOUD_IAM_SERVICE_ACCOUNT"` per service account — the runtime SA and the migrate SA — with no password anywhere; the grants are the `data-gcp` skill's § Roles recipe).

```hcl
# IAM database authentication: one database user per service account, no password anywhere
# (the instance needs database_flags { name = "cloudsql.iam_authentication" value = "on" }).
resource "google_sql_user" "run" {
  name     = trimsuffix(google_service_account.run.email, ".gserviceaccount.com")
  instance = google_sql_database_instance.db.name
  type     = "CLOUD_IAM_SERVICE_ACCOUNT"
}

resource "google_sql_user" "migrate" {
  name     = trimsuffix(google_service_account.migrate.email, ".gserviceaccount.com")
  instance = google_sql_database_instance.db.name
  type     = "CLOUD_IAM_SERVICE_ACCOUNT"
}

resource "google_project_iam_member" "iam_db_login" {
  for_each = { run = google_service_account.run.member, migrate = google_service_account.migrate.member }
  project  = var.project
  role     = "roles/cloudsql.instanceUser" # IAM login; roles/cloudsql.client is granted beside the service
  member   = each.value
}
```
- Private IP instead of public: `ip_configuration { ipv4_enabled = false  private_network = <vpc self_link> }` plus the service's `vpc_access` block below.
- https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/sql_database_instance ; versions: https://docs.cloud.google.com/sql/docs/postgres/db-versions

## Cloud Run v2 service

```hcl
# Only with a private-IP target: the project's own VPC and one subnet for Direct VPC egress
# (the auto-mode "default" network ships allow-internal and open SSH/RDP rules; never attach to it).
resource "google_compute_network" "app" {
  name                    = "${var.app}-vpc"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "app" {
  name          = "${var.app}-${var.region}"
  region        = var.region
  network       = google_compute_network.app.id
  ip_cidr_range = "10.10.0.0/24"
}
```

```hcl
resource "google_cloud_run_v2_service" "app" {
  name     = var.app
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL" # or INGRESS_TRAFFIC_INTERNAL_ONLY / INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER
  # deletion_protection defaults to true; set false only in a throwaway environment

  template {
    service_account = google_service_account.run.email
    scaling {
      max_instance_count = 10
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project}/${var.repo}/${var.app}:${var.git_sha}"
      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.db_url.secret_id
            version = google_secret_manager_secret_version.db_url.version # pinned to the managed version; the reference also orders the two resources
          }
        }
      }
      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.db.connection_name]
      }
    }

    # Only when a private-IP target exists (a private Cloud SQL instance, Memorystore, an internal LB):
    vpc_access {
      network_interfaces {
        network    = google_compute_network.app.name # the project's own VPC: the auto-mode "default" network carries allow-internal and open SSH/RDP rules
        subnetwork = google_compute_subnetwork.app.name
      }
      egress = "PRIVATE_RANGES_ONLY" # or ALL_TRAFFIC
    }
  }
  lifecycle {
    # cloud-run-deploy owns the revisions: the image tag of each deploy, the canary split, a rollback.
    # Terraform shapes the service and never resets those on the next apply.
    ignore_changes = [template[0].containers[0].image, traffic, client, client_version]
  }
}
```

- `volumes { cloud_sql_instance }` with the `/cloudsql` mount is the HCL form of `gcloud run deploy --add-cloudsql-instances`; the runtime SA needs `roles/cloudsql.client`. https://docs.cloud.google.com/run/docs/configuring/connect-cloudsql
- Direct VPC egress is GA and the default choice; a Serverless VPC Access connector is the exception for workloads hit by Cloud NAT cold starts. https://docs.cloud.google.com/run/docs/configuring/vpc-direct-vpc
- `launch_stage` defaults to GA; a startup probe (`startup_probe { http_get { path = "/healthz" } }`) belongs in the container block when the app has a health endpoint, the same one `cloud-run-deploy` passes with `--startup-probe`.
- https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloud_run_v2_service

## Public or IAM-gated

```hcl
# Public, Google's recommended form (no allUsers binding; blocked where the org policy
# constraints/run.managed.requireInvokerIam is enforced): a top-level argument on the service.
#   invoker_iam_disabled = true

# IAM-gated, the default (DEPLOY-gcp says Public: no): one `google_cloud_run_v2_service_iam_member`
# per caller with role = "roles/run.invoker" — the scheduler's is `scheduler_invokes` under § Job and
# Scheduler below; a second caller gets its own block with its own member.

# Public, the IAM form (blocked where constraints/iam.allowedPolicyMemberDomains is enforced):
# the same resource with member = "allUsers" — only when DEPLOY-gcp says Public: yes, and then
# every route the scheduler calls (/tick below) verifies the OIDC token itself — signature, audience, the issuer
# (accounts.google.com), AND that `email` is the scheduler SA and `email_verified` is true (idtoken.Validate alone
# accepts any SA's token for this URL and never compares the issuer).
```

`google_project_iam_member` with `roles/run.invoker` would grant every service in the project; keep project-level bindings for project-scoped roles. Never mix an authoritative `_iam_policy` with `_iam_member` on one resource. https://docs.cloud.google.com/run/docs/authenticating/public ; https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloud_run_v2_service_iam

## Job and Scheduler

```hcl
# The migration job's own identity, so IAM and logs tell it from the service. The Postgres role still comes
# from the DSN: the sample runs migrations as `app` (see the job's env below); an owner/DML split is a project decision.
resource "google_service_account" "migrate" {
  account_id   = "${var.app}-migrate"
  display_name = "${var.app} schema migrations"
}

resource "google_project_iam_member" "migrate_sql_client" {
  project = var.project
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.migrate.email}"
}

resource "google_secret_manager_secret_iam_member" "migrate_reads_db_url" {
  project   = google_secret_manager_secret.db_url.project
  secret_id = google_secret_manager_secret.db_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.migrate.email}"
}
```

```hcl
resource "google_cloud_run_v2_job" "migrate" {
  name     = "${var.app}-migrate"
  location = var.region
  template {
    template {                                               # yes, template.template
      service_account = google_service_account.migrate.email # its own SA, so IAM and logs tell the job from the service
      containers {
        image = "${var.region}-docker.pkg.dev/${var.project}/${var.repo}/${var.app}:${var.git_sha}"
        args  = ["migrate"]
        env {
          name = "DATABASE_URL" # the same DSN as the service: one role, `app`, owns the schema (the password path)
          value_source {        # (the owner/runtime split is IAM database authentication: data-gcp skill § Roles)
            secret_key_ref {
              secret  = google_secret_manager_secret.db_url.secret_id
              version = google_secret_manager_secret_version.db_url.version
            }
          }
        }
        volume_mounts {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
      }
      volumes {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [google_sql_database_instance.db.connection_name]
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].template[0].containers[0].image] # gcloud run jobs deploy owns the job's image too
  }
}

resource "google_service_account" "scheduler" {
  account_id   = "${var.app}-scheduler"
  display_name = "${var.app} Cloud Scheduler caller"
}

resource "google_cloud_run_v2_service_iam_member" "scheduler_invokes" {
  project  = google_cloud_run_v2_service.app.project
  location = google_cloud_run_v2_service.app.location
  name     = google_cloud_run_v2_service.app.name
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member
}

resource "google_cloud_scheduler_job" "tick" {
  name             = "${var.app}-tick"
  region           = var.region
  schedule         = "*/15 * * * *"
  time_zone        = "Etc/UTC"
  attempt_deadline = "320s"
  http_target {
    http_method = "POST"
    uri         = "${google_cloud_run_v2_service.app.uri}/tick"
    oidc_token {
      service_account_email = google_service_account.scheduler.email
      audience              = google_cloud_run_v2_service.app.uri # Google: the full URL of the service
      # Cloud Run checks this token only while invoker IAM is enforced. On a public service the /tick handler
      # validates it: idtoken.Validate with this audience, then the issuer (accounts.google.com, which Validate
      # never compares), then payload.Claims["email"] == this SA's email and email_verified == true — any service
      # account anywhere can mint a token for a public URL, so the audience check alone proves nothing about the caller.
    }
  }
}
```

Running the job once: `gcloud run jobs execute APP-migrate --region REGION --wait`. Job `deletion_protection` also defaults to true. https://docs.cloud.google.com/run/docs/triggering/using-scheduler ; https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloud_run_v2_job ; `…/cloud_scheduler_job`

## Worker pool

A `google_cloud_run_v2_worker_pool` is the shape for a pull-based consumer with no HTTP endpoint (a queue reader); it takes the same `template { containers {} }` and service account, no `ingress`, and provider 8.0 removed its `custom_audiences`. Reach for it only when the plan names a worker.
