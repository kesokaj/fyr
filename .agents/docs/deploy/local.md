---
owner: main
date: 2026-09-17
target: local
---
# Deploy — local (this machine)

"Deploying" the harness means installing the context, skills, and agents into a home directory. Development never touches the real `$HOME` (`REQUIREMENTS.md` R6); deploying to it is the operator's action.

## Prerequisites

Go 1.24+ and git. `~/.local/bin` on `PATH`. Claude Code and/or the Antigravity CLI installed; both are optional — the installer writes for the vendor recorded by the existing install, else both, or selectively with `--vendor`.

## Build and install

```bash
cd ~/workspace/styrman-harness
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...   # before deploying anything; gofmt takes the source dirs, not `.` (it walks .agents/scratch, BUG-048); -timeout 30m for a one-CPU box (cmd/sh-agent ~13 min under -race)
go run ./cmd/sh-agent install                        # builds the binary from this checkout, writes ~/.claude and ~/.gemini/config, installs the build to ~/.local/bin/sh-agent
# or for Antigravity only:
# go run ./cmd/sh-agent install --vendor antigravity
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

## Report back

`sh-agent report` sends this machine's experience to the private `styrman-harness-feedback` repository so the harness can be improved from it. On a release-binary install `--repo` is not used — `report` always reports the harness built into the binary — and passing it prints `report: --repo is not used here; this release binary reports the harness built into it` rather than doing anything with it. What leaves: a COPY of `~/.config/sh-agent/feedback.jsonl`, `retros.jsonl`, `state.json`, `proposals/*.md` and `proposals/dream/harness/*.md`, redacted, plus `report.json` — host, OS and arch, vendor, installed file count and time, the harness checkout's commit, drift, skill and agent counts, the Go that built the running binary, and the list of redactions. The originals are never modified. A symlink among them is skipped and named, never followed.

```bash
sh-agent report --dry-run     # prints where it would write, every file, and every redaction; writes nothing, clones nothing
sh-agent report               # clones a throwaway holding only this machine's reports, commits, pushes, removes it
```

- **Redaction is structural, and no longer walks into JSON.** In `feedback.jsonl` and `retros.jsonl`, a line whose raw text OR JSON-decoded text carries a needle is replaced whole by `{"redacted":"[redacted by sh-agent report]"}`; a key block (`-----BEGIN` — or a `BEGIN … PRIVATE KEY` header whose dashes were rewritten — to its footer, two concatenated keys kept open, unless the dashes were also stripped and only a space joins them, in which case the second key's body is sent — a stated limit) is found first and replaced by `{"redacted":"[redacted by sh-agent report: N lines]"}` instead. A footer closes the block its header opened only when it is on that same line and its mention contains the header's label ("... up to -----END below" does not close it, because "below" is not the label); a footer on any later line closes the block regardless of what it names, so a footer naming a different label, or one merely mentioned on a later line, still closes it early. `state.json` is one document: any needle anywhere replaces the whole file with the plain JSON marker. Text files (`proposals/*.md`) get the same whole-line or whole-block treatment with the plain text marker, never the JSON object. The shapes, by kind, are the deny-list `prefixNeedles` and `keywordNeedles` in `internal/harness/report.go` — read that list rather than this paragraph for the enumeration itself: vendor token prefixes matched as written — the common prefixes of AWS access keys, Google API keys, OAuth access tokens and client secrets, GitHub and GitLab tokens, Anthropic and OpenAI keys (`sk-None-` included), Slack bot/user/app tokens, Stripe webhook secrets (plus Stripe live and test keys by prefix and sixteen alphanumerics), PyPI tokens for pypi.org and test.pypi.org, and PEM headers — not every token those vendors issue; case-insensitive assignment and header shapes (`password`/`passwd`/`secret`/`token`/`api-key` assignments, `Bearer`, `Authorization:`, and any `hooks.slack` webhook host in any case, a port, a trailing dot, an encoded path or a gov domain included); URL credentials `scheme://user:pass@` (the user may be empty) and a bare userinfo token of sixteen or more characters with an empty or no password, escapes and padding included (a long username, or a pathless URL followed by an unrelated `@` on the same line, is over-redacted too, on purpose); length-and-alphabet shapes with no distinctive prefix (a JWT, an OpenAI `sk-` key of 32+ alphanumerics, an npm token, a Hugging Face token) matched without a leading word boundary, so a token after a literal `\n` or an underscore is still caught; and, since the 2026-09-30 review, one rule per kind for the families it named (a Google refresh token, Discord and Teams webhooks, SendGrid, an Azure storage connection string, a cookie header, Vault, age, DigitalOcean, a JWT with a short payload), each widened once for the near shapes a second review named. A shape past those rules is sent, and adding one is a design question for the operator, not another batch. The list is a deny-list and a safety net, never a guarantee (`delivery.md` § The loop back says so); the gaps named in the 2026-09-30 review (an upper-case webhook host, a userinfo token under 20 characters) were closed the same day — see `.agents/docs/reviews/2026-09-30-review-project-merged.md` § Outcome for the history. `report.json` records file, line (absent for a whole document), pattern, lines and unterminated — never a JSON key path, and never the text. It over-redacts on purpose — "tokens: 3" or "ASIAN" go too, and an unterminated `-----BEGIN` in a JSONL file redacts to end of file, dropping later records rather than risk shipping the rest of the key (both `--dry-run` and a real run show `unterminated: everything after it in this file was dropped` next to that line, and `report.json` carries `unterminated: true`).
- **Anything that matches no shape IS sent, and `--dry-run` cannot show it:** the dry run lists what was caught, never what was missed. This is a safety net, not a guarantee, so before EVERY report, not just the first, read what was added under `~/.config/sh-agent/` since the last one, `proposals/dream/harness/*.md` included: those are model-written from any indexed project's redacted content, so a project's text can reach the report through them. Known gaps in the net: a secret on a later line than the needle that located it — a YAML `password: |` or `>-` block, an indented continuation, an HCL heredoc, a shell `\` continuation, pretty-printed JSON — is sent, because redaction only removes lines a needle itself matched. The decoded check reads every JSON value that decodes from the start of the line, numbers kept as written, but this is not exhaustive: a leading BOM, `NaN`, or anything else that stops decoding partway — a leading log prefix, a raw control character, a truncated record, `Infinity`, very deep nesting — is checked only as raw text from that point; a value shadowed by a duplicate key decodes fine but is discarded before the decoded check ever sees it, so only the raw-text check (run on the line regardless) can still catch it. Text files (`proposals/*.md`) are checked only as raw text, never decoded, so a needle reachable only through a JSON escape there is missed.
- **Nothing is kept here.** Each run clones `~/.config/sh-agent/report-<pid>` with `--filter=blob:none --no-checkout --depth 1`, limits it with `git sparse-checkout set --no-cone reports/<host>/`, writes, commits, pushes, and removes that directory on every path out — a failed push and a refusal included. A run killed by a signal cannot run that removal, so the NEXT run sweeps any `report-<pid>` whose process is gone (one whose process is still running is left alone). No other machine's report content is ever fetched to this disk (this machine's own earlier runs do come along, which is what makes the `-2` suffix below pick a free name). A server that does not support the filter makes git warn and send everything: slower, still correct. `sh-agent update` removes the full clone earlier versions kept at `~/.config/sh-agent/feedback` — but not under `--check`, which reports and changes nothing — and only when that path is a real directory (never a symlink), is a checkout of the feedback repository, has nothing uncommitted, untracked, ignored or stashed, and has no commit that no remote has; anything else, including a state git cannot report, leaves it in place with the reason named. `--feedback-repo DIR` is unchanged: that checkout is the operator's, so it is written into and never removed.
- **Where it goes:** `reports/<host>/<YYYY-MM-DD-HHMMSS>/` in the clone or the named checkout, suffixed `-2`, `-3` when that directory exists, so no run overwrites another. Only the paths the run wrote are staged and committed; any other file in the checkout is named in the output and left alone. The push names the feedback URL itself rather than `origin`, so `origin`'s own `pushurl` or an extra `url` cannot redirect it; a git URL rewrite (`url.*.insteadOf`, `pushInsteadOf`) or a remote named after the feedback URL still applies, as it does to every push on the machine — that is the operator's own configuration, trusted rather than refused. `-u` points the branch at that URL so a later plain `git pull --rebase` fetches from the same place.
- **Only the feedback repository.** The clone this run made, or `--feedback-repo DIR`, must be the top of its own git checkout with `origin` set exactly to `git@github.com:kesokaj/styrman-harness-feedback.git`, or `report` refuses before writing anything; a harness checkout or any other project fails that. `--feedback-repo` is never cloned into. `report` also refuses to run with `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY` or `GIT_COMMON_DIR` set, and refuses a `reports` or `reports/<host>` path in the checkout that is not a real directory.
- **No harness checkout here** (a plain copy, a release binary, or nothing): `report` still runs. From a RELEASE BINARY, `harness_commit` is `null` and `harness_version` carries the release, with `drift`, `skills` and `agents` counted from the files built into the binary. From a plain copy, `harness_commit` is `null` and the rest are counted from that tree. With no harness tree at all, `drift`, `skills` and `agents` are `null` too. With nothing installed, `vendor`, `files` and `installed_at` are `null`. With nothing under `~/.config/sh-agent/` it still sends `report.json` — "this machine is alive" is the signal.

| What fails | What you see and do |
|---|---|
| The clone fails (no SSH key, no network, repository missing) | `report: could not reach <url>: …; check the connection or pass --feedback-repo`, exit 1, nothing left behind. Fix access, or clone the repository by hand somewhere and pass `--feedback-repo`. |
| Push rejected — another place pushed first | Without `--feedback-repo`: the run says so, re-clones and re-applies itself once; if that is rejected too, `report: push rejected twice; re-run sh-agent report — nothing is kept locally`, exit 1, and nothing is left behind. With `--feedback-repo`: `report: push rejected; run git -C <dir> pull --rebase and re-run — the commit stays local`, exit 1. |
| `~/.config/sh-agent/feedback` is still there from an older version | `report` ignores it and keeps nothing; `sh-agent update` (not `--check`) removes it when it is clean and fully pushed, and otherwise says `kept …: has uncommitted changes`, `has stashed changes`, `a symlink, not the kept clone`, `not the feedback repository`, or `has N commit(s) that none of its remote-tracking refs hold` — and leaves it for you. That last one is usually NOT lost work: `report` pushed with `-u <url>`, so the branch tracks a URL, nothing updates the remote-tracking refs, and commits that reached the remote long ago still count. The message gives the command that settles it — `git fetch` in that directory, then `git log --oneline --all --not --remotes FETCH_HEAD`, which covers the same refs the count does (a HEAD-only check would say "nothing unique" about a commit sitting on another local branch). An empty list means the clone holds nothing only this machine has, and you can remove it by hand. The guard never removes it on a count it cannot trust. |
| The checkout's origin is not the feedback repository | `report: <dir> is not the feedback repository: its origin is not "…"`, exit 1, nothing written — the checkout's own origin is never printed. Point it at the right checkout. |
| `GIT_DIR` (or another redirecting variable) is set | `report: GIT_DIR is set; report commits only in the feedback checkout — unset it and re-run`, exit 1. |

## The background dreamer (optional, once per machine)

```bash
sh-agent dream --install-timer   # systemd user timer (Linux) or launchd agent (macOS): sh-agent dream --all nightly at 03:00
sh-agent status                  # its line: dream      last run <date>: <n> projects, <m> proposals; timer installed
sh-agent dream --all --dry-run   # what each indexed project would send, and the CLI command; writes nothing
sh-agent dream --remove-timer    # stop and remove it (uninstall does too)
```

- The unit runs `~/.local/bin/sh-agent`, with the absolute entries of the PATH of the shell that ran `--install-timer`, so it finds `claude` or `agy` there; one reachable only through a relative PATH entry is not found at 03:00. Run it from the shell you work in. A rerun from a shell with another PATH renders other text: the installed unit is kept and named, so run `--remove-timer` first.
- The unit is `sh-agent-dream.timer` (Linux) or `com.styrman.sh-agent-dream` (macOS) for the real home and carries a short hash suffix for any other `--home`, and runs `dream --all --home <that home>`, so a scratch install or removal leaves the real timer alone and a scratch timer never dreams over the real home (the service manager is shared by every home of the user; BUG-144). A timer installed by v2.0.0 or earlier has unit text without `--home`; `--install-timer` keeps it and says so, so run `sh-agent dream --remove-timer` then `--install-timer` once after updating. The real home is the account's home in the account database, never `HOME`. A unit that v2.0.0 or earlier wrote under the bare name from any other home is never removed or stopped by `--remove-timer` or `uninstall`: `sh-agent dream --remove-timer` and `uninstall` now name the files they find and print the check and the steps (and how to install the timer again afterwards); the `dream` line of `status` only says such files exist and names the dry run that prints the steps, `sh-agent dream --remove-timer --dry-run --home '<home>'`. The by-hand cleanup is one of two ways. A scratch home: delete its files, `<home>/.config/systemd/user/sh-agent-dream.service` and `.timer`, or `<home>/Library/LaunchAgents/com.styrman.sh-agent-dream.plist`, and stop the job by name only after checking that the manager loaded it from that scratch file and not from the real home's (`systemctl --user show -p FragmentPath sh-agent-dream.timer`; on macOS, where v2.0.0 loaded a scratch plist by path, `launchctl print gui/$(id -u)/com.styrman.sh-agent-dream | grep path`): a stop by name ends whichever one is loaded. The real home of an account the database does not list (there the bare unit is the only one, and it is loaded): `systemctl --user disable --now sh-agent-dream.timer`, delete the two files, `systemctl --user daemon-reload`, then `sh-agent dream --install-timer` (on macOS `launchctl bootout gui/$(id -u) <the plist>` before deleting it).
- Claude Code runs with `--setting-sources local`: a user-level `apiKeyHelper` is not loaded and the run fails; a `claude` login works.
- Antigravity needs the operator's login: on Linux `~/.gemini/antigravity-cli/antigravity-oauth-token`, which the run copies into a private HOME under `~/.config/sh-agent/dream-agy-home-*`; on macOS agy keeps it in the Keychain, so the run links `~/Library/Keychains` into that HOME instead (and copies the token file too when one exists), so the dream job's agy shares the operator's real Keychain login: a refresh it makes reaches the interactive agy too. The HOME is removed after the run; a locked Keychain (no user logged in to the Mac) makes agy ask for a browser login, which it gives up on after its own 60-second wait, so each project fails in about a minute with `agy is not logged in inside the dream job's private HOME` (BUG-166).
- A project is dreamt over only once `sh-agent search` or `index` has run in it (the global index is the list).
- On Linux a user timer fires only while your user manager runs (a login, or `loginctl enable-linger`). `Persistent=true` runs a missed night at the next login.
- The CLI is the manifest's vendor (`all` uses Claude Code); `--vendor agy|claude` overrides it for a run by hand.
- Every real run first runs `sh-agent metrics ingest`. It reads the shape of both CLIs' sessions into `~/.config/sh-agent/metrics.db` (never sent by `report`; `uninstall` keeps it) and logs its summary line to `dream.log`. Each project's prompt then carries a numbers-only summary of its metrics. The store is the only copy once Claude Code or Antigravity deletes a transcript, so back it up now and then with `sh-agent metrics export <file>`. Look at it with `sh-agent metrics show` or `sh-agent metrics report` (spec `specs/sh-agent/metrics.md`).
- Output: `~/.config/sh-agent/proposals/dream/` (per project; `harness/` for harness targets, which `report` sends; `drafts/` for draft skills). A failed project's output goes to `dream-failed/`, and the log is `dream.log`. Nothing is applied until `/retro` does it.

## Configure

Nothing to configure. `--vendor VENDOR` selects the target CLI: `antigravity` (or `agy`) or `claude`; the default is the vendor recorded by the existing install, else `all` — passing `--vendor all` explicitly widens a single-vendor install to both, through `install` or `update`; without `--vendor`, both keep the recorded vendor. Any other value is a usage error on every verb, exit 2 before the verb does anything (BUG-171). The recorded vendor also decides whether `/init-project` gives projects a `.claude/`: with `antigravity` alone it writes nothing under `.claude/`; an Antigravity-only machine installed with the default `all` needs `sh-agent install --vendor agy` for that. `--repo DIR` overrides where the checkout is; `$SH_AGENT_REPO` does the same persistently. Without either, install and update discover the checkout by walking up from the working directory, then fall back to the path recorded at the last install; when that discovery finds a checkout OTHER than the one already on record — an unpacked copy, a second clone sitting in a harness-shaped directory — the build is refused rather than run and deployed: `"checkout %s is not the installed one (%s); pass --repo %s to build and install from it"`, or, on a release-binary install, `"this home was installed from the release binary %s, not from a checkout; pass --repo %s to build and install from it"`. `--home DIR` installs elsewhere and is what every development run uses (a scratch home under `.agents/scratch/`, never `/tmp`). From its own git checkout, `install` rebuilds the binary with `go build` first; the running binary is installed as is when `go` is missing (with a warning) or when the tree is a plain copy with no `.git` — an unpacked bundle (BUG-015).

## Verify

- `sh-agent status` shows the current commit, a `binary` line carrying the same revision (a warning there means the installed binary is older than the checkout or unstamped: run `sh-agent update`), and no drift, with no `shadowed` line — a checkout place can be shadowed the same way a release-binary place was (BUG-078). `sh-agent version` run unqualified must match what was just installed; if it does not, `hash -r` (bash) or `rehash` (zsh) first — the shell caches where it last found `sh-agent`, and `update` replaces only `~/.local/bin/sh-agent` (BUG-079; `update` says so when it ran from another copy, but only against the real home, never `--home` elsewhere — remedy is `ln -sfn` to the installed binary when that copy is yours, or, when it is not, removing it with the rights that own it or running the installed one by its path) — and if still wrong, an earlier `sh-agent` on `PATH` is running instead of the installed one (`type sh-agent` shows which). `install` writes `"attribution": {"commit": "", "pr": ""}` and `"autoCompactWindow": 300000` in `~/.claude/settings.json`, each when its key is absent (creating the file when needed, every other key untouched) and prints a `settings:` line per key written; `status` prints an advisory note per key the file does not set (for `attribution`, a different value counts as unset).
- `sh-agent render | head -3` prints the assembled context.
- Start each CLI once and check it read the files: Claude Code `/memory` lists `~/.claude/CLAUDE.md` importing `AGENTS.md`, `/skills` and `/agents` list the harness entries; Antigravity `/skills` and `/agents` do the same, and `grep 'Failed to parse' ~/.gemini/antigravity-cli/log/cli-*.log` is empty (BUG-004).

## First session after install

Claude Code asks once per session before the first read or write under `~/.config/sh-agent/` (feedback log, proposals, the retro index), because it is outside the project. Approve it, or add the directory to `permissions.additionalDirectories` in `~/.claude/settings.json` yourself — the installer touches no vendor setting except the attribution key (ADR-025). Antigravity prompts the same way for paths outside the workspace. The project's own memory store (`.agents/memory/`, written by `sh-agent memory` on both CLIs; Claude Code's own auto memory is switched off per project by `/init-project`) is inside the project, so it needs no such prompt.

## Operator steps

Standing by-hand procedures on this box, never pending items (those live in the newest handoff):

- After a push from another machine: `sh-agent update`, then `sh-agent status` shows a stamped `binary` line and no drift.
- After any change to `context/`, `agents/`, or `skills/` reaches this home: the three smoke checks of `.agents/docs/testing/2026-09-03-verify-real-home.md` on each CLI, from a fresh clone of the demo service.
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
