---
owner: main
date: 2026-10-24
status: agreed
---
# Spec — fyr: the metrics store   (2026-10-08)

## Purpose

`fyr metrics` keeps the **shape** of every Antigravity session in `~/.config/fyr/metrics.db`. Shape means:
- the tools each session called;
- the normalised command verbs;
- tokens, timings and errors;
- events joined from git and the trail.

The operator queries the store for agent evaluations (`show`, `report`). The nightly dreamer and `/retro` read it as evidence for proposals.

It is not:
- a transcript archive: it holds no arguments and no prose;
- a cache: it is the only copy once a transcript expires (R4);
- sent anywhere: it stays on the machine (ADR-062).

Decisions: ADR-055. Plan: `plans/2026-10-08-metrics-store.md`.

## Interfaces

### Verbs

The verbs reuse the global `--home` flag. `--vendor` does not exist (a usage error, exit 2): `ingest` reads Antigravity only (ADR-062, plan `2026-10-24-fyr-agy-only.md` D7).

```
fyr metrics ingest [--home H] [--source DIR --label KEY=VALUE [--outcome pass|fail]]
fyr metrics show   [--home H] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--json]
fyr metrics report [--home H] [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--out FILE]
fyr metrics export [--home H] FILE
```

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--source` | directory | none | A sandbox home: `DIR/.gemini/antigravity-cli`. Requires `--label`. |
| `--label` | `KEY=VALUE` | none | On `ingest`: stamped on every session added from `--source`. On `show`/`report`: shows only that label. |
| `--outcome` | `pass`, `fail` | none | The run's acceptance result, supplied by the kit, used for pass^k. Given after the run (a second ingest of the same `--source` and `--label`), it stamps every session of the label that has no outcome yet, even when no transcript changed; an earlier run keeps the outcome it was stamped with. |
| `--all-labels` | bool | false | Include labelled (sandbox) sessions. Without it, and without `--label`, they are excluded. |
| `--since` | date | 30 days ago | Start of the window. |
| `--until` | date | now | End of the window, exclusive (`show`, `report`). `--since A --until B` and `--since B` never overlap; `/retro` reads the window before its own this way. A session belongs to the window it starts in, `[since, until)`, and is counted whole there, so adjacent windows never share one. |
| `--project` | directory | all | Only sessions whose `project_root` is this directory or under it (a session started in a subdirectory is the project's; a sibling that only shares the name's prefix is not). |
| `--context-target` | token count above 0 | 300000 | The peak context a main session should stay under (`show`, `report`). B3 counts the main sessions whose peak passed it; the value is a flag, not a pin, so `/retro` can tune phase sizing against it without a rebuild (plan `2026-10-23-session-sized-work.md` D4, D15). |
| `--json` | bool | false | Print the `## Output` object instead of tables. |
| `--out` | file | `~/.config/fyr/reports/<YYYY-MM-DD-HHMM>-metrics.html` | Where the report is written. An existing file is refused. |

### Where transcripts are read from

The first rule that applies wins:
1. `--source DIR`
2. `--home H` → `H/.gemini/antigravity-cli`
3. the real home

Ingest reads only the Antigravity CLI's directory; there is no other source.

### `ingest` output and exit status

On success it prints this line and exits 0:

```
metrics: N sessions, M steps, K events added; U lines skipped (unknown shape); F sources failed
```

- Exit 1 when F > 0. Each failed source is named on its own line, and every other source is still ingested.
- Exit 2 on a usage error.

### Normalised verb

`Verb(cmd string) []string` reduces a command line to its shape: one string per command in it, `program [subcommand] [flag names…]`.

**What is removed before splitting:**
- here-document bodies (`<<EOF`, `<<-EOF`, `<<'EOF'`);
- comments;
- the insides of quotes, `${…}` and `$((…))`, which stay part of their word and are never split.

**Where it splits:** at `;`, `&&`, `||`, `|`, `&`, newlines, `(`, `$(`, backticks, `{` and `}`. A redirection such as `2>&1` or `&>f` is part of a word, not a separator.

**What is skipped in front of the program:**
- shell keywords that lead a command (`if then else elif do while until !`);
- the wrappers `sudo env command time nohup exec`, with their own flags and those flags' values (`env -u NAME`);
- `VAR=value` words;
- leading redirections and their targets.

