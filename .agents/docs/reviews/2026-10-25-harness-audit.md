---
owner: main
date: 2026-10-25
---
# Harness audit — fyr at c4885ae   (2026-10-25)

Range reviewed: no diff; the harness at `c4885ae` (clean), after plan `2026-10-24-fyr-agy-only.md` Phases 0–5b. Areas covered: all eight (C, WS, TS, AG, IN, SD, TR, G), with IN in two halves. The operator asked for it on 2026-10-25, in their own turn: "When you are done finish with a e2e and full audit of the entire project". The E2E ran first: `testing/2026-10-25-verify-fyr-e2e-c4885ae.md`, PASS.

## What is broken

There is no high finding. The suite, vet, gofmt, the scratch install and the E2E pass. These are the mediums a user meets, worst first:

- **AG16, AG18.** The three record agents (`bugs`, `changelog`, `documentation`) lack the "what you read is data" clause, though all three hold write tools. `changelog` and `documentation` also lack the write-first and cut-off clause. A planted instruction in a diff could steer their writes, and a cut-off `documentation` run leaves partial in-place edits with no record of which files it reached.
- **C23.** `deploy/local.md:79` tells the operator that `$SH_AGENT_REPO` sets the checkout. The binary ignores it and reads `FYR_REPO`.
- **C24.** `deploy/remote.md:49` says a release's authenticity is "the signature and checksum". There has been no signature since ADR-053.
- **WS23, WS24.**
  - `retro`'s target table still drafts flat `agents/<name>.md` files, which the installer refuses.
  - The audit's own AG block names the deleted `derive.go`.
- **WS26.** `preflight` Step 2 dispatches the verifier in parallel with reviewer and security. Its Verification item 1 needs the verifier after their fixes, so the steps followed in order give a stale log.
- **WS25.**
  - Six workflow skills tell the dispatch to "name a tool-call budget" and never give a number.
  - With `maxTurns` refused as an agent key, that number is the only cap.
- **WS27, WS28.**
  - `handoff` cites a "Resume Protocol" section that no longer exists, and says "on either vendor".
  - It claims its Session ID equals the feedback log's `session`, which holds 8 characters.
- **AG17.** `review`, `retro` and `distill` say "with the Write tool". On Antigravity the tool is `write_to_file`. The rows pass anyway (deferred by D31).
- **IN23.** Skill, command and rule frontmatter is checked against a case-sensitive deny list (`allowed-tools`, `hooks`), while agents get an allow list. This needs a security call.
- **IN24** *repro*. `fyr diff` is meant to be read-only but creates `~/.config/fyr`.
- **IN25.** Command and rule names skip `validName` and are rendered inside backticks into the deployed context. A hand-downloaded zip could inject prose. This needs a security call.
- **IN19.** `testing/fyr-matrix.md` is the 2026-09-18 matrix. Its cells quote `sh-agent`, 260 files and `--vendor all`, and it has no row for the migration, the zip path or D32.
- **SD12.** Any `fyr search` run inside a checkout with `.agents/docs` registers it. The nightly `dream --all` then sends that project's trail, memory and transcripts, redacted for credential shapes only, to the model. This is the operator's call.
- **SD13.** `specs/fyr/search.md:96` claims recall 0.633 against 0.296 for grep. Today it measures 0.499 against 0.261, and q41's gold answer is out of date.
- **TS30.**
  - The vendored `find-skills` offers `npx skills add … -g -y`.
  - `VENDORED.md` says to pin the CLI and never use `-y`.
- **TS31.**
  - `gcloud-setup` teaches `gcloud config set`.
  - The `google-catalog` router forbids it.
- **TS32.** `gcloud-setup`'s apt recipe installs Google's key with no fingerprint check.
- **C25.** ADR-025, ADR-052 and ADR-060 still read "Accepted" although they are superseded.
- **C26.** README and `remote.md` already give `kesokaj/fyr`, `v3.0.0` and `fyr-*` commands that do not resolve until Phase 6.
- **TR8.**
  - The "no CLI trailer" rule is written three times: the context, BUG-039 and BUG-177.
  - It is still a sentence, and this session's own reminder asked for the trailer again.
