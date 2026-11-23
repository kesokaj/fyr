# Preflight Ship, Tag, Artifact, and Deploy Procedure

Reference for `skills/fyr-preflight/SKILL.md` Step 6.

## NO-SHIP handling

Stop immediately, report blocking findings and the gate report record ID/path, and never override NO-SHIP. Any pre-gate Step 1 commit stays on the branch — name its SHA in the report so the operator can amend or drop it. The follow-up task resolving blocking findings appends `## Outcome` (`fixed`, `rebutted` per `/fyr-review` Step 4, or `left`, one line per finding) in the same commit as the fix.

## Pre-tag checks on SHIP

- **Non-blocking findings:** Decide every low/advisory finding explicitly in the gate report — either fix now (which invalidates prior verification and requires re-running it) or accept with reason (`left: operator` for low findings that would add new flags/refusals/operator steps, queued to handoff `Next Steps`). Never ship a known defect silently.
- **Bridge check:** If the previous release was a bridge release (per its notes, release entry, or an ADR) and installations only jump to latest, ask in the operator's own turn whether every deployment has crossed the bridge; stop before committing or tagging without a `yes`. Unattended runs never tag past a bridge.

## Release commit, tag, push, artifacts, and deploy

1. **Release entry & notes:** Store the release entry (`date +%F-%H%M`) and draft release notes in `.agents/scratch/` grouping `change`/`handoff` items since the prior tag into **New features**, **Improvements and optimisation**, **Fixes** (with `BUG-*` IDs), and **Other updates**, plus install/update instructions (omit empty groups, or follow the deploy record's format). Print the notes and file path to chat and continue straight into tag/push/upload (the ship request is approval unless project `AGENTS.md` requires a pre-deploy confirmation).
2. **Version bump:** Bump only files where the repo already tracks a version (never invent version tracking).
3. **Single release commit & annotated tag:** Stage version file and tracked `.agents/trail.jsonl` (if any), re-run the Step 1 secret scan on the staged diff (refuse on hit), commit, and tag HEAD with an annotated tag (`git tag -a <tag> -m "<tag>"`, never lightweight `git tag <tag>`). Follow any tag format, signing command, or pre-push checks in project `AGENTS.md` / deploy record. Confirm `test "$(git cat-file -t <tag>)" = tag` and `git rev-parse <tag>^{commit}` equals `git rev-parse HEAD` before pushing.
4. **Trailer check & plain push:** Run `bash <handoff skill directory>/scripts/check-trailers.sh` (`~/.gemini/config/skills/fyr-handoff` or `.agents/skills/fyr-handoff`, whichever exists first). Any non-zero exit stops before push (exit 1 lists unpushed commits carrying `Claude-Session:` or `Co-Authored-By:`; delete the local tag, reword, and re-tag). Push branch and tag without `--force`. Ask once here only if project `AGENTS.md` explicitly requires confirmation before deploy.
5. **Release artifacts:** Any check gating artifact contents runs before tagging in item 3. Build artifacts strictly from the tagged clean tree per the deploy record, run the exact consumer verification checks (checksums, allowed-signers signature), create the release, and upload each artifact in its own call (retrying individual uploads so one failure does not roll back the release). If artifact build/upload fails, stop with the tag pushed and report clearly.
6. **Deploy by deploy record only:**
   - `DEPLOY-gcp` (+ `cloud-run-deploy`): build for `linux/amd64`, push to Artifact Registry, run any pending schema migration Cloud Run Job (`data-gcp` § Migrations as a Cloud Run Job: `gcloud run jobs deploy` + `gcloud run jobs execute --wait`) before routing live traffic, deploy revision, verify live health check and logs, and report revision + URL.
   - `DEPLOY-remote`: run documented SSH update and smoke checks.
   - `DEPLOY-local`: run documented local build/run/health checks.
   - Missing deploy path or rollback procedure: stop after push and state exactly what is missing.
7. **Rollback on deploy failure:** Execute the documented rollback procedure (e.g. `cloud-run-deploy`); if none is documented, report the failed deploy and last good revision and stop — never improvise a rollback or claim shipped.
