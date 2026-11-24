# Deploy — remote machine

<!-- Agent-only brain.db record (Telegraphic/Structured Markdown; no YAML frontmatter; keep Caution before destructive commands). -->

Deploy this project to a Linux host or VM over SSH only when the operator requests it. Record host aliases here from operator input; never assume, guess, or commit credentials.

**Not applicable yet:** this project has no remote target. Delete this line and fill the sections when it gets one.

## Target

Record the remote host parameters confirmed by the operator.

| Item | Value |
|---|---|
| Host alias | |
| OS / arch | |
| Service user | |
| Install path | |

## Prerequisites on the target

<!-- Runtime, service manager (systemd unit name), firewall ports, and log location. -->

## Procedure

Build the Linux artifact locally, copy it to the target host, and swap it into place.

<!-- Container/compose or static SPA option: replace the Go scp block below with `docker compose pull && docker compose up -d` over SSH, or build (`npm run build`) and sync `dist/` assets to the target host or object store. -->

> **Caution:** Moving the binary and running `sudo systemctl restart <unit>` replaces the live executable and interrupts active connections until the restarted service passes health checks. Keep a copy of the previous binary before restarting.

```bash
GOOS=linux GOARCH=amd64 go build -o dist/server ./cmd/server
scp dist/server <host>:<install path>/server.new
ssh <host> 'mv <install path>/server.new <install path>/server && sudo systemctl restart <unit>'
```

## Verify

<!-- External health check, startup log line proving the new build serves traffic, and reported version. -->

## Dependency cadence

<!-- Run `govulncheck ./...`, `npm audit --omit=dev`, and base-image checks before every release and monthly; record the last run date. -->

## Operator steps

<!-- Standing manual procedures on this target, never pending tasks (those live in the newest handoff). -->

## Rollback

> **Caution:** Restoring the previous binary and restarting `<unit>` reverts production traffic immediately. Read all `change` records since the previous release before rolling back a security fix or schema migration.

<!-- Keep the previous artifact; list the exact commands that restore and restart it. -->

## Operate

Restore service to the previous known-good artifact first. Then inspect host logs, resource metrics, and health probes, and capture every production incident as a `BUG-*` record.

| Row | Procedure / Command |
|---|---|
| Rollback first | Restore the previous artifact (`mv <install path>/server.prev <install path>/server && sudo systemctl restart <unit>`), record the change with `fyr record add change`, then inspect logs |
| Service status & metrics | `ssh <host> 'systemctl status <unit> --no-pager'`; check host health with `ss -tlnp`, `df -h`, `free -m` |
| Logs | `ssh <host> 'journalctl -u <unit> -p err -n 100 --no-pager'` (`--since "1 hour ago"` or `-f` while triaging) |
| Uptime & alerting | External HTTP health check on `/healthz` (or systemd `OnFailure=` unit alert) and notification target |
| Dependency cadence | `govulncheck ./...` / `npm audit --omit=dev` and OS security patches before every release and monthly; last run date |
| Incidents | Open `BUG-*` (`fyr record add bug`), fix with `/fyr-bug`, and mark `--status graduated` once covered by a regression test or probe/alert |
