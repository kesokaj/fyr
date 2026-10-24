# Requirements — fyr

> Immutable without the operator's explicit approval. Agents never edit this file.

## R0 — Purpose: the second brain of the Antigravity CLI

`fyr` is the one place the Antigravity CLI keeps what a session cannot hold on its own: the context, skills, rules and agents (`install`), the trail and its index (`search`), the project memory (`memory`), the shape of every session (`metrics`) and the nightly consolidation (`dream`). The CLI consults it at the moments the context names — resume, before touching an area, at a phase end — and the harness grows there, in the binary and its specs, never in the context, whose hand-written part stays under its size pin: a new consult moment is one clause on an existing context line plus the verb's own usage text. The brain is consulted, never consulting: nothing in `fyr` acts on the CLI between its turns (R3), and the CLI only learns what it asks for, so a moment the context does not name never fires. (ADR-061, ADR-062.)

## R1 — One source, one vendor

`context/AGENTS.md` (plus `context/*.md`) is the single global context, deployed to the Antigravity CLI and to no other CLI. Subagents are authored in Antigravity's format as `agents/<name>/agent.md` and installed unchanged; nothing is derived, and nothing generated is committed. (ADR-062.)

## R2 — Standards, not vendor formats

The context follows the agents.md standard and is named `AGENTS.md` at every level; a project has `AGENTS.md` at its root and no vendor-named copy of it. A template `AGENTS.md` never sits beside source. Skills follow the Agent Skills `SKILL.md` standard so that anything from skills.sh or any other source drops into `skills/` unchanged.

## R3 — No hooks, no gates, no CI

The harness is context, skills, and subagents. Behaviour comes from what is written in them, not from lifecycle hooks, commit gates, or pipelines. Git is backup and versioning only. One exception: `fyr dream --all`, run nightly by an OS user timer that `fyr dream --install-timer` installs and `fyr dream --remove-timer` or `uninstall` removes, reads every indexed project's trail and memory, the feedback log and the Antigravity CLI's conversations, and a numbers-only summary of the metrics store (its metric keys and values for each project's window and the window before, never a tool, skill, agent, label or path), first ingests those conversations into the metrics store (R4), runs the CLI with no tools over what it read, and writes only under `~/.config/fyr/`, its proposals to `proposals/dream/`; it never edits a project, a skill, a rule or the context, and a proposal takes effect only through `/retro`. (ADR-047a, ADR-062.)

## R4 — One binary, one dependency, easy on and off

`fyr` is a single Go binary. Its one dependency is `modernc.org/sqlite`, a pure-Go SQLite driver used only by the search index and the metrics store, pinned in `go.mod` and `go.sum` and not vendored; `fyr dream` also opens Antigravity's conversation databases with it, read-only; any other dependency, and any driver that needs cgo, is a requirements change. The search indexes (`.agents/scratch/search.db` in a project, `~/.config/fyr/search.db`) are caches derived from files git or `~/.config/fyr/` already holds: never committed, rebuilt when deleted. The metrics store (`~/.config/fyr/metrics.db`) is not a cache: it holds the shape of every ingested session (tools, normalised command verbs, tokens, timings, never arguments or prose, and no path but each session's project root and the transcript files it read), including the Claude Code sessions ingested before ADR-062, kept as history, and is the only copy once a transcript expires, so it is migrated forward and never rebuilt or deleted by the binary, never committed, and `uninstall` leaves it. `install` is idempotent and overwrites only files it manages; `uninstall` removes exactly what its manifest lists, and the dream timer when one is installed (R3); `update` pulls the repo from git and reinstalls. `update` refuses a checkout with uncommitted changes on every path, because it deploys the working tree rather than the commit it fetched; `install` stays the deliberate way to deploy a dirty tree. `install` writes no vendor setting (Non-goals). (ADR-045, ADR-047a, ADR-062.)

## R5 — Hand-added customizations are first class

Adding a skill, subagent, command, or rule is: put the file in `skills/`, `agents/`, `commands/`, or `rules/` (by hand, by `npx skills add` from the repo root, or by `fyr adopt`), then `fyr install`. No registration, no manifest edits, no code. The installer maps each directory to its place under `~/.gemini/config/`.

## R6 — Development never touches the real home

Every test and every development run of the installer uses `--home` with a scratch directory. Deploying to the operator's real `$HOME` is the operator's action.

## R7 — Projects remember and stay resumable

Every project keeps its scratch in `.agents/scratch/` (gitignored), its memory store in `.agents/memory/` (written by `fyr memory`; gitignored, one per checkout), its failures as numbered records in `.agents/docs/bugs/`, its progress in plan files with task checkboxes, and its retro, distill and dream records in `.agents/docs/retros/`, so a zero-context session can resume and does not repeat a mistake. The harness's own state (manifest, feedback log, the harness proposal queue, the cross-project index, the metrics store) lives in `~/.config/fyr/` and nowhere else; a project's retro and distill records are the project's own state, in its trail (ADR-015).

## R8 — One delivery channel, and it is `fyr`

