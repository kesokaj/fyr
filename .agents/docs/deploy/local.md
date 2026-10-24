---
owner: main
date: 2026-10-24
target: local
---
# Deploy — local (this machine)

"Deploying" the harness means installing the context, skills, and agents into a home directory. Development never touches the real `$HOME` (`REQUIREMENTS.md` R6); deploying to it is the operator's action.

## Prerequisites

Go 1.24+ and git. `~/.local/bin` on `PATH`. The Antigravity CLI (`agy`) installed; it is the only target. The installer writes under `~/.gemini/config/` and `~/.local/bin`, never under `~/.claude`; `--vendor` is a usage error (ADR-062).

## Build and install

```bash
cd ~/workspace/styrman-harness
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...   # before deploying anything; gofmt takes the source dirs, not `.` (it walks .agents/scratch, BUG-048); -timeout 30m for a one-CPU box (cmd/sh-agent ~13 min under -race)
go run ./cmd/sh-agent install                        # builds the binary from this checkout, writes ~/.gemini/config, installs the build to ~/.local/bin/sh-agent
sh-agent status                                      # must end with "drift none"
```

## Update

```bash
sh-agent update            # fetch; refuse a dirty checkout; if behind: pull --ff-only; rebuild and reinstall
sh-agent update --check    # only reports; exit 2 when behind
```

The normal way a checkout moves forward, run whenever the operator decides the harness has changed; `remote.md` § The resync loop covers the timer and the dirty-checkout refusal.

## Update a release-binary install

A place installed from a release binary (README § Install without cloning) has no checkout and nothing to build from, so `update` moves it to the newest RELEASE instead:

```bash
sh-agent update --check    # only reports; exit 2 when a newer release exists
sh-agent update            # download the newest release, verify it, replace ~/.local/bin/sh-agent, reinstall
```

What it does, in order: asks `gh` for the newest release's tag; refuses anything that is not a `vX.Y.Z` tag, and anything not numerically newer than the installed version — nothing but this check stops a downgrade to an older, genuine release; downloads this platform's binary with `SHA256SUMS` into a temporary directory under `~/.config/sh-agent/`, removed on every path out; checks that `SHA256SUMS` names this release and that the binary matches its line in it (integrity only; no key signs a release, ADR-053); and only then replaces `~/.local/bin/sh-agent` through a rename and runs the new binary's `install`. Any failure leaves the installed binary untouched and names the check that failed. It needs `gh` on `PATH`; a development build is never overwritten from a release.

## From a plain copy (no `.git`)

A copy of the repository with no `.git` — for a major remodel, or a machine that cannot authenticate to GitHub. It needs Go, and the Go module proxy or a filled module cache for the one dependency, `modernc.org/sqlite` (ADR-045):

```bash
cd <unpacked directory>
go run ./cmd/sh-agent install        # installs the running binary as is; never rebuilds (BUG-015)
sh-agent status                      # first line names a plain copy with no commit; must end with "drift none"
```

`sh-agent update` refuses a plain copy, by design, with a message naming `install` still deploying it as it is and the alternatives. To move it forward, unpack a newer copy and run `install` from it again; to roll back, install the earlier one. This path is not one of the ways in — `.agents/docs/specs/sh-agent/delivery.md` § The model names the three (a checkout, a release binary, a zip place — `README.md` § Install from a zip (no SSH, no gh)) and this plain copy as their fallback; it stays because the code path does and because it is the fallback when none of the three can reach GitHub.

## What stays on this machine

Nothing is sent from this machine but the dreamer's redacted prompt (below; `.agents/docs/specs/sh-agent/delivery.md` § What stays on the machine). `sh-agent report` is gone (ADR-062): the feedback log, the retro proposals, the search index and the metrics store stay in `~/.config/sh-agent/`, and a harness-target proposal reaches the harness checkout only through `/retro --harness`, run there by you. An older version left two things no verb removes any more: a kept clone at `~/.config/sh-agent/feedback` (versions before the throwaway clone) and a `~/.config/sh-agent/report-<pid>` directory after an interrupted run. Each is a clone of the feedback repository; read what it holds, then delete it by hand.

## The background dreamer (optional, once per machine)

```bash
sh-agent dream --install-timer   # systemd user timer (Linux) or launchd agent (macOS): sh-agent dream --all nightly at 03:00
sh-agent status                  # its line: dream      last run <date>: <n> projects, <m> proposals; timer installed
sh-agent dream --all --dry-run   # what each indexed project would send, and the CLI command; writes nothing
sh-agent dream --remove-timer    # stop and remove it (uninstall does too)
```

