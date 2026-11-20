# Deploy — remote machine

A Linux box or VM reached over SSH, used only on the user's explicit request. Access details (host, user, key) come from the user and are never assumed, guessed, or committed; record aliases here, never credentials.

**Not applicable yet:** this project has no remote target. Delete this line and fill the sections when it gets one.

## Target

| Item | Value |
|---|---|
| Host alias | |
| OS / arch | |
| Service user | |
| Install path | |

## Prerequisites on the target

<!-- Runtime, service manager (systemd unit name), firewall ports, log location. -->

## Procedure

<!-- Build locally for the target's architecture, copy the artifact, install, restart, in the exact commands. -->
<!-- Container/compose or static SPA option: replace the Go scp block below with `docker compose pull && docker compose up -d` over SSH, or build (`npm run build`) and sync `dist/` assets to the target host/object store. -->

```bash
GOOS=linux GOARCH=amd64 go build -o dist/server ./cmd/server
scp dist/server <host>:<install path>/server.new
ssh <host> 'mv <install path>/server.new <install path>/server && sudo systemctl restart <unit>'
```

## Verify

<!-- Health check from outside, the log line that proves the new build is serving, the version it reports. -->

## Dependency cadence

<!-- `govulncheck ./...`, `npm audit --omit=dev`, and the base image when there is one: before every release and monthly; the last run's date. -->

## Operator steps

<!-- What the operator does by hand on this target: standing procedures, never pending items (those live in the newest handoff). -->

## Rollback

<!-- Keep the previous artifact; the exact commands that put it back. -->

## Operate

Restore service to the previous known-good artifact first, then inspect host logs, resource metrics, and health probes, and capture every production incident as a `BUG-*` record.

| Row | Procedure / Command |
|---|---|
| Rollback first | Restore the previous artifact/unit (`mv <install path>/server.prev <install path>/server && sudo systemctl restart <unit>`), record the change via `fyr record add change`, then inspect logs |
| Service status & metrics | `ssh <host> 'systemctl status <unit> --no-pager'`; host health via `ss -tlnp`, `df -h`, `free -m` |
| Logs | `ssh <host> 'journalctl -u <unit> -p err -n 100 --no-pager'` (`--since "1 hour ago"` or `-f` while triaging) |
| Uptime & alerting | External HTTP health check on `/healthz` (or systemd `OnFailure=` unit alert) + notification target |
| Dependency cadence | `govulncheck ./...` / `npm audit --omit=dev` and OS security patches before every release and monthly; last run date |
| Incidents | Open `BUG-*` (`fyr record add bug`), fix via `/fyr-bug`, and mark `--status graduated` once covered by a regression test or probe/alert |