The harness reaches a machine one of three ways: a checkout the operator owns; a release's `fyr` binary with the harness built in; or a zip of the repository that the operator downloads with their own credential and passes to `fyr update --from-zip <file>`. Each is kept current with `fyr update`: the checkout and the binary as today, and a zip place by the operator repeating the download and `--from-zip`. There is no other channel — no vendored mirror inside a consumer project, no per-project copy, no service. A project that wants the harness uses `fyr`; this repository names no consumer project and depends on none. Nothing flows back: no verb sends the machine's corrections, proposals, memory or metrics anywhere, and the one thing that leaves it is the nightly dreamer's redacted prompt to the model of the CLI the machine already uses (R3). Experience reaches the harness only through the operator's own hands in their checkout. (ADR-030, ADR-033, ADR-038, ADR-062.)

## R9 — Three procedures, set in stone

The harness is operated by three procedures and no others, drawn in `README.md` § How it works:

- **Install:** a place either clones `fyr` with the operator's own credential and runs `fyr install`, or downloads a release's `fyr` binary with that credential — `gh release download`, `go install` over SSH, or the Releases page — and runs `fyr install`, which installs the harness built into the binary. Either way installed files go into `~/.gemini/config/` and no repository is created.
- **Update:** the operator edits, verifies, commits and pushes `main` in their checkout. A checkout place runs `fyr update` when the operator decides — fetch, refuse a dirty checkout, fast-forward, rebuild, reinstall. A binary-only place runs `fyr update`, which downloads the newest release, checks it against the release's `SHA256SUMS`: integrity, not authenticity — trust rests on the operator's GitHub credential and HTTPS, as for a checkout place — replaces the binary and reinstalls; it changes only when a release is cut. Nothing is pushed to a machine.
  - **Zip place:** the operator downloads the repository zip by hand and runs `fyr update --from-zip <file>`. It unpacks the zip into a temporary directory, refuses any entry that would leave it, installs that tree with the running binary, and deletes the directory. Nothing is fetched, no repository is created, and nothing in the zip replaces the binary. When the release's `SHA256SUMS` sits beside the zip, the zip is checked against it and refused on a mismatch; a zip without it is installed marked `UNVERIFIED`.
- **Release:** `/preflight vX.Y.Z`, from the operator's own turn, gates the tree, pushes an annotated tag on `main`, and publishes that tag's binaries and a zip of its tree (`git archive`), with their `SHA256SUMS`; no key is needed, so any machine with the operator's GitHub credential can cut one. Checkout places follow `main`; binary-only places update to the newest release.

The three procedures and the map change together, only with the operator's explicit approval, an ADR, and one commit carrying the code, the docs and the map. (ADR-031, ADR-033, ADR-038, ADR-039, ADR-040, ADR-041, ADR-053, ADR-062; each quotes the operator's approval of its text.)

## R10 — One behaviour on every place

Every harness feature is set up and runs the same at every place, whichever of the three ways in (R8) it came by, and on every operating system the operator runs it on (Linux, macOS). What only the Antigravity CLI's own runtime controls, and neither the context nor `fyr` can reach, is a Known limit below. (ADR-047, ADR-062.)

## Known limits

- **Antigravity global rules are one 24,000-byte block.** CLI 1.2.7 builds one block from `~/.gemini/config/rules/*.md` and cuts it at 24,000 bytes with a `<truncated N bytes>` marker; a second file costs the first one room, and what falls past the cut is dropped without a marker (BUG-085, measured 2026-09-21); files that fit under the cut all load (measured 2026-10-05: context and `critical-thinking.md`, 16,993 bytes; ADR-051; the vendor page, read 2026-09-30, now says 24,000 bytes per file plus a 20,000-token budget across all always-on rules, so the discrepancy with the measured single block stands as measured). The deployed context is pinned under 22,000 bytes by a test, and `rules/` files share the block.
- **Antigravity global rules path.** The CLI's customization root is `~/.gemini/config/`; rules there are documented for the workspace form (`.agents/rules/*.md`) and by analogy for the global one. Verified on the Linux box 2026-09-03 (`.agents/docs/testing/2026-09-03-verify-real-home.md`): the CLI reads rules, skills, and agents from there. Confirmed on the Mac for Antigravity on 2026-09-04 (`.agents/docs/testing/2026-09-04-verify-mac-real-home.md`).
- **Antigravity tool names cannot be enumerated.** An unrecognised name in an agent's `tools:` hangs the subagent, so `fyr install` fails on any tool name it does not know rather than guessing.
- **Antigravity IDE is out of scope.** Only the `agy` CLI is targeted; the IDE's global paths are unverified.
- **Antigravity has no compaction window a file can set.** A session compacts when the CLI decides; the session-sizing rule (`rules/session-sizing.md`, ADR-058) is the only mechanism that ends a session at its checkpoint.

## Non-goals

- Hooks of any kind. Researched and rejected 2026-09-03 (ADR-007, `.agents/docs/plans/2026-09-03-research-hooks.md`); the harness is enforced by reading, not by gates.
- Plugin or marketplace packaging: a namespace would rename every skill and agent.
- Editing any vendor setting: the harness is files in the customization directories, nothing else. (ADR-062.)
- Symlinking the customization directory to the checkout or a shared store: a copy has no dangling-symlink failure mode, and a release-binary place has no checkout to link to.