**Segments that yield no verb:**
- a segment opened by `for`, `case`, `select` or `function`, or closed by `done`, `fi`, `esac` or `in`;
- a case pattern, i.e. the words before its `)`.

**The program:**
- It is the basename of the first remaining word. For `go run <target>`, see the `go run` rule below.
- It must match `^[A-Za-z_][A-Za-z0-9_.+-]*$`; any other word in command position (a glob, an expansion, a quoted path) yields no verb.
- A subcommand is kept only from a fixed allowlist per tool (`subcommands` in `internal/metrics/verb.go`): git, go, gh, docker, gcloud, kubectl, npm, terraform and fyr. An unlisted word is dropped and the verb is the program alone, because that position can hold a short secret (`gh hunter2pass`). npx's package is never kept. git's global options (`-C dir`, `-c k=v`) are skipped before the subcommand. After fyr's subcommand, one more word is kept when it is one of `ingest show report export add` (operator, 2026-10-09: allowlists).

**Flag names:**
- A long flag `--name[=v]` is kept as `--name` only when the name is lowercase letters, digits and dashes, at most 24 characters (`--sk_live_…`, `--Authorization` are dropped).
- For `go gofmt govulncheck find`, `-name[=v]` is kept as `-name` only when the name is lowercase letters and digits, at most 24 characters.
- A short group of at most three letters is kept whole (`-la`, `-rnw`).
- Any other short flag keeps its first letter only (`-c1-200`, `-psecret`, `-xzvf` become `-c`, `-p`, `-x`), because a short option can carry its value glued on.
- A dash followed by a digit (`-20`) is a value and is dropped.

**Never kept:** positional words, except a bare `.` after `git add`.

**Here-documents:**
- A delimiter is read literally, as bash does. A quoted one runs to its closing quote (`<<'END!'`, `<<"$D"`). An unquoted one is the whole word up to a shell metacharacter (`<<END!`, `<<$END`). The body ends only at a line equal to it.
- A here-doc with no delimiter word cannot be matched, so nothing after its line is read.

**`go run`:** the target counts as the program only when it is a tool the subcommand allowlist knows (`go run ./cmd/fyr search` is `fyr search`). Any other target is content, and the verb is `go run` plus its flag names.

A program name longer than 32 characters is content and is dropped.

**Transcript lines:** a line is read in bounded chunks. A line over 16 MB is consumed and counted under "unknown shape", never held in memory whole. An unterminated tail over that size is passed over rather than re-read on every ingest.

**Tool names** follow the identifier rule below; one that fails it is stored as `unnamed`. Everything `metrics ingest` prints (paths, project roots, errors) goes through search's BUG-152 filter: control, format (bidi overrides, zero-width marks), line-separator and paragraph-separator characters become `?`, by Unicode class, because a `--source` sandbox writes those values. `show`'s text output prints a key holding any of them as `?`. Every file ingest reads (a transcript, a trail record, a `.meta.json`) is opened without blocking and checked on the opened file to be a regular file, so a FIFO swapped in after an earlier check neither hangs the ingest nor is read.

**Identifiers:** a skill name, a model or an agent type is stored only when it matches `^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,63}$`; otherwise it is left empty. So no escape bytes or prose reach the store or the terminal. A subagent's `.meta.json` is read only as a regular file of 64 KB or less.

**Known limit:** a shell construct the tokenizer does not model can still put one identifier-shaped word in command position. That is never more than a single name-shaped word, never a path, a value or prose. Verbs reflect the normaliser of the binary that ingested them; the command itself is never stored, so older rows are not re-normalised.

### Shared tool vocabulary

Antigravity tool names map to a common name. Unknown names are kept as `agy:<name>`. The Claude Code column maps nothing new: it names the tools of the historical rows an older ingest stored, which are kept and read, never written.

| Common | Claude Code (historical) | Antigravity |
|---|---|---|
| Bash | `Bash` | `run_command` |
| Read | `Read` | `view_file` |
| Write | `Write` | `write_to_file` |
| Edit | `Edit` | `replace_file_content`, `multi_replace_file_content` |
| Grep | `Grep` | `grep_search` |
| Glob | `Glob` | `find_by_name` |
| LS | (none) | `list_dir` |
| Agent | `Agent` | `invoke_subagent` |
| SendMessage | `SendMessage` | `send_message` |

The Antigravity names come from the spike over the local conversation databases (plan D35). `manage_subagents` and any other name not in this table are kept as `agy:<name>`.