- The unit runs `~/.local/bin/sh-agent`, with the absolute entries of the PATH of the shell that ran `--install-timer`, so it finds `agy` there; one reachable only through a relative PATH entry is not found at 03:00. Run it from the shell you work in. A rerun from a shell with another PATH renders other text: the installed unit is kept and named, so run `--remove-timer` first.
- The unit is `sh-agent-dream.timer` (Linux) or `com.styrman.sh-agent-dream` (macOS) for the real home and carries a short hash suffix for any other `--home`, and runs `dream --all --home <that home>`, so a scratch install or removal leaves the real timer alone and a scratch timer never dreams over the real home (the service manager is shared by every home of the user; BUG-144). A timer installed by v2.0.0 or earlier has unit text without `--home`; `--install-timer` keeps it and says so, so run `sh-agent dream --remove-timer` then `--install-timer` once after updating. The real home is the account's home in the account database, never `HOME`. A unit that v2.0.0 or earlier wrote under the bare name from any other home is never removed or stopped by `--remove-timer` or `uninstall`: `sh-agent dream --remove-timer` and `uninstall` now name the files they find and print the check and the steps (and how to install the timer again afterwards); the `dream` line of `status` only says such files exist and names the dry run that prints the steps, `sh-agent dream --remove-timer --dry-run --home '<home>'`. The by-hand cleanup is one of two ways. A scratch home: delete its files, `<home>/.config/systemd/user/sh-agent-dream.service` and `.timer`, or `<home>/Library/LaunchAgents/com.styrman.sh-agent-dream.plist`, and stop the job by name only after checking that the manager loaded it from that scratch file and not from the real home's (`systemctl --user show -p FragmentPath sh-agent-dream.timer`; on macOS, where v2.0.0 loaded a scratch plist by path, `launchctl print gui/$(id -u)/com.styrman.sh-agent-dream | grep path`): a stop by name ends whichever one is loaded. The real home of an account the database does not list (there the bare unit is the only one, and it is loaded): `systemctl --user disable --now sh-agent-dream.timer`, delete the two files, `systemctl --user daemon-reload`, then `sh-agent dream --install-timer` (on macOS `launchctl bootout gui/$(id -u) <the plist>` before deleting it).
- The dream job always runs `agy` (plan mode, the prompt on stdin); with no `agy` on PATH it fails before the first project.
- Antigravity needs the operator's login: on Linux `~/.gemini/antigravity-cli/antigravity-oauth-token`, which the run copies into a private HOME under `~/.config/sh-agent/dream-agy-home-*`; on macOS agy keeps it in the Keychain, so the run links `~/Library/Keychains` into that HOME instead (and copies the token file too when one exists), so the dream job's agy shares the operator's real Keychain login: a refresh it makes reaches the interactive agy too. The HOME is removed after the run; a locked Keychain (no user logged in to the Mac) makes agy ask for a browser login, which it gives up on after its own 60-second wait, so each project fails in about a minute with `agy is not logged in inside the dream job's private HOME` (BUG-166).
- A project is dreamt over only once `sh-agent search` or `index` has run in it (the global index is the list).
- On Linux a user timer fires only while your user manager runs (a login, or `loginctl enable-linger`). `Persistent=true` runs a missed night at the next login.
- Every real run first runs `sh-agent metrics ingest`. It reads the shape of Antigravity sessions into `~/.config/sh-agent/metrics.db` (never sent anywhere; `uninstall` keeps it) and logs its summary line to `dream.log`. Each project's prompt then carries a numbers-only summary of its metrics. The store is the only copy once Antigravity deletes a transcript, so back it up now and then with `sh-agent metrics export <file>`. Look at it with `sh-agent metrics show` or `sh-agent metrics report` (spec `specs/sh-agent/metrics.md`).
- Output: `~/.config/sh-agent/proposals/dream/` (per project; `harness/` for harness targets, which wait there for `/retro --harness`; `drafts/` for draft skills). A failed project's output goes to `dream-failed/`, and the log is `dream.log`. Nothing is applied until `/retro` does it.

## Configure

Nothing to configure. The global flags are `--repo` and `--home`; `--vendor` is gone and any use of it is a usage error on every verb, exit 2 (`flag provided but not defined: -vendor`). The manifest still records `"vendor": "antigravity"`; an older value is read and ignored. An install over a manifest an older version wrote removes each `~/.claude/...` file that manifest lists (`remove  ~/<path> (no longer in the source)`), keeps a symlink, a file under a linked directory and `~/.claude` itself, and never touches a file the manifest did not list; `~/.claude/settings.json` is never read or written, so its `attribution` and `autoCompactWindow` keys stay as the operator's. `--repo DIR` overrides where the checkout is; `$SH_AGENT_REPO` does the same persistently. Without either, install and update discover the checkout by walking up from the working directory, then fall back to the path recorded at the last install; when that discovery finds a checkout OTHER than the one already on record — an unpacked copy, a second clone sitting in a harness-shaped directory — the build is refused rather than run and deployed: `"checkout %s is not the installed one (%s); pass --repo %s to build and install from it"`, or, on a release-binary install, `"this home was installed from the release binary %s, not from a checkout; pass --repo %s to build and install from it"`. `--home DIR` installs elsewhere and is what every development run uses (a scratch home under `.agents/scratch/`, never `/tmp`). From its own git checkout, `install` rebuilds the binary with `go build` first; the running binary is installed as is when `go` is missing (with a warning) or when the tree is a plain copy with no `.git` — an unpacked bundle (BUG-015).

## Verify

- `sh-agent status` shows the current commit, a `binary` line carrying the same revision (a warning there means the installed binary is older than the checkout or unstamped: run `sh-agent update`), and no drift, with no `shadowed` line — a checkout place can be shadowed the same way a release-binary place was (BUG-078). `sh-agent version` run unqualified must match what was just installed; if it does not, `hash -r` (bash) or `rehash` (zsh) first — the shell caches where it last found `sh-agent`, and `update` replaces only `~/.local/bin/sh-agent` (BUG-079; `update` says so when it ran from another copy, but only against the real home, never `--home` elsewhere — remedy is `ln -sfn` to the installed binary when that copy is yours, or, when it is not, removing it with the rights that own it or running the installed one by its path) — and if still wrong, an earlier `sh-agent` on `PATH` is running instead of the installed one (`type sh-agent` shows which). `status` shows one `agy  ~/.gemini/config …` line, no `claude` line, no `(vendor: …)` text and no settings note.
- `sh-agent render | head -3` prints the assembled context.
- Start Antigravity once and check it read the files: `/skills` and `/agents` list the harness entries, and `grep 'Failed to parse' ~/.gemini/antigravity-cli/log/cli-*.log` is empty (BUG-004).

## First session after install

Antigravity prompts once per session for paths outside the workspace, such as the first read or write under `~/.config/sh-agent/` (feedback log, proposals, the retro index); approve it. The installer touches no vendor setting file (ADR-062). The project's own memory store (`.agents/memory/`, written by `sh-agent memory`) is inside the project, so it needs no such prompt.

## Operator steps

Standing by-hand procedures on this box, never pending items (those live in the newest handoff):

- After a push from another machine: `sh-agent update`, then `sh-agent status` shows a stamped `binary` line and no drift.
- After any change to `context/`, `agents/`, or `skills/` reaches this home: the three smoke checks of `.agents/docs/testing/2026-09-03-verify-real-home.md` on Antigravity, from a fresh clone of the demo service.
- When `govulncheck ./...` names a toolchain-fixed advisory: get a newer Go, never raise the `go` directive or add a `toolchain` line in `go.mod` (ADR-029 — a pin makes the module unbuildable on a host that cannot fetch a toolchain). Try the system package manager first; when it has nothing newer, as Debian trixie did on 2026-09-16 (`golang-1.24-go 1.24.4-1` and no later package), install a user-level SDK instead, which needs no root:

```bash
go install golang.org/dl/go1.27.2@latest     # the wrapper lands in ~/.go/bin
~/.go/bin/go1.27.2 download                  # the SDK lands in ~/sdk/go1.27.2
ln -sfn "$HOME/sdk/go1.27.2/bin/go" "$HOME/.local/bin/go"   # ~/.local/bin is ahead of /usr/local/bin on PATH here
go install golang.org/x/vuln/cmd/govulncheck@latest         # rebuild it, or it cannot parse the newer sources
go version && "$(go env GOPATH)/bin/govulncheck" ./...   # the full path: GOPATH/bin is not on every shell's PATH
```

  Undo by deleting that one symlink: `/usr/local/bin/go` (a root-owned install at `/usr/local/go`, go1.27.1 on 2026-10-09) and `/usr/bin/go` stay where they were. On 2026-10-09 this box moved to go1.27.2 this way, for GO-2026-6604, -6600 and -6599. After the switch, run the full gate and `sh-agent install`, so the deployed binary is built by the new toolchain — `go version -m ~/.local/bin/sh-agent` names it.

## Roll back

`sh-agent uninstall` removes exactly the files the manifest lists, plus the dream timer when one is installed, even with no manifest left. To return to an earlier harness commit: `git checkout <commit> && go run ./cmd/sh-agent install`.

## Sandbox instead of the real home

`.agents/docs/testing/2026-09-03-verify-sandbox.md` records how a throwaway `HOME` with both CLIs logged in is used to test a change end to end before it reaches the real home.
