# AGENTS.md

## Project Overview

<!-- Brief description: what this project does, who it serves, and why it exists. -->

## Setup Commands

<!-- The few commands a contributor runs before claiming something works — build, run, test.
     Keep it to those: coverage runs, e2e scripts and container smoke tests belong in
     the GUIDE-testing record, which this section points at once it exists. -->

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
<!-- For ADRs: fyr record list --kind adr -->

## Project-Specific Standards

<!-- Only list standards that DIFFER from the global context (~/.gemini/config/rules/AGENTS.md). -->
<!-- If nothing differs, delete this section. -->

## Testing

<!-- Project-specific testing instructions: the verification tiers the plan names and the commands behind them; the standing procedure is the GUIDE-testing record. -->
<!-- For detailed guides: fyr record list --kind guide -->

## Deploy

<!-- One line per target that exists. The procedures live in the DEPLOY-local, DEPLOY-remote and DEPLOY-gcp records; GCP defaults are recorded in DEPLOY-gcp, never invented. -->

Local only — see `DEPLOY-local` (`fyr record get DEPLOY-local`).

## Key Files

<!-- Help agents navigate this codebase quickly. -->

| Path | Purpose |
|------|---------|
| `cmd/<app>/main.go` | Application entry point |
| `REQUIREMENTS.md` | Non-negotiable project requirements |
| `.agents/trail.jsonl` | The trail as records (plans, changes, ADRs, specs, guides, bugs), written only by `fyr record` |
