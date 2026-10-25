---
owner: main
date: 2026-10-25
---
# fyr — every verb on every kind of place

The verb × place-kind matrix, first built for `plans/2026-09-18-sh-agent-works-everywhere.md` and re-run under `fyr` on 2026-10-25 (audit IN19, plan `2026-10-25-harness-audit-fix.md` D14). A cell is `exercised` (ran, output logged), `refused` (a message naming the cause and the alternative), `n/a` (the verb has no meaning there), `test` (covered by the named Go test, not run by hand here) or `not run` (with the reason). Nothing is blank. The 2026-09-18 cells, recorded under the old binary name, and the real-machine records of that day are in this file's git history (`git show 3797da5:.agents/docs/testing/fyr-matrix.md`).

## How the cells were run (this box, 2026-10-25, tree 3797da5)

- **Scripts.** `.agents/scratch/p5/matrix.sh`, `matrix2.sh` and `matrix3.sh`, with logs `run.log`, `run2.log` and `run3.log` beside them (gitignored).
- **Go.** `/usr/bin/go` go1.24.4 (plan D16).
- **The binary's code.** The matrix ran on tree `3797da5`. The Phase 5 commit changes no code the binary carries (one test comment only).
- **Place kinds.** Three kinds, built from a `git clone` of this checkout:
  - **checkout**: the clone itself, driven by a development build (`go build ./cmd/fyr`) with `--repo`.
  - **plain copy**: `git archive HEAD | tar -x`, with no `.git`, driven by the same development build with `--repo`.
  - **release binary**: `go build -trimpath -ldflags "-X main.version=v9.9.9"` of the clone, with no `--repo`.
- **Homes.** Each kind gets a fresh `--home` under `.agents/scratch/p5/matrix/homes/`.
- **PATH.** It holds only links to the tools the verbs call. There is no `gh`, so nothing reaches the network.
- **Environment.** `GIT_CEILING_DIRECTORIES` is set to the matrix directory, so the plain copy never finds this checkout's `.git`. No `FYR_*`, `SH_AGENT_*` or `GIT_*` variable is set.
- **`version`.** It was run without `--home`. Its stderr names this box's real `~/.config/sh-agent`, which has not crossed yet. The line says it reads nothing from it.

## Matrix

