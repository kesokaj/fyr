# PLAN — clean the project and release v5.5.0   (2026-10-31)

Status: open. P1 is next. This is the only plan.

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
| 40 | If borken's `fyr update` failed in its `git fetch` (on dave on 2026-10-31, ssh to github.com:22 timed out twice and `ssh.github.com:443` worked), the update's fetch error names the way out | fix if P3 confirms it, else strike by D-row | P3 |

### Items found during the run

(none yet)

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
1. A table test over every binary kind (release, development) and every place kind (checkout, binary-only, zip), for `status`, `update` and `update --from-zip`. It fails first on the development-build-at-a-zip-place cells (the bug skill: the failing test before the fix; a `bug` record, BUG-199).
2. `status` at a zip place reports from the state with no source.
3. `update` without `--from-zip` at a zip place prints how that place moves (a new zip, or a release binary for the version) and exits 2.
4. `update --from-zip` run by a development build at a place whose state records a checkout is refused, naming `fyr update` and `fyr install --repo <path>`. A development build never turns a checkout place into a zip place.
5. The error that strands a place names `fyr install --repo <checkout>` as the way back.
6. `SPEC-fyr-delivery` § The model, the README map's zip label and `GUIDE-fyr-matrix` change in the same commit (AGENTS.md § How the harness works, rules 3 and 6), with an ADR amending ADR-038.

Rejected: building the binary from the zip's Go source. That would be a new delivery path (rule 3) and would need Go on every zip place.

Now, by hand on borken, before the fix: `git -C ~/workspace/fyr pull`, then `go build -o ~/workspace/fyr/.agents/scratch/fyr ./cmd/fyr` run inside `~/workspace/fyr`, then `~/workspace/fyr/.agents/scratch/fyr install --repo ~/workspace/fyr`. That makes borken a checkout place again, and `fyr update` works from then on. If the pull fails on port 22, item 40 applies.

## Phases

### P1 — cold read and hygiene (one session)
- [ ] Cold read of this file: one `reviewer` given `PLAN.md` only. Each finding is answered by a D-row, a fix to this file, or a question to the operator. Verify: the review record. Expect: no BLOCKING item left unanswered. Who: AI.
- [ ] Items 38 and 39. `notInRuleA` gains `PLAN.md` with a test that fails without it, and AGENTS.md § Layout names `PLAN.md` while it exists. Verify: `go test ./internal/check`. Who: AI.
- [ ] Item 27, the AGENTS.md pointer. Item 30, `rm fyr` in the root (ignored, checked by `git check-ignore -v fyr` first). Item 31, `rmdir` of the empty template directory. Verify: `git status --short --untracked-files=all`, and `ls skills/init-project/assets/agents-tree`. Who: AI.
- [ ] Checkpoint (rule 3). Expect: the full test command exit 0, and `fyr check` from a tree binary gives 0 findings.

### P2 — `fyr check` speed (item 21; one session)
- [ ] Profile: `go test -cpuprofile` or a `pprof` run of `fyr check` over this checkout, the hot path named with numbers. Verify: the profile's top 10 under `.agents/scratch/`. Who: AI.
- [ ] Fix the hot path, test first. A benchmark or a timed test pins the bound. Verify: `go test -race ./internal/check`, then `time fyr check` from a tree binary. Expect: under 30 s of CPU here, with the same findings as before on a mutated tree. Who: AI.
- [ ] Checkpoint (rule 3). `SPEC-fyr-check` names the bound.

### P3 — borken's update (items 37 and 40; one session; the `bug` skill)
- [ ] BUG-199 stored. The matrix test fails first on the development-build-at-a-zip-place cells. Who: AI.
- [ ] D5's fixes 2–5 in `cmd/fyr` and `internal/harness`. Verify: the matrix test and `go test -race ./cmd/fyr ./internal/harness`. Expect: pass, and every cell matches `GUIDE-fyr-matrix`. Who: AI.
- [ ] The ADR amending ADR-038, `SPEC-fyr-delivery`, the README map's label, `GUIDE-fyr-matrix` and `fyr help update`, all in the fix's commit (rules 3 and 6). Verify: `TestHarnessMap`, `/review --code --docs`. Who: AI.
- [ ] Item 40: reproduce a fetch failure against an unreachable remote in a scratch checkout. If the message does not name the way out, fix it, test first; else strike it by D-row. Who: AI.
- [ ] Checkpoint (rule 3), with `security` (update is install behaviour).

### P4 — `fyr work` guard and container limits (items 12 and 13; one session)
- [ ] Item 12: the lead's HEAD reflog is read before the run and compared after. If it moved, the run is refused with a line naming the reflog entry, test first. BUG-198's status becomes fixed. `SPEC-fyr-work` is updated. Verify: `go test -race ./internal/work`. Who: AI.
- [ ] Item 13: the limits and the run network in `internal/sandbox/docker*.go`, with argv tests, and the network removed after the run. Verify: `go test -race ./internal/sandbox`, and one real docker worker on dave (GUIDE-testing § 3k). Expect: PASS. Who: AI.
- [ ] Checkpoint (rule 3), with `security`.

### P5 — the PRD reshape (item 10; one session)
- [ ] The ADR, then `PRD.md` as `.agents/scratch/prd-reshape/PRD.diff` (re-checked with `git apply --check` first), `SPEC-fyr-check`'s `### R0` line, and `SDD.md`'s and `TDD.md`'s traces if they name `### R`. All in one commit. Verify: `fyr check` from a tree binary. Expect: 0 findings and neither PRD note. Who: AI.
- [ ] Checkpoint (rule 3).

