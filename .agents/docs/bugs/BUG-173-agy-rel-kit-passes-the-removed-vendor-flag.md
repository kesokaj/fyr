---
owner: main
date: 2026-10-24
id: BUG-173
status: fixed
area: testing kits (agy-rel/run.sh)
---
# BUG-173: the committed Antigravity kit passes `--vendor agy`, which Phase 2 made a usage error, so every row dies at its install

## Symptom
`testing/agy-rel/run.sh` installs each row's sandbox home with `go run ./cmd/sh-agent install --home "$KIT_HOME" --vendor agy`. Since `5a8f492` (plan `2026-10-24-fyr-agy-only.md` Phase 2) `--vendor` is gone from every verb and any use of it exits 2. Under the kit's `set -euo pipefail` the install line ends the row before the CLI starts, so no Antigravity release row could run. Found by the main agent on 2026-10-24 while preparing Phase 3a's behavioural runs; no row had run since `5a8f492`.

## Repro
`go run ./cmd/sh-agent install --home .agents/scratch/fyr/home-vendor-probe --vendor agy` on `a2e6a5a` → `exit status 2`, nothing written (`.agents/scratch/fyr/vendor-probe.txt`).

## Root cause
Phase 2's sweep for the flag covered the Go tree (`TestUsageErrorsExit2`, six verbs) and the docs the `documentation` agent was given; the committed run kits under `.agents/docs/testing/` are shell scripts that no test runs (`-short` skips the preamble's own test, and the kits call the CLI under test, which a unit suite cannot). The checkpoint's grep was `claude`, not `--vendor`. `ab-kit/run.sh` and `seed-security/run.sh` carry `--vendor claude` the same way; both are Claude Code kits that Phase 3b deletes.

## Fix
`agy-rel/run.sh` installs with `--home` alone, and its header no longer names the flag (Phase 3a, 2026-10-24). Verified by the Phase 3a rows, which ran through a scratch copy of the fixed kit (`testing/2026-10-24-verify-fyr-agents.md`).

## Prevention
A flag or verb removed from the binary is grepped for in every committed script (`git grep -n -- '--<flag>' -- '*.sh'`), not only in Go and the docs, and the removing phase runs one row of each committed kit that calls the binary before its checkpoint.
