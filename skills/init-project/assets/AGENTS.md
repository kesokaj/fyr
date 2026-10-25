# AGENTS.md

## Project Overview

<!-- Brief description: what this project does, who it serves, and why it exists. -->

## Setup Commands

<!-- The few commands a contributor runs before claiming something works — build, run, test.
     Keep it to those: coverage runs, e2e scripts and container smoke tests belong in
     .agents/docs/testing/guide.md, which this section points at once it exists. -->

```bash
# Install dependencies
go mod download

# Build
go build ./cmd/...

# Run
go run ./cmd/server/

# Test
go test ./...

# Lint
go vet ./...
```

## Architecture

<!-- Project-specific architecture notes. -->
<!-- For ADRs, see .agents/docs/architecture/DECISIONS.md -->

## Project-Specific Standards

<!-- Only list standards that DIFFER from the global context (~/.gemini/config/rules/AGENTS.md). -->
<!-- If nothing differs, delete this section. -->

## Testing

<!-- Project-specific testing instructions: the verification tiers the plan names and the commands behind them; the standing procedure is .agents/docs/testing/guide.md. -->
<!-- For detailed guides, see .agents/docs/testing/ -->

## Deploy

<!-- One line per target that exists. The procedures live in .agents/docs/deploy/ (local.md, remote.md, gcp.md); GCP defaults are recorded in gcp.md, never invented. -->

Local only — see `.agents/docs/deploy/local.md`.

## Key Files

<!-- Help agents navigate this codebase quickly. -->

| Path | Purpose |
|------|---------|
| `cmd/<app>/main.go` | Application entry point |
| `REQUIREMENTS.md` | Non-negotiable project requirements |
| `.agents/docs/` | All project documentation |
