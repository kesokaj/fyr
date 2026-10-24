---
owner: main
date: 2026-10-24
target: remote
---
# Deploy — other machines

The harness reaches a machine one way: `sh-agent` (R8, R9, ADR-030, ADR-033); what each kind of place fetches is `specs/sh-agent/delivery.md` § The model, on the operator's own credentials; there is no push-based deploy, no CI, no hooks, no service.

## First install on a machine

```bash
git clone git@github.com:kesokaj/styrman-harness.git ~/workspace/styrman-harness
cd ~/workspace/styrman-harness
go run ./cmd/sh-agent install
sh-agent status
```

`install` writes only under `~/.gemini/config` and `~/.local/bin`; it never touches `~/.claude/settings.json` (ADR-062). Check `sh-agent status` ends with "drift none".

On a machine that installs from a RELEASE BINARY instead, the download is verified before it is moved and the install is verified after it — two different checks, and skipping the second is what BUG-078 cost:

```bash
# One chained command: a failed check stops the move. V is the version you expect, from OUTSIDE
# the download (you cut it, or read it on a machine you already trust); ASSET is sh-agent-linux-amd64,
# sh-agent-darwin-arm64 or sh-agent-linux-arm64. macOS: shasum -a 256 -c -
V=vX.Y.Z && ASSET=sh-agent-linux-amd64 \
&& D=$(mktemp -d) && cd "$D" \
&& gh release download "$V" --repo github.com/kesokaj/styrman-harness --clobber \
     --pattern "$ASSET" --pattern 'SHA256SUMS' --dir "$D" \
&& grep -qxF "# styrman-harness $V" SHA256SUMS \
&& grep -v '^#' SHA256SUMS | sha256sum -c - --ignore-missing \
&& chmod +x "$ASSET" \
&& test "$("./$ASSET" version)" = "sh-agent $V" \
&& mv "$ASSET" ~/.local/bin/sh-agent && cd ~ && rm -r "$D"
# The `grep` and the `test` tie the checksum file and the binary to the version YOU named — what
# `sh-agent update` does with the tag it asked GitHub for (update also refuses a tag that is not
# newer than what it has, which no first install can mirror). The checksums are not signed
# (ADR-053): they prove the download is whole and is the file the release lists; who made the
# release rests on your GitHub credential and HTTPS. The checksum file leaves with "$D". A system
# path instead of ~/.local/bin is a `sudo mv` on that one line and changes nothing else.

hash -r                               # bash (zsh: rehash): the shell caches where it last found sh-agent
sh-agent version                      # unqualified: what the SHELL resolves, after the move
type sh-agent                         # when the version is wrong: the path the shell actually runs, cached or not
which -a sh-agent                     # every candidate, in PATH order
```

`sh-agent version` unqualified is the one that matters, and `hash -r` comes first because a shell caches where it last found `sh-agent`: `update` replaces `~/.local/bin/sh-agent` and nothing else, so a shell that once resolved the name to a copy elsewhere keeps running that copy whatever PATH says — devbox stayed on v1.2.1 this way after a successful update to v1.2.2 (BUG-079); `update` now says when it ran from such a copy — but only when the update ran against the real home (never a `--home` elsewhere), and only when the copy it ran from was actually written; the remedy is `ln -sfn` to the installed binary when that copy is yours, or, when it belongs to someone else, removing it with the rights that own it, or leaving it and running the installed one by its path. A binary moved into a system path is not installed if an earlier `PATH` entry holds another `sh-agent`: that one runs, installs whatever source it resolves, and reports `drift none` about it. It tells stale from fresh, never genuine from forged — it is the suspect binary's own output; authenticity is the signature and checksum on the file, before the move. `install` and `status` name such a binary now (a `warning:` line and a `shadowed` line), and a symlink from the earlier entry to the installed binary settles it — it runs the same file, so it is not a shadow. `--clobber` matters for the same class of reason: without it a leftover file from an earlier attempt makes the download refuse, and the stale file is what gets moved.

## The four places

