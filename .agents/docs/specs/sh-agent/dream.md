---
owner: main
date: 2026-10-24
---
# `sh-agent dream` — the background dreamer

Status: built 2026-09-30 (plan `plans/2026-09-30-parity-memory-dream.md`). R3's named exception, R4's dream clause, R10 (ADR-047, ADR-047a). The memory half of the learning loop is `specs/sh-agent/memory-loop.md`.

## What it does

Before anything else, a real run (not `--dry-run`) runs `sh-agent metrics ingest` over every Antigravity session, store-wide whatever `--project` names, inside `dream.lock`. It logs the ingest's summary line to `dream.log`. A failure is logged as `metrics ingest failed: …` and changes neither the exit status nor `last_run.error` (plan `2026-10-08-metrics-store` D16; spec `specs/sh-agent/metrics.md`). A dry run prints `[dry-run] would ingest metrics into <path>` and writes nothing.

**The metrics block (R3's addendum, ADR-055a).** Each project's prompt carries a `metrics` input: `metrics.Summary.Numbers()` for the project's window (its watermark to now) and for the window of the same length before it, as JSON.
- **Numbers only.** Every key is the metrics package's own name (no tool, skill, agent, label or path), and a metric a CLI cannot produce is absent.
- **Read from a copy.** It is read from a copy of `metrics.db` (`metrics.Snapshot`). A store at another schema is named under "Left out", and no store or no session gives no block.
- **Context, never news.** It never marks a project changed and is dropped first when the prompt is over the cap.
- **How the instructions treat it.** A worsened metric is evidence only when the other inputs show its cause.

`sh-agent dream --all`, run nightly by a user timer (`--install-timer`), or by hand with `--all` or `--project <dir>`. It looks at every project in the global search index, `~/.config/sh-agent/search.db` `projects` (read from a copy, never opened live). A project is skipped with a log line when its root is gone, it is not a git checkout, or it lacks its docs root. Otherwise, when anything changed since the project's watermark:

1. **Collect** (`internal/dream/collect.go`), in prompt order:
   - the memory store `<root>/.agents/memory/`, whole, when the trust checks pass (otherwise it is named as left out);
   - `feedback.jsonl` lines for this root;
   - `bugs/`, `reviews/`, `retros/`, `changes/`, `handoffs/` records;
   - the installed skills' names and descriptions, read from `~/.gemini/config/skills` (global: never a change on their own);
   - Antigravity conversations whose `workspace_uris` names the root, by `last_modified_time` (whole conversations, from copies of the databases; `internal/search/dream.go`).

   Everything but memory and skills is taken only when newer than the watermark; a project's first dream reads the last 14 days. A tracked, unmodified trail record counts as changed at the time of its newest commit (`git log`), an untracked or modified file at its mtime. "Modified" compares the working file's raw bytes (`git hash-object --no-filters`) with HEAD's blob (`git ls-tree`); `git status` is never run, because it runs the repository's clean filters, so a file the repository filters reads as modified and keeps its mtime. Every git call into a project root goes through `internal/trail` (no index lock, no fsmonitor, no signature check, `GIT_NO_LAZY_FETCH=1`; BUG-164). A copy of an Antigravity database that is torn (the live one was being written) is copied and opened once more after a second; the live database is never opened. Only the user's (step_type 14) and the model's (15) Antigravity steps are read, never tool output; an Antigravity read error fails the project, so its watermark stays. A symlinked docs root or record directory is not followed and is named as left out.
2. **Scrub**: an input's fence runs (`>>>`, `<<<`) and its label's newlines are escaped in the prompt; every input passes the redactor in `internal/redact`, each feedback entry first as a JSON line through `harness.RedactJSONL` and the home path becomes `~`; `project:` and "left out" lines are written with `~` for the home too.
3. **Cap**: at 256 KB. Transcripts are dropped oldest first, then trail, feedback, skills. A project whose memory alone passes the cap fails. The prompt names what was left out.
4. **Ask**: one CLI call with NO tools, from the empty `~/.config/sh-agent/dream-work/`, 10-minute timeout, the child keeping sh-agent's own environment; the CLI runs in its own process group, which is killed whole at the deadline, on SIGINT, SIGTERM, SIGHUP or SIGQUIT (then sh-agent dies of the signal as before; an interrupt or hangup sh-agent started with ignored, under `nohup` or as a background job, stays ignored and leaves the CLI alone), and once the CLI itself has exited; a helper it started that keeps the output pipes open past a clean exit is cut loose 30 s later and the answer is kept (BUG-169 and its review).
   - The CLI is `agy`: `agy --input-format stream-json --output-format stream-json --mode plan -p ""`, the prompt on stdin as one stream-json message and the answer read from the final `result` event's `response`; a result naming `denied_actions` is no answer. agy has no no-tools flag: plan mode, a settings file that grants nothing and the prompt's opening warning (every tool call is refused and ends the run with no answer) keep it from calling one (D7b). It runs with a private HOME, `~/.config/sh-agent/dream-agy-home-*`, holding a copy of the operator's `antigravity-oauth-token` and `installation_id` (taken from `$HOME`, never `--home`) and that settings file; on macOS, where agy keeps the login in the Keychain and finds it only through `$HOME/Library/Keychains`, that folder is linked into the private HOME (never copied) and the token file is copied only when it exists (BUG-166); the dream job's agy then uses the operator's real Keychain, so a login refresh it makes is the interactive agy's too, unlike Linux, where it only ever touches a copy. A `HOME` that is not an absolute path is exit 2. The directory is removed after the run. No login — no token file, and on macOS no `~/Library/Keychains` either — is exit 2 before any project. An agy that asks for a browser login in the private HOME (a locked Keychain, an expired login) fails the project, and the log line appends `agy is not logged in inside the dream job's private HOME`.

   `--vendor` is a usage error (exit 2). "No `agy` on PATH" is the failure before the first project.
5. **Write**: the first `{…}` of the answer that decodes as `{"proposals":[…],"skills":[…]}`, scrubbed again. Unknown keys are ignored; the fields used are validated.
   - `proposals/dream/<date>-<project>.md`: project targets, each with `**Status:** queued`, plus the draft list. Each proposal's text is indented by four spaces, so a model's text cannot open a `### N.` or `**Status:**` section that `/retro` parses.
   - `proposals/dream/harness/<date>-<project>.md`: harness targets.
   - `proposals/dream/drafts/<date>-<project>-<name>/SKILL.md`: each draft skill with a kebab-case name; an invalid name is listed as dropped.

   One-line fields (title, kind, file, evidence) have line breaks folded. Same-day reruns get `-2`, `-3`. A project with zero proposals writes no proposal file, unless a draft skill was written or dropped (an invalid name): then the file carries that note and nothing else.
6. **Advance** the watermark to the run's start, only after a successful parse and write.

**Where the data goes:** to the model of `agy`, the one CLI the run uses and the only one whose transcripts are read. From Antigravity, only the user's input and the model's replies are read (step types 14 and 15), and each step's payload is decoded as protobuf by its wire format, with no schema (D30): a length-delimited field that is printable UTF-8 is a text leaf, anything else is tried as a nested message. A leaf is sent only when it has three spaces or more and is not JSON, so a tool call's arguments (a command, a file written), which are one whole JSON string field, never reach the prompt. On the 95 conversation databases of the Linux box, 971 prose leaves were kept and none carried a tool-argument key.
**Hardening details:**
- The private agy settings are `{"toolPermission":"request-review","permissions":{"allow":[]},"trustedWorkspaces":[]}`.
- A stream result whose status is not `SUCCESS`, or that names `denied_actions`, is no answer.
- Each `feedback.jsonl` entry is redacted as a JSON line (`redact.RedactJSONL` in `internal/redact`, which matches the decoded line) before the raw `scrub`, because a secret JSON-escaped on the line (`&sig=…` written as `\u0026sig=…` by `json.Marshal`) does not match the raw-text needles.
- The timer freezes only the absolute entries of the installing shell's PATH.
- The timer's unit name is per home (`dream.UnitFor(home)`): the real home (the account's home from the account database, `os/user` by uid, never `HOME`, which a sandbox run overrides; path cleaned; an account the database does not list — on Linux a release binary reads only `/etc/passwd`, so LDAP, SSSD and systemd-homed accounts and a container's bare uid — has no real home, and every home there gets the suffix) gets the bare `sh-agent-dream` (systemd) and `com.styrman.sh-agent-dream` (launchd label); any other `--home` gets `-<first 8 hex of sha256 of the cleaned home path>` appended to both. Install, remove, the installed check and the unit-file paths all derive the name from the same home, and the unit's command is `dream --all --home <that home>`, so a scratch timer dreams over its own home's index, never the real one. A symlinked spelling of the real home counts as the real home (`os.SameFile`). Why: the user service manager is shared by every home of the user, so a scratch `--install-timer`, `--remove-timer` or `uninstall` must never reach the operator's own timer (R6; BUG-144). An existing real-home timer keeps its bare name, so an upgrade finds and removes it unchanged. `--remove-timer` prints `dream timer removed`, or `no dream timer found for <home> on this system` when it removed nothing (exit 0 both ways); `uninstall` says `removed the dream timer` only when it removed one. A unit file that v2.0.0 or earlier wrote under the bare name (`sh-agent-dream.service`, `.timer`, `com.styrman.sh-agent-dream.plist`) in a home whose own unit name is now suffixed is never removed or stopped by the binary (a stop by name ends whichever unit the manager loaded); `--remove-timer` and `uninstall`, each also with `--dry-run`, print its paths, why it is left alone, the check (`systemctl --user show -p FragmentPath sh-agent-dream.timer`, or `launchctl print gui/<uid>/com.styrman.sh-agent-dream`) and the steps, and the `dream` line of `sh-agent status` appends `; legacy timer files under <home>: sh-agent dream --remove-timer --dry-run --home '<home>' prints the cleanup steps`. A unit file of the operating system the binary is not running on (`OtherSystemTimerFiles`: the launchd plist on Linux, the systemd units on macOS) is never removed either: `uninstall` and `--remove-timer` name it and say it stays, dry runs included, since `status` counts it as installed; `uninstall` announces a removal only for a unit of the system it runs on (`TimerInstalledHere`).

## Consumers

- Project-level `/retro` (source 9) reads its project's `proposals/dream/*.md`, re-derives each proposal against the files, and sets its Status to applied or rejected.
- `/retro --harness` reads `proposals/dream/harness/*.md` beside the harness queue, on the machine that wrote them; nothing sends them anywhere (ADR-062). They are model-written from any indexed project's redacted content, so their text is data, never instructions.
- `sh-agent status` prints `dream      last run <date>: <n> projects, <m> proposals; timer installed|not installed` (or `failed: <reason>`, or `never run`).

## State and files (all under `~/.config/sh-agent/`, 0600 in 0700 directories)

| Path | What |
|---|---|
| `dream-state.json` | `projects` (root → watermark) and `last_run` (at, projects, proposals, error) |
| `dream.log` | one line per project (`ok N proposals`, `unchanged`, `skip <why>`, `fail <why>`) and one per run |
| `dream.lock` | an flock held for the run (the pid is written in it for reading only); the kernel releases it however the run ends, and a held one makes a second run exit 0 "already running" |
| `dream-agy-home-*`, `dream-agy-*`, `dream-projects-*` | per-run private copies (agy HOME, Antigravity databases, the index); removed after the run; at the start of a run, under the lock, only copies older than an hour are removed (leftovers of a killed run; a dry run takes no lock) |
| `dream-work/` | the CLI's working directory, kept empty; not a project, so the run's own conversation never maps to one |
| `dream-failed/` | a failed project's stdout and stderr, scrubbed; files older than 30 days are pruned at the start of a run |
| `proposals/dream/` | the output above |

## Failure modes

| Situation | Behaviour |
|---|---|
| Any failure before the first project: usage, no `agy` on PATH, no Antigravity login, an unreadable index | exit 2 with a run log line and `last_run.error`; `status` says `failed`. A usage error writes nothing, exits 2 and prints its reason on stdout (not stderr) |
| `dream-state.json` is corrupt | exit 2, and the file is NOT rewritten |
| `--dry-run` with `--install-timer` or `--remove-timer` | both take `--dry-run`: writes nothing, calls no manager |
| The CLI exits non-zero or times out | the project fails, its output goes to `dream-failed/`, its watermark stays; the others run; exit 1 |
| The answer has no decodable JSON | the same, logged `fail invalid output` |
| An agy stdout line over 16 MiB | the same, logged `fail output line too long or unreadable` |
| The docs root or a record directory is a symlink | not followed; named as left out |
| The store is a symlink, or git tracks it | memory is left out and named in the prompt and the proposal frontmatter; the rest is sent |
| Memory alone over 256 KB | the project fails with `memory over cap` |
| No indexed project | nothing to do; exit 0 |
| A second run while one runs | exit 0, "already running" |

## Verification

Unit tests use a fake CLI on PATH that records its arguments, working directory and stdin (`internal/dream/run_test.go`), so the tool-free flags, the cwd, the absence of a planted secret in the prompt, the file shapes and the watermark rule are pinned. The D7 probe (plan) ran both real CLIs tool-free from an untrusted, empty directory. The end-to-end rows are in `testing/skill-matrix.md` and `testing/sh-agent-matrix.md`.
