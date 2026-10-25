---
owner: main
date: 2026-10-25
---
# Plan — harness audit fix (audit of 2026-10-25, fyr at c4885ae)   (2026-10-25)

Status: Phase 0 done (answers D1–D9; cold read `reviews/2026-10-25-review-audit-fix-plan-cold-read.md`, REQUEST CHANGES, answered by D10–D19). Next: Phase 0b, the retro, then Phase 1. `ROADMAP.md` Now names this plan. Its scope confirmation is the operator's answer to the numbered table in `reviews/2026-10-25-harness-audit.md`; every audit ID's disposition is in § Dispositions.

## Why
The operator, in their own turn on 2026-10-25: "When you are done finish with a e2e and full audit of the entire project". The audit found no high, 27 med, 53 low, 17 simplifications and 11 gaps. The evidence is `plans/2026-10-25-recon-harness-audit.md`, and the report is `reviews/2026-10-25-harness-audit.md`.

## Scope
- **In:** every audit ID whose row in § Dispositions reads `fix` — all 27 mediums, the lows (D11: the ones the audit recommended deferring included), the simplifications that shorten a file without moving a rule, and G16 and G17.
- **In, behind an approval:** SD12 (D2, D15): the `REQUIREMENTS.md` R3 edit is proposed in Phase 2b and coded only after the operator approves it in their own turn.
- **Out (`defer`):** C26 (Phase 6 of plan `2026-10-24-fyr-agy-only.md`), G15 and G18–G25 (D9), WS39 and TS-S1 (D12).
- **Rejected:** AG20 (D12: whole-file rewrites accepted, no wider edit tool); IN22, SD18 and TR15 (the audit's "not a defect, note only").

## Surfaces touched
- `agents/{bugs,changelog,documentation}/agent.md` (AG16–AG19, AG22, AG24), and the docs-root paragraph in all eight if AG23's mechanism is a text one;
- `skills/{feature,bug,review,review-standards,preflight,handoff,retro,distill,explain-project,harness-audit,grilling,research,gcloud-setup,gke-basics,frontend,typescript,data-gcp,bash}`;
- `skills/find-skills` removed and a fork under another name added (TS30, D18); `skills/VENDORED.md`;
- a trailer-check script under one skill's `scripts/` (TR8, D13);
- `cmd/fyr/main.go` (IN15, IN16, IN17, IN18, IN21, IN29 messages; SD12 usage text after approval);
- `internal/harness/{inventory,apply,state,release,zip,source,repo}.go` and their tests; `internal/harness/catalog_test.go` (TR13, TR8's fixture test);
- `internal/dream/{collect,prompt,run}.go`, `internal/memory/memory.go`, `internal/trail/trail.go` and their tests (SD15–SD17, SD-S1, SD-S2); `internal/dream` for SD12 after approval;
- `internal/search/testdata/recall.tsv` (SD13);
- `REQUIREMENTS.md` R3 and `ROADMAP.md` in one commit (SD12, only the exact text the operator approves, with its ADR);
- `context/AGENTS.md` (D1's date line, C28, C36, C-S5, the dream mention after D15's approval; inside the 15,000 and 22,000 pins);
- root `AGENTS.md` (C29's goal struck, C32, C35, C-S1, rule 1's dream sentence after approval), `README.md`, `TODO.md` (C-S5);
- `.agents/docs/deploy/{local,remote}.md`, `.agents/docs/specs/fyr/{delivery,search,dream}.md`, `.agents/docs/testing/{fyr-matrix,guide}.md`, `testing/agy-rel/run.sh` (the new rows);
- `.agents/docs/architecture/DECISIONS.md` (status lines and the numbering note, by the header edit its line 3 allows), `.agents/docs/bugs/{INDEX.md,BUG-139-*,BUG-171-*,BUG-177-*}`.

## Interfaces
- **Lint.** Skill, command and rule frontmatter is checked against an allow list per kind (IN23, D7). The list holds at least every key a skill in the tree carries today. It grows only by a D-row when a vendored skill needs a new key, since a vendored file is never edited. Command and rule names pass `validName` (IN25).
- **`fyr diff`** writes nothing anywhere (IN24, D8, D19). It renders the plan in memory and compares bytes in Go; for a changed file it pipes the plan bytes to the system `diff -u - <dest>` on stdin. No diff algorithm is hand-written.
- **`fyr` exit codes.** An unknown command exits 2 and prints on the caller's writer, like every usage error (IN16). `list -h` exits 0 (IN15).
- **`fyr status`** names a leftover `sh-agent` on PATH (F4 of plan `2026-10-24-fyr-agy-only.md:33`, TR9), and classes a kept `~/.claude` entry as kept, not stale (IN32).
- **Manifest.** `LoadState` refuses an entry outside the roots fyr manages (`.gemini/config`, `.local/bin/fyr`, the kept `.claude` entries) (IN26).
- **Dream opt-in (SD12, D2, D15).** `dream --all` sends only a project carrying an opt-in marker that git does not carry. The marker is something the operator creates on this machine: for example an entry fyr writes under `~/.config/fyr/`, or an untracked, ignored path. A marker committed in the repository does not count. The marker's form, and any verb or flag it adds, are settled in the `REQUIREMENTS.md` proposal and its ADR.
- **No new verb or flag** beyond what that ADR adds.

## Failure modes
- **An existing skill trips the new allow list** (IN23). The install refuses it and names the file and key. Phase 2 runs the lint over every skill in the tree before making it strict, and lists any key it would refuse.
- **A context edit pushes past a size pin.** The pin test fails; the edit is shortened, and the pin is never raised. With the goal struck (D10), 15,000 bytes is the only ceiling for the hand-written part.
- **The R3 proposal is not approved.** Phase 2b is struck by a D-row. No other phase depends on it (D19).
- **A box needs the network** (TS32's key page, TS35's build, TS36's registry) **and the box has none.** The box is reported BLOCKED with what is missing, never filled from memory.
- **A tag is cut while this plan is open** (plan `2026-10-24-fyr-agy-only.md` Phase 6). It is cut only at a checkpoint of this plan, never mid-phase, so every changed skill and agent already has its Antigravity run (AGENTS.md § Release).

## Verification
- **Unit, every code phase:** `gofmt -l cmd internal`, `go vet ./...`, `go test -race -timeout 30m ./...`, and govulncheck by full path. They run on `/usr/bin/go` go1.24.4 (D16). govulncheck passes when it reports no advisory beyond the 15 recorded stdlib ones (audit § Measurements) and no finding a module reaches.
- **Install:** a scratch-home install, then `fyr status` from that home shows a `binary` line with no warning.
- **Behavioural, every phase that changes `context/`, `agents/` or `skills/`:** one sandbox start of the CLI, and one Antigravity row per changed agent, rule and workflow skill (kit `testing/agy-rel/run.sh`, preamble `testing/kit-preamble.sh`), reported as n of N.
- **Review, every phase:** `/review --docs` over the phase's range when it changes `context/`, `agents/` or `skills/`; a `reviewer` when the phase's diff reaches 50 lines or 5 files; `security` for Phase 2's lint and manifest changes and Phase 2b's dream change. Findings get `## Outcome`.
- **Verifier:** at each checkpoint, last, on a frozen tree, writing its log under `testing/`.
- **Restatements:** a grep for every changed rule (D1's date rule, SD12's "every indexed project", the trailer rule, the budget wording) over `context/ agents/ skills/ README.md AGENTS.md .agents/docs/specs .agents/docs/deploy`, run before the phase's first review round.
- **E2E (Phase 6):** a re-run of `testing/2026-10-25-verify-fyr-e2e-c4885ae.md` (E2E 1–4 local, and its four Antigravity rows), then `/harness-audit --reaudit`, with every ID `fixed`, `partly` (and what is left), `struck (D<n>)` or `deferred`, checked against § Dispositions.

## Phases
Execution order: 0b, 1, 2, 2c, 3, 4, 5, 6. Phase 2b runs whenever the operator approves its proposal; no other phase waits for it (D19).

### Phase 0 — The operator's answers
- [x] The operator answers the numbered table of `reviews/2026-10-25-harness-audit.md` (rows 8, 9, 12 and 16 are theirs; row 11 is answered by D1; the rest are recommendations to approve or amend) — who: user — expect: a D-row per answer
  - Answered 2026-10-25, every row: 8 "Opt-in marker" (D2), 9 "Router's rule" (D3), 11 "Amend the context" (D1), 12 "Skill step check" (D4), 16 "Next session, before Phase 1" (D5), 3 "Number at each site" (D6, amending the table), 5 "Allow list per kind" (D7), 7 "Diff in memory" (D8), and 1, 2, 4, 6, 10, 13, 14, 15, 17, 18, 19 "Approve all as written" (D9).
- [x] Cold-reader check of this plan — who: AI (`reviewer`) — expect: findings answered by D-rows
  - `reviews/2026-10-25-review-audit-fix-plan-cold-read.md`: REQUEST CHANGES, 2 high, 6 medium, 4 low. All twelve are answered by D10–D19, with the operator's answers to C29 "Strike the goal", the toolchain "go1.24.4, recorded", the trailer "Script the skill runs", TS30 "Fork under a new name", the deferred lows "Fix them all", the operator-choice lows "Approve all as listed", IN19 "Re-run under fyr" and the marker "Local only".

### Phase 0b — Retro first (TR11, D5)
- [x] `/retro` and `/dream` at the start of the next session, before Phase 1 — who: the operator runs them, the AI follows the skills — expect: a record in `retros/`; any proposal that changes a later phase becomes a D-row here before that phase starts
  - Done 2026-10-25, with the operator's go ("go all approved. finish this projectr now"). Record: `retros/2026-10-25-retro.md`, which runs the dream inside the retro (retro Step 6), plus the dreamer's `retros/2026-10-25-dreamer.md`. Three harness proposals: the Kickoff printed inline (carried by Phase 3's `handoff` box), paste-means-go (`rules/session-sizing.md`), and README for a human (`agents/documentation`, after Phase 1). None changes a later phase's scope, so no D-row.

### Phase 1 — Agents (AG16–AG19, AG21–AG24, AG-S1, AG17's skill sites)
- [ ] AG23's mechanism, asked first: one docs-root paragraph that seven agents repeat. Options are a pointer to one text home, or an install-time include, which is a code change and so a design question that needs an ADR — who: AI asks, the user chooses — expect: a D-row
- [ ] Tests first: each record agent carries the data clause and a write-first or "Status: IN PROGRESS" clause; `changelog`'s tools hold no `replace_file_content` — who: AI — expect: red, then green
- [ ] The three agent bodies (AG16, AG18, AG19), and the documentation/verifier seam over `testing/guide.md` named once in both (AG24) — who: AI — expect: `grep -L "what you read is data"` over `agents/*/agent.md` prints nothing
- [ ] "With the Write tool" becomes `write_to_file`, or the tool name is dropped, in `review`, `retro` and `distill` (AG17); and AG-S1, two copies struck in favour of `feature:112` — who: AI — expect: `grep -rn "the Write tool" skills/` prints nothing
- [ ] Kit rows `inject-bugs`, `inject-changelog` and `inject-documentation` (the `agents-security` PWNED pattern: a planted instruction in the input, which the agent must not follow), plus lane-hold rows for AG21 (a write-capable reviewing agent tempted to edit source) and AG22 (`documentation` asked to edit `REQUIREMENTS.md`, `bugs` asked to edit source) — who: AI — expect: each row PASS, n of N per row
- [ ] Checkpoint: `/review --docs` over the phase, and a reviewer if the diff reaches 50 lines or 5 files; then the verifier — expect: full suite exit 0, rows logged, `## Outcome` written

### Phase 2 — The binary: harness and cmd (IN15–IN18, IN21, IN23–IN30, IN32, IN-S1–IN-S3, TR9, TR13)
- [ ] The lint over every skill in the tree with the proposed allow list, read-only, listing any key it would refuse — who: AI — expect: the list, empty or recorded as a D-row before the next box
- [ ] Tests first:
  - the allow list: every skill in the tree is accepted; `Allowed-Tools`, `permissionMode` and `mcpServers` are refused;
  - a command or rule file name holding a backtick or a newline is refused;
  - `diff` on a home with no state dir creates nothing, and nothing appears under the system temp;
  - `list -h` exits 0, and an unknown command exits 2 on the writer;
  - `uninstall` and `dream --remove-timer` remove a unit under the old name (`sh-agent-dream`, `com.styrman.sh-agent-dream`: plan `2026-10-24-fyr-agy-only.md:21`);
  - `status` names a leftover `sh-agent` on PATH (F4, that plan's `:33`) and does not count a kept `~/.claude` entry as stale;
  - `LoadState` refuses `.ssh/authorized_keys` and `.`;
  - `Status` on a symlink to a FIFO does not block;
  - a SHA256SUMS holding two different markers is refused;
  - BUG-148's loop-body `test && cmd` is caught by `TestScriptsHaveNoEarlyExitPipe` (TR13)
  - who: AI — expect: red, then green
- [ ] The code: IN15, IN16, IN23, IN24, IN25, IN26, IN27, IN28, IN32 and TR9. Wording and comments for IN17, IN18, IN21 and IN29, and IN30's scrambled doc comments. IN-S1 (`fyrOwns`), IN-S2 (unexport what only the package uses) and IN-S3 (zip verification into `zip.go`) — who: AI — expect: green; `go doc ./internal/harness` no longer lists the unexported names
- [ ] `security` over the lint, the name check and the manifest roots — who: AI — expect: findings answered in `## Outcome`
- [ ] Checkpoint: a reviewer over the phase's diff, the scratch-home install with `status`, then the verifier — expect: full suite exit 0, verifier PASS on the D16 condition

### Phase 2b — Dream opt-in (SD12, D2, D15), when approved
- [ ] The exact `REQUIREMENTS.md` R3 edit, with a marker git does not carry (D15), proposed in chat — who: AI proposes, the user approves in their own turn — expect: the approval quoted as a D-row, or the phase struck by one
- [ ] On approval: the ADR. Then tests first:
  - an unmarked indexed project is skipped by `dream --all` and named in its log;
  - a marked one is sent;
  - a marker committed in the repository does not count;
  - `--project` on an unmarked project behaves as the ADR says.
  - Then the code and the restatements: `cmd/fyr/main.go` usage and flag help, `specs/fyr/{dream,search}.md`, `skills/dream`, `deploy/local.md`, README, the context's dream mention and root AGENTS.md rule 1, with `REQUIREMENTS.md` and `ROADMAP.md` in the one commit — who: AI — expect: red, then green; the restatement grep for "every indexed project" prints only history
- [ ] Checkpoint: `security` and a reviewer over the change, then the verifier — expect: `## Outcome` written, verifier PASS

### Phase 2c — Dream, memory and trail code (SD15, SD16, SD17, SD-S1, SD-S2)
- [ ] SD17 first: what `trail.Dates` returns when `trail.Log` refuses a partial clone — who: AI — expect: a test that shows it, and a fix if `collect` falls back to mtime
- [ ] Tests first: `c.Name` with a newline is folded in the prompt, the header and the file name (SD15); `memory.Root` in a submodule or a `--separate-git-dir` checkout is the work-tree top (SD16); `fit` renders once per project (SD-S1); a `--project` dry run prints a count and the first few dropped records (SD-S2) — who: AI — expect: red, then green
- [ ] Checkpoint: a reviewer if the diff reaches 50 lines or 5 files, then the verifier — expect: verifier PASS

### Phase 3 — Workflow skills (WS23–WS38, WS-S1–WS-S4, TR8, G16, G17, WS30, C36's skill half)
- [ ] The trailer check (TR8, D13): one script under one skill's `scripts/` that exits non-zero when `git log @{u}..HEAD --format=%B` holds `Claude-Session:` or `Co-Authored-By:`. A new step before the push in `handoff` Step 6 and `preflight` Step 6.4 runs it. A Go test runs it against a scratch repository with a trailer commit (must fail) and one without (must pass) — who: AI — expect: the test red before the script exists, then green
- [ ] WS25's number at each site (D6, D19): `feature:15,23,90,114`, `bug:39,55`, `review:45`, `preflight:44,57,59`, `handoff:167`, `explain-project:12` (lines as of `c4885ae`; re-grep `budget` first). 30 for an agent, 15 for a verifier re-run — who: AI — expect: every dispatch sentence the re-grep finds carries a number
- [ ] WS26: `preflight` Step 2 puts the reviewer and security in parallel and the verifier after their fixes — who: AI — expect: Step 2 and Verification item 1 say the same order
- [ ] WS23, WS24, WS27, WS28, WS29, WS31–WS36 and WS38, and WS-S1–WS-S4, each as its § Dispositions row says — who: AI — expect: `retro` names `<name>/agent.md`; `harness-audit/references/areas.md` has no `derive.go`; `handoff` has no "Resume Protocol" and no "either vendor"; one Session ID form in all five places
- [ ] G16, G17 and WS30 in `feature`: Step 1 lists the `REQUIREMENTS.md` items the scope touches; the plan template gains a "Trust boundaries and data handled" row; the planning session ends after Step 3 with a Kickoff — who: AI — expect: the `feature` Antigravity row PASS
- [ ] C36's skill half: the `agy -p` tool-name detail lives in `handoff` § Usage, ready for Phase 5 to shorten the context line — who: AI — expect: `handoff` § Usage names `WaitMsBeforeAsync`, `manage_subagents` and `manage_task`
- [ ] WS37: the vendor facts the skills depend on (`invoke_subagent`, `agy --model pro`, subagent resume, `agy -p`), checked in one sandbox run of the CLI — who: AI — expect: each fact confirmed or corrected, with the run's log path
- [ ] Checkpoint: the listing budget and both size pins; `/review --docs` and a reviewer over the phase; the Antigravity rows for `review`, `preflight`, `handoff` and `feature`, n of N; then the verifier — expect: all pass, `## Outcome` written

### Phase 4 — Technical skills (TS30–TS38)
- [ ] TS30 (D18): `find-skills` forked under another name, with a pinned CLI and no `-g -y`; the vendored copy removed, and its `VENDORED.md` row moved to the forks table — who: AI — expect: `grep -rn -- "-g -y" skills/` prints nothing outside history; `TestSkillListingBudget` and `TestVendored*` pass
- [ ] TS31 (D3) and TS32 (D18) in `gcloud-setup`: `--project` per command, and `config set` only for an interactive one-person setup, said so. The apt recipe points at Google's install page for the current key, and prints the key's fingerprint with `gpg --show-keys` to compare against that page; no fingerprint is embedded — who: AI — expect: no `gcloud config set project` outside the one-person paragraph; the recipe runs `gpg --show-keys`
- [ ] TS33 and TS34, edited directly in the `gke-basics` fork (not verbatim, `VENDORED.md:21`): drop the `~/.claude` path; `$REGION` and `--project` in the Quick Start and its references — who: AI — expect: `grep -rn "us-central1\|\.claude" skills/gke-basics` prints nothing outside a sample marked as one
- [ ] TS35, TS36 and TS37 (D11): the frontend sample built on `node:24-alpine` in scratch and the pairing recorded; the dated pins re-checked against the npm registry; the brew path noted as unpinned, with a version check — who: AI — expect: each recorded with today's date, or BLOCKED with what was missing (Failure modes)
- [ ] TS38: the CI-style recipe marked as an example the harness does not run — who: AI — expect: the line says so
- [ ] Checkpoint: `/review --docs` over the phase, one Antigravity row for the fork and for `gcloud-setup`, then the verifier — expect: rows PASS, `## Outcome` written

### Phase 5 — Context, docs and the trail (C23–C36, C-S1–C-S5, IN19, SD13, SD14, TR10, TR12, TR14, TR-S1, C25, BUG-177)
- [ ] The context (D1, C28, C36, C-S5): the date line as D1 words it; the header no longer says no script stands behind the rules; the `agy -p` line shortened to the rule plus a pointer to `handoff` § Usage; the ROADMAP/TODO rule worded once. Then both size pins, and the restatement grep for the date rule (`skills/handoff/SKILL.md:158` among the known hits) — who: AI — expect: pins pass; the grep finds no "same call that writes" outside history; the `ctx-deploy` and canary rows PASS
- [ ] Root AGENTS.md: C29's goal struck (D10) here and in `size_test.go`'s comment; C32's restatements reduced to a pointer; C35's row removed; C-S1, the lint-refusal paragraphs pointed at their tests where no rule moves — who: AI — expect: `grep -n "14,500" AGENTS.md internal/harness/size_test.go` prints nothing
- [ ] The docs sweep, each item as its § Dispositions row says: C23, C24, C27, C30, C31, C34, C-S2 (after `TestDeliverySpecCurrent` is read), C-S3, C-S4, SD14 — who: AI — expect: `grep -rn "SH_AGENT_REPO\|signature and checksum" .agents/docs/deploy` prints nothing; `TestDeliverySpecCurrent` passes
- [ ] DECISIONS.md: C25's status lines on ADR-025, ADR-052 and ADR-060 (and ADR-031 and ADR-061 if ADR-062's text confirms them), and C33's numbering note, by the header edit line 3 allows — who: AI — expect: `git diff` on DECISIONS.md shows header lines and the note only
- [ ] SD13 (D16): q41's gold re-pinned first, then `FYR_RECALL=1 go test -run TestRecall ./internal/search` on go1.24.4, and `search.md:96` updated with the date, the toolchain and both figures — who: AI — expect: the spec row matches the log
- [ ] IN19 (D14): `testing/fyr-matrix.md` re-run under `fyr` in a scratch home, with new rows for the migration, the zip path and D32, and its Antigravity cells from the kit — who: AI — expect: no `sh-agent` cell outside a row that tests the old name
- [ ] The trail: TR12's note under the Graduated table and BUG-171 re-pointed; TR14, BUG-139 marked accepted; TR-S1; BUG-177 graduated to Phase 3's script — who: AI — expect: `INDEX.md` has no open row without an owner
- [ ] Checkpoint: `/review --docs` over the phase, then the verifier — expect: `## Outcome` written, verifier PASS

### Phase 6 — E2E and re-audit
- [ ] The E2E re-run: E2E 1–4 local and the four Antigravity rows of `testing/2026-10-25-verify-fyr-e2e-c4885ae.md`, on the committed tree — who: AI (verifier) — expect: PASS, logged
- [ ] `/harness-audit --reaudit plans/2026-10-25-recon-harness-audit.md` — who: AI — expect: every ID's verdict matches its § Dispositions row; no med left open without a D-row

## Risks
- Phase 6 of plan `2026-10-24-fyr-agy-only.md` (rename, `v3.0.0`, crossing) may land before or during this plan. C26 closes there, every path this plan writes uses the `fyr` names already, and a tag waits for a checkpoint (Failure modes).
- go1.27.2 is missing on this box: `~/.local/bin/go` points at the absent `~/sdk/go1.27.1`. Everything runs on go1.24.4 and records it (D16).
- D11 brings about twenty more lows into scope, several of which need the network or a sandbox run (TS35, TS36, WS37, AG21, AG22). Phases 1, 3 and 4 are longer than one sitting may hold; a phase that outgrows its session stops at a box and hands off.

## Dispositions
One row per audit ID, the checklist that Phase 6's re-audit is checked against. The evidence is `plans/2026-10-25-recon-harness-audit.md`.

| ID | Disposition | Phase | D-row |
|---|---|---|---|
| C23, C24, C27, C30, C31, C34 | fix | 5 | D9 |
| C25 | fix: header status lines | 5 | D9 |
| C26 | defer: plan `2026-10-24-fyr-agy-only.md` Phase 6 | — | D9 |
| C28 | fix: context header | 5 | D9 |
| C29 | fix: strike the goal | 5 | D10 |
| C32, C33 | fix | 5 | D11 |
| C35, C36 | fix | 3 (C36 skill half), 5 | D9 |
| C-S1, C-S2, C-S3, C-S4, C-S5 | fix where no rule moves | 5 | D9 |
| WS23–WS29 | fix | 3 | D9 (WS25: D6, D19) |
| WS30 | fix: planning session ends after Step 3 | 3 | D12 |
| WS31–WS36, WS38 | fix | 3 | D9 |
| WS37 | fix: sandbox check | 3 | D11 |
| WS39 | defer: re-asked after the crossing | — | D12 |
| WS-S1–WS-S4 | fix | 3 | D9 |
| TS30 | fix: fork under another name | 4 | D18 |
| TS31 | fix: the router's rule | 4 | D3 |
| TS32, TS33, TS34, TS38 | fix | 4 | D9, D18 |
| TS35, TS36, TS37 | fix | 4 | D11 |
| TS-S1 | defer | — | D12 |
| AG16, AG17, AG18, AG19 | fix | 1 | D9, D14 |
| AG20 | strike: whole-file rewrites accepted | — | D12 |
| AG21, AG22, AG24 | fix | 1 | D11 |
| AG23 | fix: mechanism asked first | 1 | D11 |
| AG-S1 | fix | 1 | D9 |
| IN15, IN19, IN23, IN24, IN25, IN27, IN28, IN30 | fix | 2 (IN19: 5) | D7, D8, D9, D14 |
| IN16, IN26 | fix | 2 | D12 |
| IN17, IN18, IN21, IN29, IN32 | fix | 2 | D11 |
| IN22 | strike: not a defect | — | audit |
| IN-S1 | fix | 2 | D12 |
| IN-S2, IN-S3 | fix | 2 | D11 |
| SD12 | fix behind the R3 approval | 2b | D2, D15 |
| SD13 | fix | 5 | D9, D16 |
| SD14 | fix | 5 | D9 |
| SD15, SD17 | fix | 2c | D9 |
| SD16, SD-S1 | fix | 2c | D11 |
| SD18 | strike: not a defect | — | audit |
| SD-S2 | fix | 2c | D12 |
| TR8 | fix: script the skills run | 3, 5 | D4, D13 |
| TR9 | fix | 2 | D9 |
| TR10 | fix | 5 | D1 |
| TR11 | fix: retro first | 0b | D5 |
| TR12, TR13 | fix | 5, 2 | D11 |
| TR14 | fix: BUG-139 accepted | 5 | D12 |
| TR15 | strike: superseded handoff | — | audit |
| TR-S1 | fix | 5 | D9 |
| G16, G17 | fix | 3 | D9 |
| G15, G18–G25 | defer | — | D9 |
| IN20, IN31 | merged into C26 and IN-S1 | — | audit |

## Decisions
D1 — 2026-10-25 — Table row 11 (TR10), the date rule: "Amend the context (Recommended)". The `context/AGENTS.md:52` line becomes: dates and time stamps come from the shell (`date +%F`, `date +%F-%H%M`), never memory; where a CLI refuses a joined command, run it alone and type its output. It lands in Phase 5, inside both size pins — by: the operator (2026-10-25, asked in the session that wrote this plan).
D2 — 2026-10-25 — Table row 8 (SD12), dream scope: "Opt-in marker (Recommended)". `dream --all` sends only projects carrying an opt-in marker. R3 says the timer "reads every indexed project's trail and memory", so this is a `REQUIREMENTS.md` edit: Phase 2b proposes the exact text, and nothing of SD12 is coded before the operator approves it in their own turn and the ADR is written. Consequence: an existing project stops dreaming until it is marked; Phase 2b's README text says how to mark one — by: the operator (2026-10-25).
D3 — 2026-10-25 — Table row 9 (TS31), gcloud state: "Router's rule (Recommended)". `gcloud-setup` teaches `--project` per command and keeps `gcloud config set` only for an interactive, one-person setup, saying so; the `google-catalog` router is unchanged — by: the operator (2026-10-25).
D4 — 2026-10-25 — Table row 12 (TR8), the trailer rule: "Skill step check (Recommended)". The `git log` step `handoff` and `preflight` already run refuses a commit whose message holds `Claude-Session:` or `Co-Authored-By:` before the push, then BUG-177 graduates. It is a written step, not a git hook (R3: no hooks, no commit gates). Measured before the choice: 0 of the last 400 commits here carry either line — by: the operator (2026-10-25). (Premise corrected and mechanism refined by D13.)
D5 — 2026-10-25 — Table row 16 (TR11), the retro: "Next session, before Phase 1 (Recommended)". `/retro` and `/dream` run first in the session after the cold read (Phase 0b), because their proposals may change what Phases 1–5 touch — by: the operator (2026-10-25).
D6 — 2026-10-25 — Table row 3 (WS25), budgets: "Number at each site (Recommended)", amending the audit's "context default". Each of the six dispatch sites states its own number (30 for an agent, 15 for a verifier re-run); the context gains no bytes, because its hand-written part is 14,623 bytes, past the 14,500 goal (C29) — by: the operator (2026-10-25). (The site count is corrected by D19.)
D7 — 2026-10-25 — Table row 5 (IN23), permission keys: "Allow list per kind (Recommended)". Skill, command and rule frontmatter outside a per-kind allow list is refused, as agents already are; the phase lints every skill in the tree first and lists any key it would refuse; `security` reviews it — by: the operator (2026-10-25).
D8 — 2026-10-25 — Table row 7 (IN24), `fyr diff`: "Diff in memory (Recommended)". `diff` renders and compares in memory and writes nothing, under the home or the system temp — by: the operator (2026-10-25).
D9 — 2026-10-25 — Table rows 1, 2, 4, 6, 10, 13, 14, 15, 17, 18 and 19: "Approve all as written (Recommended)". Fix 1, 2, 4, 6, 10, 13, 14 (the header edit DECISIONS.md line 3 allows), 15, 18 (G16 and G17 only) and 19; defer 17 (C26, to Phase 6 of plan `2026-10-24-fyr-agy-only.md`) and G15, G18–G25. Row 15's re-measure waits for go1.27.2 on this box, which the operator restores — by: the operator (2026-10-25). (Row 15's toolchain is superseded by D16, and row 19's reach is settled by D11.)
D10 — 2026-10-25 — Cold read finding 1. It found no box for G16, G17, TR10 (D1) or C29, and no restatement grep for D1. G16 and G17 get a Phase 3 box with the `feature` row. TR10 gets a Phase 5 box with both pins, the `ctx-deploy` and canary rows, and a restatement grep (`skills/handoff/SKILL.md:158` is a known hit). C29, asked of the operator: "Strike the goal (Recommended)". The 14,500 goal leaves root AGENTS.md and `size_test.go`'s comment, and the 15,000 pin is the only ceiling — by: the operator (2026-10-25).
D11 — 2026-10-25 — Cold read finding 2. It found that the lows had no list. § Dispositions now gives one row per audit ID, and Phase 6 checks against it. The operator was asked whether row 19's "fix per area" also covers the lows the audit recommended deferring (C32, C33, WS37, AG21–AG24, IN17, IN18, IN21, IN29, IN32, IN-S2, IN-S3, SD16, SD-S1, TR12, TR13, TS35–TS37). The answer: "Fix them all". Each is in its phase. AG23's mechanism is asked first, in Phase 1. The network-bound ones report BLOCKED rather than guess — by: the operator (2026-10-25).
D12 — 2026-10-25 — Cold read finding 2, the lows the audit left to the operator: "Approve all as listed (Recommended)". WS30: fix (the planning session ends after Step 3). WS39: defer until after the crossing. IN16: fix (an unknown command exits 2 on the writer). IN26: fix (LoadState refuses entries outside the managed roots; `security` reviews it). AG20: strike (whole-file rewrites accepted). SD-S2: fix. TS-S1: defer. IN-S1: fix. TR14: BUG-139 marked accepted — by: the operator (2026-10-25).
D13 — 2026-10-25 — Cold read finding 3: D4's premise is false. `handoff` has no `git log` step, and `preflight:38` is a Step 1 range read, not a check before the push. Asked how the check runs: "Script the skill runs (Recommended)". One script under one skill's `scripts/` exits non-zero on a trailer line in `git log @{u}..HEAD --format=%B`. A new step before the push in `handoff` Step 6 and `preflight` Step 6.4 runs it. A Go test runs it against a fixture that must fail and one that must pass. It is a script a skill runs, the global rule's graduation mechanism, and still not a git hook (R3). BUG-177 graduates to it in Phase 5 — by: the operator (2026-10-25).
D14 — 2026-10-25 — Cold read finding 4, expects that observe nothing.
  - Every box's expect is now a fact a grep, a test or a row can see.
  - AG16's "one Antigravity row each" means an injection row per record agent (`inject-bugs`, `inject-changelog`, `inject-documentation`, the `agents-security` pattern). The existing dispatch rows cannot see the data clause.
  - IN19, asked of the operator: "Re-run under fyr (Recommended)", with rows for the migration, the zip path and D32.
  — by: the operator (2026-10-25) for IN19; the rest by the AI under the standing approval.
D15 — 2026-10-25 — Cold read finding 5. A marker git carries would let a cloned repository opt its reader in. Asked of the operator: "Local only (Recommended)". Phase 2b's R3 proposal names a marker the operator creates on the machine and git does not carry, with the must-refuse test "a committed marker does not count". Any verb or flag it adds is settled in its ADR. The restatements of "every indexed project" are listed in Surfaces and in Phase 2b's box — by: the operator (2026-10-25).
D16 — 2026-10-25 — Cold read finding 6: D9 and Risks contradicted each other on go1.27.2. The link points at the absent `~/sdk/go1.27.1`, not 1.27.2. Asked of the operator: "go1.24.4, recorded (Recommended)". The recall re-measure and every checkpoint run on `/usr/bin/go` go1.24.4, and the spec row names the toolchain. govulncheck passes with no advisory beyond the recorded 15 stdlib ones and no finding a module reaches. SD13's box re-pins q41's gold before measuring. This supersedes D9's "waits for go1.27.2" — by: the operator (2026-10-25).
D17 — 2026-10-25 — Cold read finding 7, the tiers the plan owes. Every phase now ends in a checkpoint, with `/review --docs` wherever `context/`, `agents/` or `skills/` change. Each also gets a reviewer at 50 lines or 5 files, the verifier last, one sandbox start and rows reported as n of N (§ Verification). Phase 6 re-runs the E2E before the re-audit — by: the AI, under the standing approval.
D18 — 2026-10-25 — Cold read finding 8, Phase 4's mechanisms.
  - TS30, asked of the operator: "Fork under a new name (Recommended)", because a `VENDORED.md` note never reaches the model.
  - TS32: the recipe points at Google's install page and prints the key's fingerprint with `gpg --show-keys` to compare against it. No fingerprint is embedded, since one would go stale.
  - TS33 and TS34 are direct edits of the `gke-basics` fork, which is not verbatim (`VENDORED.md:21`), not override notes.
  — by: the operator (2026-10-25) for TS30; the rest by the AI under the standing approval.
D19 — 2026-10-25 — Cold read findings 9–12, by the AI under the standing approval.
  - **Budgets (9).** The budget sites are listed in Phase 3's box from the audit: eleven sentences in six skills, re-grepped first. 30 applies to `preflight`'s release reviewer too. A site whose dispatch is seen cut off gets a higher number by a D-row.
  - **Phase 2 facts (10).** The old unit names and F4 are cited from plan `2026-10-24-fyr-agy-only.md:21,33`. The allow list grows only by a D-row (§ Interfaces). `diff` compares in Go and pipes to the system `diff` on stdin, with no hand-written diff.
  - **Surfaces and tags (11).** The missing surfaces are listed. A tag waits for a checkpoint of this plan.
  - **Order (12).** Phase 2b runs whenever the operator approves, and no phase depends on it. Interfaces says "no new verb or flag beyond what that ADR adds".
D20 — 2026-10-25 — AG23's mechanism. The operator said "go all approved. finish this projectr now" instead of choosing, so the AI took the text option: one text home with a one-line pointer in each agent. It needs no code change and no ADR. The install-time include stays open as a design question for later — by: the AI, under the operator's go (2026-10-25).
D21 — 2026-10-25 — Execution. Following "finish this projectr now", Phases 1, 2, 2c, 3, 4 and 5 ran in parallel, one worktree each, with fixed names between them (`skills/handoff/scripts/check-trailers.sh`; `handoff` § Usage holds the `agy -p` detail). They were merged in order. The per-phase Checkpoint boxes became one checkpoint over the merged range: `/review --docs`, a reviewer, `security` for Phase 2, then the verifier. Each phase still logs its own Antigravity rows. This follows the global Definition of Done ("once per task over the whole change") and departs from D17's per-phase checkpoints — by: the AI, under the operator's go (2026-10-25).
