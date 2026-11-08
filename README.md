# fyr

Your rules, skills and subagents for the **Antigravity CLI** (`agy`), kept in one repository and installed the same way on every machine you work from, plus `fyr`, a small Go binary that does the installing and keeps what a session cannot hold on its own: the searchable trail of each project, its memory, the shape of every session and a consolidation of idle projects that runs hourly while an `agy` session of yours is open. One operator, their own machines, a private repository. No daemons, no gates, no CI, no service.

## Why it exists

- **Context drifts.** A rule you add on the laptop is missing on the workstation a week later. Here every rule, skill and agent is a file in this repository; `fyr install` puts the same set on every machine, and `fyr update` moves a machine forward when you say so.
- **Sessions forget.** A CLI session ends and its lessons go with it. The harness writes them down in the project's own trail (handoffs, decisions, bugs, plans) and in a memory store the CLI reads at the start of the next session.
- **A CLI cannot measure itself.** `fyr metrics` records the shape of every session (tools, tokens, errors, never your prose), so you can see where the work goes and tune the harness from numbers instead of impressions.

## How it works

This map is the contract for how the harness reaches a machine and for what leaves one: the dreamer's redacted prompt, the operator's `fyr eval` runs, the operator's `fyr work` workers and `fyr auto`'s workers. The strict rules that keep it true are in `AGENTS.md` § How the harness works; `TestHarnessMap` fails when the map names a command the binary does not have.

```mermaid
flowchart LR
    subgraph checkoutLane["Your harness checkout"]
        edit["Edit context/, skills/, agents/, commands/, rules/"]
        ship["gofmt, vet, test -race<br/>commit and push main"]
        preflight["/preflight vX.Y.Z<br/>tag, binaries and zip, no key"]
        retroHarness["/retro --harness<br/>applies the proposals queued on this machine"]
    end
    subgraph githubLane["GitHub, private"]
        harnessRepo[("fyr<br/>main: what checkout places follow<br/>tags: release markers")]
        releases[("Releases<br/>platform binaries + release zip + SHA256SUMS")]
    end
    subgraph placeLane["Each place"]
        checkoutPlace["Checkout<br/>the normal path"]
        binaryPlace["Release binary<br/>harness built in, no repository"]
        zipPlace["Zip place<br/>repo zip downloaded by hand, no tree kept<br/>a development build leaves by install --repo"]
        installer["fyr install / fyr update"]
        homeFiles["~/.gemini/config<br/>installed files, no repository"]
        sessions["Antigravity sessions<br/>feedback-log skill logs, /retro queues"]
        configDir["~/.config/fyr/<br/>brain.db, metrics.db, search.db, state.json<br/>stays on this machine"]
        dreamer["fyr dream --all<br/>hourly user timer, while agy runs<br/>a CLI with no tool granted"]
        evalRun["fyr eval rows<br/>typed by you, each row a sandboxed clone and HOME"]
        workRun["fyr work Kickoff file<br/>typed by you, a sandboxed clone with no remote and HOME, in docker or host"]
        autoRun["fyr auto run<br/>hourly user timer, when turned on<br/>one M18 worker or eval run, in docker or host"]
    end
    edit --> ship
    ship -- "git push" --> harnessRepo
    ship --> preflight
    preflight -- "tag marks a version" --> harnessRepo
    preflight -- "binaries, zip and SHA256SUMS" --> releases
    harnessRepo -- "clone once, update pulls" --> checkoutPlace
    releases -- "update downloads and checks" --> binaryPlace
    harnessRepo -. "zip, downloaded by hand; update --from-zip repeats it" .-> zipPlace
    releases -. "release zip + SHA256SUMS, by hand; checked" .-> zipPlace
    checkoutPlace --> installer
    binaryPlace --> installer
    zipPlace -.-> installer
    installer -- "writes files" --> homeFiles
    homeFiles -- "read at session start" --> sessions
    sessions -- "logs" --> configDir
    configDir -. "same machine only" .-> retroHarness
    retroHarness --> edit
    sessions -- "memory, trail, transcripts" --> dreamer
    dreamer -- "proposal records, metrics.db" --> configDir
    dreamer -. "redacted prompt, no tool granted" .-> provider[("the CLI's model provider")]
    dreamer -. "one conversation per project, in /resume" .-> sessions
    evalRun -- "testing record, metrics.db" --> configDir
    evalRun -. "a sandbox's prompts and the files its agent reads, unredacted" .-> provider
    workRun -. "a worker's prompts and the files its agent reads, unredacted" .-> provider
    autoRun -- "report records, runs.json; at level harness, eval-gated local commit" --> configDir
    autoRun -. "a worker's or eval's prompts and the files its agent reads, unredacted; redacted learning inputs" .-> provider
    linkStyle 0,1,5,6,7,8 stroke:#0B7A74,stroke-width:2px
    linkStyle 2,3,4 stroke:#8A3A78,stroke-width:2px
    linkStyle 9,10,11,12,13 stroke:#3B4FB8,stroke-width:2px
    linkStyle 14,15,16 stroke:#A8660F,stroke-width:2px
    linkStyle 17,18,19,20,21,22,23,24,25 stroke:#5B7F1E,stroke-width:2px
```

