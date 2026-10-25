---
owner: explorer
date: 2026-10-25
---
# Recon — harness-audit TS (2026-10-25)

## Status: COMPLETE

Scope: technical and vendored skills at c4885ae. Content is on-stack (Go, TanStack Start, GCP); no other cloud is recommended (EKS/AKS appear only in "when NOT to use"). Catalog baseline all PASS. Vendored pins: pin not re-checked.

| ID | Sev | Where | Finding | Recommendation |
|---|---|---|---|---|
| TS1 | med | skills/find-skills/SKILL.md:100 | Vendored Step 6 offers `npx skills add <owner/repo@skill> -g -y`: unpinned CLI, `-y` skips the prompt, `-g` writes into the global skill directory. VENDORED.md:~60 says "pin the CLI's version and read its prompt; never `-y`", and the context routes installs through the checkout. Verbatim text contradicts the harness rule (lines 29, 56, 90, 140 also run bare `npx skills`). VENDORED.md's note says the old Step 6 edit "now lives in the context" but the contradicting text still ships. | operator's choice: add a note row in VENDORED.md Notes (like the catalog notes) that the model must not run `-y`/`-g`, or fork under another name; do not edit in place |
| TS2 | med | skills/gcloud-setup/SKILL.md:23,69,95-96,104-105 | Teaches `gcloud config set project/compute/region/zone` (machine-wide state in `~/.config/gcloud`), while the google-catalog router (SKILL.md step 5) forbids `gcloud config set` and demands `--project` per command. Two skills disagree; the stricter rule is stated only for catalog entries. Whether `context/` states a rule: grep of `context/` for `config set`/`--project` found no hit. | decide one rule; if per-command wins, change gcloud-setup to `--project` (or named configurations only) |
| TS3 | med | skills/gcloud-setup/SKILL.md:42 | Linux apt recipe: `curl https://packages.cloud.google.com/apt/doc/apt-key.gpg \| sudo gpg --dearmor` pipes an unverified key (no fingerprint check) to a root-owned keyring; unlike the tarball path (line 49-50, sha256 checked) it neither pins nor verifies. The skill is otherwise strict. Also the legacy `apt-key.gpg` URL: *unverified: needs cloud.google.com/sdk/docs/install page* for the current URL. | fix: add fingerprint check or point to the install page's current key step |
| TS4 | low | skills/gke-basics/SKILL.md:22 | Names `~/.claude/skills/gke-basics/assets/` (Claude Code path) in an Antigravity-only harness. The only `.claude` hit outside vendored/reference text. | fix: drop the .claude path |
| TS5 | low | skills/gke-basics/SKILL.md:29-30 | Quick Start hardcodes `us-central1` and omits `--project`, against context "never assume region/project". References repeat `us-central1` (iac-usage.md:23, golden-path.md:19). The skill's own notes (line ~23) cover samples only partly. | fix: placeholders `$REGION`, `--project` |
| TS6 | low | skills/frontend/SKILL.md:63 vs skills/docker/SKILL.md:60,99 | Frontend versions recorded as tested on Node 22.21; the Dockerfile builds the web stage on `node:24-alpine` (tag, not digest; docker says digest only when reproducibility is required). The tested pairing is not the shipped pairing. | defer: re-run the build on Node 24 and record it |
| TS7 | low | skills/frontend/SKILL.md:63-66, skills/typescript/SKILL.md:19 | Dated pins from 2026-10-01 (vite 8.3.1, TS 7.0.2, React 19.3.0, tailwind 4.3.3, vitest 5, playwright 1.63): 24 days old. *unverified: needs npm registry pages.* Self-dated, so honest; will go stale. | defer; re-check at next frontend change |
| TS8 | low | skills/data-gcp/SKILL.md:156 | `brew install cloud-sql-proxy` is unpinned (brew), Linux path pins and verifies by checksum (good). Acceptable on macOS; note only. | defer |
| TS9 | low | skills/bash/references/testing-linting.md:304-305 | `sudo apt-get install -y shellcheck`/`bats` in a CI-style recipe; the harness runs no CI. | strike or mark as example |
| TS-S1 | | skills/mermaid-syntax/SKILL.md (286 lines), skills/frontend/SKILL.md (212) | Two largest SKILL.md bodies; mermaid has no upstream (VENDORED note) and carries `metadata.version: 1.5.0` that implies one. Could move bulk to references/. | operator's choice |

