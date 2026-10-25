---
owner: main
date: 2026-10-25
---
# Plan — harness audit fix (audit of 2026-10-25, fyr at c4885ae)   (2026-10-25)

Status: written, NOT started. `ROADMAP.md` Now does not name this plan until the operator says to start. Its scope confirmation is the operator's answer to the numbered table in `reviews/2026-10-25-harness-audit.md`, and its cold-reader check comes before any code (`feature` skill).

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
- **In, once the operator chooses** (table rows 8, 9, 11, 12, 16): SD12, TS31, TR10, TR8, TR11.
- **Out (`defer`):** C26 (Phase 6 of plan `2026-10-24-fyr-agy-only.md`), G15 and G18–G25, TS35–TS37 (dated versions, re-checked at the next frontend change), SD-S1, IN-S3, AG23 and AG24.
- **Rejected:** none yet. A `strike` the operator chooses becomes a `Rejected:` row here, with its reason.

## Surfaces touched
- `agents/{bugs,changelog,documentation}/agent.md`;
- `skills/{feature,bug,review,preflight,handoff,retro,distill,explain-project,harness-audit,gcloud-setup}`;
- `skills/VENDORED.md`;
- `internal/harness/{inventory,apply}.go` and tests;
- `internal/search/testdata/recall.tsv`;
- `context/AGENTS.md` (only for row 11, inside both size pins);
- `.agents/docs/deploy/{local,remote}.md`, `.agents/docs/specs/fyr/{delivery,search}.md`, `.agents/docs/testing/fyr-matrix.md`;
- `.agents/docs/architecture/DECISIONS.md` (status lines).

## Interfaces
- **Lint.** Skill, command and rule frontmatter is checked against an allow list per kind (IN23). Command and rule names pass `validName` (IN25).
- **`fyr diff`** writes nothing under the home (IN24).
- **No other verb or flag changes.** SD12 option (a) would add an opt-in marker for the dream. That is a requirements question: a proposal and an ADR before code.

## Failure modes
- **An existing skill trips the new allow list** (IN23). The install refuses it and names the file and key. The phase that adds the allow list runs the lint against every skill in the tree first and lists any key it would refuse.
- **A context edit (row 11) pushes past a size pin.** The pin test fails, and the edit is shortened, never the pin raised.

## Verification
- **Tiers:** unit (`go test -race`, gofmt, vet, govulncheck by full path), the scratch-home install, and one Antigravity row per changed agent (kit `testing/agy-rel/run.sh`) and per changed workflow skill whose row exists.
- **Docs:** `/review --docs` over the docs phase.
- **E2E:** `/harness-audit --reaudit` at the end, with every ID `fixed`, `partly` (with what is left), `struck (D<n>)` or `deferred`.

## Phases
### Phase 0 — The operator's answers
- [ ] The operator answers the numbered table of `reviews/2026-10-25-harness-audit.md` (rows 8, 9, 11, 12 and 16 are theirs; the rest are recommendations to approve or amend) — who: user — expect: a D-row per answer
- [ ] Cold-reader check of this plan — who: AI (`reviewer`) — expect: findings answered by D-rows
### Phase 1 — Agents (AG16–AG19)
- [ ] Tests first where a test can see it (`TestCheckoutAgentsAreNative`-style: each record agent carries the data and write-first clauses; `changelog`'s tools hold no `replace_file_content`) — who: AI — expect: red, then green
- [ ] The three agent bodies, and the dispatch sentence's tool name in `review`, `retro` and `distill` (AG17) — who: AI — expect: the three record agents' Antigravity rows PASS
- [ ] Checkpoint — expect: full suite exit 0, rows logged
### Phase 2 — The binary (IN23, IN24, IN25, IN15, TR9)
- [ ] Tests first: the frontmatter allow list (every skill in the tree accepted; `Allowed-Tools`, `permissionMode` and `mcpServers` refused); a command or rule file name holding a backtick or newline refused; `diff` on a home with no state dir creates none; `list -h` exits 0; `uninstall` and `--remove-timer` remove an old-named unit; `status` names a leftover `sh-agent` on PATH (F4) — who: AI — expect: red, then green
- [ ] The code, and `security` over the lint change — who: AI — expect: findings answered in `## Outcome`
- [ ] Checkpoint — expect: full suite exit 0, verifier PASS
### Phase 3 — Workflow skills (WS23–WS29 and the WS lows)
- [ ] The edits — who: AI — expect: the changed skills' Antigravity rows PASS (`review`, `preflight`, `handoff`, `feature` dry path where one exists)
- [ ] Checkpoint — expect: listing budget and size pins pass
### Phase 4 — Technical skills (TS30, TS32, TS33; TS31 per the answer)
- [ ] `VENDORED.md` override notes for `find-skills` and `gke-basics`; the fingerprint check in `gcloud-setup` — who: AI — expect: `TestVendored*` pass, no vendored file edited in place
### Phase 5 — Docs and the trail (C23–C34, IN19, SD13, SD14, TR8, TR10 per the answers)
- [ ] The docs sweep, the ADR status lines, the recall re-measure, and the matrix re-run under `fyr` or marked historical — who: AI — expect: `/review --docs` over the range with `## Outcome`
- [ ] Checkpoint
### Phase 6 — Re-audit
- [ ] `/harness-audit --reaudit plans/2026-10-25-recon-harness-audit.md` — who: AI — expect: no med open without a D-row

## Risks
- Phase 6 of plan `2026-10-24-fyr-agy-only.md` (rename, `v3.0.0`, crossing) may land before or during this plan. C26 closes there, and every path this plan writes uses the `fyr` names already.
- go1.27.2 is missing on this box, so govulncheck and the recall re-measure run on go1.24.4 until the operator restores the SDK.

## Decisions
None yet; Phase 0 writes them.
