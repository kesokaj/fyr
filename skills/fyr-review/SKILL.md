---
name: fyr-review
description: "Multi-lens review (/fyr-review): --code, --security, --simplify, --docs, --prompts, --tests, --resume, --judge, --fix."
argument-hint: "[target] [--code|--security|--simplify|--docs|--prompts|--tests|--all|--resume|--judge <criterion>] [--fix] [--high]"
---

# Review

- **Never self-review or edit without `--fix`:** Parse one scope class, check for shadowing project review agents, dispatch independent read-only subagents in parallel (`review_parallel.py` for 2+ lenses), and merge findings FAIL-first.
- **Cross-family & guarded `pro`:** `PreToolUse` upgrades guarded lenses (`reviewer`, `security`, `verifier`) on fast/orchestrator turns; `review_parallel.py` routes cross-family via `fyr models --pick judge --json`; `--code` and `--docs` share one dispatch.
- **Fix loop (`--fix`):** Main agent applies edits (capped at 2 rounds); security medium+ findings require Round 2 resume; every acted report gets `## Outcome`.
- **References:** Load `references/lens-briefs.md` (verbatim `--docs`, `--prompts`, `--resume` briefs & `review_parallel.py` args) and `references/fix-and-record.md` (rounds, rebuttals, `## Outcome`, `--fix` loop).

```
/fyr-review                          # working tree, code lens
/fyr-review --security --docs        # working tree, two lenses, merged record
/fyr-review main..HEAD --all         # range, every lens (except --resume, --judge)
/fyr-review #42 --fix                # PR, code lens, apply findings
/fyr-review internal/api --simplify  # named paths, read whole, simplicity lens
/fyr-review AGENTS.md .agents/rules --prompts   # instruction files, prompt-audit lens
/fyr-review --project --all          # whole repo, every lens
/fyr-review --resume                 # trail-only cold-reader resume check (runs alone)
/fyr-review --judge "<criterion>"    # isolated cross-family judge over diff + verify output
/fyr-review --high                   # strongest model for reviewer and security
```

**Trail:** `~/.gemini/config/skills/fyr-init-project/references/trail.md`; dates via `date +%F`.

## Step 1: Parse

Flags start with `--`: lenses `--code` (default), `--security`, `--simplify`, `--docs`, `--prompts`, `--tests`, `--all` (all except `--resume` and `--judge`), `--resume` (runs alone — any scope token, extra lens, `--all`, `--judge`, or `--fix` is error `"--resume runs alone"`), `--judge "<criterion>"` (consumes the next argument as `<criterion>`; missing `<criterion>` errors); plus `--fix`, `--high`, `--project`.

Non-flag tokens define the scope: `#<digits>` = PR (requires `gh` on PATH), `..` or `git rev-parse --verify` = range/ref (single ref -> `<ref>..HEAD`), existing path = path (checked last so path wins over ref), else error. Allow exactly one scope class (paths, one range/ref, one PR, or `--project`; mixing errors `"one scope per run"`). No scope token -> working tree. Refuse `--fix` with `--project` (`"repo-wide edits need a plan — run the review, then fix per finding as a task"`).

## Step 2: Compute the scope text

Pass this text verbatim to every agent (which echoes it on `Range reviewed:`):
- **Working tree:** `Scope: working tree — changed files: <git diff HEAD --name-only>; untracked, not ignored: <untracked from git status --porcelain, minus .agents/scratch/>; read the diff with git diff HEAD -- <files> and read each untracked file whole.` (Empty lists -> `"nothing to review"`, stop.)
- **Range:** `Scope: commits <a>..<b> (git log <a>..<b>); the diff is git diff <a>...<b>.`
- **PR:** `Scope: PR #<n> — files: <gh pr diff <n> --name-only>; the diff is gh pr diff <n>.` (`--fix` requires the PR branch checked out.)
- **Paths:** `Scope: the named files as they are in the working tree, read whole — not a diff: <paths>.`
- **`--project`:** `Scope: the whole repository at HEAD (<git rev-parse --short HEAD>), no diff.`

`<scope>` slug for artifacts: `worktree`, `pr-<n>`, `<a>-<b>` (`/` -> `-`), `project`, or first path basename.

## Step 3: Dispatch — parallel lenses (`--code` and `--docs` share one)

