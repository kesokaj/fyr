# Requirements — styrman-harness

> Immutable without the operator's explicit approval. Agents never edit this file.

## R0 — Purpose: the second brain of both CLIs

`sh-agent` is the one place Claude Code and the Antigravity CLI keep what a session cannot hold on its own: the context, skills, rules and agents (`install`), the trail and its index (`search`), the project memory (`memory`), the shape of every session (`metrics`) and the nightly consolidation (`dream`). A CLI consults it at the moments the context names — resume, before touching an area, at a phase end — and the harness grows there, in the binary and its specs, never in the context, whose hand-written part stays under its size pin: a new consult moment is one clause on an existing context line plus the verb's own usage text. The brain is consulted, never consulting: nothing in `sh-agent` acts on a CLI between its turns (R3), and a CLI only learns what it asks for, so a moment the context does not name never fires. (ADR-061.)

## R1 — One source, two vendors

`context/AGENTS.md` (plus `context/*.md`) is the single global context, deployed to Claude Code and the Antigravity CLI. Subagents are authored once in Claude Code format under `agents/`; the Antigravity flavour is derived at install time. Nothing generated is committed.

## R2 — Standards, not vendor formats

The context follows the agents.md standard and is named `AGENTS.md` at every level. A project has no `CLAUDE.md`: Claude Code 2.1.277+ reads `AGENTS.md` natively, and a one-line `@AGENTS.md` stub is written by hand only where it cannot. The init-project skill is the one home of that list (Claude Code versions and setups): it reaches every place, and this file only a checkout or a zip. The context points at it. `~/.claude/CLAUDE.md` stays a one-line import: Claude reads a user-level `AGENTS.md` only as an ancestor file, and a project with its own `CLAUDE.md`, or one outside the home directory, would then lose the global context. Native reading also loads an `AGENTS.md` in any directory above the project and, on read, in any subdirectory; a template `AGENTS.md` never sits beside source. Skills follow the Agent Skills `SKILL.md` standard so that anything from skills.sh or any other source drops into `skills/` unchanged.

## R3 — No hooks, no gates, no CI

The harness is context, skills, and subagents. Behaviour comes from what is written in them, not from lifecycle hooks, commit gates, or pipelines. Git is backup and versioning only. One exception: `sh-agent dream --all`, run nightly by an OS user timer that `sh-agent dream --install-timer` installs and `sh-agent dream --remove-timer` or `uninstall` removes, reads every indexed project's trail and memory, the feedback log and both CLIs' transcripts, and a numbers-only summary of the metrics store (its metric keys and values for each project's window and the window before, never a tool, skill, agent, label or path), first ingests both CLIs' transcripts into the metrics store (R4), runs a CLI with no tools over what it read, and writes only under `~/.config/sh-agent/`, its proposals to `proposals/dream/`; it never edits a project, a skill, a rule or the context, and a proposal takes effect only through `/retro`. (ADR-047a.)

## R4 — One binary, one dependency, easy on and off

`sh-agent` is a single Go binary. Its one dependency is `modernc.org/sqlite`, a pure-Go SQLite driver used only by the search index and the metrics store, pinned in `go.mod` and `go.sum` and not vendored; `sh-agent dream` also opens Antigravity's conversation databases with it, read-only; any other dependency, and any driver that needs cgo, is a requirements change. The search indexes (`.agents/scratch/search.db` in a project, `~/.config/sh-agent/search.db`) are caches derived from files git or `~/.config/sh-agent/` already holds: never committed, never sent by `report`, rebuilt when deleted. The metrics store (`~/.config/sh-agent/metrics.db`, not the metrics record `report` sends, R8) is not a cache: it holds the shape of every ingested session of both CLIs (tools, normalised command verbs, tokens, timings, never arguments or prose, and no path but each session's project root and the transcript files it read) and is the only copy once a transcript expires, so it is migrated forward and never rebuilt or deleted by the binary, never committed, never sent by `report`, and `uninstall` leaves it. `install` is idempotent and overwrites only files it manages; `uninstall` removes exactly what its manifest lists, and the dream timer when one is installed (R3); `update` pulls the repo from git and reinstalls. `update` refuses a checkout with uncommitted changes on every path, because it deploys the working tree rather than the commit it fetched; `install` stays the deliberate way to deploy a dirty tree. A hand-written `~/.claude/CLAUDE.md` is never overwritten. `install` also sets one key in the operator's `~/.claude/settings.json`, `attribution` (commit and PR trailers off), only when the key is absent; it never changes or removes another key, and `uninstall` leaves it. (ADR-045, ADR-047a.)