The operator installs the harness in four places: the work Mac (Antigravity only), the home Mac (both CLIs), and two Linux hosts where they work on other projects (both CLIs). Every one is reached on the operator's own SSH key and `gh` login — there are no deploy keys and no per-machine credentials, because the operator authenticates as themselves everywhere and a key would restrict nothing (ADR-030 D1). A place gets the harness one of three ways, named in `.agents/docs/specs/sh-agent/delivery.md` § The model:

- **A checkout the operator owns** — the normal path. First install as above; after that, `sh-agent update` whenever the operator decides the harness has changed. A timer (§ The resync loop) is a convenience a machine may add, not the mechanism.
- **A release binary** — one downloaded `sh-agent` with `context/`, `skills/`, `agents/`, `commands/` and `rules/` built into it, for a place that only USES the harness and should not have a checkout of it sitting beside its projects. `sh-agent install` installs what the binary carries and copies the binary itself; `sh-agent status` shows `source embedded vX.Y.Z`; `sh-agent update` downloads the newest release, checks it against its `SHA256SUMS` and replaces `~/.local/bin/sh-agent`, so the place changes only when a release is cut. `sh-agent adopt` needs a checkout and says so. See `README.md` § Install without cloning and `local.md` § Update a release-binary install.
- **A zip place** — a repository zip downloaded by hand, unpacked and installed with the running binary by `sh-agent update --from-zip <file>`, which then deletes the unpacked copy; nothing is fetched and nothing in the zip replaces the binary, so the operator moves it forward by repeating the download and `--from-zip`. See `README.md` § Install from a zip (no SSH, no gh).

A plain copy with no `.git` — an unpacked bundle — still installs, and `update` refuses it, naming `install` still deploying it as it is and the alternatives above (`local.md` § From a plain copy); it is not one of the ways in, it is the fallback when none of the three can reach GitHub, and it differs from a zip place in what is kept — a plain copy is a tree someone unpacked and left on disk, while a zip place keeps no tree at all.

Nothing else carries the harness: no copy vendored inside a project, no per-project copy, no service. A project that wants the harness installs it with `sh-agent` into a home. That home receives installed FILES — `sh-agent install` writes the context, skills, agents, commands and rules and creates no repository. That alone does not stop a change flowing back: changing the harness takes a credential that can push to this repository, not a checkout, and anything that reaches the operator's SSH key or `gh` login can clone and push. A home seeded for agents is therefore read-only only when it reaches no such credential — no forwarded ssh-agent, no `gh` token, no readable key. This repository names no consumer project and depends on none (R8).

Nothing flows back: a place's feedback log, retro proposals and index stay under `~/.config/sh-agent/` on that place, and the project's own memory store stays inside the project at `.agents/memory/`; the one thing that leaves a place is the dreamer's redacted prompt (ADR-062; `local.md` § What stays on this machine).

## The resync loop

```bash
# machine A: change, verify, install, commit, push
sh-agent install && git add <the file> && git commit -m "context: tweak" && git push
# machine B
sh-agent update            # fetch; refuse a dirty checkout; if behind: pull --ff-only; rebuild and reinstall
sh-agent update --check    # only reports; exit 2 when behind, 0 when current; on a dirty checkout it prints the real run's refusal and exits 1 (so does --dry-run)
```

`sh-agent update` finds the checkout from `$SH_AGENT_REPO`, then the working directory or any of its parents, then the path recorded at the last install, so it works from anywhere. It refuses a non-fast-forward pull; resolve that by hand in the checkout.

**`update` refuses a checkout with uncommitted changes, whether or not it is behind.** It deploys the WORKING TREE, not the commit it just fetched, so on a machine where the harness is also being edited it would put half-finished work into the live context — which is what an hourly timer did here on 2026-09-16 (BUG-046). Commit or stash, or run `sh-agent install` to deploy a dirty tree deliberately. That makes `update` safe to run from a timer:

```ini
# ~/.config/systemd/user/sh-agent-update.service  — with the matching hourly .timer
[Service]
Type=oneshot
ExecStart=%h/.local/bin/sh-agent update --repo %h/workspace/styrman-harness
Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin
```

On a box where nobody edits the checkout the timer simply keeps it current; on a box where somebody does, it now declines instead of deploying their draft.