Before dispatching, check `<project>/.agents/agents/` for any `reviewer`, `security`, `verifier`, or `explorer` file/directory or frontmatter `name:` shadowing a harness review agent; if found, stop immediately without writing anything and name the shadowing file.

Every dispatch gets: verbatim scope text, today's date, slug, lens brief, `"Write your artifact under .agents/scratch/agents/; report only means no source edits."`, and a 30-call budget (15 for fix re-dispatches) with `"write the artifact early, then update it"`. `PreToolUse` enforces omitted/`"inherit"` `Model` on guarded subagents (`reviewer`, `security`, `verifier`, `dreamer`) and upgrades fast/orchestrator turns to `Model="pro"`. For 2+ lenses, run `run_workflow` with `<HOME>/.gemini/config/workflows/review_parallel.py` (expanded `$HOME`, never `~`; see `references/lens-briefs.md` for args and `fyr gate --judge` / `fyr land --judge`).
- `--high`: Route `reviewer` cross-family via `fyr models --pick judge`; `security` inherits the session model.

| Lens | Agent | Slug | Brief |
|---|---|---|---|
| `--code` | `reviewer` | `<scope>-code` | `"Review the scope against review-standards, criteria 1–6 and the rules in §§ 7–8; correctness, security, and rule findings only."` |
| `--security` | `security` | `<scope>` | `"Defensive review of the scope and its blast radius."` (`--project`: `"every trust boundary in the repository, not a diff's blast radius."`) |
| `--simplify` | `reviewer` | `<scope>-simplify` | `"Keep your report template; findings come only from criteria 1, 5 and 6 (dead code, simplicity, decoupling) plus reuse and efficiency; write not in this lens on criteria 2, 3 and 4; propose the shorter version for each finding; style is not a finding."` |
| `--docs` | `reviewer` | `<scope>-docs` | Verbatim Docs brief in `references/lens-briefs.md` (with `--code`, bundle as ONE dispatch `<scope>-code-docs` under `## Docs lens`; `--project` activates item 9). |
| `--prompts` | `reviewer` | `<scope>-prompts` | Verbatim Prompts brief in `references/lens-briefs.md` (`--project`: `"every instruction file in the repository."`). |
| `--tests` | `verifier` | `<scope>` | `"Run live e2e/smoke suites (fyr verify covers unit, vet, and fyr check) and map every changed source file to its covering test; report only, never patch."` (+ draft `GUIDE-testing` when absent; `--project`: map whole repo). |
| `--project` | `explorer` | `<scope>` | `"Inventory and structure map of the repository: uniformity, orphaned and dead-code candidates."` |
| `--resume` | `explorer` | `project-resume` | Verbatim Resume brief in `references/lens-briefs.md`; store draft as `plan` record `Recon — project resume (<date>)`; main agent fixes at source (`--fix` adds nothing). |
| `--judge "<criterion>"` | `reviewer` (cross-family) | `<scope>-judge` | `"Evaluate strictly against Judge: <criterion> over the scope's diff and verification output only (never the builder's transcript or reasoning). Write .agents/scratch/agents/review-judge-<stamp>.md ending with VERDICT: PASS — <reason> or VERDICT: FAIL — <reason>."` |

Store each agent's `.agents/scratch/agents/<agent>-<stamp>.md` draft (`review` for reviewer/security, `testing` for verifier, `plan` for explorer). Partial turn-limit drafts are noted in the record.

## Step 4: The record

Store single-lens (`--code --docs` or `--judge` alone, no `--fix`) drafts directly as the `review` record; for multi-lens or `--fix`, write a merged `review` record (FAIL-first, verdict per lens, severity + confidence, collapsed duplicate `file:line` hits, artifact links, `Not checked`). Append `## Outcome` (`fixed`, `rebutted`, or `left` per finding) when findings are acted on. Full rules for rounds, security Round 2 requirement, and rebuttals: `references/fix-and-record.md`.

## Step 5: `--fix`

Without `--fix`, report only. With `--fix`, the main agent applies edits per lens, runs project checks bare, logs `## Round N`, and resumes lens agents in Round 2 (cap: 2 rounds) via `send_message` with the fix verification prompt in `references/fix-and-record.md`.

## Step 6: Report

Reply concisely with one verdict per lens and the record path, the recommended next step, and a worst-first numbered table of findings (with what `--fix` fixed or rebutted). A review fulfills DoD step 2; remaining DoD steps follow normally.
