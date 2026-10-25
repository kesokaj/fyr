---
owner: main
date: 2026-10-25
---
# Plan — harness audit fix (audit of 2026-10-25, fyr at c4885ae)   (2026-10-25)

Status: Phase 0 answered (D1–D9, 2026-10-25); cold-reader check next, before any code (`feature` skill). `ROADMAP.md` Now names this plan from Phase 0's answer. Its scope confirmation is the operator's answer to the numbered table in `reviews/2026-10-25-harness-audit.md`.

## Why
The operator, in their own turn on 2026-10-25: "When you are done finish with a e2e and full audit of the entire project". The audit found no high, 27 med, 53 low, 17 simplifications and 11 gaps. The evidence is `plans/2026-10-25-recon-harness-audit.md`, and the report is `reviews/2026-10-25-harness-audit.md`.

## Scope
- **In (recommended `fix`):**
  - AG16, AG17, AG18, AG19;
  - C23, C24, C25, C27, C29, C30, C31, C34;
  - WS23, WS24, WS25, WS26, WS27, WS28, WS29;
  - TS30, TS32, TS33;
  - IN15, IN19, IN23, IN24, IN25;
  - SD13, SD14;
  - TR9;
  - G16, G17;
  - the remaining lows as their area's phase reaches them.
- **In, chosen:** TR10 (row 11, D1); SD12 (row 8, D2: opt-in marker, behind a `REQUIREMENTS.md` approval); TS31 (row 9, D3); TR8 (row 12, D4); TR11 (row 16, D5: the retro before Phase 1).
- **Out (`defer`):** C26 (Phase 6 of plan `2026-10-24-fyr-agy-only.md`), G15 and G18–G25, TS35–TS37 (dated versions, re-checked at the next frontend change), SD-S1, IN-S3, AG23 and AG24.
- **Rejected:** none yet. A `strike` the operator chooses becomes a `Rejected:` row here, with its reason.

## Surfaces touched
- `agents/{bugs,changelog,documentation}/agent.md`;
- `skills/{feature,bug,review,preflight,handoff,retro,distill,explain-project,harness-audit,gcloud-setup}`;
- `skills/VENDORED.md`;
- `internal/harness/{inventory,apply}.go` and tests;
- `internal/dream` and its tests, `README.md` and `context/AGENTS.md`'s dream mention (SD12, only after D2's requirements approval);
- `REQUIREMENTS.md` R3 (SD12, only the exact text the operator approves, with its ADR);
- `.agents/docs/bugs/{INDEX.md,BUG-177-*.md}` (TR8: graduated once the check lands);
- `internal/search/testdata/recall.tsv`;
- `context/AGENTS.md` (only for row 11, inside both size pins);
- `.agents/docs/deploy/{local,remote}.md`, `.agents/docs/specs/fyr/{delivery,search}.md`, `.agents/docs/testing/fyr-matrix.md`;
- `.agents/docs/architecture/DECISIONS.md` (status lines).

## Interfaces
- **Lint.** Skill, command and rule frontmatter is checked against an allow list per kind (IN23). Command and rule names pass `validName` (IN25).
- **`fyr diff`** writes nothing anywhere: it renders the plan in memory and compares (IN24, D8).
- **Dream opt-in (SD12, D2).** `dream --all` sends only a project that carries an opt-in marker; the marker's name and form are settled in the `REQUIREMENTS.md` proposal (the audit's example: `.agents/dream-ok`). It is a requirements question: the exact edit is proposed, approved in the operator's own turn, an ADR written, then the code — never before.
- **No other verb or flag changes.**

## Failure modes
- **An existing skill trips the new allow list** (IN23). The install refuses it and names the file and key. The phase that adds the allow list runs the lint against every skill in the tree first and lists any key it would refuse.
- **A context edit (row 11) pushes past a size pin.** The pin test fails, and the edit is shortened, never the pin raised.

## Verification
- **Tiers:** unit (`go test -race`, gofmt, vet, govulncheck by full path), the scratch-home install, and one Antigravity row per changed agent (kit `testing/agy-rel/run.sh`) and per changed workflow skill whose row exists.
- **Docs:** `/review --docs` over the docs phase.
- **E2E:** `/harness-audit --reaudit` at the end, with every ID `fixed`, `partly` (with what is left), `struck (D<n>)` or `deferred`.

