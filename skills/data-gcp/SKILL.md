---
name: data-gcp
description: "Cloud SQL PostgreSQL for a Go service on Cloud Run: create the instance cost-consciously (Enterprise edition, the smallest tier, PITR backups), connect with pgx over the /cloudsql socket first or the Cloud SQL Go connector with IAM authentication, run schema migrations as a Cloud Run Job with golang-migrate, decide the owner/runtime role split, connect locally through the Auth Proxy, and check indexes and constraints before shipping a schema. Use for any database, Postgres, Cloud SQL, migration, schema, connection string or connection-pool question. Do NOT use for the HCL that creates the instance (terraform), the service's code shape (golang), the deploy or its flags (cloud-run-deploy), or a local Postgres in compose (docker)."
---

# Data on GCP: Cloud SQL Postgres for a Go service

This skill decides how the one database of a Cloud Run service is created, reached,
migrated and kept correct. The project's defaults (project, region, instance name) come
from the `DEPLOY-gcp` record (`fyr record get DEPLOY-gcp`); this skill never invents them.

**When NOT to use this skill:** the `google_sql_database_instance` and secret HCL
(`terraform`); the HTTP service and its pgx code shape (`golang`, whose
`golang/references/service.md` § Database holds the pool sample); the deploy and
`--add-cloudsql-instances` (`cloud-run-deploy`); Postgres in `docker compose` for local work
(`docker`); IAM for who may CALL the service (`api-auth-gcp`).

## Instance

```bash
gcloud sql instances create "${APP}-db" --region "${REGION}" \
  --database-version=POSTGRES_18 \
  --edition=enterprise --tier=db-f1-micro --availability-type=zonal \
  --storage-size=10 --storage-auto-increase \
  --enable-point-in-time-recovery --backup-start-time=03:00 --retained-backups-count=7 \
  --ssl-mode=ENCRYPTED_ONLY \
  --database-flags=cloudsql.iam_authentication=on \
  --deletion-protection
gcloud sql databases create app --instance "${APP}-db"
```

- **`--edition=enterprise`, always written out.** For PostgreSQL 16 and later the default
  edition is Enterprise Plus, which has no shared-core tier: without the flag the smallest
  instance costs several times more. `db-f1-micro` and `db-g1-small` are shared-core, carry no
  SLA, and are the right size until measured load says otherwise; a dedicated core on
  Enterprise uses `--cpu`/`--memory` and drops `--tier`. HA (`--availability-type=regional`)
  doubles the price: ask before it.
- **Backups and PITR on** from day one: a destructive migration or an injection can be undone
  only with them. `--ssl-mode=ENCRYPTED_ONLY`: the API default also accepts plaintext.
- **No authorized networks, no public clients.** The service reaches the instance through the
  `/cloudsql` socket or the connector; people reach it through the Auth Proxy (§ Local).
  Private IP (`--no-assign-ip --network`) plus Direct VPC egress on the service is the
  upgrade when the project needs it, not the default.
- The HCL form of all of this is the `terraform` skill (`terraform/references/resources-gcp.md` § Cloud SQL); when the
  instance is in Terraform, Terraform owns it and these commands are not run.

## Connect — the path, in order

1. **The `/cloudsql` Unix socket**, the default: `gcloud run deploy --add-cloudsql-instances
   PROJECT:REGION:INSTANCE` mounts `/cloudsql/PROJECT:REGION:INSTANCE`; the service's
   service account holds `roles/cloudsql.client`; the DSN is
   `host=/cloudsql/PROJECT:REGION:INSTANCE user=app dbname=app password=…` with the
   password from Secret Manager (`--set-secrets`, a pinned version; never `--set-env-vars`).
   `pgxpool` over it: `golang/references/service.md` § Database. No connector library, no
   certificates, encrypted by the platform. ONE Postgres role on this path: `app` owns the
   schema and runs the migrations, so it has DDL at runtime — the trade-off of the simplest
   path, stated in `deploy/gcp.md`; the role split is path 2. Creating the role and its
   secret without the password ever touching an argument list, a transcript or source — two
   operator steps, since both prompt:
   ```bash
   gcloud sql users create app --instance "${APP}-db"                                 # a Cloud SQL (non-IAM) user: Google puts it in cloudsqlsuperuser
   gcloud sql users set-password app --instance "${APP}-db" --prompt-for-password     # typed at the prompt, never --password=
   gcloud secrets create "${APP}-db-url" --replication-policy=automatic
   read -rs DSN && printf '%s' "$DSN" | gcloud secrets versions add "${APP}-db-url" --data-file=-   # -s: no echo; pin the version it prints
   gcloud secrets add-iam-policy-binding "${APP}-db-url" --member "serviceAccount:${RUN_SA}"     --role roles/secretmanager.secretAccessor
   gcloud secrets add-iam-policy-binding "${APP}-db-url" --member "serviceAccount:${MIGRATE_SA}" --role roles/secretmanager.secretAccessor   # the job reads the same DSN on this path
   ```
