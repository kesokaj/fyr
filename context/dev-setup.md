## Environment & Stack

- **Local by default.** Build, run and test locally per the project's `DEPLOY-local` record; touch cloud only when asked.
- **GCP is the only cloud.** Before any GCP deploy or image push, ask for project ID, region, Artifact Registry repo, service name, runtime service account and public/private access — never assume or reuse another project's — and save them in `DEPLOY-gcp`. Never suggest another cloud.
- **Backend: Go** (`golang` skill): `cmd/<app>/` + `internal/`, `log/slog` JSON logging.
- **Frontend: TanStack Start + TypeScript + Tailwind** in one `web/` or `ui/` directory (`typescript` skill; `frontend-ui-modern` for shadcn/ui app UI; `frontend-ui-economist` for plain HTML/CSS editorial/report UI). Client JS runs on plain V8: no Node APIs outside server entry.
- **Machines:** Docker via Colima on macOS (Apple Silicon), plain Docker on Linux. Use remote hosts/VMs only when asked; ask for access.
- **Root steps on Linux:** passwordless `sudo -n` runs only when asked AND `DEPLOY-local` § Operator steps grants standing permission (inspect target first; log command and verification in the `change` record); otherwise return the exact command.
- **Build and deploy mechanics** (local test first, amd64 images, git-SHA Artifact Registry tags): `docker` and `cloud-run-deploy`.
- **Compute:** Cloud Run first (`cloud-run-deploy`), else GKE (`gke-basics`) or Compute Engine; Terraform (`terraform`) when managing >1 service.
- **Auth:** `gcloud auth login` and `gcloud auth application-default login` (`gcloud-setup`). Pass Cloud Run secrets via `--set-secrets`, never `--set-env-vars`.