| Verb | Checkout | Plain copy | Release binary |
|---|---|---|---|
| `version` | exercised: `fyr 3797da5f3c2b` | exercised: the same | exercised: `fyr v9.9.9` |
| `install --dry-run` | exercised: `[dry-run] the binary would be rebuilt from …/checkout`, 896 `add` lines, nothing on disk | exercised: `[dry-run] plain copy, not its own git checkout: the running binary would be installed as is` | exercised: `[dry-run] release binary v9.9.9: the harness built into it and the running binary would be installed` |
| `install` (fresh home) | exercised: `building fyr from …/checkout`, 896 `add` | exercised: `plain copy, not its own git checkout: the running binary is installed as is`, 896 `add` | exercised: `release binary v9.9.9: installing the harness built into it and the running binary`, 896 `add` |
| `status` | exercised: `repo …/checkout (… @ 3797da5, clean)`, `installed … from commit 3797da5 (896 files, manifest ~/.config/fyr/state.json)`, `binary 3797da5f3c2b`, `drift none — 895 files match the repo`, exit 0 | exercised: `repo …/plain (plain copy, not its own git checkout; update is refused)`, `installed … from commit ` (no commit to name), drift none, exit 0 | exercised: `source embedded v9.9.9 (release binary; update downloads the newest release)`, `installed … from release v9.9.9`, `binary 3797da5f3c2b` (a local build has no module version), drift none |
| `list` | exercised: 36 skills, 8 agents, 0 commands, 2 rules | exercised: the same | exercised: the same, from the embedded files |
| `render` | exercised: the marker line, then `# AGENTS.md — global agent context` | exercised | exercised |
| `sync` | exercised: `0 written, 896 unchanged, 0 removed, 0 kept — 896 files managed` | exercised: the same | exercised: the same |
| `diff` (after a drifted file) | exercised: a unified hunk `--- installed ~/.gemini/config/rules/AGENTS.md` / `+++ repo …`, `-drift`, exit 0 | exercised | exercised |
| `status` (after a drifted file) | exercised: `differs ~/.gemini/config/rules/AGENTS.md`, `drift 0 missing, 1 differ, 0 stale — run `fyr diff`, then `fyr install``, exit 1 | exercised, exit 1 | exercised, exit 1 |
| `install` (repairs drift) | exercised: `update ~/.gemini/config/rules/AGENTS.md`, `1 written, 895 unchanged` | exercised | exercised: `1 written, 895 unchanged, 0 removed, 0 kept — 896 files managed` |
| `update --check` | exercised: `up to date with upstream (3797da5)`, exit 0 | refused: `not its own git checkout; a plain copy cannot be updated with git — `install` still deploys it as it is; to move forward, use a checkout (`git clone git@github.com:kesokaj/fyr.git`) or a release binary (README § Install without cloning)`, exit 1 | refused (no `gh` on PATH): `update: this install has no checkout; install gh and run gh auth login, or download a newer release by hand (README § Install without cloning)`, exit 1. The path with `gh` and a newer published release is `TestUpdateFromRelease*` and a real place's (Phase 6 of plan `2026-10-24-fyr-agy-only.md`) |
| `update` | exercised: `up to date with upstream (3797da5)`, `building fyr from …/checkout`, `0 written, 896 unchanged` | refused: the same message as `--check` | refused (no `gh`): the same message as `--check` |
| `adopt <dir>` | exercised: `adopted into …/checkout/skills/widget`, `next: review it, run `fyr install`, then commit and push the repo` | exercised: `adopted into …/plain/skills/widget`, `next: … then copy it into your harness checkout and commit there — this tree is a plain copy with no repository` | refused: `adopt needs a harness checkout (`git clone git@github.com:kesokaj/fyr.git`, then run adopt from a build of it); this install is a release binary and carries no tree to adopt into`, exit 1 |
| `uninstall --dry-run` | exercised: lists what would be removed, `~/.local/bin/fyr` included; nothing removed | exercised | exercised |
| `uninstall` | exercised: `removed 896 files and the manifest` | exercised: the same | exercised |
| `status` (after uninstall) | exercised: `installed nothing — run `fyr install``, exit 1 (and `uncommitted changes` on the clone, from the adopted widget) | exercised, exit 1 | exercised, exit 1 |
| `install --repo <tree>` | n/a (it IS the tree) | n/a | refused: `this is the release binary v9.9.9: it installs the harness built into it and never a tree on disk, so --repo=… is refused — unset it, or build fyr from that tree and run that build's install`, exit 1 |
| `$FYR_REPO` set | n/a | n/a | refused: the same, naming `$FYR_REPO=…`, exit 1 |
| `list --check` (a flag the verb does not define) | refused: `flag provided but not defined: -check` plus the verb's usage, exit 2 | refused: the same, exit 2 | refused: the same, exit 2 |
| `list foo` (a stray argument) | refused: `list takes no arguments: ["foo"]`, exit 2 | refused: the same, exit 2 | refused: the same, exit 2 |
| `index` | exercised: `files 1892` | refused: `search: not inside a git checkout; use grep`, exit 1 | exercised in the checkout clone (the `search` row) |
| `search <words>` | exercised: `search: 10 hits for "zip verify", 10 with every word (index current: 1892 files, reconciled 0 changed in 183 ms; left out by a noindex marker: skills/google-catalog/catalog/)` | refused: the same message, exit 1 | exercised in the clone: the same, 230 ms |
| `search --refs ADR-040` | exercised: `ADR-040  .agents/docs/architecture/DECISIONS.md:383  Superseded by ADR-041`, `referenced by 27:` | refused: the same message | test (`TestRefs*`); the code path is the checkout's |
| `search --open --kind plan` | exercised: `search: 2 open records`, `2026-10-25-harness-audit-fix.md` (42 unticked) and `2026-10-24-fyr-agy-only.md` (6 unticked) | refused: the same message | test; the code path is the checkout's |
| `index --rebuild` | exercised: the same counts after a rebuild | refused: the same message | test; the code path is the checkout's |
| `search --global zip` | exercised: the checkout registered itself; `10 hits … (index current: 1634 files …)`, `checkout:.agents/docs/plans/2026-09-28-signed-zip.md` first | exercised: answers outside a checkout, `0 hits` from a fresh home (nothing registered) | test; the code path is the checkout's |
| `memory add` / `memory show` | exercised: `saved …/.agents/memory/prefer-tables.md` (body on stdin), `show` prints `- [prefer-tables](prefer-tables.md) — reports use tables`; the store is 0700 and its files 0600 | refused: `fyr memory: not a git checkout: …/plain`, exit 2 | not run: the verb runs before the harness tree is resolved, so the place kind never reaches it |
| `memory show` on a symlinked index | test: `TestSymlinkedIndexRefused` (`internal/memory`): exit 2, "is a symlink: the index must be the store's own file", the link target unchanged | not run: the same verb-before-tree path | not run: the same |
| `dream --all --dry-run` / `dream --project DIR --dry-run` | exercised: `[dry-run] …/checkout: memory 2, feedback 0, trail 36, skill 0, metrics 0, transcript 0; 260036 bytes; would run: agy --input-format stream-json …`, every input over the 256 KB cap named `left out`, exit 0 | not run: runs before the tree is resolved | not run: the same |
| `dream --all` (real CLI) | not run on 2026-10-25: it sends each project's redacted content to the model; last exercised 3 of 3 on Antigravity (`testing/2026-09-30-verify-parity-memory-dream-phase-4.md`) | not run: as above | not run: as above |
| `dream --install-timer` / `--remove-timer` | test: against a fake `systemctl` on PATH (`TestInstallSaysATurnedOffTimerStaysOff`, `TestUninstallDryRunLeavesTheTimer`); the real user manager is the operator's step | not run: as above | not run: as above |
| `metrics ingest` | exercised: on the kit's `canary-p5` sandbox home after its real Antigravity session (`~/sandbox/agy-rel/homes/canary-p5`): `metrics: 1 sessions, 3 steps, 0 events added; 0 lines skipped (unknown shape); 0 sources failed` | not run: the verb does not depend on the place kind | not run: the same |
| `metrics show` | exercised: with no store, `fyr metrics: no metrics store at …; run `fyr metrics ingest` first`, exit 1; on `canary-p5`, `main sessions 1`, `tokens in+out 18662` | not run | not run |
| `metrics report` / `metrics export` | exercised: `metrics: report written to …/canary-p5/.config/fyr/reports/2026-10-25-1441-metrics.html`; `export` with no file prints `fyr metrics export FILE`, exit 2; the export itself is `TestMetricsVerb` | not run | not run |

