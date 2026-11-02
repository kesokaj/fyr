# GUIDE-eval — the behavioural suite

The rows `fyr eval <row>...` runs against the Antigravity CLI, each in its own
sandbox (the verb: `fyr eval --help`; the trial's steps: `internal/eval`). This
file is tracked: `fyr eval` and the auto gate read it from the checkout, and
`TestSuiteHarness` pins every row by id, agent, grants, kind and version, so a
changed row changes its `want` entry in the same commit (ADR-500, amending
ADR-091). A row's version goes up when its prompt, setup or check changes after
a trial, so old trials stop counting against the new row.

## The fixture

Every row's setup is `bash .agents/eval/fixture.sh <scenario>`, run in the
trial's clone of this checkout. It replaces the clone's history with a small
demo project (a Go greeting service: `cmd/server`, `internal/greet` and its
table test), removes every tag and the remote so nothing the agent runs can
reach this checkout, and seeds the records a scenario needs into the trial
home's store through `$FYR`. The scenarios:

| scenario | what it adds to the demo |
|---|---|
| `bare` | nothing: no scaffold, no records |
| `project` | the `/init-project` scaffold (AGENTS.md, PRD, SDD, TDD, TODO, `.gitignore`) and the `GUIDE-testing` and `DEPLOY-local` records |
| `review` | `project`, plus an uncommitted unused function `shout` in `internal/greet/greet.go` |
| `reviewer` | `project`, plus a committed regression: `Hello` no longer trims its name |
| `work` | `project`, plus a finished `fyr work` wave: plan `PLAN-2026-11-02-greet-tab`, branch `work/greet-tab-p1`, its worker's handoff, a bug draft and the verb's printed result in `.agents/scratch/work/result.txt` |
| `retro`, `distill` | `project`, plus three global feedback records about logging through `log/slog` |
| `dream` | `project`, plus three memories, two of them saying the demo ships on Friday |
| `preflight` | `project`, plus tag `v0.1.0` and one commit after it |

## Writing a check

A check runs in the clone after the session, with `HOME` and `SB` set to the
trial home; `"$SB/bin/fyr"` is the fyr wrapper pinned to that home, `$OUT` and
`$OUT2` are the two sessions' outputs. A check counts only what the run added:
the fixture seeds no record of the kind a check looks for. Each check is shown
to pass on a good outcome and to fail on a bad one by `TestSuiteChecks`, which
runs the row's setup, applies the outcome, and runs the check; a new row adds
its outcomes there.

## Known limits

- The rows items 10 and 22 of `PLAN-2026-11-02-1806-fyr-speed` name are added
  by those items, with the behaviour they test.
- A row tests one path of its skill or agent; the baselines are n of 3 at the
  agy default model.

## The suite

| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |
|---|---|---|---|---|---|---|---|---|---|---|
| `init-project` | `bash .agents/eval/fixture.sh bare` | Run /init-project in this repository, a small Go greeting service with no harness scaffold. Do not ask me anything: the overview is "a greeting service over HTTP", the deploy target is none (local only), the commands derived from go.mod are confirmed, and it runs locally with go run ./cmd/server on port 8080. Commit the scaffold; there is no remote, so never push. |  | `for f in AGENTS.md PRD.md SDD.md TDD.md TODO.md; do test -s "$f" \|\| exit 1; done; git check-ignore -q .agents/scratch/probe && "$SB/bin/fyr" record get DEPLOY-local >/dev/null && test -z "$(git status --porcelain)"` |  | wide: the skill writes a tree of files and runs fyr record in loops | 1 | capability |  |  |
| `feature` | `bash .agents/eval/fixture.sh project` | /feature Add GET /version to cmd/server, answering the module's version string. The scope is confirmed as written; do not ask me anything. Write and store the plan record and close its cold-reader check, then stop before any code. |  | `id=$("$SB/bin/fyr" record list --kind plan \| cut -d' ' -f1 \| head -1); test -n "$id" && p=$("$SB/bin/fyr" record get "$id") && printf '%s' "$p" \| grep -qi version && printf '%s' "$p" \| grep -q -- '- \[ \]' && printf '%s' "$p" \| grep -qiE '^- \[(x\|-)\] cold-reader' && git diff --quiet HEAD -- cmd internal && test -z "$(git ls-files --others --exclude-standard cmd internal)"` |  | named | 1 | capability |  |  |
| `review` | `bash .agents/eval/fixture.sh review` | /review the working tree. Do not ask me anything; store the review record and fix nothing. |  | `b=$(for id in $("$SB/bin/fyr" record list --kind review \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id"; done); printf '%s' "$b" \| grep -qiE 'shout\|unused\|dead code'` |  | wide: the review skill pipes git output through its own scripts | 1 | capability |  |  |
| `reviewer` | `bash .agents/eval/fixture.sh reviewer` | Dispatch the reviewer agent over the last commit (git diff HEAD~1 HEAD), giving it the diff only, then store its draft as a review record. Do not ask me anything and fix nothing. |  | `b=$(for id in $("$SB/bin/fyr" record list --kind review \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id"; done); printf '%s' "$b" \| grep -qiE 'trim\|whitespace\|blank'` | reviewer | named | 1 | capability |  |  |
| `documentation` | `bash .agents/eval/fixture.sh project` | Dispatch the documentation agent: the DEPLOY-local record must also say that PORT=9090 go run ./cmd/server serves the demo on port 9090. Store its draft back into DEPLOY-local. Do not ask me anything. |  | `"$SB/bin/fyr" record get DEPLOY-local \| grep -q 'PORT=9090'` | documentation | named | 1 | capability |  |  |
| `handoff` | `bash .agents/eval/fixture.sh project` | We just decided that the next task is structured request logging in cmd/server; nothing is done yet. Run /handoff now. Do not ask me anything. |  | `b=$(for id in $("$SB/bin/fyr" record list --kind handoff \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id"; done); printf '%s' "$b" \| grep -q '^## Kickoff' && printf '%s' "$b" \| grep -qi logging` |  | wide: the skill runs its check-trailers script | 1 | capability |  |  |
| `work` | `bash .agents/eval/fixture.sh work` | /work PLAN-2026-11-02-greet-tab: resume at step 6. fyr work already ran this wave, and what it printed is in .agents/scratch/work/result.txt. Do steps 6 to 9 for that worker: merge it, tick the plan, store its bug draft. Then commit; there is no remote, so never push, and never run fyr work. Do not ask me anything. |  | `git log --format=%s \| grep -qxF 'PLAN-2026-11-02-greet-tab p1 (work/greet-tab-p1)' && ! git rev-parse -q --verify refs/heads/work/greet-tab-p1 >/dev/null && grep -q TestHelloTab internal/greet/greet_test.go && "$SB/bin/fyr" record get PLAN-2026-11-02-greet-tab \| grep -q -- '- \[x\]' && test "$("$SB/bin/fyr" record list --kind bug \| grep -cF 'Bug: Hello accepted a tab-only name')" = 1 && test -z "$(git status --porcelain)"` |  | wide: the skill reads the handoff through awk | 1 | capability |  |  |
| `retro` | `bash .agents/eval/fixture.sh retro` | /retro. You were corrected three times in this project about logging; the global feedback records hold the corrections. Do not ask me anything: store the retro record and apply nothing. |  | `b=$(for id in $("$SB/bin/fyr" record list --kind retro \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id"; done); printf '%s' "$b" \| grep -qi slog` |  | wide: the skill reads the trail and the git log through pipes | 1 | capability |  |  |
| `distill` | `bash .agents/eval/fixture.sh distill` | /distill. The same procedure, routing server logs through log/slog, came up three times; the global feedback records hold it. Do not ask me anything: draft the skill under .agents/scratch, store the retro record, install nothing. |  | `"$SB/bin/fyr" record list --kind retro \| grep -q '^RETRO-'` |  | wide: the skill reads the trail and the git log through pipes | 1 | capability |  |  |
| `dream` | `bash .agents/eval/fixture.sh dream` | /dream. I will not answer during this run. |  | `b=$(for id in $("$SB/bin/fyr" record list --kind retro \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id"; done); printf '%s' "$b" \| grep -qi merge && test "$("$SB/bin/fyr" memory show \| grep -c .)" = 3` |  | wide: the skill greps and searches each memory's key words in loops | 1 | capability |  |  |
| `spec` | `bash .agents/eval/fixture.sh project` | /spec demo-hello: the GET /hello surface of cmd/server as it is built today. Do not ask me anything; store the spec record. |  | `b=$(for id in $("$SB/bin/fyr" record list --kind spec \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id"; done); printf '%s' "$b" \| grep -q '/hello' && printf '%s' "$b" \| grep -q 'hello, world'` |  | named | 1 | capability |  |  |
| `feedback-log` | `bash .agents/eval/fixture.sh project` | Actually, we never use fmt.Println for logging in this project: server logs always go through log/slog. Do not ask me anything. |  | `b=$(for id in $("$SB/bin/fyr" record list --global --kind feedback \| cut -d' ' -f1); do "$SB/bin/fyr" record get "$id" --global; done); printf '%s' "$b" \| grep -qi slog && "$SB/bin/fyr" memory show \| grep -qi slog` |  | named | 1 | capability |  |  |
| `preflight` | `bash .agents/eval/fixture.sh preflight` | /preflight v0.1.1. This repository has no remote and no deploy target: tag locally and never push. Do not ask me anything. |  | `"$SB/bin/fyr" record list --kind review \| grep -qi preflight && if git rev-parse -q --verify refs/tags/v0.1.1 >/dev/null; then test "$(git cat-file -t v0.1.1)" = tag; fi` |  | wide: the gate runs the project's commands and its trailer script | 1 | capability |  |  |
| `project-memory` | `bash .agents/eval/fixture.sh project` | Remember this: the demo ships on Fridays, never on another weekday. | On which weekday does the demo ship? Answer with the weekday only. | `"$SB/bin/fyr" memory show \| grep -qi friday && grep -qi friday "$OUT2"` |  | named | 1 | capability |  |  |
