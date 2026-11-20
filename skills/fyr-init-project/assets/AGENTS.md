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
go test -race ./...

# Lint & Verify (Go: gofmt, go vet, go test -race, fyr check -> .agents/scratch/verify.stamp)
go vet ./...
fyr verify
```

<!-- Standalone CLI / non-Go script / TS app: replace cmd/server/ with cmd/<app>/ (or script/app entry point) and omit unused server steps. Non-Go projects run their own test/lint commands above and close tasks with `fyr record add handoff` instead of `fyr close` (which checks a Go verify.stamp). -->

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

<!-- One line per target that exists. The procedures live in the DEPLOY-local, DEPLOY-remote and DEPLOY-gcp records; GCP defaults are recorded in DEPLOY-gcp, never invented. For non-GCP hosts (AWS, Fly, VPS, or self-hosted compose), record the procedure in DEPLOY-remote and leave DEPLOY-gcp as Not applicable yet. -->

Local only — see `DEPLOY-local` (`fyr record get DEPLOY-local`).

## Key Files

<!-- Help agents navigate this codebase quickly. -->

| Path | Purpose |
|------|---------|
| `cmd/<app>/main.go` | Application entry point (or `<script>` / entry point for a Standalone CLI) |
| `PRD.md` | What and why: problem, goals, the non-negotiable requirements |
| `SDD.md` | How the parts fit: components, flows, decisions |
| `TDD.md` | How each part is built and tested |
| `TODO.md` | The active phase's tasks, the next phases, the backlog |
| `.agents/` | Project customizations and `scratch/` (trail records live in `fyr`'s brain store; `fyr record export` writes `.agents/trail.jsonl` on demand) |