## Phases
### Phase 0 — The operator's answers
- [x] The operator answers the numbered table of `reviews/2026-10-25-harness-audit.md` (rows 8, 9, 12 and 16 are theirs; row 11 is answered by D1; the rest are recommendations to approve or amend) — who: user — expect: a D-row per answer
  - Answered 2026-10-25, every row: 8 "Opt-in marker" (D2), 9 "Router's rule" (D3), 11 "Amend the context" (D1), 12 "Skill step check" (D4), 16 "Next session, before Phase 1" (D5), 3 "Number at each site" (D6, amending the table), 5 "Allow list per kind" (D7), 7 "Diff in memory" (D8), and 1, 2, 4, 6, 10, 13, 14, 15, 17, 18, 19 "Approve all as written" (D9).
- [ ] Cold-reader check of this plan — who: AI (`reviewer`) — expect: findings answered by D-rows
### Phase 0b — Retro first (TR11, D5)
- [ ] `/retro` and `/dream` at the start of the session after the cold read, before Phase 1 — who: operator runs them, AI follows the skills — expect: a record in `retros/`; any proposal that changes a later phase becomes a D-row here before that phase starts
### Phase 1 — Agents (AG16–AG19)
- [ ] Tests first where a test can see it (`TestCheckoutAgentsAreNative`-style: each record agent carries the data and write-first clauses; `changelog`'s tools hold no `replace_file_content`) — who: AI — expect: red, then green
- [ ] The three agent bodies, and the dispatch sentence's tool name in `review`, `retro` and `distill` (AG17) — who: AI — expect: the three record agents' Antigravity rows PASS
- [ ] Checkpoint — expect: full suite exit 0, rows logged
### Phase 2 — The binary (IN23, IN24, IN25, IN15, TR9)
- [ ] Tests first: the frontmatter allow list (every skill in the tree accepted; `Allowed-Tools`, `permissionMode` and `mcpServers` refused); a command or rule file name holding a backtick or newline refused; `diff` on a home with no state dir creates none; `list -h` exits 0; `uninstall` and `--remove-timer` remove an old-named unit; `status` names a leftover `sh-agent` on PATH (F4) — who: AI — expect: red, then green
- [ ] The code, and `security` over the lint change — who: AI — expect: findings answered in `## Outcome`
- [ ] Checkpoint — expect: full suite exit 0, verifier PASS
### Phase 2b — Dream opt-in (SD12, D2)
- [ ] The exact `REQUIREMENTS.md` R3 edit, proposed in chat — who: AI proposes, user approves in their own turn — expect: approval quoted as a D-row, or the phase struck
- [ ] On approval: the ADR, then tests first (an unmarked indexed project is skipped by `dream --all` and named in its log; a marked one is sent; `--project` on an unmarked project says why it refuses or proceeds, per the ADR), then the code, README and the context's dream mention inside both size pins, with `REQUIREMENTS.md` and `ROADMAP.md` in one commit — who: AI — expect: red, then green; `security` over the change
### Phase 3 — Workflow skills (WS23–WS29, TR8 and the WS lows)
- [ ] The edits, with WS25's number at each of the six dispatch sites (D6) and TR8's trailer check in the `git log` step of `handoff` and `preflight` (D4) — who: AI — expect: the changed skills' Antigravity rows PASS (`review`, `preflight`, `handoff`, `feature` dry path where one exists)
- [ ] Checkpoint — expect: listing budget and size pins pass
### Phase 4 — Technical skills (TS30, TS31, TS32, TS33)
- [ ] `VENDORED.md` override notes for `find-skills` and `gke-basics`; the fingerprint check in `gcloud-setup`; `gcloud-setup` teaches `--project` per command, `config set` only for an interactive one-person setup (TS31, D3) — who: AI — expect: `TestVendored*` pass, no vendored file edited in place
### Phase 5 — Docs and the trail (C23–C34, IN19, SD13, SD14, TR8, TR10 per the answers)
- [ ] The docs sweep, the ADR status lines, the recall re-measure, and the matrix re-run under `fyr` or marked historical — who: AI — expect: `/review --docs` over the range with `## Outcome`
- [ ] Checkpoint
### Phase 6 — Re-audit
- [ ] `/harness-audit --reaudit plans/2026-10-25-recon-harness-audit.md` — who: AI — expect: no med open without a D-row

## Risks
- Phase 6 of plan `2026-10-24-fyr-agy-only.md` (rename, `v3.0.0`, crossing) may land before or during this plan. C26 closes there, and every path this plan writes uses the `fyr` names already.
- go1.27.2 is missing on this box, so govulncheck and the recall re-measure run on go1.24.4 until the operator restores the SDK.

## Decisions
D1 — 2026-10-25 — Table row 11 (TR10), the date rule: "Amend the context (Recommended)". The `context/AGENTS.md:52` line becomes: dates and time stamps come from the shell (`date +%F`, `date +%F-%H%M`), never memory; where a CLI refuses a joined command, run it alone and type its output. It lands in Phase 5, inside both size pins — by: the operator (2026-10-25, asked in the session that wrote this plan).
D2 — 2026-10-25 — Table row 8 (SD12), dream scope: "Opt-in marker (Recommended)". `dream --all` sends only projects carrying an opt-in marker. R3 says the timer "reads every indexed project's trail and memory", so this is a `REQUIREMENTS.md` edit: Phase 2b proposes the exact text, and nothing of SD12 is coded before the operator approves it in their own turn and the ADR is written. Consequence: an existing project stops dreaming until it is marked; Phase 2b's README text says how to mark one — by: the operator (2026-10-25).
D3 — 2026-10-25 — Table row 9 (TS31), gcloud state: "Router's rule (Recommended)". `gcloud-setup` teaches `--project` per command and keeps `gcloud config set` only for an interactive, one-person setup, saying so; the `google-catalog` router is unchanged — by: the operator (2026-10-25).
D4 — 2026-10-25 — Table row 12 (TR8), the trailer rule: "Skill step check (Recommended)". The `git log` step `handoff` and `preflight` already run refuses a commit whose message holds `Claude-Session:` or `Co-Authored-By:` before the push, then BUG-177 graduates. It is a written step, not a git hook (R3: no hooks, no commit gates). Measured before the choice: 0 of the last 400 commits here carry either line — by: the operator (2026-10-25).
D5 — 2026-10-25 — Table row 16 (TR11), the retro: "Next session, before Phase 1 (Recommended)". `/retro` and `/dream` run first in the session after the cold read (Phase 0b), because their proposals may change what Phases 1–5 touch — by: the operator (2026-10-25).
D6 — 2026-10-25 — Table row 3 (WS25), budgets: "Number at each site (Recommended)", amending the audit's "context default". Each of the six dispatch sites states its own number (30 for an agent, 15 for a verifier re-run); the context gains no bytes, because its hand-written part is 14,623 bytes, past the 14,500 goal (C29) — by: the operator (2026-10-25).
D7 — 2026-10-25 — Table row 5 (IN23), permission keys: "Allow list per kind (Recommended)". Skill, command and rule frontmatter outside a per-kind allow list is refused, as agents already are; the phase lints every skill in the tree first and lists any key it would refuse; `security` reviews it — by: the operator (2026-10-25).
D8 — 2026-10-25 — Table row 7 (IN24), `fyr diff`: "Diff in memory (Recommended)". `diff` renders and compares in memory and writes nothing, under the home or the system temp — by: the operator (2026-10-25).
D9 — 2026-10-25 — Table rows 1, 2, 4, 6, 10, 13, 14, 15, 17, 18 and 19: "Approve all as written (Recommended)". Fix 1, 2, 4, 6, 10, 13, 14 (the header edit DECISIONS.md line 3 allows), 15, 18 (G16 and G17 only) and 19; defer 17 (C26, to Phase 6 of plan `2026-10-24-fyr-agy-only.md`) and G15, G18–G25. Row 15's re-measure waits for go1.27.2 on this box, which the operator restores — by: the operator (2026-10-25).
