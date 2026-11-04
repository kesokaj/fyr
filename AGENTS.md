# AGENTS.md — fyr

Project rules for this repo. They override the global context, which this repo is the source of (`context/`).

## What this repo is

The agent harness: one global context (agents.md standard), skills (Agent Skills `SKILL.md` standard), subagents, and `fyr`, the Go binary that installs them into the Antigravity CLI. No hooks, no gates, no CI.
`fyr` is also the second brain the Antigravity CLI consults at the moments the context names (search, memory, metrics, dream): the purpose and its two constraints are `PRD.md` R0.

## How the harness works

The map in `README.md` § How it works defines how the harness reaches a machine and how experience comes back. It is the contract, not an illustration, and these rules are strict:

1. **Three ways in, no way back, nothing else.** A place gets the harness from a checkout kept current with `fyr update`; from a release binary with the harness built into it, installed with `fyr install` and moved forward by `fyr update`; or, as a zip place, from a repository zip the operator downloaded by hand and passes to `fyr update --from-zip <file>` (what each kind of place fetches: rule 6's one home). Experience does not come back: it stays on the machine that logged it, and a harness-target proposal reaches this checkout only through `/retro --harness`, run by the operator on that machine, or an eval-gated `fyr auto` commit to this checkout's local branch (R9, ADR-090), which the operator pushes (ADR-062). No service, no push to a machine, no copy vendored into a project, no timer as the mechanism (R8, R9, ADR-030, ADR-033, ADR-038, ADR-062). The dream timer (R3's exception, ADR-047a, ADR-073; hourly, while an `agy` session of the operator's runs) and `fyr auto`'s timer (R3's second exception, ADR-090; hourly, when the operator turned `auto` on) are neither: they move no harness onto a machine, send content only by design — to the model of the CLI the machine already uses: the dreamer's redacted prompt, and the runs of `fyr eval` and the workers of `fyr work`, started by the operator or by `fyr auto` when turned on, plus a learning worker's redacted inputs, and to skills.sh and the npm registry, a missing-skill worker's search (ADR-065, ADR-076, ADR-090) — and their harness-target proposals wait as global `proposal` records in `~/.config/fyr/brain.db` for the operator's own hands or, at level `harness`, the eval gate (R9).
2. **A tag is a release.** `/preflight vX.Y.Z` makes an annotated tag and publishes that tag's binaries and a zip of its tree, with their checksums — no key signs anything (ADR-053); what each kind of place does with it is rule 6's one home (D18 of the release-binary plan, ADR-033 superseding ADR-031 D3).
3. **Changing a path is a design question.** Adding, removing or rerouting any line on the map goes to the operator first, gets an ADR, and lands in ONE commit with the code, the docs and the map. A change to install, update or release behaviour whose diff does not touch the map is incomplete; `/review --docs` checks for it.
4. **Dashed means a gap, and it stays honest.** A dashed line is done by hand or limited to one machine; it turns solid in the commit that closes the gap, and nothing is drawn solid before it works for every place. The one exception is the olive lines to the model provider, the dreamer's, `fyr eval`'s, `fyr work`'s and `fyr auto`'s: dashed because they are not harness paths at all (ADR-049, ADR-065, ADR-076, ADR-090), and they never turn solid.
5. **The map is pinned by a test.** `TestHarnessMap` fails when the map is missing, when it names a `fyr` verb the binary does not have, or when install, update, dream or auto is missing from it, and when it still names `report` or the feedback repository. The interactive page of the map (a private artifact page, not in this repository) is only a view: when the map changes, the page is updated from it, and it is never the source.
6. **The delivery claim has one home.** What a tag delivers, to which kind of place, and how a place moves forward is stated once, in `SPEC-fyr-delivery` § The model; README's prose, this file, the deploy guides and the ADRs point at it in one sentence and never restate it; the README map draws the paths and its labels name them, which is not a restatement, and a guide or README row describing what a COMMAND does at a place (`update` fetches, verifies, replaces, reinstalls) is that command's documentation, not the claim — rule 6 is about which place gets what from a tag, never about how a command works. A change to the claim is one file, plus the map's lines when a path moves (rule 3; why: `RETRO-2026-09-18-retro-2`).