## The crossing: state dir, the zip place and D32 (added 2026-10-25)

Every row below ran with the checkout kind's development build (`--repo` the clone), except the zip rows. Those ran with the release binary, as a zip place does.

| Row | Setup | Result |
|---|---|---|
| Migration, read-only verbs | a home holding only `~/.config/sh-agent/feedback.jsonl` | `status` (exit 1, nothing installed), `diff`, `uninstall --dry-run` and `install --dry-run` each print `fyr: ~/.config/sh-agent holds the state from before the rename; this command reads nothing from it — run fyr install to migrate ~/.config/sh-agent to ~/.config/fyr`; afterwards `~/.config` holds `sh-agent` only |
| Migration, `install` | the same home | `migrated ~/.config/sh-agent → ~/.config/fyr`, `897 written` (the clone carried the adopted widget), exit 0; afterwards `~/.config` holds `fyr` only |
| Both directories present | `~/.config/sh-agent` and `~/.config/fyr` | `both ~/.config/sh-agent and ~/.config/fyr exist; fyr reads ~/.config/fyr and leaves ~/.config/sh-agent as it is — merge or remove it by hand`, install exit 0 |
| Old directory is a symlink | `~/.config/sh-agent` → a directory elsewhere | refused: `~/.config/sh-agent is a symlink, and fyr does not move a link: move what it points to to ~/.config/fyr (or point a ~/.config/fyr link at it), then run the command again`, exit 1; the link and the home unchanged |
| D32: the old manifest's `~/.claude` entries | an old manifest (`"vendor":"all"`) listing four `~/.claude` files and `~/.local/bin/sh-agent`, plus the operator's `~/.claude/settings.json` | `migrated …`, `remove ~/.local/bin/sh-agent (no longer in the source)`, `keep ~/.claude: 4 files an install before fyr wrote for Claude Code; fyr never removes them, and they leave its manifest`, `896 written, 0 unchanged, 1 removed, 4 kept`; the five files under `~/.claude` are sha256-identical before and after; a second `install` prints `0 written, 896 unchanged, 0 removed, 0 kept`; `uninstall --dry-run` names no `~/.claude` path; `status` exit 0, drift none |
| Zip, no `SHA256SUMS` beside it | `git archive --format=zip --prefix=fyr-v9.9.9/` of the clone, named `fyr-v9.9.9.zip` | `--dry-run` and the real run: `zip … (sha256 3f5a4ed7…, UNVERIFIED — no SHA256SUMS beside it; trust rests on your own download): installing its harness and the running binary v9.9.9; nothing is downloaded and nothing in the zip replaces the binary`, `896 written`; `status`: `installed … from zip sha256:3f5a4ed71c34 (unverified)`, `drift not compared — installed from a zip that is not kept; 896 of 896 manifest files present` |
| Zip, `SHA256SUMS` beside it | `# fyr v9.9.9` and the zip's line | `CHECKED against the SHA256SUMS of release v9.9.9 (integrity only)`, `896 written`, exit 0 |
| Zip, `SHA256SUMS` mismatch | a wrong digest for the zip | refused: `checksum mismatch for fyr-v9.9.9.zip against the SHA256SUMS of v9.9.9: the zip is not the one that release published — download fyr-v9.9.9.zip from the release's assets again (GitHub's "Source code" archive is a different file)`, exit 1; nothing written under the home |