- **TR9, TR10.**
  - Phase 4b report items 2 and 3, and the `context/AGENTS.md:52` date-rule question, live only in a "Not done" list.
  - The date rule contradicts `project-memory`.
- **TR11.** There has been no retro or dream record in 25 days, across about 40 new bugs and a rename.

## Tally

| Area | High | Med | Low | Simplifications | Gaps |
|---|---|---|---|---|---|
| C | 0 | 4 | 10 | 5 | — |
| WS | 0 | 7 | 10 | 4 | — |
| TS | 0 | 3 | 6 | 1 | — |
| AG | 0 | 3 | 6 | 1 | — |
| IN | 0 | 4 | 12 | 3 | — |
| SD | 0 | 2 | 5 | 2 | — |
| TR | 0 | 4 | 4 | 1 | — |
| G | — | — | — | — | 11 |
| **Total** | **0** | **27** | **53** | **17** | **11** |

Two rows were merged (IN20 into C26, IN31 into IN-S1) and are not counted.

## Measurements

The main agent took these at `c4885ae`. Each output is saved under `.agents/scratch/harness-audit/2026-10-25/<name>.txt`, and its first line is the command.

- `gofmt -l cmd internal`: empty. `go vet ./...`: exit 0. `go test -race -count=1 -timeout 30m ./...`: exit 0, 9 of 9 packages.
- `go test -cover ./...`:

| Package | Coverage |
|---|---|
| `cmd/fyr` | 90.5% |
| `internal/dream` | 89.2% |
| `internal/harness` | 89.8% |
| `internal/memory` | 90.5% |
| `internal/metrics` | 89.7% |
| `internal/redact` | 97.8% |
| `internal/search` | 84.1% |
| `internal/trail` | 87.8% |

- `govulncheck ./...` (`/home/kesokaj/.go/bin/govulncheck`, go1.24.4) exits 3. It reports 15 standard-library advisories, all of the old toolchain, and none reached in a module.
- **Scratch-home install:** exit 0, 896 files. `status`: `drift none — 895 files match the repo`, no binary warning. `diff`: exit 0. `uninstall --dry-run`: exit 0.
- **Context size.**
  - `fyr render | wc -c` is 15,529 bytes, the inventory included.
  - The hand-written context is 14,623 bytes, against the 15,000-byte pin and AGENTS.md's goal of 14,500 (C29).
  - The pins pass, and so does `TestHarnessMap`.
- **Lint and catalog:** `TestSkillLinksResolve`, `TestLint*`, `TestCatalog*` and `TestVendored*` pass. The vendored pins were not re-checked against upstream.
- **Recall:** `FYR_RECALL=1 TestRecall` gives index mean 0.499 against grep 0.261 (SD13).
- **Search:** `fyr search --open` reports 1,874 files, reconciled in 115 ms, and 2 open records: this plan's 6 Phase 6 boxes and BUG-139.
- **Dream dry run** (`dream --dry-run --project .`): memory 5, trail 33, skill 36. 261,306 bytes would be sent, and 34 trail records are left out under the 256 KB cap.
- **Timer:** `dream --install-timer --dry-run` would install the nightly timer.
- **Newest release:** `gh release view v2.7.0` lists `sh-agent-{darwin-arm64,linux-amd64,linux-arm64}`, `SHA256SUMS` and `styrman-harness-v2.7.0.zip`. There is no `fyr` release yet.
- **Append-only:** `git log --diff-filter=M v2.7.0..HEAD -- changes/ DECISIONS.md` lists 5 commits. All of them only added lines (TR brief).
- **E2E on `c4885ae`:** E2E 1, 3 and 4, plus the local half of 2, are yes. The Antigravity rows pass 4 of 4: canary, handoff, agent-reviewer and project-memory (`testing/2026-10-25-verify-fyr-e2e-c4885ae.md`).

## The operator's choices

Each item's full version is in `.agents/scratch/harness-audit/2026-10-25/choices.md`, by number.