2. **The Cloud SQL Go connector** (`cloud.google.com/go/cloudsqlconn`, Go 1.26 or later)
   when the project wants **IAM database authentication** (no password at all: the service
   account IS the database user) or a private-IP instance reached over Direct VPC egress:
   `cloudsqlconn.NewDialer(ctx, cloudsqlconn.WithIAMAuthN(), cloudsqlconn.WithLazyRefresh())`
   as the pool's `DialFunc`; `WithLazyRefresh` refreshes certificates on demand, the right mode
   for Cloud Run's scale-to-zero. Compiling sample: [references/connect.md](references/connect.md)
   § Connector with IAM authentication. The IAM user:
   ```bash
   gcloud sql users create "${APP}-run@${PROJECT}.iam"     --instance "${APP}-db" --type=CLOUD_IAM_SERVICE_ACCOUNT
   gcloud sql users create "${APP}-migrate@${PROJECT}.iam" --instance "${APP}-db" --type=CLOUD_IAM_SERVICE_ACCOUNT
   # the username is the SA email WITHOUT the .gserviceaccount.com suffix; each SA also needs
   # roles/cloudsql.instanceUser and roles/cloudsql.client. Google: an IAM user "is granted no privileges on
   # any databases, by default" and is NOT in cloudsqlsuperuser (a non-IAM user created through Cloud SQL is);
   # the migrate SA is named ${APP}-migrate so ${MIGRATE_SA} and the SQL in § Roles agree
   ```
3. **Never**: a public IP with authorized networks, a password in `--set-env-vars`, a DSN in
   source or in a log line (the DSN carries the password: log the host and database name only),
   `sslmode=disable` anywhere but the local compose database.

## Migrations as a Cloud Run Job

`github.com/golang-migrate/migrate/v4` with `source/iofs` over `//go:embed migrations/*.sql`
and the `database/pgx/v5` driver, exposed as the service binary's `migrate` subcommand
(`golang/references/service.md` § Database item 3), run as a Job from `/preflight` BEFORE the
new revision receives traffic, never on instance start (every cold start would race every
other):

```bash
gcloud run jobs deploy "${APP}-migrate" --image "${IMAGE}:${TAG}" --region "${REGION}" \
  --args migrate --service-account "${MIGRATE_SA}" \
  --set-cloudsql-instances "${PROJECT}:${REGION}:${APP}-db" \
  --set-secrets "DATABASE_URL=${APP}-db-url:${VERSION}" \
  --max-retries 0 --task-timeout 10m
gcloud run jobs execute "${APP}-migrate" --region "${REGION}" --wait   # exit status is the gate
```

That block is the socket-and-password path. On the IAM-authentication path (§ Roles) the job
reaches the instance through the connector, so `--set-cloudsql-instances` and `--set-secrets` go
and `--set-env-vars "DATABASE_URL=user=${APP}-migrate@${PROJECT}.iam dbname=app"` carries the
password-less DSN.