**Where the shape is read:** Antigravity: the tool name is at `steps.metadata` 4→2 and the arguments JSON at 4→3, which gives `CommandLine` and `AbsolutePath`. The step time is at `metadata` 1→1, in seconds. An error is status 7. A subagent is its own conversation; `conversation_summaries.agent_name` and `parent_conversation_id` give its type and parent. Usage, the model and the context are in `gen_metadata`: 1→4→2/3/5, 1→19 and 1→9→10→1.

A **skill load** is a read of `skills/<name>/SKILL.md` (only `<name>` is stored). A historical Claude Code row's `Skill` call stays as stored.

### Output (`show --json`)

Keys are snake_case. Every metric is keyed by vendor, and `columns` lists the vendors present: `["agy"]`, or `["agy","claude"]` when the window holds sessions with vendor `claude` that an older ingest stored (historical rows: kept, never written again, plan D7). The `claude` keys appear only then; the example below shows that case. A metric the CLI cannot produce is `null` and is named in `notes`, never shown as 0. The shape is the Go type `metrics.Summary`:

```json
{
  "window": {"since": "2026-09-08", "until": "2026-10-08"},
  "columns": ["agy", "claude"],
  "sessions": {"claude": 16, "agy": 11},
  "subagents": {"claude": {"reviewer": 9}, "agy": {"explorer": 2}},
  "A": {"dod_closed": {"claude": {"closed": 3, "ambiguous": 1, "of": 16}},
        "verifier": {"PASS": 22, "FAIL": 3}, "review_findings": {"high": 8, "medium": 38, "low": 60},
        "escaped_bugs": 0, "corrections": 0, "pass_k": {"arm=lean": {"k": 3, "passed": 3, "pass_k": 1}}},
  "B": {"tool_calls": {"claude": 1899}, "tool_calls_by_tool": {"claude": {"Bash": 1300}},
        "turns": {"claude": 127}, "tokens": {"claude": {"in": 0, "out": 0, "cache_read": 0, "cache_write": 0}},
        "cost_usd": {"claude": 135.24, "agy": null}, "wall_min": {}, "active_min": {},
        "peak_context": {"claude": 412000, "agy": 42000}, "compactions": {"claude": 0, "agy": null},
        "context_target": 300000, "sessions_over_context_target": {"claude": 5, "agy": 0},
        "session_peaks": {"claude": [{"session": "a1b2…", "date": "2026-10-07", "peak": 412000}], "agy": [{"session": "c3d4…", "date": "2026-10-06", "peak": 42000}]}},
  "C": {"sqlite_verbs": {"claude": 42}, "grep_calls": {"claude": 1545}, "search_grep_ratio": {"claude": 0.027},
        "forbidden": {"git add -A": 4, "git add .": 0, "git push --force": 0, "--no-verify": 0, "commit chained to test": 1},
        "first_reads": {"plan": 3}, "skills": {"claude": {"golang": 3}}},
  "D": {"tool_error_rate": {"claude": 0.023}, "denials": {"claude": 10, "agy": null},
        "interrupts": {"claude": 2, "agy": null}, "retry_loops": {}, "subagent_tool_calls": {"claude": {"reviewer": 391}}},
  "daily": [{"date": "2026-10-07", "sessions": {}, "tool_calls": {}, "tokens": {}, "cost_usd": {},
             "sqlite_verbs": {}, "grep_calls": {}, "errors": {}}],
  "notes": ["agy: cost is not exposed by the CLI", "agy: denials, interrupts and compactions are not recorded by the CLI"]
}
```

The values above illustrate the shape. They are not fixtures. In text, `show` prints one `agy` column, plus `claude (historical)` as a second one only when the window holds such rows. `daily` holds one entry per UTC day of the window, and the report's trend charts read it. `session_peaks` lists main sessions only, largest peak first, one list per vendor that has a session with a context value in the window; `sessions_over_context_target` counts the entries of that list past `context_target` and is `null`, named in `notes`, for a vendor with no such session (never 0). A session's id and start day are the only identifiers carried: no project path, no content (plan D6, D16).

### Report (`metrics report`)

`metrics report` writes one HTML file, mode 0600. An existing file is refused. The default path is `~/.config/fyr/reports/<YYYY-MM-DD-HHMM>-metrics.html`.