## Setup commands

```bash
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...   # before any completion claim; why the source dirs and the timeout: GUIDE-testing § 1 # auto: test
go run ./cmd/fyr install --home .agents/scratch/home   # exercise the installer against a scratch home (gitignored)
go run ./cmd/fyr list   # sanity-check skills and agents parse
go mod download   # once per version, while online: the one module (R4), for an offline build later
"$(go env GOPATH)/bin/govulncheck" ./...   # by its full path: GOPATH/bin is not on every shell's PATH; watch for NEW findings (the known ones: DEPLOY-local § Operator steps)
```

A change to `context/*.md` or `rules/*.md` must also pass the two size pins in `internal/harness/size_test.go` (part of `go test ./...`; the limits: `TDD.md` § T1); never raise a limit. `go.mod` carries NO `toolchain` directive (ADR-029: a pin makes the module unbuildable offline); an advisory fixed in a newer Go is fixed by upgrading the machine's Go (`DEPLOY-local` § Operator steps).

## Layout

```
PRD.md SDD.md TDD.md   what and why (R#), how the parts fit (M#), how each is built (T#); TODO.md, README.md
context/               the global context, single source (AGENTS.md, then dev-setup.md appended at install)
agents/<name>/agent.md subagents, installed as written (ADR-062)
skills/<name>/         skills; .agents/skills links here, so `npx skills add` lands in skills/
skills/google-catalog/catalog/  Google skills at a pin, reached through the router, never installed
commands/, rules/      deployed verbatim; rules share the context's block; commands/ empty by design
cmd/fyr, internal/     the binary; harness (plan.go, the lint in inventory.go), search, memory, redact,
                       trail, check, dream, metrics, brain, eval, sandbox, work, serve, auto
embed.go, *_test.go    package `fyr`: the go:embed of the five content directories and `docker/`, the tree's tests
docker/                `fyr work`'s docker runner image: work.Dockerfile and its default-deny ignore file
.agents/               this repo's `.agents/skills` symlink and `.agents/scratch/`; `.agents/eval/` holds the eval suite (`GUIDE-eval.md`) and its fixture (`fixture.sh`); trail records live in `~/.config/fyr/brain.db` (`fyr record export` writes `.agents/trail.jsonl` on demand)
.agents/scratch/       gitignored; every generated or temporary file, never /tmp
```

Each package, what it holds and imports: `SDD.md` § Components, then its `TDD.md` section. Nothing generated is committed; `fyr install` assembles the deployed files.

## Rules for working here

