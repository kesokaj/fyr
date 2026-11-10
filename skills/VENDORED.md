# Vendored third-party skills — provenance manifest

Skills in the first table are vendored verbatim from the open skills.sh ecosystem
(SKILL.md standard). Before a skill is vendored or kept, its text is read for steps that
send code or project data to an external service (a "register", "publish" or telemetry
step); such a step is scoped to public or OSS work only, or the skill is not kept (BUG-022). Update by re-fetching from the pinned source and bumping the entry
here; never edit vendored content in place (fork it into a differently-named skill
instead). The second table lists the forks: `gke-basics` (which predates the rename rule and
keeps its upstream name), `discover-skills` (forked from `find-skills`), `fyr-docs` (forked from `docs-writer`),
`fyr-triage` (forked from `triage`), and the `bash` wizard files (folded from `wizard` into `skills/bash/`). The third table lists
vendored catalogs: a subtree of third-party skills inside a harness skill, reached through that
skill and never installed as skills themselves. A path in a Source cell is written `<upstream>/…`: it is in the upstream repository, not in this checkout.
A Pin is written only after `git archive <pin> <path> | tar -x` into scratch and `diff -r` against the directory: the diff is the row's "Local changes", an empty diff is what "verbatim" means, and a pin chosen from a date is a guess (BUG-111; `TestVendoredRowsCarryAPin` refuses a row with no repository or pin, not a wrong one).

| Skill | Source | Pin | License | Vendored | Update via |
|-------|--------|-----|---------|----------|------------|
| grilling | github.com/mattpocock/skills (`<upstream>/skills/productivity/grilling`) | 6654f6b | MIT | 2026-08-31 | `npx skills@<version> add mattpocock/skills -s grilling` |
| codebase-design | github.com/mattpocock/skills (`<upstream>/skills/engineering/codebase-design`) | 6654f6b | MIT | 2026-08-31 | `npx skills@<version> add mattpocock/skills -s codebase-design` |
| prompt-audit reference (one file, not a skill: `review-standards/references/prompt-audit/prompt-audit.md`, with `LICENSE.txt` beside it) | github.com/anthropics/skills (`<upstream>/skills/claude-api/shared/prompt-audit.md`) | 8a1541c (sha256:c33200da…1878f2) | Apache-2.0 | 2026-09-29 | `curl -sL https://raw.githubusercontent.com/anthropics/skills/<sha>/skills/claude-api/shared/prompt-audit.md`, then bump the pin and digest here |

## Forks

`gke-basics` is NOT verbatim: it was rewritten here from an upstream base and keeps the
upstream name. Its provenance was "not recorded when vendored" until the project review
of 2026-09-30 (security finding 4) traced it by diffing against upstream's history
(`git archive <commit> <path> | diff -r`). It declares its licence and publisher in its
own `SKILL.md` frontmatter, which is the authority for attribution. A re-fetch from the
pin is a diff to read, never a copy to apply over the local text. Two skills that used to
sit here are gone: `terraform` was rewritten clean-room for GCP on 2026-10-01 (plan
audit-fix D8a, D22) and is the harness's own skill with no upstream text left; and
`gcloud-auth-verification` by google (v1, Apache-2.0), was merged into `gcloud-setup` on
2026-09-13 — its three rules paraphrased under "Before asking the user to log in" — and
removed.

`discover-skills` is a fork of `find-skills` (vercel-labs/skills, MIT) under a new name, made
on 2026-10-25 (plan `2026-10-25-harness-audit-fix`, TS30, D18). The verbatim copy's Step 6 installed
with the global and skip-prompt flags of an unpinned CLI, against this harness's install rule,
and a note here never reaches the model that loads the skill. The vendored copy was diffed
against `<upstream>/skills/find-skills/SKILL.md` at `435076e` before the fork (identical, the
one file the directory held) and removed. A re-fetch from the pin is a diff to read, never a
copy to apply.