The timer follows `main` by fast-forward: its trust root is whoever can push to this repository, exactly as a hand-typed `sh-agent update` is. That is the operator, on their own machines, and the accepted position (ADR-030). A box where that is not acceptable runs `sh-agent update --check` on the timer and updates by hand (on a dirty checkout that run exits 1 with the refusal, not 2). What a tag delivers, and to which place: `specs/sh-agent/delivery.md` § The model.

## Releases — tags, as release markers

A release is an annotated `vX.Y.Z` tag on `main`, and the binaries published with it, with their `SHA256SUMS`. No key signs anything (ADR-053, superseding ADR-026's signing, ADR-033's signed checksums and ADR-052). What a tag delivers, and to which kind of place, is `specs/sh-agent/delivery.md` § The model (ADR-033, superseding ADR-030 D2; the history of the channel is there too). A release is cut by `/preflight vX.Y.Z` from the operator's own turn, from this checkout (never a plain copy), on any machine where the operator is logged in to GitHub (`gh auth status`). Nothing else is tagged: no lightweight tag, no `-rc` suffix, no other prefix. Only a canonical `vX.Y.Z` (no leading zeros, e.g. never `v1.02.2`) counts as a release tag anywhere in this repository's tooling, so never cut one with a leading zero — every place refuses it. The tag is the only version tracking; there is no version file.

Procedure — what `/preflight` runs at its Step 6 here (the skill's Step 6.3 follows a project's Release section), each command its own call, its exit status read before the next; `<gated head>` is the head SHA of the gate report's `Range reviewed:` line, the one external input — the tag itself takes no target, so no placeholder can sit on both sides of a check:
```bash
gh auth status                                                                                          # exit 0: this machine can push and publish
# gate passed and the release commit made (preflight Step 6.3): HEAD is that commit; <gated head> is the head SHA of the gate report's `Range reviewed:` line
go test -count=1 -run TestDeliverySpecCurrent ./cmd/sh-agent                                           # exit 0: the release entry under changes/ and `specs/sh-agent/delivery.md` § Where things stand both name vX.Y.Z (BUG-167)
test -z "$(git status --porcelain --untracked-files=all)"                                              # exit 0: clean tree — the bare form inherits status.showUntrackedFiles (BUG-049)
# BEFORE the tag, never after the push: `go:embed` reads the WORKING TREE, so a stray file under the
# five embedded directories is compiled into every released binary. The clean-tree test above cannot
# see an IGNORED file, and a tag is unrecallable once pushed (D23, BUG-066; security review 2026-09-17)
test -z "$(git status --porcelain --ignored --untracked-files=all -- context skills agents commands rules)"   # exit 0: nothing untracked or ignored there
go test -count=1 -run TestEmbedHoldsOnlyTrackedFiles ./...                                             # exit 0: the embedded set is exactly what git tracks
test "$(git rev-parse HEAD^)" = "$(git rev-parse <gated head>)"                                        # exit 0: the release commit sits on the gated head
git diff --quiet <gated head> HEAD -- ':(top)' ':(top,exclude).agents/docs'                            # exit 0: records only — ':(top)' because a bare '.' is relative to the cwd and misses a change in a sibling directory (measured)
git tag -a vX.Y.Z -m "styrman-harness vX.Y.Z"                                                         # no target argument: tags HEAD
# check the tag before any push — every line carries its verdict in its exit status:
test "$(git rev-parse vX.Y.Z^{commit})" = "$(git rev-parse HEAD)"                                      # exit 0: the tag is on the release commit
git merge-base --is-ancestor vX.Y.Z^{commit} main                                                       # exit 0: on main
test "$(git cat-file -t vX.Y.Z)" = tag                                                                  # exit 0: annotated ("commit" means lightweight)
git push origin main
git push origin vX.Y.Z                                                                                  # plain pushes, never --force
```

