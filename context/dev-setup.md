## Environment & Stack

- **GCP is the only cloud.** Before any GCP deploy or image push, ask for project ID, region, Artifact Registry repo, service name, runtime service account and public/private access — never assume or reuse another project's — and save them in `DEPLOY-gcp`. Never suggest another cloud.
- **Backend: Go** (`golang` skill): `cmd/<app>/` + `internal/`, `log/slog` JSON logging.
- **Root steps on Linux:** passwordless `sudo -n` runs only when asked AND `DEPLOY-local` § Operator steps grants standing permission (inspect target first; log command and verification in the `change` record); otherwise return the exact command.
