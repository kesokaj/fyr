---
name: fyr-init-project
description: "Scaffold a fyr project (/fyr-init-project): .agents/, root docs (AGENTS/PRD/SDD/TDD/TODO), seed guides, and run fyr index."
---

# Init Project Workflow

Scaffold a new project, or update an older scaffold, so its rules, skills and agents live in `.agents/` and its trail — records in `fyr`'s brain store (`fyr record`), exported on demand to `.agents/trail.jsonl` with `fyr record export` and loaded on another machine with `fyr record sync` — lets a zero-context agent resume. The templates ship with this skill in its `assets/` directory; how the trail works is `references/trail.md`.

## Usage

```
/fyr-init-project
```

In a new repository it scaffolds everything below. In a project an older harness scaffolded, it runs as an update that adds only what is missing; Step 1 decides which.

## Step 1: Confirm the project root

Confirm the current workspace is the intended project root (`git rev-parse --show-toplevel` when it is a repo). If `git rev-parse --show-toplevel` fails (not inside a git checkout), confirm with the user that the current directory is the intended project root, run `git init`, and check for nested `*/.git` checkouts (`find . -mindepth 2 -maxdepth 3 -name .git`) to add to `.gitignore` so nested clones are not skipped as `160000` gitlinks or duplicated. If unsure, ask the user.

- **New:** no `.agents/` and no `AGENTS.md`.
- **Update:** `.agents/` or `AGENTS.md` exists. Every step below adds only what is missing and never rewrites or deletes what the project wrote without the user's yes to Step 3's migration line: an update never edits `PRD.md`, an existing `AGENTS.md`, a record, or anything in `.agents/{rules,skills,agents,commands,workflows,scratch}`. Where the note under a file's title differs from the template's (`TODO.md`), it shows both and takes the template's note only on the user's yes. It asks the new-project questions only for a file it creates, leaves out a file it would change that has uncommitted changes and names it in the summary, and a second run adds nothing.
- **Legacy root:** `REQUIREMENTS.md` or `ROADMAP.md` at the root — an update whose Step 3 offers the migration line (its last paragraph covers a `PRD.md` already there); a session that finds `PRD.md` missing at resume names it (global context § Resume, then checkpoint); this update is the fix.
- **Migration:** `<project>/.agents/docs/` or a `docs/` trail tree exists and no records exist yet in the brain store (`fyr record list` refuses). The trail is still files: Step 4 migrates it before anything else is added.

Every write this skill makes — a root file, a template copy, a `.gitignore` line — first checks that the target path and its directory are not symlinks (`test -L`): a committed link would carry the write outside the project. A linked path is reported and skipped, never written through.

## Step 2: Locate the templates

The templates live next to this SKILL.md (`$ASSETS` below is the directory this prints):

```bash
ls -d ~/.gemini/config/skills/fyr-init-project/assets .agents/skills/fyr-init-project/assets 2>/dev/null | head -1
```

If none exists, say so and stop — do not improvise the templates from memory. The templates live outside the project, so reading them may take one permission approval; a headless run passes the skill directory explicitly (a `read_file` allow-rule for it).

## Step 3: Root files

Copy each template only when the file does not already exist, then fill in the ones this run copied (`PRD.md` only in part, below); a file that existed keeps its content. Filling a section means replacing its template comment with the project's own text, or — when the project genuinely has nothing there yet — one line saying so and what would go in it ("No test framework yet; the first test command lands here."). A section left as the template's comment lines reads as scaffolding nobody finished and tells the next session nothing; every `##` heading the run copied ends with a sentence a cold reader can act on:

```bash
for f in AGENTS.md PRD.md SDD.md TDD.md TODO.md; do
  [ -L "$f" ] && { echo "kept $f: a symlink, not written through"; continue; }
  [ "$f" = PRD.md ] && [ -e REQUIREMENTS.md ] && { echo "PRD.md: the migration below makes it"; continue; }
  [ -e "$f" ] || cp "$ASSETS/$f" "$f"
done
```