Then the release's binaries (`specs/sh-agent/delivery.md` § The model; R9, ADR-033). Built here, from the tagged tree, never by a service:
```bash
# 0. the contamination guard AGAIN, here: it ran before the tag so a bad tree is
#    never tagged, and it runs again here because `go:embed` reads the working
#    tree at BUILD time — which is after the tag and the pushes, so a .DS_Store
#    or an editor swap file appearing under skills/ in between would otherwise be
#    embedded unguarded (D23, BUG-066; code review 2026-09-17 round 2). Both
#    commands are read-only, so running them twice costs nothing.
test -z "$(git status --porcelain --ignored --untracked-files=all -- context skills agents commands rules)"   # exit 0: nothing untracked or ignored there
go test -count=1 -run TestEmbedHoldsOnlyTrackedFiles ./...                 # exit 0: embedded set == git's
# 1. the three platforms, from the tag's own tree, reproducible flags
for target in linux/amd64 darwin/arm64 linux/arm64; do
  GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 GOTOOLCHAIN=local \
    go build -trimpath -ldflags "-X main.version=vX.Y.Z" \
    -o ".agents/scratch/release-vX.Y.Z/sh-agent-${target%/*}-${target#*/}" ./cmd/sh-agent
done
# 1b. the zip of the tagged tree, for a zip place (ADR-039): `update --from-zip` checks it
#    against this release's SHA256SUMS when that sits beside it
git archive --format=zip --prefix=styrman-harness-vX.Y.Z/ -o .agents/scratch/release-vX.Y.Z/styrman-harness-vX.Y.Z.zip vX.Y.Z
# 2. the checksums, with the VERSION as the first line — `sh-agent update` refuses a
#    release whose SHA256SUMS names another version or none (D26 of the
#    release-binary plan), and `sha256sum -c` ignores a '#' line
cd .agents/scratch/release-vX.Y.Z
printf '# styrman-harness %s\n' vX.Y.Z > SHA256SUMS
sha256sum sh-agent-linux-amd64 sh-agent-darwin-arm64 sh-agent-linux-arm64 styrman-harness-vX.Y.Z.zip >> SHA256SUMS
sha256sum -c SHA256SUMS                                                    # exit 0: the file describes these files
# 3. BEFORE anything is published: this host's own built binary checks the release zip
#    against SHA256SUMS, as a zip place will (ADR-039)
mkdir -p ../zipcheck && "./sh-agent-$(go env GOOS)-$(go env GOARCH)" update --from-zip styrman-harness-vX.Y.Z.zip --home ../zipcheck --dry-run > ../zipcheck.out   # into a file, never a pipe (BUG-086)
grep -q "CHECKED against the SHA256SUMS of release vX.Y.Z" ../zipcheck.out   # exit 0
# 4. the release, with notes read by the operator before the upload — created with the
#    notes ALONE, then one upload per asset: a create carrying every asset rolls the whole
#    release back when a single upload fails, which is what GitHub did twice for v1.1.0
#    (`HTTP 500: Error creating asset temp dir`), while one call per file then worked first
#    time each. Retry an individual upload before calling it a failure.
gh release create vX.Y.Z --repo github.com/kesokaj/styrman-harness --verify-tag --title "styrman-harness vX.Y.Z" \
  --notes-file ../release-notes-vX.Y.Z.md
for a in SHA256SUMS sh-agent-linux-amd64 sh-agent-darwin-arm64 sh-agent-linux-arm64 styrman-harness-vX.Y.Z.zip; do
  gh release upload vX.Y.Z --repo github.com/kesokaj/styrman-harness "$a" --clobber || echo "retry $a by hand"
done
gh release view vX.Y.Z --repo github.com/kesokaj/styrman-harness --json assets --jq '.assets[].name'   # all five (six for v2.4.0, with the bridge's SHA256SUMS.sig)
cd -
```