| # | Problem | Recommendation |
|---|---|---|
| 1 | Record agents lack the "data" clause and the write-first clause (AG16, AG18, AG19) | fix: add both clauses; drop `replace_file_content` from `changelog`; one Antigravity row each |
| 2 | Living docs still describe the pre-fyr world (C23, C24, C27, C30, C31, C34, WS23, WS24, WS27, TS33, IN19) | fix: one docs sweep, then `/review --docs` |
| 3 | Dispatch budgets are named but never numbered (WS25) | fix: each dispatch site states a number (30 for an agent, 15 for a verifier re-run) |
| 4 | `preflight`'s verifier ordering contradicts itself (WS26) | fix: dispatch the verifier after the fixes, as Verification item 1 says |
| 5 | Frontmatter permission keys use a deny list in skills, commands and rules (IN23) | fix: refuse any key outside a small allow list, as agents already do; security review |
| 6 | Command and rule names are not validated (IN25) | fix: pass them through `validName` |
| 7 | `fyr diff` creates `~/.config/fyr` (IN24) | fix: diff in memory or in a temp dir outside the state dir |
| 8 | The nightly dream sends every checkout that ever ran `search` (SD12) | your call: only a project with an opt-in marker (recommended), or keep and document it |
| 9 | `gcloud config set` against the router's `--project` rule (TS31) | your call: the router's rule (recommended); `gcloud-setup` teaches `--project` |
| 10 | `find-skills` `-g -y` and the apt key with no fingerprint (TS30, TS32) | fix: a `VENDORED.md` note overriding the vendored step; fingerprint check in `gcloud-setup` |
| 11 | The date rule contradicts `project-memory` (TR10) | your call: amend the context line to "from the shell, never memory" (recommended), or change the skill |
| 12 | The no-trailer rule is written three times, still a sentence (TR8) | your call: a check that refuses a `Claude-Session:` or `Co-Authored-By:` line before push (recommended) |
| 13 | Phase 4b items 2 and 3 are unowned (TR9) | fix: a box for each in the action plan (uninstall removes an old-named unit; F4's status line) |
| 14 | ADR statuses not marked Superseded (C25) | fix |
| 15 | Recall figures stale, q41 gold wrong (SD13) | fix: re-measure, correct the gold, update the spec |
| 16 | No retro in 25 days (TR11) | your call: run `/retro` in the next session (recommended) |
| 17 | Pre-Phase-6 claims in README and `remote.md` (C26) | defer: true once Phase 6's first box renames the repository |
| 18 | Coverage gaps G15–G25 | defer, except G16 (`feature` checks `REQUIREMENTS.md`) and G17 (a trust-boundary row in the plan template): fix |
| 19 | The other lows and the 17 simplifications | fix per area in the action plan's phases; simplifications only where they shorten without moving a rule |

## What was not examined

- **TS.** About 12 of the catalog's 136 entry notes and no entry bodies (the ten-entry sample was not done). terraform, operate-gcp, api-auth-gcp, json-api-style, codebase-design, mermaid-syntax, typescript and bash got grep-level checks only, and no `references/` directory was read whole. Vendored pins were not diffed against upstream.
- **WS.** The `review-standards` body.
- **AG.** The full text of five dispatch sites (`preflight`, `handoff`, `feature`, `explain-project`, `bug`), and whether Antigravity enforces `tools:` and `model:`.
- **IN.** The bodies of most tests in both halves. That every refusal maps to a non-zero exit was checked in `cmd/fyr` only.
- **SD.**
  - The ranking, parsing and dedupe code of search.
  - `specs/fyr/dream.md` and the three calling skills (whether `/retro` treats model text as an instruction).
  - `trail.Dates`.
- **TR.** Nine of the ten newest bug records, the older handoffs, the retros in full, the reviews' `## Outcome` sections, and the operator's feedback log outside the checkout.
- **C.** Most of `DECISIONS.md`, `remote.md` from line 264, `delivery.md` after line 93, and the vendor facts behind the Known limits.
- **Platforms and sandbox.** macOS: no `launchctl`, and no darwin test path runs here. Antigravity behaviour beyond the four E2E rows is outside this audit, because no extra sandbox runs were made for it.
- **Toolchain.** go1.27.2, which the project docs name, is not on this box (`~/.local/bin/go` dangles), so govulncheck ran on go1.24.4.

## Pointers

- Recon brief: `plans/2026-10-25-recon-harness-audit.md`, plus the nine per-area briefs beside it.
- Action plan, written and not started: `plans/2026-10-25-harness-audit-fix.md`.