## R5 — Hand-added customizations are first class

Adding a skill, subagent, command, or rule is: put the file in `skills/`, `agents/`, `commands/`, or `rules/` (by hand, by `npx skills add` from the repo root, or by `sh-agent adopt`), then `sh-agent install`. No registration, no manifest edits, no code. The installer maps each directory to its place under both vendors.

## R6 — Development never touches the real home

Every test and every development run of the installer uses `--home` with a scratch directory. Deploying to the operator's real `$HOME` is the operator's action.

## R7 — Projects remember and stay resumable

Every project keeps its scratch in `.agents/scratch/` (gitignored), its memory store in `.agents/memory/` (written by `sh-agent memory` on both CLIs; gitignored, one per checkout, never sent by `sh-agent report`), its failures as numbered records in `.agents/docs/bugs/`, its progress in plan files with task checkboxes, and its retro, distill and dream records in `.agents/docs/retros/`, so a zero-context session can resume and does not repeat a mistake. The harness's own state (manifest, feedback log, the harness proposal queue, the cross-project index, the metrics store) lives in `~/.config/sh-agent/` and nowhere else; a project's retro and distill records are the project's own state, in its trail (ADR-015).

## R8 — One delivery channel, and it is `sh-agent`

The harness reaches a machine one of three ways: a checkout the operator owns; a release's `sh-agent` binary with the harness built in; or a zip of the repository that the operator downloads with their own credential and passes to `sh-agent update --from-zip <file>`. Each is kept current with `sh-agent update`: the checkout and the binary as today, and a zip place by the operator repeating the download and `--from-zip`. There is no other channel — no vendored mirror inside a consumer project, no per-project copy, no service. A project that wants the harness uses `sh-agent`; this repository names no consumer project and depends on none. Experience flows back the one way too: `sh-agent report` sends a COPY of this machine's corrections, proposals and a metrics record to the private feedback repository. The originals stay in `~/.config/sh-agent/`, which R7 keeps as the one place that state lives; a copy leaving for the operator's own private index is not a second home for it. (ADR-030, ADR-033, ADR-038.)

## R9 — Four procedures, set in stone

The harness is operated by four procedures and no others, drawn in `README.md` § How it works:

- **Install:** a place either clones `styrman-harness` with the operator's own credential and runs `sh-agent install`, or downloads a release's `sh-agent` binary with that credential — `gh release download`, `go install` over SSH, or the Releases page — and runs `sh-agent install`, which installs the harness built into the binary. Either way installed files go into the vendor directories and no repository is created.
- **Update:** the operator edits, verifies, commits and pushes `main` in their checkout. A checkout place runs `sh-agent update` when the operator decides — fetch, refuse a dirty checkout, fast-forward, rebuild, reinstall. A binary-only place runs `sh-agent update`, which downloads the newest release, checks it against the release's `SHA256SUMS`: integrity, not authenticity — trust rests on the operator's GitHub credential and HTTPS, as for a checkout place — replaces the binary and reinstalls; it changes only when a release is cut. Nothing is pushed to a machine.
  - **Zip place:** the operator downloads the repository zip by hand and runs `sh-agent update --from-zip <file>`. It unpacks the zip into a temporary directory, refuses any entry that would leave it, installs that tree with the running binary, and deletes the directory. Nothing is fetched, no repository is created, and nothing in the zip replaces the binary. When the release's `SHA256SUMS` sits beside the zip, the zip is checked against it and refused on a mismatch; a zip without it is installed marked `UNVERIFIED`.