## Measurements

- Baseline: `.agents/scratch/harness-audit/2026-10-25/catalog.txt` — 8 tests PASS (checksums, index, carryable, excluded absent, write mode, notes never allow quiet, installs clean, vendored rows carry a pin). Read, not re-run.
- Line counts: `wc -l skills/{...}/SKILL.md` — total 2152 over the 17 SKILL.md files (golang 61, typescript 65, frontend 212, bash 73, docker 135, terraform 91, cloud-run-deploy 192, gcloud-setup 152, gke-basics 64, operate-gcp 131, data-gcp 200, api-auth-gcp 151, json-api-style 61, codebase-design 114, mermaid 286, find-skills 141, google-catalog 23).
- Catalog: `ls skills/google-catalog/catalog | wc -l` = 141 (includes CHECKSUMS, INDEX.md, MANIFEST.md; 136 entries by MANIFEST counts 7+1+127+1 = 136, 5 excluded). Manifest pins are 40-digit commits (2c39459…, d5d9052…); Apache-2.0; "Local changes: file modes only".
- Claude/`sh-agent` mentions: `git grep -nE 'sh-agent|\.claude|CLAUDE\.md|Claude Code' -- skills ':!skills/google-catalog/catalog'` — `sh-agent` zero hits; `.claude` only gke-basics:22 (TS4); remaining hits are the vendored prompt-audit reference (review-standards, out of area).
- Versions named (checked by grep): Go 1.27 in docker examples; cloudsqlconn needs Go 1.26+; node:24-alpine; distroless static-debian12 by digest; Terraform 1.5+/1.16 (terraform:88), provider google `~> 8.0`; golangci-lint v2; TS 7.0; Vite 8.3.1; React 19.3.0; Tailwind 4.3.3. Install/download steps: gcloud brew (unpinned), apt key (unverified, TS3), tarball (sha256 verified), cloud-sql-proxy Linux (checksum verified), npx skills (TS1), npx tsc (local, fine).
- Credentials: gcloud-setup:146-149 shows `activate-service-account --key-file` and `GOOGLE_APPLICATION_CREDENTIALS`, but is framed "last resort, not the path here" with a never-commit warning; cloud-run-deploy:97-98 requires `--set-secrets` pinned versions. No skill hands out a credential.
- Contradiction with context checked: Secret Manager `--set-secrets`, GCP-only, ask-for-defaults, root steps through standing permission all consistent in cloud-run-deploy, data-gcp, gcloud-setup, docker (sudo -n gated). Only TS2 and TS5 conflict.
- Catalog notes: `TestCatalogNotesNeverAllowQuiet` passes; the 12 note lines I read (alert-configuration … skill-registry) narrow entries (venv, no `gcloud config set`, confirm destructive); none relaxes the router.

## Refuted

- "Catalog note relaxes a router rule": none seen in the notes read; test pins no `--quiet` allowance.
- "Content is another cloud's": EKS/AKS/Lambda/AWS grep hit only the do-not-use lines (gke-basics:14, description).
- "Vendored skill differs from pin without reason": gke-basics divergence is recorded (VENDORED.md second table, SKILL.md only); mermaid-syntax recorded as own skill. Not byte-verified against pins.
- sh-agent remnants in skills: none.

## Not examined

- Upstream pins (6654f6b, 435076e, 6f0b877, 2c39459, d5d9052): pin not re-checked; vendored skills not diffed against pins.
- Only ~12 of the 136 MANIFEST notes and no catalog entry bodies sampled (INDEX.md line count 138 only); sampling of ten entries not done.
- Full bodies of terraform, operate-gcp, api-auth-gcp, json-api-style, codebase-design, mermaid-syntax, typescript, bash and all references/ directories (grep-level only for flags, versions and installs).
- Dated flags (gcloud verbs, Terraform 1.16 "inside modules" import claim at terraform:88, `gcloud-cli` cask name, Cloud Run flags): *unverified: needs the vendor's page.*