**Roles.** The job runs under its own service account (`${MIGRATE_SA}`) so IAM and logs tell
it from the service. The Postgres ROLE, however, comes from the DSN, not the service account:
on the socket-and-password path (§ Connect item 1) there is one role, `app`, a `cloudsqlsuperuser`
member with DDL at runtime — and `cloudsqlsuperuser` carries CREATEROLE and CREATEDB, so a SQL injection on this path can create roles and databases, not only tables; the IAM path is the least-privilege one. The owner/runtime split is path 2, **IAM database authentication**,
where each service account IS a database user with no privileges at all until granted
(Google's documented default); the service and the job carry no password at all (the built-in `postgres` user keeps one, for the one-time step below, § Local). The job authenticates as the
migrate user through the connector, never the socket: open the pool as in
[references/connect.md](references/connect.md) § Connector with IAM authentication, then hand
`golang-migrate` the `*sql.DB` it insists on — `db := stdlib.OpenDBFromPool(pool.Pool)`
(`github.com/jackc/pgx/v5/stdlib`), `driver, _ := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})`
(`github.com/golang-migrate/migrate/v4/database/pgx/v5`), `migrate.NewWithInstance("iofs", src, "postgres", driver)`.
Its `DATABASE_URL` is `user=<app>-migrate@<project>.iam dbname=app` — no host and no password, so a
plain env var. Three steps:

```sql
-- 1. once, as the built-in `postgres` user through the Auth Proxy (an operator step; § Local shows the login):
--    the migrate user may create objects in public; `public` keeps its owner, so step 3 still measures something
GRANT CREATE ON SCHEMA public TO "<app>-migrate@<project>.iam";
```
```sql
-- 2. migrations/000001_roles.up.sql, run by the job as the migrate user: the runtime user gets DML on what the
--    migrate user creates, nothing more (USAGE on public is PUBLIC's already)
ALTER DEFAULT PRIVILEGES FOR ROLE "<app>-migrate@<project>.iam" IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "<app>-run@<project>.iam";
ALTER DEFAULT PRIVILEGES FOR ROLE "<app>-migrate@<project>.iam" IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO "<app>-run@<project>.iam";
```
```sql
-- 3. migrations/000002_assert_roles.up.sql: fails closed if the runtime user holds CREATE on public at that point
DO $$ BEGIN
  IF has_schema_privilege('<app>-run@<project>.iam', 'public', 'CREATE') THEN
    RAISE EXCEPTION 'runtime role has CREATE on schema public: the role split is broken';
  END IF;
END $$;
```

No `REVOKE cloudsqlsuperuser` and no ownership transfer: IAM users were never members, since
PostgreSQL 16 a fellow member cannot revoke it anyway, and `GRANT <role> TO postgres` needs an
ADMIN OPTION Cloud SQL's `postgres` may not hold (both measured on 18: ERROR). No password in
source, in a secret or in an argument: the files name roles only. Each service account connects as its own user through the connector (§ Connect item 2). Down
migrations in production run only on the operator's word in their own turn. `CREATE INDEX
CONCURRENTLY` cannot run inside a transaction: it gets its own migration file with the
driver's no-transaction marker, and never sits beside other statements.

## Local

```bash
brew install cloud-sql-proxy                       # macOS; unpinned: brew installs the formula's current version (2.26.3 on 2026-10-25)
cloud-sql-proxy --version                          # read the version brew gave you; Linux: the release binary with its checksum, see below
cloud-sql-proxy --auto-iam-authn --port 5432 "${PROJECT}:${REGION}:${APP}-db"
# the one-time grant (§ Roles step 1) and a person's SELECT grant run as the built-in `postgres` user instead:
#   gcloud sql users set-password postgres --instance "${APP}-db" --prompt-for-password   # once, typed at the prompt
#   cloud-sql-proxy --port 5432 "${PROJECT}:${REGION}:${APP}-db"                      # no --auto-iam-authn for this session
#   psql "host=127.0.0.1 port=5432 user=postgres dbname=app"   # then: GRANT SELECT ON ALL TABLES IN SCHEMA public TO "<person>"; (postgres, as cloudsqlsuperuser, may grant on the migrate user's tables)
# once per person: the database user is the person's FULL email (only service accounts drop .gserviceaccount.com);
# the person also needs roles/cloudsql.instanceUser and roles/cloudsql.client, and a GRANT by the schema owner (issued as `postgres` with `SET ROLE` to the migrate user, below) — SELECT for reading production data, never more
gcloud sql users create "${USER_EMAIL}" --instance "${APP}-db" --type=CLOUD_IAM_USER
psql "host=127.0.0.1 port=5432 user=${USER_EMAIL} dbname=app"   # --auto-iam-authn supplies the token; no PGPASSWORD
```

Linux: download `cloud-sql-proxy.linux.amd64` for the release from
`https://github.com/GoogleCloudPlatform/cloud-sql-proxy/releases`, verify it against the SHA-256
table in that release's notes (`sha256sum -c -` with the line pasted on stdin, its exit status read bare), put it under `~/.local/bin` (never `gcloud components`: the Auth Proxy is
not a component). The proxy uses the developer's ADC (`gcloud-setup`); nothing else reaches
the instance from a laptop. For day-to-day development the compose database (`docker` skill)
is faster; the proxy is for reading production data or a one-off migration check.

## Schema checklist, before a migration ships

- Every foreign-key column has an index (Postgres creates none).
- `NOT NULL`, `CHECK`, `UNIQUE` and foreign keys live in the schema, not in the application:
  the database is the last line, the Go validation the first.
- `EXPLAIN (ANALYZE, BUFFERS)` on the real query before adding an index; an index that no
  query uses costs every write.
- `timestamptz`, never `timestamp`; `text` with a `CHECK` over `varchar(n)`; `bigint`
  identity columns; a `created_at timestamptz NOT NULL DEFAULT now()` on every table.
- A migration is forward-only once applied in production: the next migration fixes it.
- The migration job's `--wait` exit status is read bare; a failed migration is a NO-SHIP
  for the revision that needs it (`preflight`).

## Operations the project records

The `DEPLOY-gcp` record holds the instance's `PROJECT:REGION:INSTANCE`, the edition
and tier with the reason, whether IAM authN is on, the backup window, and the role model
(one role, or owner + runtime). Rollback of a bad migration is the `operate-gcp` runbook
(restore from PITR to a new instance, then point the service at it); `data-gcp` only says
that the backups it created make that possible.

## When gcloud cannot run here

When the commands are denied or no project is recorded, print them with the project's values
filled in for the operator, say what each creates and what it costs per month at the chosen
tier, and stop; never run `instances create` on a guessed project.