- The installer never runs against the operator's real `$HOME` in development (`--home .agents/scratch/<name>` for every test); deploying there is the operator's job, except on dave, where a session's checkpoint ends with `fyr install` into the real home from a clean, pushed tree, and a lead defers it to its closing handoff (`DEPLOY-local` § Operator steps; the operator, 2026-10-29).
- Every `fyr` verb that writes state (`record`, `memory add`, `index`, `search`, `metrics ingest`, `dream`, `eval`, `work`, `serve`, `auto`) carries `--home .agents/scratch/<name>` in development and in every Kickoff and handoff command line: without it the verb writes the real `~/.config/fyr` (BUG-006, 091, 094, 098, 107, 115, 144, 149, 162, 179, 182; the 2026-10-27 audit, TR16); tests of `fyr auto` never turn auto on or install its timer in the real home. Three exceptions take a home outside every checkout: `fyr eval` (`GUIDE-testing` § 4), a development `fyr work` run, whose home is `~/.config/fyr/dev/fyr-work` (`GUIDE-testing` § 4; the verb writes no store, but its sandboxes need a home that reaches no checkout, D11, D9), and a visible `fyr dream` run (§ 3e), its path approved in the plan.
- When `fyr install` rebuilds or copies the binary: `TDD.md` § T6. After a change under `cmd/` or `internal/`, an install is done only when `fyr status` from the target home shows a `binary` line without a warning.
- Adding a tool to an agent's `tools:` list means adding it to `knownTools` (`internal/harness/inventory.go`): an unknown tool fails the install on purpose, because Antigravity hangs such a subagent.
- The installer lints every skill, agent, command and rule before deploying (the refusals: `TDD.md` § T6, each pinned by a `TestLint*` test or `TestAgentDescriptionRules`). A new frontmatter key joins an allow list by hand under a D-row, also for a vendored skill, which is never edited; never a permission key nor a restriction the CLI drops, such as `disallowedTools` (ADR-056, ADR-056a): the operator's permission mode decides what runs unprompted.
- Adding a skill, agent, command, or rule is a file drop — never a code change, except a frontmatter key no allow list holds yet. A new kind of customization that needs code is a design question. A description that fails `TestSkillListingBudget` (`TDD.md` § T2) is shortened, never the number raised.
- Third-party skills are vendored verbatim and listed in `skills/VENDORED.md` (its forks table names the exceptions); never edit them in place — fork into a differently named skill. A vendored catalog changes only by its refresh script, its diff security-reviewed before the commit, and its entries never become global skills (`skills/VENDORED.md` § Vendored catalogs). A pin is written only from a `git archive` and `diff -r` check, never chosen from a date (BUG-111; the procedure heads `skills/VENDORED.md`).
- Stdlib only in the Go code, with one exception: `modernc.org/sqlite`, imported only by `internal/search` (R4, ADR-045, with the dream's reads, ADR-047a), `internal/metrics` (ADR-055) and `internal/brain` (ADR-063); never by the installer. Any other dependency is a requirements change.
- `internal/redact/redact.go`'s needles grow by KIND: a vendor's length and alphabet as one regexp, or a prefix only when it is distinctive as written (`sk_test_` sits inside `task_test_`); a third missed instance on one list gets a rule per kind, not a fourth batch (BUG-110). Every needle gets a benign near-miss in `TestRedactShapes` (how a needle is written: `TDD.md` § T12); over-redaction in the safe direction is pinned there, never argued about.
- Commit and push after each task, per the global git workflow. This repo is on `main`.

## Testing

`go test` only; the three root-package pins are `TDD.md` § Testing strategy. The procedure is `GUIDE-testing`: § 1 for every change, § 3a–3l by the package each names, § 2 and § 4 for `context/`, `agents/` and `skills/` (a behavioural run per changed agent or rule, reported as n of N), § 5 after any real-home install. A behavioural run is `fyr eval <row>...` from this checkout (`GUIDE-testing` § 4 § Behavioural runs; rows `.agents/eval/GUIDE-eval.md`, verb `SPEC-fyr-eval`). A `GUIDE-eval.md` edit is a test change: `TestSuiteHarness` pins every row by id, agent and grant, and `TestSuiteChecks` runs every check against a good and a bad outcome (`internal/eval/suite_outcomes_test.go`), so a changed row changes its `want` entry and its outcomes in the same commit, after `go test ./internal/eval` (BUG-183, BUG-191, ADR-500). A change to `context/`, `agents/` or `skills/` gets `/review --docs` over its range; the living docs include `context/`, `agents/` and every non-vendored skill, searched at `skills/` because `.agents/skills` is a symlink.

## Release

Releases are annotated `vX.Y.Z` tags on `main`, cut only by `/preflight vX.Y.Z` (its Steps 1–5 are the gate) from the operator's own turn, from this checkout; no key signs anything (ADR-053). The tag is checked before the push, never moved once pushed (`DEPLOY-remote` § Releases); the tag is the only version tracking (no version file; preflight Step 6.2); preflight Step 6 holds the bridge-release wait (BUG-161). This project's blocking condition: a gate whose "What was not checked" names an Antigravity run of a skill or agent the range changed is NO-SHIP until that run exists — the real-home smoke of `TEST-2026-09-03-verify-real-home` plus one run of each changed skill and agent on Antigravity, through `fyr eval` where `GUIDE-eval` has a row (its `testing` record is the evidence), before the tag (the operator's rule of 2026-09-29, after v1.12.0). Catalog entries are data: for them the rule is met by the router's rows `catalog-no-leak`, `catalog-route`, `catalog-precedence` and, when the range refreshes the catalog, a security review of its diff (D11, D12 of `PLAN-2026-10-01-google-skills-catalog`). What a tag delivers is `SPEC-fyr-delivery` § The model (rule 6).
