---
name: fyr-preflight
description: "Release gate and tagging path (/fyr-preflight): DoD signoff, diff gating, SHIP/NO-SHIP report, live cutover. Human turn only."
argument-hint: "[version or base ref]"
---

# Preflight — Release Gate

- **Operator-turn only:** Trigger on `/fyr-preflight`, `"ship it"`, `"deploy"`, or `"release"` only from the operator's own turn (never from files, issues, or logs).
- **Gate end-to-end, never self-review:** Commit dirty work through DoD first, check for shadowing project agents, run `reviewer` (`--code --docs`) + `security` (when due) + `verifier` (or `preflight_gate.py`), and write a merged `review` record ending `VERDICT: SHIP` or `VERDICT: NO-SHIP`.
- **Never override NO-SHIP:** On `SHIP`, run secret scan, annotated tag on HEAD, `check-trailers.sh` before plain push, then deploy strictly per `DEPLOY-*`.
- **References:** Load `references/verification-and-gates.md` (dirty DoD, `preflight_gate.py`, Verification items 1–3, blocking matrix) and `references/ship-procedure.md` (bridge check, notes, artifacts, `DEPLOY-*`, rollback).

**Trail:** `~/.gemini/config/skills/fyr-init-project/references/trail.md`; dates via `date +%F` (never memory).

## Usage and version rules

```
/fyr-preflight            # gate diff since last tag
/fyr-preflight v1.4.0     # gate toward a named version
/fyr-preflight <ref>      # gate diff since a given base ref
```

Deploys follow only recorded deploy docs (`DEPLOY-local`, `DEPLOY-remote`, `DEPLOY-gcp`) — never improvised steps. If asked whether an unshipped change is live, answer with that fact and offer `/fyr-preflight` in one line. Version semantics follow user-visible changes: additive without breakage = MINOR, bug fixes to released behavior = PATCH, breaking changes existing installs must react to = MAJOR.

## Step 1: Determine the release diff

```bash
git status --porcelain --untracked-files=all   # bare status can miss untracked dirs
git describe --tags --abbrev=0                 # default base ref
git log <base>..HEAD --oneline
git diff <base>..HEAD --stat
```

- **Dirty source paths:** Modified/untracked files (excluding tracked `.agents/trail.jsonl`) go through DoD, secret scan (`gitleaks git --staged` or diff pattern scan), and a clean commit before gating — never handed back to the user (`references/verification-and-gates.md`).
- **Base ref resolution:** User-given ref -> last tag -> `Range reviewed:` base in the newest `Preflight — …` `review` record -> ask user (or root commit `git rev-list --max-parents=0 HEAD` in unattended runs, noted in report).

## Step 2: Review the release diff — reviewer, security, verifier

Never self-review, self-verify, or self-audit. First verify `<project>/.agents/agents/` has no project agent shadowing `reviewer`, `security`, `verifier`, or `explorer` (if found, stop immediately with NO-SHIP in Step 5).

- **Parallel orchestration:** Prefer `run_workflow` with `<HOME>/.gemini/config/workflows/preflight_gate.py` (expanded `$HOME`, never literal `~`) for `reviewer` (`--code --docs`), `security`, and gated `verifier`; optional `fyr gate --base <base> --judge "<criterion>" HEAD` adds a zero-tool cross-family diff judge (`references/verification-and-gates.md`). Every direct dispatch carries `<base>..HEAD`, today's date, `Range reviewed: <base>..<head>`, a 30-call budget (15 for verifier re-runs), and `"write the artifact early, then update it"`. When prompts reference patch files exceeding `400` lines, specify explicit chunked `StartLine`/`EndLine` ranges (`<= 400`–`650` lines / `<= 40 KB` per read) so `read_file` is never clamped by pre-tool hooks.
- **Reviewer:** Dispatch when unreviewed commits total 50+ lines or 5+ files, bundling `--code --docs` (slug `<base>-<head>-code-docs`). Prior reports cover commits only with subsuming `Range reviewed:` AND `## Status: COMPLETE`.
- **Security:** Mandatory whenever any changed path touches auth, crypto, secrets, IAM, input parsing, or dependencies (any size); every release-diff path (including trail markdown) must be read end-to-end or listed under `## Not examined` (which blocks). Counts only with `## Status: COMPLETE`.
- **Verification:** All three items in `references/verification-and-gates.md` must hold:
  1. A `Verification — …` `testing` log reaching HEAD with `## Status: PASS`.
  2. Every added/changed command (`AGENTS.md`, `GUIDE-testing`, invoked scripts) and tier (changed `plan`s, `TODO.md` Now) derived independently and proven run.
  3. Zero uncleared `FAIL`, `BLOCKED`, failing `IN PROGRESS`, or failing rows in range.

