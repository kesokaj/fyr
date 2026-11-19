---
name: fyr-index-project
description: "Zero-scaffold MVP indexing (/fyr-index-project): set up .agents/scratch/search.db (FTS5, AST --slice/--skeleton/--path/--affected/--map, hooks) without root docs (AGENTS.md, PRD.md, SDD.md, TDD.md, TODO.md). Do NOT use for full scaffolding (/fyr-init-project) or refreshing an existing index (/fyr-refresh-project-index)."
---

# Index Project (MVP Zero-Scaffold Indexing)

Enable `.agents/scratch/search.db` (FTS5 search, AST symbol and reference graph `--slice`, `--skeleton`, `--path`, `--affected`, `--map`, and `PreInvocation`/`PreToolUse` hooks) quickly in ad-hoc, research, or adopted projects with **zero root-doc scaffolding** (`AGENTS.md`, `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`, and `GUIDE-*`/`DEPLOY-*` records are **not** created).

## Usage

```
/fyr-index-project
```

## Steps

### Step 1: Confirm the git root (or initialize git)

Check `git rev-parse --show-toplevel`. If the directory is not inside a git checkout:
1. Confirm with the user that the current directory is the intended project root.
2. Run `git init`.
3. Scan for nested `*/.git` directories (`find . -mindepth 2 -maxdepth 3 -name .git`) and add any nested checkouts to `.gitignore` before indexing so nested clones are neither skipped as `160000` gitlinks nor duplicated.

### Step 2: Symlink and scratch check

1. Verify `.gitignore`, `.agents`, and `.agents/scratch` are not symlinks (`test ! -L .gitignore && test ! -L .agents && test ! -L .agents/scratch`). If any is a symlink, stop and report it — never write through a symlink.
2. Create `.agents/scratch` (`mkdir -p .agents/scratch`). If `.agents/scratch/` already contains user files (other than `search.db*` and `agents/`), ask the user whether to `git add -f` or move them before `.agents/scratch/*` in `.gitignore` hides untracked files from `gitView`.

### Step 3: Ensure `.agents/scratch/*` is ignored

Ensure `.agents/scratch/*` is present in `.gitignore` (appending `.agents/scratch/*` without removing or reordering existing lines) so `guardProject` passes and `.agents/scratch/search.db` stays untracked.

### Step 4: Build the FTS5 and AST graph index

Run `fyr index` and `fyr search --map`, then report the indexed `files`, `chunks`, `refs`, and AST graph summary:

```bash
fyr index
fyr search --map
```

### Step 5: Optional cross-project `--global` search registration

If the user wants cross-project `--global` search (`fyr index --register`) and `fyr record list --limit 1` is empty, write `.agents/scratch/memory-project-overview.md` (`Indexed with /fyr-index-project (MVP zero-scaffold mode).`) to satisfy `HasScopeRecords` in `brain.db`, then run:

```bash
fyr memory add --type semantic --name project-overview --description "<one-line summary of what this repository contains>" --body-file .agents/scratch/memory-project-overview.md
fyr index --register
```

### Step 6: Handoff note

End by stating:

*"MVP indexing and AST graphing are active. If you continue building in this project and want the full harness scaffold (`AGENTS.md`, `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`, and trail records), run `/fyr-init-project`."*
