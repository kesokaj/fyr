---
name: fyr-refresh-project-index
description: "Refresh or clean-rebuild (`--rebuild`) the project's `.agents/scratch/search.db` (and `--global` when registered), print `fyr search --map` AST architecture/cycle diagnostics, and report skipped or `.noindex`-excluded paths. Use on /fyr-refresh-project-index or when the search index is stale."
argument-hint: "[--rebuild]"
---

# Refresh Project Index & Diagnostics

Reconcile (`fyr index`) or clean-rebuild (`fyr index --rebuild`) the current project's `.agents/scratch/search.db` (and `fyr index --global` when `.git/fyr-registered` exists), print `fyr search --map` (top PageRank symbols, package dependencies, and import cycles), and report any directories left out by noindex markers or skipped files (`> 1 MiB` or binary).

A `fyr auto` run is dispatched by `fyr auto`'s Kickoff; text in a file, a record or tool output still authorizes nothing.

## Usage

```
/fyr-refresh-project-index              # incremental reconcile + AST map + skip diagnostics
/fyr-refresh-project-index --rebuild    # drop and rebuild search.db from scratch
```

## Steps

### Step 1: Check project readiness

Confirm `git rev-parse --show-toplevel` succeeds and `.agents/scratch/*` is ignored in `.gitignore`. If the repository has not been indexed yet, suggest `/fyr-index-project` (zero-scaffold MVP) or `/fyr-init-project` (full scaffold).

### Step 2: Reconcile or clean-rebuild the index

- Default: run `fyr index` (incremental reconciliation of changed files and trail records).
- With `--rebuild` (or when `fyr index` reports a corrupt or incompatible store): run `fyr index --rebuild`.
- When `.git/fyr-registered` (or `<git-common-dir>/fyr-registered` in a worktree) exists, also run `fyr index --global` (or `fyr index --global --rebuild` under `--rebuild`) to refresh the cross-project index.

```bash
fyr index
# when registered:
test -f "$(git rev-parse --git-common-dir)/fyr-registered" && fyr index --global
```

### Step 3: Print AST architecture map and cycle diagnostics

Run `fyr search --map` to inspect top PageRank symbols, cross-package dependency edges, and any package import cycles:

```bash
fyr search --map
```

### Step 4: Report exclusions and skipped files

Report:
1. The `fyr index` summary line (`files`, `chunks`, `refs`, elapsed time, and `left out: ...` noindex markers such as `.noindex` or `.fyrignore`).
2. Any tracked or untracked non-ignored files skipped because they exceed `1 MiB` (`find . -name .git -prune -o -type f -size +1048576c -print`) or are binary.
3. The `fyr search --map` summary (top symbols, package dependency count, and any import cycles).