- `AGENTS.md` — ask the user for a one-paragraph project overview, the project archetype (Go service, **Standalone CLI** / script / library, or standalone TypeScript/React SPA), and the deploy target (none, a remote box / non-GCP host, or GCP); when `go.mod`, root/nested (`web/`, `ui/`) `package.json`, `compose.yaml` / `docker-compose.yml`, or a Dockerfile exists, derive the build, run, compose/DB, and test commands from them (`npm ci`, `npm run build`, `npm run preview`, `npm test`, `npm run typecheck` for standalone TS/React; `go build`/`go test -race ./...` + `fyr verify` for Go; native lint/tests for scripts), print them and ask "confirm or correct", otherwise ask for them; delete every template command line the user did not confirm; fill the sections (`docs-ste100-80` BLUF openings); `## Deploy` names the targets that exist and points at `DEPLOY-local`, `DEPLOY-remote` (including non-GCP hosts/compose), and `DEPLOY-gcp`. For a **Standalone CLI** / script or standalone TS/React app without Go, omit `<PORT>`, `go run ./cmd/server/`, and `web/`/`DEPLOY-*` server boilerplate, and explicitly note in `AGENTS.md` that non-Go projects run their native test/lint commands and close tasks with `fyr record add handoff` instead of `fyr close` (`verify.stamp` is Go-only). Only list standards that **differ** from the global context; delete the section if nothing differs.
- No other instruction file is written: the CLI reads the root `AGENTS.md`. Say in the summary that an `AGENTS.md` in a vendored or cloned subtree, if present, is instruction text too, and never keep a template `AGENTS.md` beside source.
- `PRD.md` — copied with only its header filled (the project name, the overview's first sentence, `Last updated:` from `date +%F`), keeping the template's example `- **R1**`/`- **R2**`/`- **S1**` items inside their ```` ```markdown ```` fences so `fyr check` reports `0` findings on fresh scaffolds until `/fyr-requirements` replaces them with user-approved items. Its problem, users, goals and requirement items are the user's, and nothing is written into them without the user's approval of the exact text. Tell the user to run `/fyr-requirements` (the `fyr-requirements` skill elicits a draft).
- `SDD.md` and `TDD.md` — `Last updated:` from `date +%F`, then filled following `docs-ste100-80` (1–3 sentence BLUF opening each `##` section, `<= 12`-node Mermaid diagrams via `mermaid-syntax`, `<details>` folding for `> 12`-row tables) from the code as it is and the answers above: one `M#` row per binary, CLI command/package, or frontend module that exists, one `T#` section per `M#`, each trace table matching them; for a **Standalone CLI**, omit HTTP/browser flow placeholders; with no code yet, one row naming the first component and each section's "not yet" line, a diagram kept only when its nodes are real. Until `PRD.md` has active unfenced `R#` items, keep `Satisfies` as `—` and omit placeholder `R#` rows from `SDD.md` `## Trace` so `fyr check` stays green: never invent a requirement ID.
- `TODO.md` — `## Now` names the open plan's phase and its tasks, or stays empty; `## Next` and `## Backlog` may be empty. Note that multi-phase `workers: yes` `PLAN-*` records created by `/fyr-feature` declare `files:`, `model:`, `base-check:`, `judge:`, and `judge-model:` lines per worker phase for `/fyr-swarm` (`fyr gate` / `fyr land`), while single-phase or `workers: no` plans omit them.

**Migrating the old root files (an update only).** When `REQUIREMENTS.md` or `ROADMAP.md` exists, ask once, as one numbered approval line naming only the files present:

```text
Migrate the root docs: REQUIREMENTS.md → PRD.md (requirement text unchanged, the missing PRD sections added as placeholders); ROADMAP.md's Now and Next → TODO.md, then ROADMAP.md deleted? [yes]
```

Only on the user's yes, and only with the files it names committed (`git status --porcelain -- REQUIREMENTS.md ROADMAP.md` prints nothing):

1. `git mv REQUIREMENTS.md PRD.md`. The template's title and header lines replace line 1, and each `##` section of the template the file lacks (`## Problem`, `## Users`, `## Goals`, `## Open questions`, and any other) is added as the template's placeholder at the template's position. Every other line stays as it was.
2. Check, before anything else is edited; it prints nothing:
   ```bash
   comm -23 <(git show HEAD:REQUIREMENTS.md | sed '1d' | sort) <(sort PRD.md)
   ```
3. The one old line then changed: in the old opening comment, `REQUIREMENTS.md and ROADMAP.md in one commit`, when present, becomes `PRD.md and TODO.md in one commit`.
4. `TODO.md` gets the template's `## Now`, `## Next` and `## Backlog` headings it lacks (its own items stay where they are), `ROADMAP.md`'s Now and Next lines, without their template comments, move under `## Now` and `## Next`, and `git rm ROADMAP.md`.

When `PRD.md` already exists beside `REQUIREMENTS.md`, the line leaves out its `REQUIREMENTS.md` part (a `ROADMAP.md` is still offered): the summary names both files and says `fyr check` reads `PRD.md`. Without the yes, both files stay as they are, `PRD.md` is not created beside `REQUIREMENTS.md` (`fyr check` would read it instead), and the summary names them as not migrated.

## Step 4: The `.agents/` tree and the trail

**Migration first.** When Step 1 found the trail still in files, migrate it before anything else, with the tree committed (the import refuses uncommitted changes):

```bash
fyr record import --dry-run        # counts per kind, every record it would keep, every file to remove
```

Show the user the first line (counts per kind), the `problem` lines and how many files would be removed; on their yes run `fyr record import`:

- Before committing, show every `fyr record import: warning:` line it printed (a record named by ID or list position whose text the redactor would change, stored as written) and fix each so the secret leaves the store (`fyr record edit <ID> --title <title>` or `--body-file <path>`; for a warning on ID or source path, rename that source file and commit before re-running import).
- Run `git rm` by name on every path printed under `remove with git rm by name` (quoted for the shell), and `rm` by name on the printed old memory files.
- Leave `problem` files in place and name them in the summary. The import keeps open plans, the newest handoff, every ADR and bug, specs, deploy docs, guides, and everything those or the project's files reference; the rest stays in git history.

**The tree:**

```bash
if [ -L .agents ]; then echo ".agents is a symlink: the tree is not written through it"; else
(cd "$ASSETS/agents-tree" && find . -type f) | sort | while read -r f; do
  d=".agents/${f#./}"; dir=$(dirname "$d")
  { [ -L "$d" ] || [ -L "$dir" ]; } && { echo "kept $d: a symlink, not written through"; continue; }
  [ -e "$d" ] && continue                                                         # the project's file wins
  [ "${f##*/}" = .gitkeep ] && [ -n "$(ls -A "$dir" 2>/dev/null)" ] && continue   # a .gitkeep only holds an empty directory
  mkdir -p "$dir" && cp "$ASSETS/agents-tree/$f" "$d" && echo "added $d"
done
fi
find .agents -maxdepth 1 | sort
```

That yields:

```
.agents/
├── rules/  skills/  agents/  workflows/  commands/   # project-level customizations
└── scratch/        # gitignored: every generated or temporary file, agents' drafts under scratch/agents/
```

**Seed records.** Each template under `$ASSETS/records/` becomes a record only when the project has none of that ID (`fyr record get <ID>` exits 1):

| Template | Command when absent |
|---|---|
| `GUIDE-testing.md` | `fyr record add guide --id GUIDE-testing --title "Testing guide — <project>" --body-file <filled copy>` |
| `DEPLOY-local.md` | `fyr record add deploy --id DEPLOY-local --title "Deploy — local" --body-file <filled copy>` |
| `DEPLOY-remote.md` | `fyr record add deploy --id DEPLOY-remote --title "Deploy — remote" --body-file "$ASSETS/records/DEPLOY-remote.md"` |
| `DEPLOY-gcp.md` | `fyr record add deploy --id DEPLOY-gcp --title "Deploy — GCP" --body-file "$ASSETS/records/DEPLOY-gcp.md"` |

A filled copy is written to `.agents/scratch/` first and follows `docs-ste100-80` (BLUF openings, compact tables, `<details>` for large reference matrices). `GUIDE-testing` is the project's one testing guide; `verifier` fills any missing e2e/smoke `<placeholders>` on its first live e2e run (the tiers it lists are the `testing-strategy` skill's; omit unused Browser e2e / Container smoke / Cloud smoke rows for a **Standalone CLI** or script). For `DEPLOY-local`, the Build, Run and Test blocks take the setup answers verbatim, `## Database & migrations` is populated when `compose.yaml` / `docker-compose.yml` or `migrations/` exists (otherwise kept as `"No database yet."`), and a `<placeholder>` the answers do not fill is deleted — the Configure row when the code reads no setting yet, the Verify command until the endpoint or CLI check exists, and `PORT`/HTTP `/healthz` lines for a **Standalone CLI** — never replaced with an invented value. `DEPLOY-remote` (including non-GCP container/compose or static SPA hosts) and `DEPLOY-gcp` (including static/SPA Cloud Run container or GCS + Cloud CDN options) keep their *Not applicable yet* line until the user names a target; the GCP record is where the defaults the global context asks for get recorded, never invented. `assets/records/SPEC-TEMPLATE.md` is not seeded: the `fyr-spec` skill copies it when a spec is written.

