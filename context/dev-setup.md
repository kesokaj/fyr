## Environment & Stack

- **Cloud (GCP only):** Before any GCP deploy or image push, ask for project ID, region, Artifact Registry repo, service name, runtime service account, and public/private access; save in `DEPLOY-gcp`.
- **Backend (Go):** `cmd/<app>/` + `internal/`, `log/slog` JSON (`golang` skill).
- **Linux `sudo -n`:** Run only when asked AND `DEPLOY-local` § Operator steps grants standing permission (inspect target; log in `change`), else print command.
