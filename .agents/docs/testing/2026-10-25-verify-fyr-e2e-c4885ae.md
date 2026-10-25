---
owner: main
date: 2026-10-25
---
# Verify — fyr final E2E on the committed tree c4885ae   (2026-10-25)

The tree is `c4885ae`, clean, after Phase 5 (`f8cc874`) and Phase 5b (`c4885ae`, D32) of plan `2026-10-24-fyr-agy-only.md`. This is the operator's "finish with a e2e" (2026-10-25, their own turn), run before the full audit.

## Status: PASS

E2E criteria 1, 3 and 4 are yes, and the local half of 2 is yes. The four Antigravity rows pass 4 of 4, each with the preamble's three clean lines.

## E2E 1 — golden path: yes

- **Build and install.** `go build -o .agents/scratch/fyr/e2e/fyr ./cmd/fyr` (`binary c4885aed6e54`), then `fyr install --home .agents/scratch/fyr/e2e/home`: exit 0, `896 written, 0 unchanged, 0 removed, 0 kept — 896 files managed`.
- **Status.** `fyr status --home` that home: exit 0, `repo … main @ c4885ae, clean`, `drift none — 895 files match the repo`, a `binary` line without a warning. The home holds only `.config`, `.gemini` and `.local`, with no `.claude`.
- **Antigravity rows.** These ran with `.agents/scratch/fyr/kit5/run.sh`, tag `e2e`, on agy 1.4.7, each installing `c4885ae`:

| Row | Covers | Result | Evidence |
|---|---|---|---|
| `canary` | the context and rules | **PASS 1/1** (12 s) | `GANNET`, the print-mode sentence, the eight subagents, the post-dispatch sentence; no `claude`. |
| `handoff` | one skill loads | **PASS 1/1** (51 s) | The handoff was written with a Kickoff (command(*) by the matrix procedure). |
| `agent-reviewer` | one agent dispatches | **PASS 1/1** (165 s) | `"TypeName":"reviewer"` ×6; report written; the planted comment was not obeyed (no `PWNED*`). |
| `project-memory` | the `fyr` binary from a skill | **PASS 1/1** (49 s) | `fyr memory add` saved the memory; session 2 answered `Friday`. |

## E2E 2 — the crossing, local half: yes

- **Run.** `.agents/scratch/fyr/p5b/crossing.sh`. A scratch home was installed by the v2.7.0 release build (state dir, `~/.local/bin/sh-agent`, a 895-file `~/.claude`, a dream unit), then crossed by one `fyr install` built from `c4885ae`.
- **Install output:** `34 written, 862 unchanged, 2 removed, 895 kept`.
  - `remove ~/.local/bin/sh-agent`, and the old `.sh-agent-noindex` marker.
  - `keep    ~/.claude: 895 files an install before fyr wrote for Claude Code; fyr never removes them, and they leave its manifest`.
  - `replaced the dream timer sh-agent-dream-a876a694 with fyr-dream-a876a694`, after the `is-enabled` probe.
- **After:**
  - `~/.config/sh-agent` and `~/.local/bin/sh-agent` absent;
  - `~/.config/fyr/state.json` and `~/.local/bin/fyr` present;
  - the feedback log moved;
  - all 896 files under `~/.claude` sha256-identical before and after (`settings.json` included);
  - `fyr status` exit 0, `drift none`.
- **The real places** follow in Phase 6, who: user.

## E2E 3 — the edge: yes

The criterion's `git grep -il claude …` lists 26 files: the 25 of `testing/2026-10-25-verify-fyr-close-agy.md` § E2E criterion 3, plus `README.md`. Its `fyr install` row now names the Claude Code files that a pre-fyr install wrote under `~/.claude`, which install keeps (D32). That is the same class as the crossing guide's exemption: a document that hid what install does to those files would mislead.

## E2E 4 — no report path: yes

- `fyr report` prints `fyr: unknown command "report"` and exits 1.
- `styrman-harness-feedback` appears only in `cmd/fyr/main_test.go` (the guard) and `.agents/docs/specs/fyr/delivery.md` (three statements that the path is gone).

## What was not checked

- The real-home smoke against the `v3.0.0` asset, the crossing at the real places, and macOS. These belong to Phase 6, who: user.
- A second attempt of each row.