Antigravity is meant to read `.agents/` natively, but a project skill or agent may not load, and which copy wins a name clash is not guaranteed: a project skill or agent never reuses a harness name — extend one with a differently named skill or a `<project>/.agents/rules/` file. Check the CLI's `/skills` and `/agents` before relying on a project one. `workflows/` is kept for older Antigravity versions; new slash commands are skills.

## Step 5: Memory

Nothing to set up: `fyr memory add` stores a project's memory as `memory` records in `~/.config/fyr/brain.db`, keyed by the main worktree's root, never in the checkout. An old `.agents/memory/` store is migrated by Step 4's import (or the operator's `fyr record import --global`), then removed by name.

## Step 6: README, .gitignore, and Project Index

- If no `README.md` exists (`test ! -e README.md`; a symlink is kept and reported), copy the template `$ASSETS/README.md` and fill it following `docs-ste100-80` (1–3 sentence BLUF per `##` section, `<= 12`-node Mermaid diagram when 3+ components interact, `<details>` for dense tables) and its `<placeholders>` from the Step 3 answers: the project name, the one-line overview, the build, run and test commands, the entry point under `cmd/` (or script/SPA directory), the two or three architecture sentences (entry point, what it talks to, where state lives — from the code, one line saying "not yet" where it has none), `<PORT>` and `<project>` from the run command and the overview (omitting `web/`, `<PORT>`, and server-only lines for a **Standalone CLI** or unused rows on standalone frontend apps), the configuration table from the flags and env vars the code reads (`grep -rn 'flag\.\|os.Getenv' cmd internal` — cite what exists, never invent a setting). An existing `README.md` is left alone, whatever its shape.
- Append each of these lines that `.gitignore` lacks never removing or reordering a line already there, and only when `.gitignore` is not a symlink (`test ! -L .gitignore`; otherwise report the lines for the user). `.agents/memory` stays ignored so an old store is never committed before it is migrated:

```
.DS_Store
.agents/memory
.agents/scratch/*
!.agents/scratch/.gitkeep
```

- Immediately after ensuring `.agents/scratch/*` is in `.gitignore`, build the project's FTS5 and AST graph index (`.agents/scratch/search.db`) by running `fyr index` and `fyr search --map`, and offer `fyr index --register` for cross-project `--global` search (which works immediately because Step 4 already seeded `GUIDE-testing` and `DEPLOY-*` in `brain.db`):

```bash
fyr index
fyr search --map
```

## Step 7: Summary, commit, push

Report what was created, or for an update what it added and what it kept as the project's, each record it seeded (ID), the `fyr index` and `fyr search --map` summary, for a migration the import's counts and the files removed, and remind the user to run `/fyr-requirements` for a `PRD.md` this run created, and for a migration name the files moved and removed. Then, per the global git workflow, stage the created or changed paths by name, read `git diff --cached --stat`, and commit in its own call (`init: scaffold agent tree and project docs`, `init: migrate the trail into records`, or `init: update scaffold to current templates`); an update that changed nothing makes no commit. Push only when `git remote` names a remote, and ask first when a deploy record says a push deploys.