- **Report back:** each place runs `sh-agent report`, which sends a redacted copy of `~/.config/sh-agent/` and a metrics record to `styrman-harness-feedback` through a throwaway clone that holds only that place's folder and is deleted after the push; the harness is improved from the reports in the operator's checkout, and that change returns through Update.
- **Release:** `/preflight vX.Y.Z`, from the operator's own turn, gates the tree, pushes an annotated tag on `main`, and publishes that tag's binaries and a zip of its tree (`git archive`), with their `SHA256SUMS`; no key is needed, so any machine with the operator's GitHub credential can cut one. Checkout places follow `main`; binary-only places update to the newest release.

The four procedures and the map change together, only with the operator's explicit approval, an ADR, and one commit carrying the code, the docs and the map. (ADR-031, ADR-033, ADR-038, ADR-039, ADR-040, ADR-041, ADR-053; each quotes the operator's approval of its text.)

## R10 — One behaviour on every CLI (R-parity)

Every harness feature is set up and runs the same on Claude Code, on Antigravity, and on a machine with both. Where one CLI has a feature natively and the other lacks it, the harness provides it once — in `sh-agent` or in a skill that calls it — and both CLIs use that version; a native feature is used only where it gives the same result on both. What only a CLI's own runtime controls, and neither the context nor `sh-agent` can reach, is listed in `.agents/docs/specs/sh-agent/memory-loop.md` § Known differences. (ADR-047.)

## Known limits

- **Antigravity global rules are one 24,000-byte block.** CLI 1.2.7 builds one block from `~/.gemini/config/rules/*.md` and cuts it at 24,000 bytes with a `<truncated N bytes>` marker; a second file costs the first one room, and what falls past the cut is dropped without a marker (BUG-085, measured 2026-09-21); files that fit under the cut all load (measured 2026-10-05: context and `critical-thinking.md`, 16,993 bytes; ADR-051; the vendor page, read 2026-09-30, now says 24,000 bytes per file plus a 20,000-token budget across all always-on rules, so the discrepancy with the measured single block stands as measured). The deployed context is pinned under 22,000 bytes by a test, and `rules/` files share the block.
- **Antigravity global rules path.** The CLI's customization root is `~/.gemini/config/`; rules there are documented for the workspace form (`.agents/rules/*.md`) and by analogy for the global one. Verified on the Linux box 2026-09-03 (`.agents/docs/testing/2026-09-03-verify-real-home.md`): the CLI reads rules, skills, and agents from there. Confirmed on the Mac for Antigravity on 2026-09-04 (`.agents/docs/testing/2026-09-04-verify-mac-real-home.md`); Claude Code on the Mac is unverified.
- **Antigravity tool names cannot be enumerated.** An unrecognised name in an agent's `tools:` hangs the subagent, so `sh-agent install` fails on any tool it cannot map rather than guessing.
- **Antigravity IDE is out of scope.** Only the `agy` CLI is targeted; the IDE's global paths are unverified.

## Non-goals

- Hooks of any kind, on either vendor. Researched and rejected 2026-09-03 (ADR-007, `.agents/docs/plans/2026-09-03-research-hooks.md`); the harness is enforced by reading, not by gates.
- Plugin or marketplace packaging on either vendor: both namespace skills and agents, which would rename all of them.
- Editing `settings.json` or any vendor setting: the harness is files in the customization directories, nothing else — with one exception, R4's `attribution` key, which `install` writes only when absent because that setting exists solely to switch off a vendor behaviour the context's Git rule forbids, and the two memory keys `/init-project` writes into a project's gitignored `.claude/settings.local.json` (`autoMemoryDirectory`, `autoMemoryEnabled: false`; R10), and, on Claude Code, the top-level `autoCompactWindow` key, which `install` writes only when absent, at 300,000 tokens: the model cannot observe its own context, hooks are refused (R3), and the CLI's own compaction at a window the harness sets is the one monitor-and-trigger the harness can own; the operator's `/autocompact` saves under `modelSettings` and takes precedence; Antigravity has no equivalent, recorded in memory-loop.md § Known differences (ADR-060); never a permission, never another key. (ADR-047a, ADR-060.)
- Symlinking the vendor directories to one shared store: neither CLI documents reading the other's directory, and a copy has no dangling-symlink failure mode.