**The bridge, v2.4.0 only (ADR-053).** Every binary up to v2.3.2 refuses a release without a `SHA256SUMS.sig` signed by the old key, so v2.4.0 — the first release whose binary needs none — carries one, made once with that key, and is the last release that does. Run this after step 2, in the same directory, and add `SHA256SUMS.sig` to step 4's upload list:
```bash
RETIRED=<path of the old private key file>                        # it exists only for this block
test -n "$RETIRED" && test -f "$RETIRED" && test "$(ssh-keygen -y -f "$RETIRED" | ssh-keygen -lf - | cut -d' ' -f2)" = SHA256:5HpYQJjpG+yE1xXBTOIZPsTkbkgkVnV8z0eG8hBawyU   # exit 0: RETIRED is set, is a file, and its PRIVATE half is the old key (a .pub beside it is not read) — nothing else is used, or deleted below
ssh-keygen -Y sign -f "$RETIRED" -n sh-agent-release SHA256SUMS
printf '%s\n' 'ostling.simon@gmail.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkx0BICH1xG18f0a0F/02Vw0OvBTfSOVNXkPkO62NCR' > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I ostling.simon@gmail.com -n sh-agent-release -s SHA256SUMS.sig < SHA256SUMS   # exit 0: what a v2.3.2 place checks
rm -f allowed_signers "$RETIRED" && test ! -e "$RETIRED"           # exit 0: the old key is off the disk
# a v2.3.2 binary, built from its own tag, accepts the release
mkdir -p ../v232src && git -C "$(git rev-parse --show-toplevel)" archive v2.3.2 | tar -x -C ../v232src   # -C: from a subdirectory, a bare git archive packs only that subdirectory
(cd ../v232src && CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -ldflags "-X main.version=v2.3.2" -o ../sh-agent-v2.3.2 ./cmd/sh-agent)
mkdir -p ../zipcheck232 && ../sh-agent-v2.3.2 update --from-zip styrman-harness-v2.4.0.zip --home ../zipcheck232 --dry-run > ../zipcheck232.out   # into a file, never a pipe (BUG-086)
grep -q "VERIFIED against the signed SHA256SUMS of release v2.4.0" ../zipcheck232.out   # exit 0: a v2.3.2 place accepts v2.4.0
```

