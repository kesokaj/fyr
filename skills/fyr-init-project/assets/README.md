# <project>

<!-- Track 1: human-first documentation (docs-ste100-80). No YAML frontmatter.
     Write short active prose with a 1-3 sentence BLUF per ## section, place Note/Caution
     safety callouts before destructive commands, cap Mermaid diagrams at <= 12 nodes, and
     fold tables with > 12 rows inside <details>. Agents: read AGENTS.md. -->

<one line: what this project does and for whom — the project overview from /fyr-init-project>

> **Note:** This README is human-first documentation (Track 1, `docs-ste100-80`). Agents read `AGENTS.md` for operational commands and boundaries.

## Quick start

Run the commands below to build, start, and verify the project locally. The same commands appear in `AGENTS.md` § Setup commands, and `GUIDE-testing` (`fyr record get GUIDE-testing`) documents the full test tiers.

```bash
<build command>      # build: go build ./...
<run command>        # run: go run ./cmd/<app>
<test command>       # verify: fyr verify or go test -race ./...
```

## Architecture

<two or three active sentences: the entry point, external dependencies, and where runtime state lives; replace dense text with a <= 12-node Mermaid diagram when three or more components interact>

```
cmd/<app>/        # entry point: flags, wiring, listener
internal/         # packages, one responsibility each
web/              # frontend assets, when present
.agents/          # project rules and gitignored scratch/ (trail records live in fyr's brain store)
```

## Configuration

Set flags and environment variables below before starting the service. Runtime data paths default outside the repository, and secrets reference Secret Manager names with no code default.

<!-- Fold tables with > 12 rows inside <details>. -->

| Setting | Flag / env | Default |
|---|---|---|
| `<PORT>` | `PORT` | `8080` |

## Deploy

Follow the deploy guides for local, remote, and GCP targets (`DEPLOY-local`, `DEPLOY-remote`, `DEPLOY-gcp`). Place a `Caution` safety callout before any destructive or traffic-shifting command. Run releases through `/fyr-preflight`.

## Where things are

Use `fyr record list` to inspect project history and `AGENTS.md` for agent rules.

- Architecture decisions: `fyr record list --kind adr`
- Changes and bug preventions: `fyr record list --kind change` and `--kind bug`
- Latest session handoff: `fyr record list --kind handoff`
