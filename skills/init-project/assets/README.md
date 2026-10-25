# <project>

<one line: what it does and for whom — the project overview the user gave /init-project>

## Quick start

```bash
<build command>      # e.g. go build ./...
<run command>        # e.g. go run ./cmd/<app>
<test command>       # e.g. go vet ./... && go test -race ./...
```

The same commands are the contract in `AGENTS.md` § Setup commands; the `GUIDE-testing` record (`fyr record get GUIDE-testing`) has the tiers behind them.

## Architecture

<two or three sentences: the entry point, what it talks to, where state lives>

```
cmd/<app>/        # the binary: flags, wiring, listener
internal/         # the packages, one responsibility each
web/              # the TanStack Start frontend, when there is one (built into the binary)
.agents/trail.jsonl  # the trail, as records: plans, changes, decisions, specs, testing, deploy guides, bugs (read with fyr record)
```

## Configuration

Flags and environment variables, with their defaults; a runtime data path (a database file, an upload directory) defaults to a location OUTSIDE the repository, never beside the source. A secret-bearing setting's row says `Secret Manager: <name>` and has no default: a credential is never a code default.

| Setting | Flag / env | Default |
|---|---|---|
| `<PORT>` | `PORT` | `8080` |

## Deploy

How it runs here, on a remote box and on GCP is the `DEPLOY-local`, `DEPLOY-remote` and `DEPLOY-gcp` records; a release goes through `/preflight`.

## Where things are

Decisions: `fyr record list --kind adr`. What changed: `--kind change`. What went wrong and the rule that came out of it: `--kind bug`. The next session starts from the newest handoff, `fyr record list --kind handoff`.