| Skill | Publisher / author | Derived from | Pin | Local changes | License |
|-------|--------------------|--------------|-----|---------------|---------|
| gke-basics | Google Cloud | github.com/google/skills (`<upstream>/skills/cloud/gke-basics`, the one skill it was before upstream split it into `gke-*` sub-skills at 5d9d054 on 2026-06-23) | 6f0b877 (2026-04-23; the tree is unchanged upstream through 2026-05-10) | `SKILL.md` only: the description rewritten with this harness's cross-references (`cloud-run-deploy`, `docker`), a "When NOT to use" line added, the closing pointer to the Developer Knowledge MCP replaced by the GKE docs URL; a Harness overrides paragraph in SKILL.md (gcloud over MCP, one project question, the two dangling asset paths named, the lab-only `deletion_protection`); `references/` and `assets/` verbatim; the Quick Start takes `$PROJECT_ID` and `$REGION` with `--project` and `--region`, the `~/.claude` asset path dropped, and the references' `us-central1` values named as samples (plan `2026-10-25-harness-audit-fix`, TS33, TS34); two more overrides (`get-credentials` writes machine-wide state: `KUBECONFIG=.agents/scratch/kubeconfig`, which the Quick Start exports; the references' installs are pinned and their `pip` does not apply) (plan `2026-10-27-harness-audit-fix`, TS41, TS42) | Apache-2.0 |
| discover-skills | Vercel (vercel-labs) | github.com/vercel-labs/skills (`<upstream>/skills/find-skills`, upstream name `find-skills`) | 435076e | `SKILL.md` rewritten from upstream's: renamed; a sentence on `fyr auto`'s Kickoff added; the CLI pinned (`npx skills@1.7.2`) on every call; Step 6 installs into the harness checkout's `skills/` without the global, skip-prompt or all flags, then a `VENDORED.md` row and `fyr install`; a check of the skills installed here first; the text check and telemetry switch added; the bare "how do I do X" trigger dropped, so an ordinary how-to question does not reach the network (plan `2026-10-27-harness-audit-fix`, WS42); the structure and the quality checks kept | MIT |
| fyr-docs | Google (google-gemini) | github.com/google-gemini/gemini-cli (`<upstream>/.gemini/skills/docs-writer`, upstream name `docs-writer`) | 1930f25 | Renamed to `fyr-docs` with `LICENSE.txt` (Apache-2.0) beside `SKILL.md`; `SKILL.md` and `references/docs-auditing.md` rewritten for `fyr`'s airy, visual-first standard (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`): BLUF (Bottom Line Up Front) spelled out on first mention, short 2–4 sentence paragraphs, Mermaid diagrams (`mermaid-syntax`) required for 3+ step/component flows and architectures, `<details>` folding for dense schemas and tables, audit scratch outputs written under `.agents/scratch/` (never `/tmp`), and Gemini CLI repo-specific paths (`packages/`, `docs/sidebar.json`, `quota-limit-style-guide.md`, `npm run format`) replaced with `fyr search` and `fyr check` | Apache-2.0 |
| fyr-triage | Matt Pocock | github.com/mattpocock/skills (`<upstream>/skills/engineering/triage`, upstream name `triage`) | 4ae2317 | Renamed to `fyr-triage` with `LICENSE.txt` (MIT) beside `SKILL.md`; `disable-model-invocation` dropped so turn-vetting (`MatchSkills`) can prefetch it; extended beyond GitHub Issues/PRs (`gh issue`, `gh pr`) to local `fyr` queues (`TODO.md` Backlog, open `BUG-*` and global `PROPOSAL-*` records); `.out-of-scope/*.md` knowledge base (`OUT-OF-SCOPE.md`) dropped in favour of `PRD.md` and `brain.db` `adr`/`plan` records (`PRD.md` R7); `domain-modeling` and `/setup-matt-pocock-skills` replaced with `codebase-design`, `grilling`, and `gh label list`; untrusted-input boundary and `/fyr-work` Kickoff mapping added in `SKILL.md` and `references/agent-brief.md` | MIT |
| bash wizard (two files in `bash`, not a standalone skill: `bash/references/wizard.md` and `bash/assets/wizard-template.sh`) | Matt Pocock | github.com/mattpocock/skills (`<upstream>/skills/engineering/wizard`, upstream name `wizard`) | 4ae2317 | Folded into `skills/bash/` as `references/wizard.md` and `assets/wizard-template.sh` (mode `0644` for `go:embed` parity); ephemeral wizard output defaulted to `.agents/scratch/wizard-<slug>.sh` (never `/tmp`); `write_env` and `_existing` rewritten without regex key expansion or system temp files; `set_gcp_secret` added using `gcloud secrets versions add --data-file=-`; sample stage updated to OAuth + GCP Secret Manager + GitHub secret + `.env` | MIT |

Apache-2.0 and MIT permit redistribution with attribution and licence notices: `fyr-docs` (`LICENSE.txt`, Apache-2.0) and `fyr-triage` (`LICENSE.txt`, MIT) carry upstream's licence text beside `SKILL.md`, and this repository's root `LICENSE` is MIT (`ADR-515`). A re-fetch from any fork's pin is a diff to read, never a copy to apply over local changes.

## Vendored catalogs

| Catalog | Source | Pin | License | Vendored | Update via |
|---------|--------|-----|---------|----------|------------|
| google-catalog/catalog | github.com/google/agents-cli (`<upstream>/skills/`, `<upstream>/extensions/langchain/template/.agents/skills`) and github.com/google/skills (`<upstream>/skills/cloud`, `<upstream>/skills/developers`); entry counts per source, exclusions and per-entry security notes in `google-catalog/catalog/MANIFEST.md` | 2c39459 and d5d9052 | Apache-2.0 | 2026-10-01 | `bash skills/google-catalog/scripts/refresh-catalog.sh <agents-cli pin> <google-skills pin>`, then `git add skills/google-catalog && go test ./...` |

A catalog is vendored by its refresh script, never by hand and never by `npx skills add`: the
script copies the entries byte for byte at the two pins (file modes aside: no executable bit; a pin is a full 40-digit commit id, since git would check a tag or branch named like a shorter one out first; the short ids in the row above are for reading),
refuses what the harness cannot carry (a symlink, a Go file, a dotfile, a name in two source sets, an
entry without frontmatter, a directory name that is not a plain skill name) until the operator passes
`--exclude <entry>`, keeps every entry named under the manifest's `## Excluded` out and that section
and `## Notes` byte for byte, replaces only a destination that is a catalog (never `/`, the home
directory, the checkout or an ancestor of either, in any spelling; a non-empty destination must hold
`CHECKSUMS`, and only the directories that file names are removed), clones into a work directory
that is its own (never `/`, the home directory or the checkout, never nested with the destination;
one that already holds files must carry the script's `.refresh-catalog-work` marker or an
`origins.txt`), takes a source override only as an `https://` URL (no user information, no query string) or an
absolute path, never shows or records such a value (the manifest names the upstream repositories; the
pins identify the content; git's own output, tracing and credential prompts (terminal and askpass) are off for the script's calls; while git runs the value is still one of its arguments in the process list, so a token belongs in a credential helper) and says, whether the run succeeds or fails after the first clone, that git
keeps the value in the work clones, and writes
`INDEX.md` and
`CHECKSUMS` through the Go tests, which then pin the tree both ways (`TestCatalogMatchesChecksums`,
`TestCatalogIndexMatchesEntries`, `TestCatalogFilesAreCarryable`). Installed as global skills
these skills push the harness's own out of the CLI's skill list
(`PLAN-2026-10-01-research-google-skills-catalog`, `fyr record get`), so they stay behind the
`google-catalog` router. A refresh's diff gets a security review before it is committed.

Notes:

- `grill-me` (a launcher for `grilling`) and `skill-repair` (repairs a `skills`-CLI manifest the
  harness does not use) were pruned 2026-09-03 after the sandbox matrix: `/grilling` does the same
  job, and `fyr adopt` replaced the manifest repair.
- `grilling` says "dispatch a sub-agent" to find a fact and names none; that agent is
  `explorer` (read-only recon), and the `--prompts` lens does not flag the vendored line for it.
- `find-skills` was vendored verbatim at `435076e` from 2026-09-28 until 2026-10-25, when it became the fork `discover-skills` (second table). Its routing of installs into this repository lives in the global context's § Global vs project, and the fork's Step 6 now says the same.
- The UIZZE UI skill (uizze.com, v1.2.13, formerly a row above) and the Python skill were removed on
  2026-10-01 (plan audit-fix D28, operator's strike): the first duplicated `frontend` and needed a paid
  MCP, and its LICENSE file carried Apache-2.0 text under an MIT label; the stack has no Python.
- `codebase-design` (`references/DESIGN-IT-TWICE.md`) names a `CONTEXT.md`: that is upstream's
  sibling file, not a file this harness has or writes; the line is vendored verbatim, read it as
  "the project's own domain vocabulary (its `AGENTS.md`)".
- `mermaid-syntax` has been in this repository since its first commit with no recorded upstream;
  its frontmatter `metadata.version: 1.5.0` suggests one. It is edited here as the harness's own
  skill (the 2026-10-01 edit moved its examples to frontmatter `config:` and `packet`), so it is
  not vendored and carries no row; if an upstream is ever identified, fork it under another name
  rather than re-fetching over these edits.
- To add a skill: run `npx skills@<version> add <owner/repo> -s <skill>` from the repo root (pin the CLI's version and read its prompt; never `-y`) (it lands
  in `skills/` through the `.agents/skills` symlink), or copy any directory holding a `SKILL.md`
  into `skills/`, then `fyr install`. Add a row above. `fyr install` deploys exactly
  the repo's skills and removes only what it installed itself, so a skill installed by hand
  into `~/.gemini/config/skills` or `~/.agents/skills` survives but is not versioned —
  `fyr adopt <skill>` moves it in here.
