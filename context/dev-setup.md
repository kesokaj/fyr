## Environment & Stack

- **Cloud (GCP only):** Ask for project ID, region, Artifact Registry, service, runtime SA, and public/private access before any GCP push/deploy; save in `DEPLOY-gcp`.
- **Backend (Go):** `cmd/<app>/` + `internal/`, `log/slog` JSON (`golang` skill).
- **Linux `sudo -n`:** Only when asked AND `DEPLOY-local` § Operator steps grants standing permission (inspect target; log in `change`), else print command.