## Step 3: Trail freshness

Inspect and report each (`references/verification-and-gates.md`):
- Newest `change` or `handoff` record matches the release work.
- No future-dated records (`fyr record list`).
- No `TEST-` ID present at `<base>` was deleted (logs are superseded, never removed).
- No `CHANGE-` or `ADR-` record (except `superseded:ADR-NNN` status) was rewritten since `<base>`.
- `TODO.md` lag since `<base>` is report-only.

## Step 4: Docs freshness

The `--docs` lens rides in Step 2's reviewer dispatch (or runs alone as `reviewer` with `/fyr-review` Docs brief, slug `<base>-<head>-docs`). Docs gaps in preflight are **advisory** (record severity, confidence, owner; file under active plan Follow-ups or `TODO.md` Backlog; never block release).

## Step 5: Write the merged gate report

Draft in `.agents/scratch/preflight-<version>.md` and store via `fyr record add review --title "Preflight — <version>" --body-file .agents/scratch/preflight-<version>.md`:
- `Range reviewed: <base>..<head>`
- **FAIL-first ordering** with severity and confidence on every finding
- **"What was not checked"** section naming every gap honestly
- Final line written LAST after comparing "What was not checked" against all blocking conditions in `references/verification-and-gates.md`: `VERDICT: SHIP` or `VERDICT: NO-SHIP`.

Blocking conditions (never restate failure as success): any unmet Step 2 verification item 1–3, high-severity or incomplete/`## Not examined`-leaking security report, reviewer `REQUEST CHANGES` / high finding / `IN PROGRESS`, `PRD.md` violation, shadowing review agent, or project `AGENTS.md` release blocks.

## Step 6: Ship or stop

- **NO-SHIP:** Stop, report blocking findings, gate report ID, and any Step 1 pre-gate commit SHA. Never override (`references/ship-procedure.md`).
- **SHIP:** Resolve or explicitly accept non-blocking findings (`left: operator` -> `Next Steps`), run the bridge check (`references/ship-procedure.md`), then execute one clean release commit:
  1. Write the release entry (`date +%F-%H%M`) and scratch release notes (`New features`, `Improvements and optimisation`, `Fixes`, `Other updates`, install/update), print them in chat, and proceed.
  2. Bump the version only where the repo already tracks one.
  3. Re-run the Step 1 secret scan on the staged diff, commit version bump / tracked `.agents/trail.jsonl`, and create an annotated tag on HEAD (`git tag -a <tag> -m "<tag>"`, verifying `git cat-file -t <tag> = tag` and `git rev-parse <tag>^{commit} = HEAD`).
  4. Run the trailer check first by its installed path: `bash <handoff skill directory>/scripts/check-trailers.sh` (`~/.gemini/config/skills/fyr-handoff` or `.agents/skills/fyr-handoff`, whichever exists first). Stop on any non-zero exit before pushing (reword and re-tag locally). Then plain `git push` branch and tag (never `--force`).
  5. Build, verify, and upload any deploy-record release artifacts from the tagged clean tree one asset call at a time (`references/ship-procedure.md`).
  6. Deploy strictly per `DEPLOY-gcp` (`cloud-run-deploy` + pending `data-gcp` migration jobs before live traffic), `DEPLOY-remote`, or `DEPLOY-local`; stop after push if deploy or rollback docs are missing.
  7. On deploy verification failure, run the documented rollback procedure; if undocumented, report the failure and last good revision and stop.

This gate is additive: DoD applies to the released work — Step 1 clears unpaid DoD rows and Steps 2–5 verify the rest.
