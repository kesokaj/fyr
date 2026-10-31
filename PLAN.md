# PLAN — clean the project and release v5.5.0   (2026-10-31)

Status: open. P1 to P10b done (2026-10-31); P11 is running. This is the only plan.

## The operator's words

> Make one big plan for all remaining fixes, strike everything we don't fix. I want the project to be 100% clean after this run and the end is a new release. We also need to clean up old files in the docs folder because we are using fyr instead.

> Ok. Lets go with your recommendations. Now generate a fixed plan we store in the root. This is the only plan. During this plan nothing can be in the backlog or roadmap. We only fix these issues and issues that come up while we are fixing. Include this in the plan, I cant update the version on fyr install on my linux borken […]. When all done this is a new release v5.5.0.

## Rules of this plan

1. **The only plan.** The four plans open before it were closed on 2026-10-31. Their remaining work is either an item below or struck (D1):
   - `PLAN-2026-10-31-0300-plan-eval-metrics-the-standard-set-collected-fro`
   - `PLAN-2026-10-29-root-docs-and-fyr-serve`
   - `PLAN-2026-10-29-workers`
   - `PLAN-2026-10-30-ui-skills`
2. **No backlog, no roadmap.**
   - `TODO.md` holds one line in Now, which points here. Next and Backlog stay empty until the release.
   - A problem found during the run is added to § Items found during the run with fix or strike. A fix goes into the current phase or a named later one. Nothing is parked.
3. **One phase per session.** Each phase ends at its Checkpoint box. Every Checkpoint does all of the following:
   - runs the full test command itself (`gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./...`);
   - dispatches the Definition of Done's `reviewer`, `security` (when due) and `verifier`;
   - stores the `change` record;
   - ticks this file;
   - commits and pushes;
   - installs into dave's real home from the clean, pushed tree (item 22; AGENTS.md § Rules for working here), and checks that `fyr status` shows a `binary` line with no warning;
   - writes the handoff. The handoff's Kickoff is the fixed one in § Kickoff.
4. **This file is the source of truth for the order.** A change to the order or to an item's verdict is the operator's, recorded as a D-row here.
5. **At the end** (P13), this file is stored as a closed `plan` record, then removed from the root, so the root listing goes back to AGENTS.md § Layout.

## Items — fix or strike

All verdicts below were approved by the operator on 2026-10-31 ("Lets go with your recommendations"). The full reasoning is in `REVIEW-2026-10-31-0920-review-audit-of-every-remaining-task-in-the-repo` and in this plan's commit message.

| # | Item | Verdict | Phase |
|---|---|---|---|
| 1 | An agent-made sandbox swap (`replaced`) is graded FAIL, not ERROR | fix | P6 |
| 2 | Eval Phase 2b: the verdict, summary, title, header, per-trial and per-row tables; `GUIDE-testing` § 4; `GUIDE-fyr-matrix`; the E2E | fix | P6 |
| 3 | Eval Phase 3: `fyr metrics evals`, the report's per-row table and Wilson whisker in a new file (`report.go` is at 402 lines), the `fyr serve` evals page | fix | P7 |
| 4 | Eval Phase 4, the paired compare (O1) | strike (D2) | — |
| 5 | Eval Phases 5a and 5b: the suite's `version`, `kind`, `cluster` and `expect` columns, and the kind-dependent view | fix | P8, P9 |
| 6 | Eval Phase 6, the failure modes | strike (D2) | — |
| 7 | Eval O2, O3, O5, O6, O7, O8 (O4 is kept as written) | strike (D2) | — |
| 8 | The root-docs plan's stale Status line, and closing that plan | fix: closed 2026-10-31 (D1) | done |
| 9 | Root-docs row 12, the operator's smoke on dave | strike: P13's real-home smoke covers it (D1) | — |
| 10 | `PRD.md` moves to the Must/Should shape, as `.agents/scratch/prd-reshape/PROPOSAL.md` 1–5 (approved as drafted) | fix | P5 |
| 11 | The workers plan's Mac boxes (Phase 4's test, 7d) | strike: already in `PRD.md` Known limits, ADR-079 (D1) | — |
| 12 | BUG-198: `fyr work` refuses a run during which the lead's HEAD reflog moved | fix | P4 |
| 13 | `--runner docker` limits: `--memory=8g --memory-swap=8g --cpus=2 --pids-limit=1024`, and a run network with `enable_icc=false` | fix | P4 |
| 14 | `work` skill step 7.3 runs the merged code's tests on the host | strike: stays in Known limits | — |
| 15 | Worker handoffs merged into the lead's trail | strike | — |
| 16 | Checking the agy token's account around a docker worker | strike | — |
| 17 | D141 3b and 3c (the ID stamp bound to the date; edited records date-checked) | strike | — |
| 18 | One shared strict trail-line parser (D65) | strike | — |
| 19 | A `work=<name>` metrics source | strike | — |
| 20 | agy `--settings` in place of a written `settings.json` | strike | — |
| 21 | `fyr check` takes 5m16s of CPU here (3m22s on 2026-10-29) | fix | P2 |
| 22 | The real-home install on dave lags HEAD | fix: every Checkpoint installs (rule 3) | every phase |
| 23 | The parallel-dispatch probe | strike | — |
| 24 | A check that `TODO.md` Now mirrors the plan | strike | — |
| 25 | `TDD.md` § T5: every verb row becomes one line plus its spec link | fix | P12 |
| 26 | The redactor's `pass_k` warning | strike: over-redaction in the safe direction is pinned (AGENTS.md) | — |
| 27 | `AGENTS.md:71`'s "in git at `9001831^`" becomes `PLAN-2026-10-01-google-skills-catalog` | fix | P1 |
| 28 | `frontend-ui-economist` and `frontend-ui-modern` replace `frontend` (D3) | fix | P11 |
| 29 | Release v5.5.0 | fix | P13 |
| 30 | The 30 MB `fyr` binary in the root (ignored, a stray build) | fix: delete | P1 |
| 31 | The empty, untracked `skills/init-project/assets/agents-tree/docs/deploy/` | fix: delete | P1 |
| 32 | `.agents/scratch/` is 13 GB in 972 entries | fix: prune by a listed keep-set | P12 |
| 33 | The file-era trail readers: the global search index's `.agents/docs`, the docs-root fallbacks, the dream's file collector and its `feedback.jsonl` reader, `config_view`'s legacy files, `state.go`'s legacy paths, `restatement-grep.sh`'s exclusions. ONE migration path stays: `fyr record import`, `unmigrated()`'s refusal and init-project's Migration step (D4) | fix | P10 |
| 34 | `~/.config/fyr/feedback.jsonl`, `retros.jsonl` and `proposals/` on dave, deleted once the import is shown to hold every line (`reports/` is live and stays) | fix: the operator's word at P10 | P10 |
| 35 | `~/.claude/AGENTS.md` (the stale pre-ADR-062 copy) refreshed from the current rendered context; never deleted | fix: the operator's word at P12 | P12 |
| 36 | `TODO.md` Next and Backlog emptied | fix: done 2026-10-31 | done |
| 37 | borken cannot move its version: see § Item 37 | fix | P3 |
| 38 | A root `PLAN.md` names what is not built yet, so `fyr check` rule (a) would report it. It joins `notInRuleA` beside `TODO.md` (plan D6 of root-docs), with a test | fix | P1 |
| 39 | AGENTS.md § Layout names `PLAN.md` while it exists | fix | P1 |
| 40 | If borken's `fyr update` failed in its `git fetch` (on dave on 2026-10-31, ssh to github.com:22 timed out twice and `ssh.github.com:443` worked), the update's fetch error names the way out | fix: confirmed in P3 (D12) | P3 |

### Items found during the run