| Colour | Path | What it carries |
|---|---|---|
| Teal | **update** | a place moving forward |
| Indigo | **install** | a place writing the installed files |
| Amber | **the loop on this machine** | corrections and proposals stay in `~/.config/fyr/` and reach this repository only through `/retro --harness`, run by you on the machine that logged them (ADR-062), or an eval-gated `fyr auto` commit to this checkout's local branch (R9, ADR-090), which you push |
| Plum | **release** | `/preflight` tagging `main` and publishing the release |
| Olive | **learn** | the dreamer, hourly while an `agy` session of yours runs, `fyr eval`, `fyr work` and `fyr auto`: on the machine, not ways in. Their dashed lines to the CLI's model provider are the things fyr sends off the machine by design: the dreamer's redacted prompt (ADR-049); the prompts of an eval sandbox you or `fyr auto` started and the files its agent reads, unredacted as in any session (ADR-065, ADR-090); the same for a `fyr work` worker you started or a `fyr auto` worker started by the timer when you turned auto on (each in docker when `fyr work`'s docker preflight passes, otherwise on the host, unless `fyr work --runner` names one; ADR-076, ADR-090, ADR-504), plus a learning worker's redacted conversations, memory, proposals and metric numbers and a missing-skill worker's search on skills.sh and the npm registry: fyr sends nothing else, but its agent runs with `command(*)` and the network, so a misled agent could send what it reads elsewhere; the docker runner keeps it off your files but its login token, not off the network. The dreamer's line to the sessions, one conversation per project that `/resume` lists, is dashed because only Linux has run it end to end; the operator's macOS run turns it solid (ADR-073). |

A dashed line is a gap: done by hand or limited to one machine. The design around the map is `SDD.md` § Deployment; what a release delivers to each kind of place is stated once, in `SPEC-fyr-delivery` § The model.

## What you get

| | |
|---|---|
| One global context | `context/AGENTS.md`, in the [agents.md](https://agents.md) standard, deployed as `~/.gemini/config/rules/AGENTS.md` |
| Skills | 39 of them, each a directory with a `SKILL.md` in the [Agent Skills](https://agentskills.io) standard; the model loads one when its description matches, but for `auto`, which a `fyr auto` Kickoff names, and you can type any of them as `/name` |
| Subagents | 8, each `agents/<name>/agent.md` in Antigravity's format, installed as written |
| Rules | always-on rules under `rules/` (today: critical thinking, one session per task or phase, code modularity, and agy dispatch) |
| The binary | `fyr`: install (including the `< 250 ms` fail-open CLI hooks in `~/.gemini/config/hooks.json`), update, status, uninstall; `record`, the brain store of every project's trail records; `search` over those records, a project's files and its AST symbol/reference graph (`--slice`, `--skeleton`, `--path`, `--affected`, `--map`, `--vet`, `--hook`); `memory`; `metrics`; the `dream`, hourly while an `agy` session runs; `auto`, a switch with its own timer, off by default |

## Quick start

You need Go 1.24 or newer and git. The repository is private, so clone with your own SSH key or `gh` login.

```bash
git clone git@github.com:kesokaj/fyr.git ~/workspace/fyr
cd ~/workspace/fyr
go run ./cmd/fyr install          # builds the binary, installs it to ~/.local/bin/fyr, deploys everything
fyr status                        # ends with "drift none"
```

Put `~/.local/bin` on your `PATH` if it is not already. Then start a session in any project: the global context loads, and the `/init-project` skill scaffolds a new project's trail. The first build fetches one Go module, the pure-Go SQLite driver behind `search`, `record` and `metrics`.

Two things to know about your home directory. `install` writes only under `~/.gemini/config/` (including `~/.gemini/config/hooks.json`, plus `~/.local/bin/fyr`) and edits no vendor setting (ADR-062, ADR-511). And the harness's own state lives in `~/.config/fyr/` (the brain store `brain.db` with the global feedback, proposal and retro records, the search index, the metrics store); Antigravity prompts for paths outside the workspace, so it may ask before first using that directory.

No checkout on this machine? § Install without cloning and § Install from a zip, below.

## Daily commands

| Command | What it does |
|---|---|
| `fyr status` | What is installed, from which commit, and any drift; exit 1 on drift |
| `fyr update` | Moves this place forward: a checkout pulls, a release binary downloads the newest release, a zip place passes the zip with `--from-zip` |
| `fyr install` (or `sync`) | Deploys the harness and removes what the repository no longer has |
| `fyr diff` | A unified diff of every drifted file |
| `fyr uninstall` | Removes exactly what it installed and both timers; the metrics store and `fyr auto`'s config and state stay |
| `fyr list` / `render` | The skill and agent inventory; the context as deployed |
| `fyr record add` / `get` / `edit` / `list` / `export` / `sync` / `import` | The brain store: this project's trail records and the global ones (`export` writes `.agents/trail.jsonl` on demand; `sync` loads it into `brain.db`) |
| `fyr resume` | Prints this checkout's seven-step session resume (`AGENTS.md`, open records, newest handoff, `TODO.md` Now, open plans, recent changes, memory) in one read-only call |
| `fyr search <words>` | Matching records first, then a ranked search of the project's files; `--slice` builds a tiered AST context capsule (`path:start-end (Symbol)` pivots, 1-hop skeleton neighbours, trail records), `--skeleton` prints signatures without bodies, `--path` traces shortest call/import hops (`--to`), `--affected` lists callers and blast radius, `--map` prints the repository symbol map, `--vet` runs the `< 25 ms` 9-mode intent classifier and 6-anchor prompt vetter, and `--hook` serves the `< 250 ms` fail-open CLI hooks (`pre-invocation`, `pre-tool-use`, `stop`) in `~/.gemini/config/hooks.json` |
| `fyr memory add` / `show` | The project memory a session loads at start |
| `fyr metrics ingest` / `show` / `report` / `evals` / `export` | The shape of every session: stored, summarised, charted, backed up; `evals` shows `fyr eval`'s trials per row (`--home` of the eval home) |
| `fyr dream --install-timer` | The hourly dreamer, while an `agy` session of yours runs; it only proposes |
| `fyr auto on` / `off` / `status` / `run` | The switch of the hourly idle-time run. A run reads its gates, runs the free checks on idle, clean projects, files a global `review` record when the findings changed, and past level `observe` starts at most one M18 worker or eval re-run per run (up to 4 a day, 30m each, in docker when `fyr work`'s docker preflight passes, else host) from a fixed-template Kickoff in `~/.config/fyr/auto/kickoffs/`; for an applied class's `PASS` (or a `harness-edit` at level `harness` that passes the eval gate at `k=3` on target, regression and held-out rows within 24 eval sessions), `fyr` squash-merges `work/<name>` in a detached worktree, runs the base tree's `# auto: test` lines and `fyr check`, fast-forwards the project's local branch by one commit and deletes `work/<name>`, never pushing and never installing into a real home (`install pending`) |
| `fyr check` | A read-only drift check of the docs, specs, `PRD.md` and the SDD and TDD traces against the binary and the tree |
| `fyr git-check` | Runs the three post-dispatch git checks (`git status --short --untracked-files=all`, `git log --branches --not --remotes --oneline`, `git tag`) in one read-only call |
| `fyr serve` | Pages of this project and this home on `127.0.0.1:3000` until Ctrl-C, with buttons for `fyr index` and `fyr metrics ingest`; open the printed URL, which holds the run's path and token |
| `fyr eval --home DIR [--runs N] [--parallel N] [--acp] [--suite-file PATH] <row>...` | The behavioural suite against the Antigravity CLI, each row run N times (default 1, at most 25; up to `--parallel N` trials at once, default 1, `kind: benchmark` rows at 1), each trial in its own sandbox (or warm multi-turn `agy acp --stdio --process-per-session` session when `--acp` is set or the row ID starts with `acp-`) and stored as a trial in the metrics store |
| `fyr work [--timeout D] [--model M] [--max-workers N] [--workers N] [--ram GB] [--cpu N] [--disk GB] [--keep] [--acp] [--runner auto\|host\|docker] [--image REF] [--agy-linux PATH] <kickoff-file>...`, `fyr work config [--workers N] [--ram GB] [--cpu N] [--disk GB]`, `fyr work --check [--runner auto\|host\|docker] [--image REF] [--agy-linux PATH]`, `fyr work --status`, `fyr work --kill <stamp>` | Each Kickoff run as an unattended worker in its own sandbox (`~/.config/fyr/sandbox/<project>/<stamp>/<name>/`), on a clone of this checkout with no remote, up to N at once across the machine (default 8, at most 32); a Kickoff's `Model:` line overrides `--model` for that worker, and every named model is checked against `agy models` before anything is made; each branch is fetched back by path and a result contract of at most 39 lines per worker is printed, in file order; the stamp line and each result line name the runner and its reason (`runner docker` or `runner host (<reason>)`); `--runner auto`, the default, picks docker when the docker preflight passes and the host otherwise, its reason saying why, `--runner docker` refuses rather than fall back, and `--image` and `--agy-linux` are refused with `--runner host` (ADR-504); the docker runner runs each worker's agy session in a hardened container of `--image` (default `fyr-work:local`, built automatically from the embedded `docker/work.Dockerfile` when the Docker daemon is running, or by the build line in `SPEC-fyr-work` § `--runner docker`) with a linux agy (`--agy-linux`, or `~/.config/fyr/work/agy-linux`, or the host's agy on linux) and syncs the macOS Keychain login to `~/.config/fyr/work/antigravity-oauth-token` when needed while every other step stays on the host; every run emits `work: [<name>]` progress lines to stderr and writes `~/.config/fyr/work/<stamp>/status.json`, while `--acp` drives host sessions over `agy acp --stdio --process-per-session` (`session.id` and `events.jsonl`) with per-step and subagent progress lines (refused with `--runner docker`, and selecting `runner host (--acp)` under `--runner auto`); `config` prints or persists the worker queue and sandbox resource limits (`workers=8`, `ram=8g`, `cpu=2`, `disk=10g`) in `~/.config/fyr/work/config.json`; `--check` prints the runner a run picks for the same flags, `work: runner docker`, `work: runner docker (fyr-work:local will be built first)` or `work: runner host (<reason>)`, and exits 0 without building an image or making a sandbox, network or slot: a missing or stale default image that a run builds first reads as docker, saying so (ADR-504a); under `--runner docker` it prints the preflight's refusal and exits 2; `--status` prints the newest wave's worker status board from `status.json`; `--kill` ends the run whose stamp line printed `<stamp>` and, with docker, removes that stamp's containers; it never pushes or merges |
| `fyr adopt <name>` | Copies a skill installed elsewhere into `skills/` |
| `fyr version` | The build version |

`fyr help <verb>` prints a verb's page. The global flags are `TDD.md` § T5; each verb's full behaviour is its `SPEC-fyr-*` record (`fyr record list --kind spec`, then `fyr record get`).

## Install without cloning

A machine that only uses the harness needs no checkout: a release binary carries `context/`, `skills/`, `agents/`, `commands/` and `rules/` inside it. Each route below ends with the binary in `~/.local/bin`; each needs your own GitHub credential, because the repository is private. Set `V` to the version you expect from a source outside the download (the person who cut it, or a checkout's changelog), never from the Releases page itself: the guarantee is "the version you named, or nothing". The checksums prove the download is whole; who made the release rests on your GitHub login and HTTPS (ADR-053).

With `gh`, one chained command, so a failed check stops the move (on macOS replace `sha256sum -c -` with `shasum -a 256 -c -`):

```bash
V=vX.Y.Z \
&& D=$(mktemp -d) && cd "$D" \
&& gh release download "$V" --repo github.com/kesokaj/fyr --clobber \
     --pattern 'fyr-linux-amd64' --pattern 'SHA256SUMS' --dir "$D" \
&& grep -qxF "# fyr $V" SHA256SUMS \
&& grep -v '^#' SHA256SUMS | sha256sum -c - --ignore-missing \
&& chmod +x fyr-linux-amd64 \
&& test "$(./fyr-linux-amd64 version)" = "fyr $V" \
&& mv fyr-linux-amd64 ~/.local/bin/fyr && cd ~ && rm -r "$D"
```

A machine on v2.7.0 or earlier crosses once by `DEPLOY-remote` § Crossing to fyr, not by this block alone.

With `go install`, for v3.0.0 or newer. The module path is an https URL and the repository is private, so git is told to fetch over SSH for this one command; this route trusts your SSH connection and git's object hashes, not a checksum file, so use it where you already trust the repository:

```bash
GOPRIVATE='github.com/kesokaj/*' \
  GIT_CONFIG_COUNT=1 \
  GIT_CONFIG_KEY_0='url.git@github.com:.insteadOf' GIT_CONFIG_VALUE_0='https://github.com/' \
  go install github.com/kesokaj/fyr/cmd/fyr@v3.0.0
```

From the Releases page in a browser, shown for a Mac (on Linux use `sha256sum -c -` and drop the `xattr` line, which lifts the browser quarantine that would otherwise stop the binary from running):

```bash
V=vX.Y.Z \
&& ls ~/Downloads/fyr-darwin-arm64 ~/Downloads/SHA256SUMS >/dev/null \
&& D=$(mktemp -d) && mv ~/Downloads/fyr-darwin-arm64 ~/Downloads/SHA256SUMS "$D" && cd "$D" \
&& grep -qxF "# fyr $V" SHA256SUMS \
&& grep -v '^#' SHA256SUMS | shasum -a 256 -c - --ignore-missing \
&& chmod +x fyr-darwin-arm64 && { xattr -d com.apple.quarantine fyr-darwin-arm64 2>/dev/null || true; } \
&& test "$(./fyr-darwin-arm64 version)" = "fyr $V" \
&& mv fyr-darwin-arm64 ~/.local/bin/fyr && cd ~ && rm -r "$D"
```

Then, in this order:

```bash
fyr version     # first, unqualified: the version you just downloaded, or another fyr on PATH is winning
fyr install     # installs the harness built into the binary
fyr status      # "source embedded vX.Y.Z" and "drift none"
```

`version` comes first on purpose: an earlier `PATH` entry holding another `fyr` would run instead and report success about the wrong tree (BUG-078). `install` and `status` warn about such a shadow; if `version` names the wrong build, `hash -r` (bash) or `rehash` (zsh) first, then `which -a fyr` to find the earlier copy and point it at this binary or remove it. The assets are `fyr-linux-amd64`, `fyr-darwin-arm64` and `fyr-linux-arm64`, beside `SHA256SUMS`. To move such a machine forward later, `fyr update` there: it needs `gh` on `PATH`, accepts only a canonical `vX.Y.Z` tag newer than what is installed, checks the binary against the release's checksums, and only then replaces itself and reinstalls.

## Install from a zip (no SSH, no gh)

A machine that can neither hold your SSH key nor run `gh` still gets the harness: download a release's `fyr-vX.Y.Z.zip` and `SHA256SUMS` from the Releases page into one folder, together with its `fyr` binary (verified as above), and run:

```bash
fyr update --from-zip ~/Downloads/fyr-vX.Y.Z.zip   # SHA256SUMS beside it: the zip is checked first
fyr status                                         # "installed … from zip sha256:<hex> (verified vX.Y.Z)"
```

A zip of `main` ("Code → Download ZIP") works too, but no release lists it, so it installs marked unverified. The zip is unpacked into a temporary directory under `~/.config/fyr/`, installed, and the directory removed; nothing of the zip is kept. To move forward, download the next zip and repeat. A development build of `fyr` at a zip place: `SPEC-fyr-delivery` § The model.

## Adding your own skills, agents and rules

Every customization is a file in a directory: drop it in, run `fyr install`, commit. Nothing to register.

| Put it in | What it is | Antigravity gets |
|---|---|---|
| `skills/<name>/SKILL.md` | a skill (Agent Skills standard); loaded when its description matches, typed as `/name` | `~/.gemini/config/skills/<name>/` |
| `agents/<name>/agent.md` | a subagent in Antigravity's `agent.md` format (`name`, `description`, `subagent`, `mainAgent`, `model`, `tools`) | `~/.gemini/config/agents/<name>/agent.md`, as written |
| `rules/<name>.md` | an always-on or path-scoped rule; it shares the context's 24,000-byte rules block | `~/.gemini/config/rules/<name>.md` |
| `commands/<name>.md` | a plain markdown prompt invoked as `/name` (prefer a skill) | `~/.gemini/config/workflows/<name>.md` |
| `context/<name>.md` | more global context, appended after `AGENTS.md` | part of `~/.gemini/config/rules/AGENTS.md` |

The installer refuses what would grant a permission behind your back: a skill without a `description`, a skill, command, rule or agent with a frontmatter key outside its kind's allow list (so `allowed-tools`, `hooks` or any other permission key), a command or rule whose file name is not one plain name, a frontmatter value a strict YAML parser would read as a comment, and an agent tool name Antigravity does not know (an unknown name hangs its subagent). `TDD.md` § T3 and § T6 list each refusal; its test names the bug behind it.

Third-party skills go in verbatim and are listed in `skills/VENDORED.md`; from [skills.sh](https://skills.sh), `npx skills@<version> add <owner/repo> -s <skill>` lands in `skills/` through the `.agents/skills` symlink, then `fyr install` and commit. A skill you already installed globally comes in with `fyr adopt <name>`.

## Skills

`fyr list` prints every skill with its description. By purpose:

| Group | Skills |
|---|---|
| Work protocols | `feature` (design before code, a plan file, a cold-reader check, phases), `work` (leads a plan's phases, or a task list with no plan, as sandboxed `fyr work` workers and merges each PASS), `bug` (diagnose, failing test first, smallest fix, record), `review` (one review command with lenses `--code --security --simplify --docs --prompts --tests --all --resume`), `review-standards` (what the reviewer checks), `preflight` (the release gate), `harness-audit` (audits this repository area by area) |
| Project shape | `init-project` (scaffold or update a project's trail), `requirements`, `spec`, `testing-strategy`, `explain-project` (onboarding notes for a person), `research`, `handoff` (the session's checkpoint, ending in the Kickoff that starts the next session) |
| Learning loop | `feedback-log`, `project-memory`, `retro`, `distill`, `dream` |
| Code | `golang`, `typescript`, `frontend-ui-modern` (shadcn/ui on Tailwind), `frontend-ui-economist` (plain HTML/CSS), `bash`, `json-api-style`, `docker`, `mermaid-syntax` |
| Google Cloud | `gcloud-setup`, `cloud-run-deploy`, `terraform`, `operate-gcp`, `data-gcp`, `api-auth-gcp`, `gke-basics`, `google-catalog` (routes to Google's published skills, vendored as data under its `catalog/`) |
| Vendored verbatim | `grilling`, `codebase-design`; provenance in `skills/VENDORED.md` |
| Forks | `discover-skills` (finds a published skill and adds it to `skills/`, forked from `find-skills`), and `gke-basics` above; provenance in `skills/VENDORED.md` |

## Layout

```
PRD.md        what the harness is for and what must never drift (R0–R10)
SDD.md        how the parts fit: components M1–M15, the main flows, the decisions
TDD.md        how each part is built: a section T1–T15 per component, and its tests
TODO.md       Now, Next and Backlog; the active plan record wins
AGENTS.md     the rules of this repository, for agents
context/      AGENTS.md (the global context) and dev-setup.md (environment, stack, GCP rule)
agents/       one directory per subagent (agent.md, in Antigravity's format, installed as written)
skills/       one directory per skill (SKILL.md, references/, assets/)
rules/        always-on or path-scoped rules
commands/     markdown prompts invoked as /name (empty; prefer skills)
cmd/fyr       the binary; internal/ its packages, each named in AGENTS.md § Layout
embed.go      the go:embed of the five content directories and docker/, pinned to what git tracks
.agents/      this repository's .agents/skills symlink, scratch/ (gitignored), and optional on-demand trail.jsonl export (records live in ~/.config/fyr/brain.db, read and written with fyr record)
```

```mermaid
flowchart LR
  prd["PRD.md<br/>requirements R0 to R10"] --> sdd["SDD.md<br/>components M1 to M15"]
  sdd --> tdd["TDD.md<br/>sections T1 to T15 and their tests"]
  tdd --> todo["TODO.md<br/>tasks name a T section"]
  tdd --> records[("SPEC and ADR records<br/>the detail")]
```

## Working on the harness

```bash
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...   # before any completion claim; -timeout 30m for a one-CPU box
go run ./cmd/fyr install --home .agents/scratch/home                         # never against your real $HOME
```

Two tests pin the deployed context's size (`internal/harness/size_test.go`): the hand-written context under 13,000 bytes (inside `PRD.md` G1's 15,000-byte ceiling), and the whole rendered block, skill inventory included, under 19,500 bytes (inside `PRD.md`'s 22,000-byte cap and Antigravity's 24,000-byte limit). `govulncheck` has two known, unreachable advisories (`DEPLOY-local` § Operator steps).

Where to read more: `AGENTS.md` for the rules of this repository; `PRD.md`, `SDD.md` and `TDD.md` for what the harness must do, how it fits and how it is built; `DEPLOY-remote` for the machines, the resync loop and the release procedure (`/preflight vX.Y.Z`, no key); the `adr` records (`fyr record list --kind adr`) for every decision and why.