### P6 — eval: the trial record (items 1 and 2; one session)
- [ ] Item 1: `replaced` becomes a FAIL in `internal/eval/cause.go`; `TestClassifyEveryReason`; `SPEC-fyr-eval` step 15. Who: AI.
- [ ] Item 2: the four boxes of the eval-metrics plan's Phase 2b, as written there, in that record's § Interfaces. The E2E runs under `~/sandbox/fyr-eval/home` (AGENTS.md's eval exception). Who: AI.
- [ ] Checkpoint (rule 3). Expect: the full test command exit 0, and the E2E record IDs.

### P7 — eval: the per-row view (item 3; one session)
- [ ] The eval-metrics plan's Phase 3 boxes, with the new report code in a new file. Who: AI.
- [ ] Checkpoint (rule 3). Expect: Phase 3's E2E criterion on the eval home.

### P8 — eval: the suite grammar (item 5, Phase 5a; one session)
- [ ] The eval-metrics plan's Phase 5a boxes. Who: AI.
- [ ] Checkpoint (rule 3), with `security` (the parser).

### P9 — eval: the suite's rows (item 5, Phase 5b; one session)
- [ ] The eval-metrics plan's Phase 5b boxes, with `TestSuiteHarness`'s `want` in the same commit. Who: AI.
- [ ] Checkpoint (rule 3). Expect: `go run ./cmd/fyr eval --home .agents/scratch/evalm --list` prints version and kind for every row.

### P10 — the file-era trail goes (items 33 and 34; one session)
- [ ] The ADR (D4), then the removals of item 33 with their tests, test first where a behaviour changes (a migrated project registers for `--global`, and the dream reads only records). `SPEC-fyr-search`, `SPEC-fyr-dream` and `skills/review/scripts/restatement-grep.sh` are updated. Verify: the full test command, and `scripts/restatement-grep.sh` for `.agents/docs` across the living docs. Expect: only the import path and its tests name `.agents/docs`. Who: AI.
- [ ] Item 34: `fyr record import` is shown to hold every line of the three legacy stores on dave, then they are deleted on the operator's word, by name. Who: AI (the proof), user (the word).
- [ ] Checkpoint (rule 3), with `security` (search input paths).

### P11 — the UI skills (item 28, D3; one session)
- [ ] Provenance first: the zip's `frontend-ui-modern` is compared with the published `anthropic-skills` copy when one is on this box. If they are the same, it is vendored verbatim and listed in `skills/VENDORED.md`, and other descriptions are shortened to fit; if not, both descriptions are shortened. Who: AI.
- [ ] The UI plan's Phase 1 box, as written there, with D3's answers. One `GUIDE-eval` row per new skill, with `TestSuiteHarness` updated. Verify: `go test ./...` (the lint and `TestSkillListingBudget`). Who: AI.
- [ ] Checkpoint (rule 3), with `security` (the new scripts) and `/review --docs`.

### P12 — the docs sweep (items 25, 32 and 35; one session)
- [ ] Item 25: `TDD.md` § T5 rows thinned, each claim kept in its `SPEC-fyr-*` or `cmd/fyr/help_text.go`. Verify: `fyr check`. Who: AI.
- [ ] Item 32: `.agents/scratch/` pruned to its keep-set (`workers/`, `evalm/`, `prd-reshape/`, `ui-skills/`, `audit/`, and what a later phase names). It is listed first and deleted by name. Who: AI.
- [ ] Item 35: `~/.claude/AGENTS.md` refreshed on the operator's word. Who: AI (the command), user (the word).
- [ ] Checkpoint (rule 3). Expect: `fyr check` 0 findings and 0 notes.

### P13 — release v5.5.0 (item 29; one session, from the operator's own turn)
- [ ] The behavioural runs owed by the range `v5.0.0..HEAD` (AGENTS.md § Release): one `fyr eval` row per changed skill and agent, plus the real-home smoke of `TEST-2026-09-03-verify-real-home`, stored as `testing` records. Who: AI.
- [ ] This file stored as a closed `plan` record, removed from the root, and AGENTS.md § Layout and `TODO.md` updated, all in one commit (rule 5). Who: AI.
- [ ] `/preflight v5.5.0`. Expect: SHIP, the tag pushed, the release published. Who: AI, from the operator's turn.
- [ ] borken moves to v5.5.0 by its own `fyr update` (`DEPLOY-remote` § Operator steps). Expect: `fyr version` prints `v5.5.0`, or `b<sha>` of the release commit at a checkout place, and `fyr status` exits 0. Who: user.

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

D5 — 2026-10-31 — Item 37's design is as in § Item 37: a matrix test, `status` and `update` working at a zip place with any binary, and a development build never turning a checkout place into a zip place. The zip never replaces the binary (ADR-038 stands). Rejected: building the binary from the zip's source. — by: implementer, as recommended to the operator; the P1 cold read may reopen it.

## Risks

- **The release range grows with every skill P10 and P11 touch.** P13 owes one Antigravity run per changed skill and agent: at least `init-project`, `review`, `frontend-ui-economist` and `frontend-ui-modern`.
- **P11 needs the zips in `.agents/scratch/ui-skills/`, which is gitignored.** P12's prune keeps them. Losing them before P11 blocks item 28.
- **P10 breaks any project still on files.** It must import first. Whether the Mac's projects have migrated is unknown, and the refusal names `fyr record import`.
- **`fyr check` takes about 5 minutes until P2 lands.** Run it in the background to a file.
- **The installed `fyr` on dave refuses `metrics.db` schema 4 until P1's install.** Use a tree binary until then.
