---
owner: main
date: 2026-10-25
---
# Verify — fyr Phase 4b: migration, timers, release, the crossing (local half)   (2026-10-25)

Range: `c2810dd..` the Phase 4b commit. This log rides in that commit. Plan `2026-10-24-fyr-agy-only.md` Phase 4b, D13, D14, D29, D30. Run by the main agent, with no verifier dispatch: Phase 5 holds it, as in Phases 2 to 4a. Go: `/usr/bin/go` go1.24.4, Linux amd64, one box.

## Status: PASS

E2E criterion 2's local half holds. A scratch home installed by a release-built v2.7.0, with its state dir, binary, manifest and a dream unit, is moved to `fyr` by one `fyr install` built from the tree. The unit suite is green, and the release-step dry run publishes `fyr-*` under one marker. Antigravity behavioural runs were not in this phase's boxes and were not run (§ What was not checked).

## § 1 — unit tier
| Command | Result |
|---|---|
| `gofmt -l cmd internal` | empty |
| `go vet ./...` | exit 0 |
| `go test -race -count=1 -timeout 30m ./...` | exit 0, 9 of 9 packages ok (`cmd/fyr` 87.6 s, `internal/harness` 85.6 s) |
| `"$(go env GOPATH)/bin/govulncheck" ./...` | 15 standard-library advisories of go1.24.4, the same count as the Phase 4a base; 0 called in required modules |

## Tests first, then mutations
- **Red.** Before any code, the new tests failed to compile (`undefined: harness.OldStateDir`, `MigrateStateDir`, `ReplaceOldTimer`, `stderr`, …). With `migrate_test.go` set aside, seven release and zip tests failed on behaviour: `TestReleaseNames`, `TestUpdateFromReleaseReplacesAndInstalls`, `…Refusals`, `…SweepStaysInHome`, `…PinsTheHost`, `TestVerifyZipSums`, `TestVerifyZipSumsWritesNothingUnderTempDir`.
- **Green** after the code.
- **Mutations** (each restored after):
  - `writesState` without `dream` → `TestStateDirMigrationByVerb/writes_dream_--remove-timer` FAIL.
  - `installFiles` without `ReplaceOldTimer` → `TestCrossingFromV270` FAIL ("old dream units left").
  - `kit-preamble.sh` at HEAD → `TestKitPreambleAssertsCommit` FAIL on the three new `~/.config/sh-agent` cases.
- **`TestCrossingFromV270` ran and did not skip** (`-v`: `--- PASS: TestCrossingFromV270 (0.65s)`). It builds v2.7.0 from `git archive v2.7.0` in a temp dir, so it starts from a plain-copy place.

## The checkpoint crossing (E2E criterion 2, local half)
Script `.agents/scratch/fyr/p4b/crossing.sh`, output `logs/crossing-2.out`:
- **Setup.** v2.7.0 was built as a release (`git archive v2.7.0`, `-trimpath -buildvcs=false -ldflags "-X main.version=v2.7.0"`; it reports `sh-agent v2.7.0`). It was installed into `.agents/scratch/fyr/p4b/home-cross` without `--repo`, as a release-binary place does, followed by `dream --install-timer --home`. A fake `systemctl` first on PATH recorded every call, so the real user manager was never reached (R6).
- **Before:**
  - `~/.config/sh-agent` and `~/.local/bin/sh-agent` exist;
  - units `sh-agent-dream-6bdd7ad8.{service,timer}`;
  - the manifest lists 1,790 files, 895 of them under `~/.claude`, and `.local/bin/sh-agent`.
- **`go run ./cmd/fyr install --repo <checkout> --home <home>`.** Its first line is `migrated ~/.config/sh-agent → ~/.config/fyr`, followed by:
  - `34 written, 862 unchanged, 897 removed, 0 kept — 896 files managed`;
  - `replaced the dream timer sh-agent-dream-6bdd7ad8 with fyr-dream-6bdd7ad8`.
- **After:**
  - `~/.config/sh-agent` and `~/.local/bin/sh-agent` are absent;
  - `~/.config/fyr/state.json` and `~/.local/bin/fyr` are present;
  - the feedback line written into the old dir before the crossing is in `~/.config/fyr/feedback.jsonl`;
  - the only file left under `~/.claude` is `settings.json`, the operator's (D2);
  - units `fyr-dream-6bdd7ad8.{service,timer}`, with `ExecStart="<home>/.local/bin/fyr" dream --all --home "<home>"`.
- **systemctl calls**, in order:
  1. `--user daemon-reload`
  2. `--user enable --now fyr-dream-6bdd7ad8.timer`
  3. `--user disable --now sh-agent-dream-6bdd7ad8.timer`
  4. `--user daemon-reload`
- **`fyr status`**, exit 0:
  - `binary c2810dd7abf1+dirty`, with no warning;
  - `dream never run; timer installed`;
  - `drift none — 895 files match the repo`.
- **The tree it ran on.** The working tree: the Go code and embedded content of the Phase 4b commit. The only edits after the run were docs and trail (`remote.md`, the plan, records).

## Release-step dry run (the preflight box)
`deploy/remote.md` § Releases steps 1 to 3, run in `.agents/scratch/fyr/p4b/release-v9.9.9` with version `v9.9.9`. The build is from the working tree and the zip from `git archive HEAD`, so not from a tag. Nothing was published:
- `sha256sum -c SHA256SUMS`: OK for `fyr-linux-amd64`, `fyr-darwin-arm64`, `fyr-linux-arm64` and `fyr-v9.9.9.zip`;
- `SHA256SUMS` has one `#` line, `# fyr v9.9.9`;
- the zip check (`./fyr-linux-amd64 update --from-zip fyr-v9.9.9.zip --home ../zipcheck --dry-run`): exit 0, `CHECKED against the SHA256SUMS of release v9.9.9` once;
- `./fyr-linux-amd64 version` → `fyr v9.9.9`.

## § 3 — scratch-home install
`go run ./cmd/fyr install --home .agents/scratch/fyr/p4b/home-s3`: exit 0, 896 files managed. `status`: `binary c2810dd7abf1+dirty`, with no warning, and `drift none`. `fyr list`: exit 0.

## Real home
After every run:
- `~/.config/sh-agent` is still there, and `~/.config/fyr` does not exist;
- `~/.config/systemd/user` holds only the operator's `sh-agent-update.{service,timer}`.

The scratch-built `fyr version` above ran against the real home, and only printed the read-only notice to stderr, which is what D13 asks. This box crosses in Phase 6.

## What was not checked
- **No Antigravity behavioural run** for the changed skills `bug` and `preflight`. Phase 5 and preflight's run rule owe it.
- **The crossing on macOS** (launchd). Only `TestReplaceOldTimer/darwin` covers it, with a fake `launchctl`.
- **A zip place's and a checkout place's crossing as a scripted run.** The Go test covers a plain-copy place, and the checkpoint covers a release-binary place.
- **F2's removal-error path** at a crossing: today's code, not exercised here.
- **The real places**, after the tag: Phase 6, who: user.