**How it is built:**
- `html/template` with SVG drawn in Go;
- no `<script>`, no URL to any host, no `@import` or `url(`;
- light and dark through `prefers-color-scheme`.

**Colours.** Colour follows the CLI and never its rank: Antigravity keeps categorical slot 2, orange (`#eb6834` / `#d95926`); the historical column, "Claude Code (historical)" in the report, is slot 1, blue (`#2a78d6` light / `#3987e5` dark), drawn only when the window holds historical rows. That pair is the dataviz reference palette's first two slots, validated in both modes. Colours are set by CSS classes, because html/template sanitises a CSS variable in a `style` attribute.

**Hover and identity.** Hover values are SVG `<title>` elements, which browsers show without script. Identity never rests on colour alone: the legend names each CLI drawn, each line is labelled where it ends, and every chart has its numbers as a table.

**Charts:**
- tool calls, tokens, cost, the search:grep ratio and tool errors per day, one line per CLI;
- tool calls by tool (the ten most used);
- subagent tool calls by type;
- runs passed of k per sandbox label;
- the summary table, where the row `sessions over context target (<N>)` reads `k of n`: k the main sessions whose peak passed the target, n the main sessions in the window with a peak at all; `n/a` for a vendor with none.

**Missing values.** A value a CLI cannot produce is not drawn:
- no Antigravity cost line;
- no search:grep point on a day with no grep call.

Tables show those values as `n/a`.

**Consistency.** Every number in the report comes from the same `Summary` as `show --json` over the same window and filters.

**Known limit:** at 400 px the SVG scales down, and tick labels get small (about 6 px) but stay legible. The tables give the same numbers at full size.

### Metric definitions