## Antigravity rows (kit)

The context these cells deploy was read by the CLI, on agy, from sandbox homes the kit installed from this worktree with the Phase 5 context. The kit is a scratch copy of `.agents/kit/agy-rel/run.sh` with the `canary` row (kit5) and `ctx-deploy` (from `skill-matrix.md`), at `.agents/scratch/p5/kit/run.sh`, run with `AGY_TAG=p5`. **2 of 2 PASS**. Each row has the preamble's three clean lines: no processes left, real config unchanged, no credentials left in KIT_HOME.

| Row | Result | Evidence (`~/sandbox/agy-rel/runs/<row>-p5/out.txt`) |
|---|---|---|
| `canary` | PASS 1/1 (9 s) | `GANNET`; the print-mode sentence quoted word for word, with the new `(how to wait: `handoff` § Usage)` ending; the eight subagents; the post-dispatch sentence |
| `ctx-deploy` | PASS 1/1 (11 s) | Definition of Done step 3 quoted; the docs layout line; GCP defaults in `docs/deploy/gcp.md` |

## Silent cells

None. Every cell ran with output, refused with a message that names the cause and the alternative, or names the test or the reason it was not run.

## Remainder (no machine here)

- `linux/arm64`: built and published for every release since v1.1.0; never executed.
- `update` from a release binary with `gh` present on this box is not run here by rule: the operator's machines are the binary-only places. The real places cross in Phase 6 of plan `2026-10-24-fyr-agy-only.md`.