| # | Item | Verdict | Phase |
|---|---|---|---|
| 41 | `SPEC-fyr-serve` and `SPEC-fyr-work` were `spec-stale` after P1's `c89b966`, whose `internal/check` change both name as code; P1's `fyr check` ran before that commit. Re-read against `c29dc47..c89b966` (rule (a)'s `notInRuleA` only, nothing either describes) and re-stamped. P2's own two-line comment in `internal/serve/pages.go` would stale the six specs naming `internal/serve` (`SPEC-fyr-dream`, `-eval`, `-memory-loop`, `-metrics`, `-record`, `-search`; P2's review, finding 1): re-read against that comment and re-stamped in the same commit | fix | P2 |
| 42 | `leadGit` (`internal/work/fetch.go`) turns off replace refs and grafts but not the commit-graph, which can also rewire parents (P2's second security review, finding 3) | fix: `-c core.commitGraph=false`, with P4's guard work | P4 |
| 43 | The `agy` audit's #4 and #10 (`REVIEW-2026-10-31-1101-review-antigravity-agy-integration-audit-against`): no real `agy` 1.4.12 output pins the shapes fyr parses (the dream's stream-json is pinned only by a fake, and the oracle's fixtures are from 1.4.9), so an upgrade can break a check silently, and the dream's `-p ""` beside `--input-format stream-json` is untested on 1.4.12. One real dream turn and one oracle run kept as fixtures, and a test that fails loudly on a missing shape; the dream's argv changes only if the real turn fails | fix (D11) | P12b |
| 44 | The audit's #5: a host `fyr work` or `fyr eval` run is confined by nothing before it runs, and nothing lists what it writes outside the clone (the sandbox HOME's `.gemini/antigravity-cli/scratch` among them). An eval row that lists those writes; `--add-dir <clone>` is passed only if it shows writes there | fix (D11) | P12b |
| 45 | P4's security review left one area "not examined": the host-side git calls in the clone whose `.git` a session wrote (fetch's local `upload-pack` reading the clone's `.git/config`, `objects/info/alternates`, or a `.git` file holding `gitdir:`; the sandbox's `After` and `ExitChecks`). This predates P4 and no finding was made | fix: a `security` review of that blast radius, its findings fixed or struck by D-row | P12b |
| 46 | BUG-204: P4's round-2 `security` agent ran a git probe that rewrote this checkout's `.git/HEAD` to `refs/heads/-x` (restored by the main thread before any commit). Nothing in `agents/security/agent.md` or `agents/reviewer/agent.md` says where a git probe may run | fix: both definitions say a git probe runs only in a repo `git init`ed under a fresh `mktemp -d` outside every checkout, with `git -C` and `GIT_CEILING_DIRECTORIES`; BUG-204 graduates there | P12b |
| 47 | P5's review: `PRD.md`'s provenance comment says each date is the day the item's title first appears in git (`git log -S`). `git log --all -S"<title>"` reproduces 9 of 11. R8 is stored as 2026-09-17, the day its earlier wording "One delivery channel" appeared (`9222762`), where its full title gives 2026-10-24. R0 is stored as 2026-10-23, where every search gives 2026-10-24 (`ebfc1cb`). Recommended `PRD.md` edit: R0's date becomes 2026-10-24, and the comment adds "or of its earlier wording (R8: `9222762`)" | fix, on the operator's word (a `PRD.md` edit); else strike | P12 |
| 48 | P6's review, finding 1: `internal/sandbox` raised `sandbox replaced` for any error opening or removing its own files (`session.go`, `docker.go`, `box.go` `removeInSandbox`), so once item 1 grades `replaced`, a full disk or no descriptors left would count against the agent. The machine's errors (ENOSPC, EDQUOT, EIO, EROFS, EMFILE, ENFILE, ENOMEM) before the first session become `sandbox write failed`, an ERROR (`write`). After a session the agent could cause them itself, so they stay `replaced`, as every other refusal does (P6's security review, finding 1). A setup or check log that cannot be opened goes the same way, where it was a bare failed script (round 2's review). `fyr work`'s `safetyReasons`, the `work` skill's stop list and eval's ingest skip hold the new reason, so none behaves differently | fix | P6 |
| 49 | P6's security review, finding 2 (it predates P6): check e (`internal/sandbox/procs_linux.go:30`) passes when `/proc` cannot be read, which an agent can cause by exhausting descriptors or memory, where `fingerprint` already fails closed; the same review's Low 1: `scanCreds`' `Lstat` (`creds.go:63-66`) and `countLines` of `err.txt` (`after.go:98-103`) also fail open | fix: check e fails closed as `procs`, and the two read sites in the graded direction, test first | P12b |
| 50 | P6's security review, finding 3 (it predates P6): a file system the agent mounts inside the sandbox (FUSE: `/dev/fuse` is world-writable and `fusermount3` setuid on dave) is not detected; `Intact`, `realDirs` and `os.Root` descend into it | fix: a mount under the sandbox is `sandbox replaced` (`st_dev` or mountinfo), test first | P12b |
| 51 | P6's security review, Low 2: eval's ingest (`cmd/fyr/eval.go` `ingestRow`) skips a sandbox by a hand-kept deny list of reasons, beside `fyr work`'s `safetyReasons`; P6 had to add `sandbox write failed` to both by hand | fix: one integrity-reason list in `internal/sandbox`, read by both, test first | P12b |
| 52 | The operator's standing rule of 2026-10-31 for every agy dispatch: when scheduling `fyr work`, choose each worker's model by tier (`--model`) and read the quota left (`agy -p "/usage"`) first, state tier, model and quota per dispatch, switch only within a tier, and stop and ask when a tier (in practice its quota pool) is out; the same check precedes every `fyr eval` run. Today it is a project memory and `DEPLOY-local`'s tiers. Its durable home: a global `rules/agy-dispatch.md`, with one-line pointers in `skills/work/SKILL.md` and `GUIDE-testing` § 4 (D15) | fix | P12 |
| 53 | P6's E2E: the second `fyr eval` run's harness key read `06fe101 (dirty)`, because the first run's testing record changed `.agents/trail.jsonl`, which `fyr install` does not install. Two runs of one harness then land under two keys, which splits the per-harness flags of P7's view | fix: the key's dirty mark looks only at what `fyr install` installs (`context/`, `skills/`, `agents/`, `commands/`, `rules/`), test first, with `SPEC-fyr-eval` § Data | P7 |
| 54 | The verb allowlist (`subcommands` in `internal/metrics/verb.go`) did not hold `fyr`'s `record`, `check`, `eval`, `serve` or `work`, so `fyr record get X` was stored as bare `fyr` and the Phase 5b golden `expect: Read,fyr record` could only ever show `1 of 2` | fix: the five join the list, and a test walks the binary's verbs | P9 |
| 55 | P9's security review (it predates P9): a quoted value shaped like a flag reached a verb as a flag (`git commit -m "--hunter2pass"` stored `--hunter2pass`, which `fyr metrics export` writes in clear), against `Verb`'s doc comment | fix: a word quoting or an escape made flag-shaped is content; the unquoted form is the stated residue | P9 |
| 56 | P10a's security review (it predates P10a): a clone tracking `.agents/scratch/search.db-wal`, `-shm` or `-journal` passed `guardProject`, and a search deleted the tracked file | fix: `guardProject` refuses each companion git tracks; an untracked one needs no ignore rule | P10a |
| 57 | P10b: the `retro` and `distill` eval rows seed `feedback.jsonl`, which neither skill reads (each lists `feedback` records), so both rows ran on no feedback; `$SB/bin/fyr` does not exist at setup, before the install | fix: setup gets `FYR`, the running binary (`internal/eval/row.go`, `SPEC-fyr-eval`, `GUIDE-eval`), and both rows store `feedback` records with it; their behavioural runs are P13's | P10b |
| 58 | P10b: `record import --dry-run` printed `0 already stored` whatever the store held, since a dry run opens no store, which read as "nothing is imported yet" and could not support item 34's proof | fix: the dry run says the store was not read and prints no stored count; the match by ID is `fyr record list --global` | P10b |
| 59 | P10b: `GUIDE-skill-matrix`'s manual rows still name `.agents/docs/...` paths and `feedback.jsonl`. `fyr search` and the restatement script do not reach the row's meaning, and `GUIDE-eval` already holds the release rows | fix: each row is re-pathed to records or struck with the guide | P12 |
| 60 | P10b: dave's real `brain.db` holds 0 global `feedback`, `retro` and `proposal` records: `record import --global` never ran there. Since ADR-087 nothing else reads the three stores, so `/retro`, `--global`, the dream and `fyr status`'s retro line see none of the 56 lines until it does | fix: the real import, then item 34's deletion, each on the operator's word | P10b |

## Item 37 — borken: a development build at a zip place is deadlocked

What happened on borken (agy's run, quoted by the operator on 2026-10-31):
- `fyr version` printed `b9cae29346b2`, a development build.
- `fyr update --from-zip ~/Downloads/fyr-v5.0.0.zip` exited 0: "installing its harness and the running binary b9cae29346b2 … nothing in the zip replaces the binary". The version did not move.
- After that, `fyr status` and `fyr update` both exit 1: "this home was installed from a zip … harness repo not found and this build is b9cae29346b2, a development build".

Checked against the code (2026-10-31, `afd92b4`):
- `status` and `update` are `source: true` verbs (`cmd/fyr/ctx_verbs.go:126-133`), so they call `harness.ResolveSource` first.
- For a development build, `ResolveSource` (`internal/harness/repo.go:22-58`) needs a checkout: `--repo`, `$FYR_REPO`, the working directory, or the recorded path.
- `--from-zip` records `zip:sha256:…` as the place's source, so the recorded checkout path (`~/workspace/fyr` on borken) is gone. Neither verb can reach `zipDrift` or the zip instruction.
- The zip never replacing the binary is by design (ADR-038, `SPEC-fyr-delivery` § The model).

The fix (D5):
1. A table test over every binary kind (release, development) and every place kind (checkout, binary-only, zip), for `status`, `update` and `update --from-zip`. It fails first on the development-build-at-a-zip-place cells (the bug skill: the failing test before the fix; a `bug` record under the next free id, BUG-203 on 2026-10-31, `fyr record add` assigns it; "the borken bug" below). The cells are § Cells below; every cell not listed there keeps `GUIDE-fyr-matrix` as it stands at `260d0b7`, and the test records today's output for those first.
2. `status` at a zip place reports from the state with no source.
3. `update` without `--from-zip` at a zip place prints how that place moves (a new zip, or a release binary for the version) and exits 1, a refusal like a plain copy's (`GUIDE-fyr-matrix`); exit 2 already means "behind" for `update --check` and a usage error (D5, approved).
4. `update --from-zip` run by a development build at a place whose state records a checkout is refused, naming `fyr update` and `fyr install --repo <path>`. A development build never turns a checkout place into a zip place.
5. The error that strands a place names `fyr install --repo <checkout>` as the way back. That includes `zipNote` in `internal/harness/repo.go` (`sourceCandidates`), whose "or run `fyr update --from-zip <file>` again" is the advice that stranded borken.
6. `SPEC-fyr-delivery` § The model, the README map's zip label and `GUIDE-fyr-matrix` change in the same commit (AGENTS.md § How the harness works, rules 3 and 6), with an ADR amending ADR-038.

### Cells (D5; approved by the operator, 2026-10-31)

| Binary | Place | Command | Expect |
|---|---|---|---|
| development | zip | `status` | Reports from the state with no source: `installed … from zip sha256:…`, `drift not compared …`; exit 0 (1 on missing files, as today) |
| development | zip | `update` | Refused, exit 1. The message names both ways the place moves: a newer zip with a release binary of that version, and `fyr install --repo <checkout>` |
| development | zip | `update --from-zip <f>` | As today (installs the zip's harness and keeps the running binary, exit 0), plus one line saying the binary did not move and naming `fyr install --repo <checkout>` |
| development | checkout | `update --from-zip <f>` | Refused, exit 1, naming `fyr update` and `fyr install --repo <path>`. The state is unchanged: the place stays a checkout place |
| release | zip | `status` | As the development row |
| release | zip | `update`, `update --from-zip <f>` | Unchanged from today: the test records today's output first, and this fix does not change it |
| either | zip | `install --repo <checkout>` | The place becomes a checkout place: `status` names the checkout and exits 0 (the way back; borken's manual line below) |

Rejected: building the binary from the zip's Go source. That would be a new delivery path (rule 3) and would need Go on every zip place.

Now, by hand on borken, before the fix (Who: user): `git -C ~/workspace/fyr pull`, then `go build -o ~/workspace/fyr/.agents/scratch/fyr ./cmd/fyr` run inside `~/workspace/fyr`, then `~/workspace/fyr/.agents/scratch/fyr install --repo ~/workspace/fyr`. Expect: `fyr version` prints `b<sha>` of the pulled HEAD, and `fyr status` exits 0 naming `repo ~/workspace/fyr`. That makes borken a checkout place again, and `fyr update` works from then on. If the pull fails on port 22, item 40 applies. The operator pastes the output of the three commands into the next session, so P3 can confirm or strike item 40 from it. No test yet shows that `install --repo` turns a zip place back into a checkout place; P3's last cell pins it.

## Phases

### P1 — cold read and hygiene (one session)
- [x] Cold read of this file: one `reviewer` given `PLAN.md` only. Each finding is answered by a D-row, a fix to this file, or a question to the operator. Verify: the review record. Expect: no BLOCKING item left unanswered. Who: AI.
- [x] Items 38 and 39. `notInRuleA` gains `PLAN.md` with a test that fails without it, and AGENTS.md § Layout names `PLAN.md` while it exists. Verify: `go test ./internal/check`. Who: AI.
- [x] Item 27, the AGENTS.md pointer. Item 30, `rm fyr` in the root (ignored, checked by `git check-ignore -v fyr` first). Item 31, `rmdir` of the empty template directory. Verify: `git status --short --untracked-files=all`, and `ls skills/init-project/assets/agents-tree`. Who: AI.
- [x] Checkpoint (rule 3). Expect: the full test command exit 0, and `fyr check` from a tree binary gives 0 findings.

### P2 — `fyr check` speed (item 21; one session)
- [x] Profile: `go test -cpuprofile` or a `pprof` run of `fyr check` over this checkout, the hot path named with numbers. Verify: the profile's top 10 under `.agents/scratch/`. Who: AI.
- [x] Fix the hot path, test first. The test pins a work count, not a time: how many times the hot operation runs over a fixture (a wall-clock bound is flaky). Verify: `go test -race ./internal/check`, then `time fyr check` from a tree binary, three runs. Expect: under 30 s of CPU here (measured, not a test). The findings must match the pre-fix binary's findings on the mutated tree `.agents/scratch/prd-reshape/clone`, with the mutation in `check-mutant.txt`. Who: AI.
- [x] Checkpoint (rule 3). `SPEC-fyr-check` names the bound.

### P3 — borken's update (items 37 and 40; one session; the `bug` skill)

Gate: P3 starts only after the operator has approved D5 and § Item 37 § Cells: met, 2026-10-31 (D5).

- [x] The borken bug stored (the next free BUG id). The matrix test fails first on the development-build-at-a-zip-place cells. Who: AI.
- [x] D5's fixes 2–5 in `cmd/fyr` and `internal/harness`. Verify: the matrix test and `go test -race ./cmd/fyr ./internal/harness`. Expect: pass. Every cell equals § Item 37 § Cells, and every other cell keeps `GUIDE-fyr-matrix` as it stood at `260d0b7`. Who: AI.
- [x] The ADR amending ADR-038, `SPEC-fyr-delivery`, the README map's label, `GUIDE-fyr-matrix` and `fyr help update`, all in the fix's commit (rules 3 and 6). Verify: `TestHarnessMap`, `/review --code --docs`. Who: AI.
- [x] Item 40: reproduce a fetch failure against an unreachable remote in a scratch checkout (the fetch is the `git fetch --quiet` in `internal/harness/repo.go`). Also use the operator's borken output if it was pasted. Expect: the message names the remote URL and quotes git's stderr. If it does not, fix it, test first; else strike item 40 by D-row. Who: AI.
- [x] Checkpoint (rule 3), with `security` (update is install behaviour).

### P4 — `fyr work` guard and container limits (items 12 and 13; one session)
- [x] Item 12: the lead's HEAD reflog is read before the run and compared after. If it moved, the run is refused with a line naming the reflog entry, test first. BUG-198's status becomes fixed. `SPEC-fyr-work` is updated. Verify: `go test -race ./internal/work`. Who: AI.
- [x] Item 13: the limits and the run network in `dockerArgs` (`internal/sandbox/docker.go`), with argv tests.
  - One network per run, labelled `fyr.work=<stamp>` so the existing label sweep (ADR-079) removes it after the run, on `--kill`, and after a killed run.
  - When the network cannot be created, the run is refused; it never falls back to the default bridge.
  - `--pids-limit` goes from 4096 to 1024 as item 13 says. If the real worker's `go test -race ./...` fails on it, the value goes back to the operator.
  - Docs in the same commit: `SPEC-fyr-work` § Docker runner, an ADR amending ADR-079, and `TDD.md` § T17.

  Verify: `go test -race ./internal/sandbox`, and one real docker worker on dave (GUIDE-testing § 3k) whose Kickoff runs `go test -race ./...`. Expect: PASS, and `docker network ls --filter label=fyr.work` is empty afterwards. Who: AI.
- [x] Checkpoint (rule 3), with `security`.

### P5 — the PRD reshape (item 10; one session)
- [x] The ADR, then `PRD.md` as `.agents/scratch/prd-reshape/PRD.diff` (re-checked with `git apply --check` first), `SPEC-fyr-check`'s `### R0` line, and `SDD.md`'s and `TDD.md`'s traces if they name `### R`. All in one commit. Verify: `fyr check` from a tree binary. Expect: 0 findings and neither PRD note. Who: AI.
- [x] Checkpoint (rule 3). From here on D6 applies to every phase.

### P6 — eval: the trial record (items 1 and 2; one session)
- [x] Item 1: `replaced` becomes a FAIL in `internal/eval/cause.go` (`errorCauses` and its comment). Tests that assert ERROR today change: `internal/eval/cause_test.go` (`TestClassifyEveryReason`), `internal/eval/row_replaced_test.go` and `TestRunSandboxReplaced` in `internal/eval/run_sandbox_test.go`. The `SPEC-fyr-eval` step 15 and status table change too, and the restatements of "replaced … ERROR" are grepped (`skills/review/scripts/restatement-grep.sh -i -- replaced`). Verify: `go test -race ./internal/eval ./cmd/fyr`. Expect: `TestRunSandboxReplaced` asserts FAIL, and the SPEC table lists `replaced` under FAIL. Who: AI.
- [x] Item 2: the boxes of the eval-metrics plan's Phase 2b (`PLAN-2026-10-31-0300-…`, § Phases § Phase 2b), as written there. The shapes are in that record's § Interfaces (`fyr eval --runs N` and the trial record). The two E2E criteria are in its § Verification: the golden path and the edge. The E2E runs under `~/sandbox/fyr-eval/home` (AGENTS.md's eval exception) and needs a logged-in agy on dave. If agy is not logged in, the session asks the operator. Who: AI, with the operator's agy login.
- [x] Checkpoint (rule 3). Expect: the full test command exit 0, and the E2E record IDs. Done 2026-10-31: exit 0, 15 of 15 packages; E2E `TEST-2026-10-31-1456-eval-3-of-3-pass-at-06fe101-k-3-0-errors` (golden) and `TEST-2026-10-31-1457-eval-0-of-0-pass-at-06fe101-k-2-2-errors` (edge); `CHANGE` record of P6.

### P7 — eval: the per-row view (items 3 and 53; one session)
- [x] Item 53, first: the harness key's dirty mark over what `fyr install` installs, test first (a run after a run that wrote its testing record keeps the clean key). Verify: `go test -race ./cmd/fyr -run 'TestEval|TestHarnessKey'`. Who: AI. Done 2026-10-31 (`238e7f1`): `TestHarnessKeyReadsInstalledDirs` and `TestDirtyUnder` failed first, then pass; `harness.ManagedDirs`, `trail.DirtyUnder` (D16).
- [x] The eval-metrics plan's Phase 3 boxes, with the new report code in a new file. Who: AI. Done 2026-10-31 (`238e7f1`): `fyr metrics evals` (`internal/metrics/evals*.go`, `cmd/fyr/verb_metrics_evals.go`), the report's eval rows (`report_evals.go`; the template moved to `report_template.go`), `/evals` in `fyr serve`; `TestEvalRows`, `TestHistoryFlags`, `TestMetricsEvals`, `TestReportEvalRows`, `TestEvalsTable`; five must-fail mutations in the change record (D16).
- [x] Checkpoint (rule 3). Expect: Phase 3's E2E criterion on the eval home. Done 2026-10-31: the full test command exit 0, 1795 pass, 0 fail (`TEST-2026-10-31-1532-verification-plan-md-p7-1795-pass-0-fail-fyr-che`); `fyr check` 0 findings on `238e7f1`; E2E `TEST-2026-10-31-1532-e2e-plan-md-p7-fyr-metrics-evals-on-the-eval-hom` (0.085 s, no row `unstable`, no unknown-only row flagged but `history`).

### P8 — eval: the suite grammar (item 5, Phase 5a; one session)
- [x] The eval-metrics plan's Phase 5a boxes. Who: AI. Done 2026-10-31 (`e9fad5c`): ADR-085 stored before the code; `internal/eval/suite_columns.go`, `Row`'s four fields, `--list`'s `v<version>  <kind>`, the trial's four fields; `TestSuiteGrammarLong`, `TestSuiteRefusals`, `TestEvalVerbListLong`, `TestEvalTrialSuiteFields`, `TestEvalRowsVersionDefault`; eleven must-fail mutations in the change record (D17).
- [x] Checkpoint (rule 3), with `security` (the parser). Done 2026-10-31: the full test command exit 0, 15 of 15 packages; verifier 1821 pass, 0 fail (`TEST-2026-10-31-1619-verification-plan-md-p8-at-e9fad5c-1821-pass-0-f`); `security` CLEAR, 2 low fixed (`REVIEW-2026-10-31-1558-security-plan-md-p8-the-suite-parser-s-four-colu`); reviewer rounds 1 and 2; `fyr check` 0 findings after seven re-read stamps.

### P9 — eval: the suite's rows (item 5, Phase 5b; one session)
- [x] The eval-metrics plan's Phase 5b boxes, with `TestSuiteHarness`'s `want` in the same commit. Who: AI. Done 2026-10-31 (`d66b87e`): `GUIDE-eval` in the long header (9 `regression`, 8 `adversarial`), the view's `kind` and `expect` columns and per-kind suite lines, the record's `expect`; items 54 and 55; D19; the E2E on real agy, version 1 and 2 of `golang` in a throwaway home (`1 of 2`, `1 of 3`).
- [x] Checkpoint (rule 3). Expect: `go run ./cmd/fyr eval --home .agents/scratch/evalm --list` prints version and kind for every row. Done 2026-10-31: 63 rows, each `v1` and a kind; verifier 1831 pass, 0 fail; `fyr check` 0 findings; `reviewer` APPROVE, docs lens and round 2 APPROVE WITH NITS, `security` one low finding, fixed (item 55).

### P10a — the file-era trail goes: search and the docs root (item 33, its search half; one session; D9)
- [x] The ADR (D4), stating D9's registration rule first: a project with `.agents/trail.jsonl` registers for `--global`, replacing `globalDocsRoot` (`internal/search/global.go`, `scope_prepare.go`). Then item 33's search readers go, with their tests, test first where a behaviour changes (a migrated project registers for `--global`): the global search index's `.agents/docs` and the docs-root fallbacks. `SPEC-fyr-search` is updated. Verify: the full test command. Who: AI. Done 2026-10-31 (`5889b4b`, `1b14564`): ADR-086, D20, item 56; `TestTrailRegistersForGlobal`, `TestDocsTreeIsPlainText`, `TestWorktreeRegistersTheMainRoot`, `TestRegistrationReadsTheRightRoot`.
- [x] Checkpoint (rule 3), with `security` (search input paths). Done 2026-10-31: verifier 1840 pass, 0 fail at `1b14564`; `fyr check` 0 findings; `reviewer` and `security` two rounds each, findings fixed; BUG-205 (the first verifier run's `TestFunctionLength`).

### P10b — the file-era trail goes: the dream and the legacy stores (items 33, its other half, and 34; one session; D9)
- [x] The rest of item 33, test first where a behaviour changes (the dream reads only records): the dream's file collector and its `feedback.jsonl` reader, `config_view`'s legacy files, `state.go`'s legacy paths, and `restatement-grep.sh`'s exclusions. `SPEC-fyr-dream` and `skills/review/scripts/restatement-grep.sh` are updated. Verify: the full test command, and `skills/review/scripts/restatement-grep.sh -i -- .agents/docs` across the living docs. Expect: only the import path, `unmigrated()` (`internal/brain`), `trail.DocsRoot` (`internal/trail`), init-project's Migration step and their tests name `.agents/docs`. Who: AI. Done 2026-10-31 (`bd2d6b4`): ADR-087, D21, items 57, 58; `TestDreamReadsOnlyRecords`, `TestSkipReasonNamesTheImport`, `TestRunSkipsAFileEraProject`, `TestGlobalReadsNoConfigFile`, `TestStatusRetroLine`.
- [x] Item 34: `fyr record import --global --dry-run` (`internal/brain/import_global.go`) is shown to hold every line of the three legacy stores on dave: no problem listed, and each `feedback.jsonl`, `retros.jsonl` and `proposals/` entry matched to a global record by count and by ID. Then they are deleted on the operator's word, by name. Who: AI (the proof), user (the word). Done 2026-10-31: the operator's word in the session ("Import, then delete"); the real import from `1e03f22`, 33, 17 and 6 by ID and by body, then the eight files and the empty `proposals/` deleted by name (item 60).
- [x] Checkpoint (rule 3), with `security` (the dream's input paths). Done 2026-10-31: 1828 pass, 0 fail, 6 skip; `fyr check` 0 findings.

### P11 — the UI skills (item 28, D3; one session)
- [x] Provenance first: the zip's `frontend-ui-modern` is compared with the published `anthropic-skills` copy when one is on this box, with `diff -r` (skills/VENDORED.md's procedure). If they are the same, it is vendored verbatim and listed in `skills/VENDORED.md`, and other descriptions are shortened to fit; if not, both descriptions are shortened. Who: AI.
- [x] The UI plan's Phase 1 box, as written there, with D3's answers. One `GUIDE-eval` row per new skill, with `TestSuiteHarness` updated. D3 Open 5's orphans (rules of `frontend` with no home in the new skills) are put to the operator in this session before `frontend` goes. The `security` agent reads every script the new skills carry. The UI plan's Phase 2 (the behavioural runs) is P13's first box. Verify: `go test ./...` (the lint and `TestSkillListingBudget`). Who: AI, user (the orphans).
- [ ] Checkpoint (rule 3), with `security` (the new scripts) and `/review --docs`.

### P12 — the docs sweep (items 25, 32, 35, 47 and 52; one session)
- [ ] Item 25: `TDD.md` § T5 rows thinned, each claim kept in its `SPEC-fyr-*` or `cmd/fyr/help_text.go`. Verify: `fyr check`, and a table in the change record that maps each removed claim to its new home's file:line. Who: AI.
- [ ] Item 32: `.agents/scratch/` pruned to its keep-set (`workers/`, `agents/`, `evalm/`, `prd-reshape/`, `ui-skills/`, `audit/`, and what a later phase names). It is listed first and deleted by name. Verify: `du -sh .agents/scratch` and `ls .agents/scratch` before and after, in the change record. Expect: only the keep-set is left. Who: AI.
- [ ] Item 35: `~/.claude/AGENTS.md` refreshed on the operator's word, from `fyr render` of the installed harness. Verify: `diff <(fyr render) ~/.claude/AGENTS.md`. Expect: exit 0. Who: AI (the command), user (the word).
- [ ] Item 52: `rules/agy-dispatch.md`, the operator's rule as D15 records it, within the size pins of `internal/harness/size_test.go`, with the pointers in `skills/work/SKILL.md` and `GUIDE-testing` § 4, and `/review --docs`; its behavioural run is P13's. Verify: `go test ./...` (the lint and the size pins). Who: AI.
- [ ] Item 47: the operator's word on the `PRD.md` provenance edit, asked in that session. On a yes, the edit as item 47 recommends, re-checked with `git log --all -S`; on a no, item 47 is struck by D-row. Verify: `fyr check`. Who: user (the word), AI (the edit).
- [ ] Checkpoint (rule 3). Expect: `fyr check` 0 findings and 0 notes.

### P12b — `agy` captures, the outside-clone probe, the clone-git review and the sandbox checks (items 43, 44, 45, 46, 49, 50 and 51; one session; D11)
- [ ] Item 43: one real dream turn and one oracle run on the installed `agy` (1.4.12 or later, named), their output kept as test fixtures beside the fake ones, and a test that fails, naming the missing key or event, when a shape fyr needs is absent. If the dream's `-p ""` with `--input-format stream-json` fails on the real turn, the argv changes with a test. The runs use a home outside every checkout, as `fyr eval` does (`GUIDE-testing` § 4), and need a logged-in `agy`; if it is not, the session asks the operator. Verify: `go test -race ./internal/dream`. Who: AI, with the operator's `agy` login.
- [ ] Item 44: a `GUIDE-eval` row whose host run lists every path written outside the clone (the sandbox HOME's `.gemini/antigravity-cli/scratch` included), with `TestSuiteHarness`'s `want` updated in the same commit. If it lists writes under `scratch`, `--add-dir <clone>` joins `agyArgs` (`internal/sandbox/session.go`) with an argv test, and `SPEC-fyr-work` and `SPEC-fyr-eval` say so; if not, item 44 is closed by a D-row naming the run. Verify: `fyr eval` of the row, n of N. Who: AI.
- [ ] Item 46: the probe rule in `agents/security/agent.md` and `agents/reviewer/agent.md` (BUG-204 → graduated), with `/review --docs`; their behavioural runs are P13's. Verify: `go test ./...` (the agent lint). Who: AI.
- [ ] Item 45: one `security` agent over the host-side git calls in a session-written clone (`internal/work/fetch.go`'s fetch, `internal/sandbox`'s `After` and `ExitChecks`). Each finding is fixed, test first, or struck by D-row. Verify: the review record and `go test -race ./internal/work ./internal/sandbox`. Who: AI.
- [ ] Items 49, 50 and 51: check e fails closed as `procs` when `/proc` cannot be read, and the other open read sites in the graded direction; a file system mounted under the clone or the sandbox home fails the trial `sandbox replaced`; one integrity-reason list read by eval's ingest and work. Each test first. `SPEC-fyr-eval` § Safety checks and `SPEC-fyr-work` say so. Verify: `go test -race ./internal/sandbox ./internal/eval ./internal/work`. Who: AI.
- [ ] Checkpoint (rule 3), with `security` (the sandbox's argv).

### P13 — release v5.5.0 (item 29; one session, from the operator's own turn)
- [ ] The behavioural runs owed by the range `v5.0.0..HEAD` (AGENTS.md § Release): one `fyr eval` row per changed skill and agent, plus the real-home smoke of `TEST-2026-09-03-verify-real-home`, stored as `testing` records. The list comes from `git diff --name-only v5.0.0..HEAD -- skills agents context rules` at that time, and § Risks' list is a forecast. Who: AI.
- [ ] This file stored as a closed `plan` record, removed from the root, and AGENTS.md § Layout and `TODO.md` updated (Now: the auto-mode plan's Phase 0, D18), all in one commit (rule 5). Who: AI.
- [ ] `/preflight v5.5.0`. Expect: SHIP, the tag pushed, the release published. Who: AI, from the operator's turn.
- [ ] borken moves to v5.5.0 by its own `fyr update` (`DEPLOY-remote` § Operator steps). This is a post-release check outside the plan's life (D7). Expect: `fyr version` prints `v5.5.0`, or `b<sha>` of the release commit at a checkout place, and `fyr status` exits 0. Who: user.

## Decisions

D1 — 2026-10-31 — This file is the only plan. Each of the four open plan records gets a closing D-row, and its unticked boxes become `[-]`: each one either moves here as an item or is struck by this file's table. `TODO.md` Next and Backlog are emptied, and every Backlog line is an item here. Row 12 of root-docs is struck as covered by P13's real-home smoke. The workers plan's Mac boxes are struck as covered by `PRD.md` Known limits (ADR-079). BUG-198 stays open until P4. — by: the operator, 2026-10-31 ("This is the only plan. During this plan nothing can be in the backlog or roadmap").

D2 — 2026-10-31 — The eval-metrics plan ends at its Phase 5b. Phase 4 (the compare, O1) is struck: Phase 3's `--harness` filter, run once per key, is the comparison. Phase 6 (the failure modes) is struck: its rules have no validation set. O2, O3, O5, O6, O7 and O8 are struck at their defaults, and O4 stands as written. — by: the operator, 2026-10-31 ("go with your recommendations").

D3 — 2026-10-31 — The UI plan's Step 1, taken as recommended:
- Open 1: (a) shorten the two descriptions, or (b) vendor `frontend-ui-modern` if it is the published skill (P11's first box).
- Open 2: `typescript` stays.
- Open 3: (b) the screenshot folders stay out of the embed.
- Open 4: the context line names both skills, so shadcn/ui is the house component set.
- Open 5: a cold-read diff of `frontend`'s rules, each orphan put to the operator.
- Open 6: the claude.ai mentions stay.
- Open 7: one `GUIDE-eval` row per skill.

— by: the operator, 2026-10-31 ("go with your recommendations").

D4 — 2026-10-31 — The file-era trail has one path left: `fyr record import`, `unmigrated()`'s refusal, and init-project's Migration step. Every other reader of `.agents/docs`, `docs/`, `feedback.jsonl`, `retros.jsonl` or `proposals/` goes. An unmigrated project is refused by every record verb and imports first. Its ADR is written in P10. — by: the operator, 2026-10-31 ("clean up old files in the docs folder because we are using fyr instead").

D5 — 2026-10-31 — Item 37's design is as in § Item 37: a matrix test, `status` and `update` working at a zip place with any binary, and a development build never turning a checkout place into a zip place. The zip never replaces the binary (ADR-038 stands). Rejected: building the binary from the zip's source. — by: implementer, as recommended to the operator; approved with § Item 37 § Cells as drafted and fix 3's exit 1 by the operator, 2026-10-31 ("Approve as drafted").

D6 — 2026-10-31 — From P5 on, `PRD.md`'s `enforced by:` names bind (rule (c) of `fyr check`). A test named there is never renamed or removed by a later phase; its body may change. If one must go, the PRD edit is put to the operator in that session (PRD.md is immutable without approval). Every Checkpoint from P5 on expects `fyr check` to report 0 findings. The alternative was moving P5 after P10; that changes the order, so it is the operator's choice. — by: implementer, answering P1's cold read (the P5/P10 finding).

D7 — 2026-10-31 — P13 keeps its order: the plan closes in the commit that preflight releases. If `/preflight` reports NO-SHIP, the plan reopens: `PLAN.md` is restored from git, the finding goes under § Items found during the run, and the plan record's status goes back to open. borken's box is a post-release check: the closing handoff's Next Steps carry it, and the plan record keeps it as `[-] (handoff Next Steps)`. — by: implementer, answering P1's cold read (the P13 finding).

D8 — 2026-10-31 — P2's hot path was rule (b)'s `git log -G`, one per current spec, each diffing all 399 revisions of the 13 MB `trail.jsonl`: a GIT_TRACE2 run of the pre-fix binary gave 10 such calls and 565 s of the run's git time, all other git calls under 0.1 s, and the Go process used 0.27 s of a 510 s profiled run. That is 5m26s of CPU in P1's `check.log`, and 5m16s in item 21's row, which was the audit's run. The fix finds each spec's commit by bisection over HEAD's first-parent trail commits, each blob read once and indexed for every spec. It descends into a merge's merged side, so a line added on a merged branch keeps that branch's commit, as `-G` found it. A commit that only moves the line still does not reset the count. The history reads and the count run without replace refs, grafts or the commit-graph, refuse a blob over 256 MiB from its size, index each line in one pass, and descend at most 64 merges (P2's security reviews, two rounds). What changes in meaning, in `SPEC-fyr-check` § Known limits: a line removed and added again is found at one of its additions, not always the first, and a revision holding a NUL byte is read where `-G` skipped it as binary. Pre- and post-fix findings, notes and exit codes are identical on this checkout, on the mutated clone `.agents/scratch/prd-reshape/clone` (equal to `check-mutant.txt`), and on a fixture clone with 7 stale specs and a commit that only moves a spec's line (`.agents/scratch/p2-check-speed/`). Rejected: one combined `git log -G -p` (about 55 s; the diff itself is the cost) and a walk over every blob (24 s, growing with history). — by: implementer, within P2's fix box.

D9 — 2026-10-31 — P10's registration rule: a project with `.agents/trail.jsonl` registers for `--global`, replacing `globalDocsRoot`. P10 splits into P10a (search and the docs root) and P10b (the dream and the legacy stores, item 34). — by: the operator, 2026-10-31 ("trail.jsonl + split").

D10 — 2026-10-31 — P5 keeps its place before P10; D6 covers the hazard. — by: the operator, 2026-10-31 ("No, keep the order").

D11 — 2026-10-31 — The `agy` audit (`REVIEW-2026-10-31-1101-review-antigravity-agy-integration-audit-against`), from the operator's four-strategy reference. fyr stays on one-shot `agy -p`, one fresh process per run. Items 43 (the audit's #4 and #10) and 44 (#5) join the plan in P12b. Struck: #1 (a persistent stream-json process), #2 (`agy_acp_server`), #3 (the Python SDK, remote control, community ACP wrappers), #6 (the token link's design), #7 (the borken keyring check), #8 (`modelProvider`), #9 (`--json-schema` for the dream's answer), and the P1 handoff's question 4 (the remote-control daemon's argv). — by: the operator, 2026-10-31 (the audit items selected; the strikes "go in either way").

D12 — 2026-10-31 — P3, how the fix reads § Item 37 § Cells where the table is silent or cannot hold:
- **Where a development build gets the zip-place answers.** Only when the source does not resolve. Inside a checkout it resolves that checkout as before. Its `status` then keeps the checkout's source line with the way-back note (`zipAdvice`). Its `update` keeps `knownCheckout`'s refusal, which names `--repo <that checkout>`. `update --check` and `--dry-run` at a zip place are refused like `update`, exit 1, because a read-only mode resolves the guard the real run does.
- **The "binary did not move" line** is printed by every development build's `--from-zip` that installs, at a binary-only or a fresh place too. Only the zip-place cell names it, but the binary stays at every place, and the line removes nothing.
- **The refusal of `--from-zip`** covers every place whose manifest records a tree: a checkout, and a plain copy too, since either would lose its tree. It runs after the state directory's migration. A dry run moves nothing, so while `~/.config/sh-agent` waits it cannot read the manifest and refuses. That is stricter than the real run, in the safe direction.
- **Reviews past the cap.** The two rounds of `reviewer` and `security` ran. Round 2's findings are fixed without a third round, as in P2:
  - the guard's order, which broke three migration tests;
  - URLs that do not parse, query tokens and an `@` in an scp path;
  - `ShellArg`'s double quotes.

  Each fix has a test that fails without it. The review records' Outcomes name them.
- **§ Cells' "either | zip | `install --repo`".** It holds for a development build only. A release binary still refuses `--repo` (ADR-034, D15), and that refusal already says to build fyr from that tree and run that build's install. `TestPlaceMatrix` pins both.
- **Item 40.** A scratch checkout whose remote is `ssh://git@127.0.0.1:1/…` gave `git fetch: git fetch --quiet: ssh: connect to host 127.0.0.1 port 1: Connection refused …`. That quotes git but names no remote URL, so the item was fixed, test first (`TestBehindFetchErrorNamesTheRemote`). The message now names the URL from `git ls-remote --get-url`, without its userinfo, query or fragment, and not at all when it does not parse (P3's security reviews: a token in the URL, or one an `insteadOf` rule adds, would print). It names the checkout to fix it in, quoted for pasting (`harness.ShellArg`), says that ssh.github.com port 443 works where port 22 is blocked, and then quotes git. The operator's borken output was not pasted this session.

— by: implementer, within P3's boxes (ADR-082).

D13 — 2026-10-31 — P4, how the fix reads items 12, 13 and 42 where they are silent:
- **What "the reflog moved" compares.** The newest reflog entry of HEAD and of the branch it points to, each read as `<sha> <ref>@{<date>} <subject>` when the base is recorded, must still be the newest at step 6. The branch's reflog is P4's security review finding 3: a move made from another worktree of the checkout writes only the branch's reflog. The count of entries is not compared, because a `gc --auto` (a worker's own fetch can start one) expires old entries mid-run. The note names how many entries the run added and the first of them.
- **A checkout with no reflog for HEAD or its branch is refused** (refusal 23), so the guard never passes because there was nothing to compare. Item 12 does not name this refusal; without it the guard would fail open.
- **A daemon that cannot apply the limits is refused** (refusal 24, `docker info`). Without the cgroup controllers dockerd drops a limit with a warning only, so item 13's limits would silently not apply (the security review, finding 1). `--kill <stamp>` of a stamp with no pid file removes that stamp's networks only, never a container, and nothing while the stamp's pid lives, since a run killed between its network and its pid file leaves only the network (finding 4), and labels span the daemon, so the stamp may be another home's live run (round 2).
- **The network is made after the refusals and before the stamp line**, so a run refused for its network makes nothing and prints nothing, like every other refusal. The run's own network is removed by the sweep every time, and the sweep line still prints only when a container was removed or something failed.
- **Item 42** is `-c core.commitGraph=false` in `leadGit`, pinned by `TestLeadGitHistoryOptions`.
- **`--pids-limit 1024` held.** The real docker worker's `go test -race ./...` on the fixture exited 0, and so did a rerun under the same limits from the host, so the value does not go back to the operator. A large module's `-race` run under these limits was not tried.

— by: implementer, within P4's boxes (ADR-083).

D14 — 2026-10-31 — P6, how the fix reads items 1 and 2 where they are silent:
- **Where `replaced` sits.** It joins the safety causes, after `procs`: a FAIL even beside an ERROR reason (an interrupted trial whose sandbox was swapped is graded), as `fyr work` already counts it among its `safetyReasons`.
- **A machine failure is not a swap, until a session has run** (item 48, from the reviews): the classifier is pinned by `TestFileFailed` over real errno-wrapped errors, before and after a session. `Intact`'s own errors stay `replaced`: every call is after a session. Its four call sites are each reached by a test in the `replaced` direction, and each test fails when its call is removed: the host session's outputs (`TestAgySessionArms`), the docker session's (`TestFileFailedDocker`), `removeInSandbox` (`TestFileFailedRemove`) and `Bash`'s log (`TestBashLogPlanted`). None is reached under a real machine error: a full disk or a descriptor limit cannot be made here without root or a process-wide limit, and the sandbox tests take no hook (their D12). The verifier's first run found the docker and remove sites untested and named four sites where this row said three.
- **The line.** `<s>s (<s>s session)`: the session part shows only when a session started. A reason that is the cause word for word is said once (`ERROR (interrupted)`, not `(interrupted; interrupted)`). The ingest error line also names `#<i>`, like the trial-write line, since under `--runs` the row alone does not say which trial failed.
- **The header.** The `- agy:` line is folded into the `- model:` line, which carries the same value. Several models print `mixed (<a>, <b>)` and none prints `unknown`: the eval-metrics plan's bare `mixed` would hide which models. The count reads `1 error` for one. Intervals print two decimals. The suite's interval needs a rate over rows, so `metrics.WilsonRate` is added and `Wilson` calls it.
- **The E2E's view.** Both criteria read `fyr metrics evals`, which is P7's verb. P6 reads the same numbers from A6 of `fyr metrics show --json --label eval=<row>` (`n`, `errors`, `pass_pow`): `pass_pow` has key `3` only when n ≥ 3, which is "no `n/a` in `pass^3`". P7's E2E runs the view itself.

— by: implementer, within P6's boxes.

D15 — 2026-10-31 — The operator's standing rule for every agy dispatch (item 52). Before scheduling `fyr work`, choose each worker's model by tier with `--model` and read the quota left; before every `fyr eval` run, read the quota (its model is agy's default, `gemini-4-argon-medium`, [pro]: a `--model` flag is the operator's design question). State the tier, the model and the quota left in one line per dispatch. A model out means a similar model in the same tier, named with why; a tier out means stop and ask, never a weaker tier. The quota reader is `agy -p "/usage"` (no turn, no quota spent). The tiers come from `agy models` and `/usage`: [pro] argon, [flash] 3.8 flash (the Gemini pool); [opus], [sonnet], [gpt-oss] (the Claude and GPT pool). There is no [plan] tier: "plan" meant the scheduling step. Quota is per pool, so a pool out is every tier in it out: stop and ask. The tiers and the command are in `DEPLOY-local`. The durable home is a global `rules/agy-dispatch.md` (option a of `.agents/scratch/p6/agy-quota-rule.md`; b, a `PRD.md` requirement, and c, this repo's `.agents/rules/`, rejected), built in P12. — by: the operator, 2026-10-31 ("This is a rule, not a preference"; "Go all approved"; "Im referring to when you schedule work … Fyr work").

D16 — 2026-10-31 — P7, how the fix reads items 53 and 3 where they are silent:
- **What "installed" means for the key.** `harness.ManagedDirs`, the directories `checkManagedTree` already guards: `context/`, `agents/`, `commands/`, `rules/`, `workflows/` and `skills/`. Item 53 names five; `workflows/` joins because `LoadInventory` reads it as commands. `trail.DirtyUnder` runs `Dirty`'s three reads (the work tree, the index, untracked files) over those paths only, so a tracked or untracked change under them still marks the key. The record body's `install source` line keeps the whole tree's mark: it describes the tree, not the key.
- **`--project` on `evals`.** Refused with the flags `evals` does not read (exit 2), and the report leaves its eval rows out under `--project`. A trial records no project: its sessions' root is the sandbox clone. The eval-metrics plan's interface named the flag; it cannot filter anything.
- **Which labels.** Only `eval=` labels are suite rows. A kit's labelled session is not one.
- **The flags.** Per row and version, over the whole store, from graded trials whose harness is not `unknown`. `unstable`: one key both passed and failed. `broken`: no trial passed. `saturated`: none failed and at least two keys have three or more trials, since a key with one trial says nothing. `history`: no graded trial under a known key, which includes a row whose only known-key trials are errors. At most one flag.
- **The means.** Per main session (a `prompt2` trial has two), counted as `show` counts them, with a step replayed from another session of the row counted once. `tokens cache` is cache read plus cache write. `peak ctx` is the mean of each session's peak. Token means print in whole tokens and seconds to one decimal.
- **Where the code went.** `report.go` was at 402 lines, so its template moved to `report_template.go` and the eval rows section is in `report_evals.go`. `RenderReport` and `WriteReport` take the view, nil for none. `/evals` in `fyr serve` shows the view's text in a `<pre>`, as every verb's text is shown there. Both it and `/metrics/report` use the default 30-day window.

— by: implementer, within P7's boxes.

D17 — 2026-10-31 — P8, how the fix reads item 5's Phase 5a where the eval-metrics plan is silent (ADR-085):
- **An `expect` entry with a space.** The plan says each entry passes the identifier rule, and its Phase 5b example is `fyr record`, which has a space. An entry is words of that rule joined by single spaces, so a normalised verb's program and subcommand fit and prose, a flag name (`--home`) or a doubled space do not. An empty or repeated entry is refused, and the list joined by `,` is at most 120 bytes, the trial's bound, so a parsed row is never refused at the store.
- **The defaults are stored.** Each trial stores version 1, kind `capability` and cluster the row's id when the cell is empty, and `expect` NULL. A trial stored before P8 has NULL in all four, and P9's view reads NULL as those defaults. Storing NULL for an empty cell would let a later change of a default reinterpret history.
- **A long table's row has eleven cells.** A seven-cell row under the long header is refused, so a missing cell is never read as an empty one. A header with some of the four columns is not a suite header.
- **`version`'s shape.** Plain digits from 1 to 999999999, no sign and no leading zero (`strconv.Atoi` alone takes `+1` and `01`).
- **`--list`** prints `v<version>` and the kind after the grants column.
- **The identifier rule is copied** into `internal/eval/suite_columns.go`, since `internal/eval` links no SQLite driver and cannot import `internal/metrics`; the comment names the source.
- **The view reads an unknown version as 1** (P8's review, finding 3). P7's `fyr metrics evals` already keyed its lines on `COALESCE(row_version, 0)`, so once P8 writes the default 1 every row would split into a `-` line and a `1` line. A NULL version and a session no trial claims now read as 1, and the `v` column prints `1` where it printed `-` (`TestEvalRowsVersionDefault`, each of its two triggers mutated). P9 keeps the per-kind rates and the `expect` match.
- **An id is at most 64 bytes** (P8's security review, finding 2): it is the default cluster, so it falls under the same bound as an explicit one. No row in this repository's suite comes near it.
- **`parseRow` returns `r` after `optionalCells` has run** (P8's security review, finding 1): `return r, optionalCells(&r, …)` leaves the order of the read and the call to the compiler. gc reads `r` after the call, so no test can fail on it; the fix is the two-statement form.

— by: implementer, within P8's box.

D18 — 2026-10-31 — The operator's auto-mode ask, given during P8, is researched in `PLAN-2026-10-31-1632-research-fyr-auto-mode-idle-time-self-improvemen` and planned in `PLAN-2026-10-31-1636-plan-fyr-auto-mode-idle-time-self-improvement-of` (approach B). That plan waits for P13: its Phase 0 starts after v5.5.0, and rule 1 still holds until then. The new plan's record stays open meanwhile, and no Kickoff of this file runs it. P13's closing commit points `TODO.md` Now at its Phase 0. The `PRD.md` wording is approved now and applied in that Phase 0 (its D2), so no requirement changes inside this plan's release. — by: the operator, 2026-10-31 ("go all approved", "with your recommendations", "Approach B as you are recommending").

D19 — 2026-10-31 — P9, how the fix reads the eval-metrics plan's Phase 5b where it is silent:
- **`m of n`.** n is the entries asked, m the entries found, pooled over a line's trials: three trials of a two-entry list give `5 of 6`. Each trial's own stored list is read, since `expect` can change with no version bump. A trial counts when it is graded, stored a list and has a main session; an ERROR or a trial with no session says nothing about what it used, so it is left out rather than read as `0 of n`.
- **Which sessions.** A trial's main sessions together, as every other column of the view reads them. A subagent's tools are not the trial's. A row expecting a dispatch names `Agent`.
- **The match.** An entry equals a tool name or a verb, or leads a verb word by word, since a stored verb keeps its flags (`fyr record` in `fyr record add --title`; never a word cut short). The vocabulary is the store's: `Read`, not `view_file`.
- **The record too.** The plan's interface says `m of n` is reported in the view and the record, so the testing record's row table gains `expect`, read with the same `ExpectMatches`.
- **`kind` of a line** is the newest counted trial's, NULL read as `capability` (D17). The view gains a `kind` column, or the suite lines could not be checked against the table.
- **Where an `indicator` row is left out.** Every number of the first suite line and the trajectory similarity, so the line stays one population, and the record's suite interval. The record's title and the summary count every graded trial: they describe a run, not the suite's rate. A `regression` or `adversarial` row stays in the suite's rate and has its own line too, as the plan's table says. A kind with no graded line prints no line.
- **`cluster`** stays stored with no reader: its one reader, the cluster-bootstrap line, was removed by the eval-metrics plan's D26, so the kind-dependent view gains nothing from it. `SPEC-fyr-eval` says so.
- **No `expect` cell is filled.** The box names none, so every row's history gains nothing to read until a row's author writes one. Rows run before P9 stored NULL.
- **Item 55's edges** (round 2's review). A short flag's glued quoted value keeps the flag (`-m"fix"` is `-m`). A flag quoted before the subcommand now reads as a positional (`git "-C" /x status` is `git`, and `fyr "--home" d record get X` gives `recordRead` nothing): it fails towards keeping less, and nobody quotes a flag, so it stays. The unquoted `--title --x` is the stated residue: it needs a flag table per program.
- **A stored `fyr` verb is not rewritten** (item 54): a session ingested before P9 keeps bare `fyr`, so an `expect` read over it finds no `fyr record`. Only new trials can store a list, and they ingest under the new list.

— by: implementer, within P9's box.

D20 — 2026-10-31 — P10a, how the fix reads item 33's search half where it is silent (ADR-086):
- **"The docs-root fallbacks" is every docs root in `internal/search`, not only `docsRootOf`'s default.** D4 retires every reader but the import path, and P10b's Expect leaves only `trail.DocsRoot` (for `internal/metrics`) naming `.agents/docs`. So the project index reads no docs root, and file records go with it: `parseRecords`, the `records` table, `recordAt`, `recordLine` and the file half of `--open`. `--refs`, `--open` and every kind but `code` and `doc` answer from the brain store. `search.KindOf` keeps its `docsRoot` argument for metrics.
- **The global index holds `~/.config/fyr` only.** With `.agents/docs` gone, a registered checkout has no file to give it, since `trail.jsonl` is never a file hit. The `projects` row stays as the list `record import --global` and the dream read. The `docs_root` column stays until P10b.
- **The schema moves to 2.** Each index rebuilds on first open, and `projects` empties until each registered checkout's next `fyr search` or `fyr index`. Until then `record import --global` does not reach it, which P10b's item 34 must allow for by running a search in each project first; the dream's targets do not depend on the row.
- **What the rule reads** (P10a's reviews). The marker is read on the checkout that ran. The row and the trail it needs are the main root's, whichever linked worktree ran, since the dream keys its target and watermark on the row, and a worktree of a bare repository is its own root. `HasTrail` needs a real `.agents` directory as well as a regular file, mirroring `guardExportPath`.
- **A drop removes the row only.** While the marker holds, the checkout's records still answer `--global`, so one search can print a hit and `note: dropped`. Consent is the marker's, so this stays and `SPEC-fyr-search` says so.
- **`index --register` with no trail** still writes the marker, and prints a note that nothing reads it yet.
- **`harness-audit`'s baseline** read `fyr search --open` for the index header, which no longer prints; `references/areas.md` now names a words search. That puts `harness-audit` in P13's list.
- **Reviews past the cap.** The two rounds of `reviewer` and `security` ran. Round 2's findings are fixed without a third round, as in P2 and P3: a `.gitignore` naming `search.db` alone refused by round 1's fix, a tracked `search.db-journal`, and the marker read on the main root, which let a worktree of an unmarked bare repository join. Each has a test that fails without it.

— by: implementer, within P10a's box.

D21 — 2026-10-31 — P10b, how the fix reads item 33's other half where it is silent (ADR-087):
- **The global index holds no file.** `config_view`'s legacy files were its last source, so `configView`, the `~config` kinds and the `--global` file query go, and the index keeps its `projects` table. `--kind feedback`, `retro` and `proposal` answer from records. This amends ADR-086's "the global index holds `~/.config/fyr` only".
- **`trail.Dates`, `memory.Store` and `harness.LastRetro` go with their last reader.** `TestStoreRunsNoProjectHook` is named by `PRD.md` R3, so by D6 it stays, its body moved to `internal/brain` on the import's read of the old store, the store's one reader. The tests that exercised `trail.changed` keep their cover in `TestDirty` (a new row: a tracked directory replaced by a link out) and `TestPartialCloneIsReadOnlyOnANewGit`.
- **The schema moves to 3** for `docs_root`, so `projects` empties a second time.
- **P10b's Expect has two more names.** `internal/search/global_trail_test.go` and `internal/dream/records_only_test.go` name `.agents/docs` as negative pins: each proves the path is NOT read, so they stay. `proposals/dream/drafts/` stays as the dream's write path, and nothing reads it.
- **Item 34's proof ran on a copy.** The stores and `brain.db` were copied under `.agents/scratch/p10b/proof`, so the real home took no write. Item 34 asks for each entry "matched to a global record"; on dave no global record exists (item 60), so the proof is the import into that copy: 33 of 33, 17 of 17 and 6 of 6 by ID, no problem listed. Item 34's `search` in each project is not needed: the projects row reaches only a project's old memory store, none of the three stores.

— by: implementer, within P10b's box.

D22 — 2026-10-31 — P11, item 28. Provenance: the zip's `frontend-ui-modern` is not the published copy (`~/.claude/skills/synced/…/frontend-ui-modern`, `diff -r`: 164 files against 95, `SKILL.md`, `README.md`, `references/workflow.md`, `scripts/mockup.mjs` and `assets/simulation-template.html` differ, and the screenshot sets differ), so D3 Open 1's branch (a) applies: both are the operator's skills, not vendored, and both descriptions are shortened, each body otherwise byte-identical to its zip. The operator's answers, in this session:
- **Open 5's orphans.** The cold read took 264 rules from `frontend`, 54 covered and 198 left without a full home after an adversarial pass (`.agents/scratch/p11/orphans.txt`). All are dropped, against the recommendation to keep the TanStack Start and accessibility groups in `typescript`: the app wiring (SPA mode, `vite.config.ts`, loaders, `src/lib/api.ts`, the Go catch-all, the dev proxy, `createServerFn`), the forms rule, `accessibility.md`, `theme-system.md`, and the rest ("Drop them", "Drop both", "Drop them"). Git keeps them. The context line still names TanStack Start, and `docker`, `typescript/references/tsconfig.md` and `review-standards` keep what they already said about `web/`.
- **Open 3 is reversed.** The screenshot folders stay in the skills and the embed ("Keep screenshots instead"), so every pointer in both bodies stays true and no sentence is added. The binary is 44.6 MB here, and an install writes 1,226 files.
- **The descriptions** are as drafted to the operator: 252 and 247 bytes.

How the fix reads the rest: `testing.md`'s ThemeToggle example stays, as it was verified, with one line saying `frontend-ui-modern`'s dark switch is the `.dark` class; its axe line loses the dead pointer and keeps the gate. `TestInstallLeavesClaudeCodeAlone`'s body looks for `settings.json`, not `settings`, since `frontend-ui-modern` ships `*-settings-*.png`; run both ways. `skills/VENDORED.md:88` stays: it is history. — by: the operator, 2026-10-31, for the three answers; implementer, within P11's boxes, for the rest.

## Risks

- **The release range grows with every skill P10 and P11 touch.** P13 owes one Antigravity run per changed skill and agent: at least `init-project`, `review`, `harness-audit`, `frontend-ui-economist` and `frontend-ui-modern`.
- **P11 needs the zips in `.agents/scratch/ui-skills/`, which is gitignored.** P12's prune keeps them. Losing them before P11 blocks item 28.
- **P10 breaks any project still on files.** It must import first. Whether the Mac's projects have migrated is unknown, and the refusal names `fyr record import`.
- **`fyr check` took about 5 minutes until P2** (5m26s of CPU); 1.5 s since (D8).
- **The installed `fyr` on dave refuses `metrics.db` schema 4 until P1's install.** Until then, use a tree binary (`go build -o .agents/scratch/p1/fyr ./cmd/fyr`) for `fyr metrics` and `fyr check`, always with `--home .agents/scratch/workers`. `fyr record` works with either binary.

## Kickoff

Every Checkpoint's handoff ends with this block, unchanged (rule 3):

```text
Project: /home/kesokaj/workspace/styrman-harness. Read AGENTS.md, then fyr record list --kind handoff --home .agents/scratch/workers and fyr record get <the newest> --home .agents/scratch/workers, then PLAN.md; do the first phase of PLAN.md that has an unticked box, and only that phase.
Standing approvals (closed set): that phase's boxes marked Who: AI and the tests they name; PLAN.md's own edits (ticks, D-rows, items found during the run); the records, ADRs and docs the phase names; the checkpoint's reviews, change record, commit and push; the real-home install on dave from the clean, pushed tree (PLAN.md item 22); the closing handoff; at P13 only, /preflight v5.5.0. Never: a Who: user box or a real-home deletion without asking me in that session; a deploy; anything outside this repository except the dave real-home install and the sandbox homes AGENTS.md names. Ask only outside the set or on a critical failure.
Done when: the phase's Checkpoint expect in PLAN.md. Then write the handoff with this same Kickoff and end the session.
```