| Id | Metric | Definition |
|---|---|---|
| A1 | DoD closure | A main session's window is `[started_at, ended_at + 30 min]`. The session is closed when the window holds at least one commit in its project AND a handoff whose `TrailDates` time falls in it. |
| A2 | Verifier status | The `## Status:` heading of `testing/*-verify-*.md` files in the window. Main sessions only. |
| A3 | Review findings | `**[high\|medium\|low / confidence: …]**` lines in `reviews/*.md` in the window. Main sessions only. |
| A4 | Escaped bugs | A `bugs/BUG-*.md` whose text contains a hex prefix of 7 or more characters of one of the session's commits. |
| A5 | Corrections | `feedback.jsonl` entries in the window whose project is the session's root. |
| A6 | pass^k | Per label, 1 when all k `--outcome` runs passed. k and the pass count are shown. |
| — | Replayed history | A resumed or forked Claude session copies earlier messages into a new transcript. `show` counts a tool step, an assistant message or a generation once per CLI and label by its id, under the first session in the query's window that holds it, so replayed tool calls, tokens, verbs and errors are not counted twice. User turns, interrupts and compactions are not deduplicated, and a replay whose original session lies outside the window still counts once inside it. The label is part of the key, so a sandbox run that reuses real ids never hides a real session's steps under `--all-labels`. |
| B1 | Tokens and cost | Antigravity: `gen_metadata` usage; cost is not exposed. Historical Claude rows keep what an older ingest stored (usage per message id, cost from the session's last `cost-state` line). |
| B2 | Time and volume | Wall time = `ended_at - started_at` of main sessions. Active time = the sum of gaps under 5 min between a main session's steps. A subagent runs inside its parent's time and is counted in neither. Also turns (typed user turns) and tool calls (main and subagent). |
| B3 | Context | Peak context = max(input + cache read + cache write) of one message (Claude); the `gen_metadata` context of one generation (Antigravity). Also the number of compactions. Also the sessions whose peak passed the context target, default 300K, and each main session's peak: the peak follows the replayed-history rule above, so a fork's copy of an earlier session's messages counts under the session that first had them, and a subagent's peak belongs to no session (it runs inside its parent's time). |
| B4 | Subagents | Count by type, the tool calls of each, and SendMessage resumes. |
| C1 | Search vs grep | `fyr search\|index` verb segments divided by `grep`/`rg` segments plus Grep tool calls. |
| C2 | Forbidden shapes | `git add -A`, `git add .`, `git push --force\|-f`, `--no-verify`, and `git commit` in the same Bash call as `go test`. |
| C3 | First reads | The trail kind of a session's first three Read targets. |
| C4 | Skills | Skill loads per session. |
| D1 | Errors | Tool error rate, denials and interrupts. |
| D2 | Retry loops | The same `cmd_hash` in two consecutive tool steps. |
| D3 | Subagent volume | Tool calls per subagent session. |

## Data

`~/.config/fyr/metrics.db` has mode 0600 in a 0700 directory. A database or companion file that is a symlink, or any other non-regular file, is refused, as in the search index. The directory itself may be a symlink. It uses WAL with a 10 s busy timeout. Every time is stored as unix milliseconds, UTC. Writer: `internal/metrics` only.

```sql
meta(key TEXT PRIMARY KEY, value TEXT)            -- 'schema', 'salt' (32 random hex)
sessions(id TEXT PRIMARY KEY, vendor TEXT, project_root TEXT, parent_id TEXT, agent_type TEXT,
         model TEXT, label TEXT, outcome TEXT, started_at INTEGER, ended_at INTEGER,
         cost_usd REAL, duration_ms INTEGER)
steps(session_id TEXT, ext_id TEXT, ts INTEGER, kind TEXT, tool TEXT, skill TEXT, target_kind TEXT,
      is_error INTEGER, denied INTEGER, tok_in INTEGER, tok_out INTEGER, cache_read INTEGER,
      cache_write INTEGER, cmd_hash TEXT, context INTEGER,   -- context: migration 2
      PRIMARY KEY(session_id, ext_id))
step_verbs(session_id TEXT, ext_id TEXT, n INTEGER, verb TEXT, PRIMARY KEY(session_id, ext_id, n))
events(session_id TEXT, ts INTEGER, kind TEXT, ref TEXT, value TEXT, PRIMARY KEY(session_id, kind, ref))
ingest_state(source TEXT PRIMARY KEY, size INTEGER, mtime INTEGER, offset INTEGER)
```

### Invariants

- **Identity.**
  - Antigravity: `ext_id` = `<conversation id>:<step idx>`.
  - Rows with vendor `claude` (`ext_id` = message id, `tool_use.id` or a line `uuid`) come from an older ingest. They are kept and read, never written: no code path inserts one, and nothing deletes them (plan D7).
  - Re-ingesting anything adds no rows.
- **Path columns.** Only `sessions.project_root` and `ingest_state.source` may hold a path. No column holds an argument or prose.
- **Identifiers.** `steps.skill`, `sessions.agent_type` and the `git add .` verb are the only identifiers taken from a tool's input.
- **`events` payloads.**

  | kind | ref | value |
  |---|---|---|
  | `commit` | full SHA | `''` or `ambiguous` |
  | `handoff` | file basename | `''` |
  | `verifier` | file basename | the status word |
  | `review` | file basename | `high=N,medium=N,low=N` |
  | `bug` | `BUG-NNN` | the linked SHA |
  | `correction` | the entry's `ts` | `''` |

- **`cmd_hash`** = `hex(sha256(salt || command))[:16]`. It supports equality checks only.
- **Fixed markers.**
  - `denied`: an Antigravity step status the spike names (none observed; the CLI does not record denials). Historical Claude rows keep the marker an older ingest set.
  - `interrupt`: a user line starting `[Request interrupted by user`.
  - `compaction`: not recorded by Antigravity; historical Claude rows keep the compact-boundary count an older ingest stored.
- **Antigravity sources.** Antigravity databases are read only from copies. `step_payload` is never read; tool names and command lines come from `steps.metadata`.
- **Export.** `export` writes every row but the `salt`: with it, an export would let anyone confirm a guessed command against `cmd_hash`.
- **git in project roots.** Every git call into a session's project root goes through `internal/trail`: `--no-optional-locks -c core.fsmonitor=false -c log.showSignature=false`, `GIT_NO_LAZY_FETCH=1`, and `--no-show-signature` on `log`. Only commands that read objects and files run there (`log`, `ls-tree`, `ls-files`, `hash-object --no-filters`, `rev-parse`, `check-ignore`, `config --get`), never `status`, which runs the repository's clean filters (BUG-164). This closes the program-starting config keys those commands honour (signature programs, clean filters, fsmonitor, a lazy fetch's ssh command); it does not make the repository's config trusted. A partial clone (`extensions.partialClone` set, or any remote marked `promisor`) is not read at all, because a git older than 2.44 ignores `GIT_NO_LAZY_FETCH`. Its sessions get no commit or trail events, and its trail files keep their mtimes in the dreamer. A toplevel is accepted only when it is the session's directory or a parent of it, so a repository's `core.worktree` cannot file a session under another project. Sessions are grouped by their git toplevel (`trail.Toplevel`), so a session started in a subdirectory joins its project's events.
- **Temporary copies.** `metrics-agy-*` (Antigravity conversation copies) and `dream-metrics-*` (a store snapshot) are removed after use. An `ingest` and every `dream` run also sweep any older than an hour, which a killed run left behind.
- **Migrations.** They run forward only, in one transaction. A store with a newer schema is refused untouched. The binary never deletes the store, and `uninstall` keeps it.

## Failure modes

| Trigger | Observable |
|---|---|
| `metrics.db`, `-wal` or `-shm` is a symlink or another non-regular file | `metrics: <name> is not a regular file; refusing to open or write through it`, exit 1 |
| The store's schema is newer than the binary's | `metrics.db schema N is newer than this binary (M); update fyr`, exit 1, store unchanged (`ingest`, `show`, `report`, `export`) |
| A transcript line is not JSON or has an unknown shape | Skipped and counted in U, exit 0 |
| A file shrank or was replaced | Re-read from offset 0, with no duplicate rows |
| An Antigravity copy or read fails | That conversation is named and counted in F; the others are ingested; exit 1 |
| An Antigravity token field is absent | Tokens are NULL; `notes` says `agy tokens unavailable for N sessions` |
| A project root is gone or not git | Session rows are still written; no commit events; `ingest` prints `metrics: note: project root gone: <root>` or `no git history: <root>` |
| Two main sessions overlap in one root | Commits in the overlap are `ambiguous`; A1/A4 are shown as `n (ambiguous m)` and excluded from rates |
| Still locked after the 10 s busy timeout | The source counts as failed (`database is locked`), exit 1; the next run picks it up |
| `show`, `report` or `export` on a home with no store (never ingested) | `fyr metrics: no metrics store at <path>; run \`fyr metrics ingest\` first`, exit 1; nothing is created |
| `export FILE` or `report --out FILE` exists | `refusing to overwrite FILE`, exit 1 |
| `--source` without `--label`, or any metrics flag the subcommand does not read (`ingest --json`, `export --label`, `show --out`, `ingest --context-target`) | Usage error naming the flag, exit 2 |
| `--context-target 0` or a negative value | `--context-target takes a token count above 0`, exit 2 |
| No main session of a vendor in the window carries a context value | That vendor's `sessions_over_context_target` is `null` (`n/a` in the tables), `notes` says so; never 0 |
| Ingest fails inside `dream` | A line in `dream.log`; the dream's exit status and `last_run.error` are unchanged |

## Non-goals

- Storing or sending arguments, prose, file contents or tool output.
- Sending metrics off the machine. Feedback reaches the harness only as dream or retro proposals, applied by hand (ADR-055, ADR-062).
- A server, a live dashboard or a JavaScript chart library.
- Rebuilding the store from transcripts that no longer exist.

## Open questions

- The Antigravity field numbers for tool name, command line, subagent link, compaction and denial. Default: unknown items stay null and are named in `notes`; REQUIREMENTS.md § Known limits holds the compaction row (Phase 3 spike).

## Verification

| What | Test |
|---|---|
| Store, migrations, refusals | `internal/metrics/store_test.go` (`-run 'Store\|Migrat'`) |
| `Verb` | `verb_test.go` (`-run Verb`) |
| Antigravity ingest from copies (read-only source directory), idempotence, transcript root, canary with its must-fail control | `agy_test.go` over `testdata/agy/` |
| Historical `claude` rows kept, shown as a second column only when present, never written; `columns` | `historical_test.go` |
| Shared vocabulary (Antigravity names, and the historical Claude names) | `tools_test.go` |
| Events and A-group definitions | `events_test.go` over a git fixture in `t.TempDir()` |
| `show --json` keys and values | `show_test.go` golden |
| Report: no script, no host, numbers equal to `show` | `report_test.go` golden, plus a browser check (plan Phase 6) |
| `uninstall` keeps the store | `internal/harness` `-run TestUninstallKeepsMetricsStore` |
| Dream ingests inside the lock, never in a dry run | `internal/dream` `-run Ingest` |
| E2E on Antigravity | testing guide § 4 runs, plan § Sandbox tests |