The release notes are built as § Release notes below says. Before this repository is ever made public, read the trail for what it names (this box's login-shell credential broker, `/etc/profile.d/zz-styrman-credentials.sh`, is described in `bugs/BUG-160-*.md` § Addendum and the 2026-10-07 security reviews under `reviews/`): `.agents/docs/` carries hostnames (`spgo-mac.roam.internal`, `devbox`), machine paths (`/opt/devbox/deploy/harness`), links to the operator's private claude.ai artifacts (the "Harness Delivery Map" page, in the 2026-10-09 handoff and changes entry), and a report record's contents, all of which are fine inside a private repository and are internal identifiers outside one. There is no secret in them — that is checked every release — but publication is a separate decision from releasing, and this is the paragraph that says so (security review 2026-09-18).

Publishing a release changes NOTHING about the repository's visibility: `styrman-harness` stays private, the release page is readable only by accounts that can already read the repository, and every asset download needs that credential — which is what README § Install without cloning depends on. `gh repo view github.com/kesokaj/styrman-harness --json visibility` after the upload is the check; only a deliberate change in the repository's settings makes anything public.

Upload the assets one call at a time, not all of them on `gh release create`: a single create-with-assets call ROLLS THE WHOLE RELEASE BACK when one upload fails, which is what GitHub's upload endpoint did twice for v1.1.0 (`HTTP 500: Error creating asset temp dir`, both attempts leaving no release and the tag intact). Create the release with `--notes-file` alone, then `gh release upload vX.Y.Z --repo github.com/kesokaj/styrman-harness <asset> --clobber` per file, retrying an individual upload before calling it a failure.

Since the Google skills catalog (plan `2026-10-01-google-skills-catalog`) each binary embeds it, about 6 MB more (linux/amd64: 19.9 MB), and the zip of the tree is about 7 MB with some 2,575 entries, inside the 5,000 entries `update --from-zip` accepts; the uploads take correspondingly longer, nothing else in the procedure changes.

After the upload: `gh release view vX.Y.Z --repo github.com/kesokaj/styrman-harness --json assets --jq '.assets[].name'` lists the five assets (six for v2.4.0), and nothing further is pushed anywhere — a binary-only place picks the release up the next time the operator runs `sh-agent update` there.

### Release notes

Every release carries notes, built from the `changes/` entries dated after the previous tag's commit and read by the operator before the upload (D19). Write `.agents/scratch/release-notes-vX.Y.Z.md` with one line per entry under the groups that have any:

```markdown
## New features
- <entry title> — <one line, from the entry's Summary>
## Improvements and optimisation
## Fixes
- <entry title> (BUG-NNN)
## Other updates
## Install
Download: see README § Install without cloning. Existing binary-only places: `sh-agent update`.
```

An entry's `## Type` decides its group: `feature` → New features; `task` or `refactor` that changes behaviour → Improvements and optimisation; `bugfix` → Fixes, with its BUG id; rules, skills, docs and context → Other updates. An empty group is left out. The same text goes into the release's own `changes/` entry.
A tag that fails any check: `git tag -d vX.Y.Z`, fix, re-gate. A pushed tag is never moved or deleted; a bad release gets the next PATCH. `git` is never run under `sudo`. The tag checks above (`test` assertions, tag-on-HEAD, parent-is-gated-head, records-only diff, ancestor, annotated) were rehearsed 2026-09-16 in `.agents/scratch/tagcheck/`: passing on a records-only release commit tagged at HEAD, failing on a tag made on the earlier commit, on a release commit carrying a source change, on a dirty tree, and on a lightweight tag. Tags were SSH-signed until v2.3.2; from v2.4.0 they are not (ADR-053).

Version: MINOR for anything an installed place gains (a new or changed skill, agent, command, rule, context rule or installer behaviour), PATCH for a fix to released behaviour only, MAJOR for what an existing deployment must react to (a moved install path, a dropped vendor, a `state.json` or manifest shape change, a new required flag) — the context's Git rule. First tag `v1.0.0`, base ref for its gate 4aa19be. The gate's verification row: the `AGENTS.md` Setup commands, `go run ./cmd/sh-agent install --home .agents/scratch/home` with `status` reporting no drift, and `sh-agent list` — 34 skills and 8 agents at v2.0.0; a release that changes them says so in its changelog entry. There is no deploy step after the push: each place moves forward with `sh-agent update` when the operator runs it (`specs/sh-agent/delivery.md` § The model). The handoff after a release names the live tag.

## Verify

Same as `local.md`: `sh-agent version` unqualified matching what was installed, `sh-agent status` with no drift and no `shadowed` line, then one start of each CLI. On the Mac, additionally confirm the Antigravity paths — passed 2026-09-04 (`.agents/docs/testing/2026-09-04-verify-mac-real-home.md`); re-run after a change that touches Antigravity-specific behavior and record the result there.

## Operator steps

Standing by-hand procedures, never pending items (those live in the newest handoff):

- On the Mac after a push from the Linux box: `sh-agent update`, `sh-agent status`, then the three smoke checks on each CLI (`.agents/docs/testing/2026-09-03-verify-real-home.md`; the Mac procedure is `2026-09-04-verify-mac-real-home.md`).
- After any change to `context/`, `skills/`, or `agents/` is pushed: `sh-agent update` at each of the four places, when the operator decides to. Nothing else needs refreshing.
- At a place that has been in use, before its harness-target proposals can matter here: run `/retro --harness` in a harness checkout on that place, which reads its own queue under `~/.config/sh-agent/` (nothing carries a queue between places, ADR-062).
- On a new machine, `gh auth status` must pass before that machine cuts a release; no key is needed (ADR-053).
- Go upgrades on the Mac: `brew upgrade go`. On the Linux box the distribution had nothing newer, so it runs a user-level SDK on PATH ahead of `/usr/bin/go` — the procedure is in `local.md` § Operator steps, and it needs no root.

## Roll back

`git checkout <last good commit> && go run ./cmd/sh-agent install` on the affected machine; `sh-agent update` will refuse to move until the branch is fast-forwardable again, so push a fix or reset the branch first. A plain copy rolls back by installing the earlier copy. A release-binary place rolls back by downloading the previous release's binary by hand and running `sh-agent install` — `update` never goes backwards, on purpose (README § Install without cloning).

## Access

Remote boxes are reached only on the operator's explicit request with details they supply. Nothing in this repo assumes a host.
