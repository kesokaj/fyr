---
owner: main
date: 2026-10-23
id: BUG-172
status: open
area: testing kits (ab-kit, the session-sizing kit; guide § 4)
---
# BUG-172: Claude Code 2.1.309 replaces the sandbox's credential symlink with a file, so a token refresh in a sandbox run would not reach the real login

## Symptom
Every Claude Code row of the session-sizing behavioural runs (2026-10-23, `testing/2026-10-23-verify-session-sizing.md`) ended with the kit's `WARNING the credential link was replaced by a file`: `<KIT_HOME>/.claude/.credentials.json`, created as a symlink to the real `~/.claude/.credentials.json`, was a regular file when the run ended. The real file's mtime was unchanged (10:56, before every run).

## Repro
A sandbox home with the link, `claude -p "Reply with the single word ok." --output-format json` under `HOME=<sb> CLAUDE_CONFIG_DIR=<sb>/.claude` (`~/sandbox/session-sizing/runs/credtest`, 2026-10-23): after the run the path is a regular file whose token values equal the real file's (no refresh happened in that run) plus one key the real file lacks (`claudeAiOauth.subscriptionTypeSource`). The CLI writes the credential file by replacing the path, not by writing through it.

## Root cause
The committed kits (`testing/ab-kit/run.sh`, `testing/agy-rel/run.sh` for Antigravity, and `guide.md` § 4) link the credential so that a refresh "lands in the real file instead of leaving the real login with a stale refresh token" (plan lean-process-2 D5). Claude Code 2.1.309 saves the file atomically (write a temp file, rename over the path), which turns the link into a file. A refresh during a sandbox run therefore lands only in the sandbox copy, which the kit deletes by name; the real file keeps the previous refresh token. Whether that token is then invalid depends on the server's rotation, which the kit cannot see. No run here refreshed, so no login was hurt; the design's guarantee is gone either way. Not checked: whether Antigravity does the same to `antigravity-oauth-token` (`agy-rel/run.sh` prints the same WARNING when it does; none printed on 2026-10-23).

## Fix
None yet (open). Options for the operator: (a) before deleting the sandbox copy, a kit compares the token values with the real file's and, when they differ, keeps the copy and stops with its path so the operator can decide — the real file is never written by a kit; (b) a copy as guide § 4 first described it, accepting the stale-token risk and keeping runs short; (c) a Claude Code setting or credential helper that points the sandbox at the real store, if one exists for 2.1.309.

## Prevention
A kit's guarantee about a vendor's file handling is re-checked when the vendor's version changes: the kit asserts the link survived (it does) and the log records the WARNING count per run as a row, never as noise. A WARNING a kit prints on every run is a finding, not a warning.
