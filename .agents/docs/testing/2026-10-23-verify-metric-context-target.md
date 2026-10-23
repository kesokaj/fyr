---
owner: main
date: 2026-10-23
---
# Verification — Phase 2 of session-sized work: the peak-context metric   (2026-10-23)

Range reviewed: d7aa3b0..working tree (committed as Phase 2's checkpoint; plan `2026-10-23-session-sized-work.md` Phase 2, D4, D6, D12, D15, D16)

## Status: PASS

Every row below ran and passed against its own criterion. The Go checks ran bare, by the main agent, as the plan's earlier sessions did (Phase 6 dispatches the `verifier` and the `reviewer` over the whole feature); the diff was read by the main agent (`git diff --numstat`: 12 source and doc files, 287 insertions, 21 deletions, before the trail files). Go: /usr/bin/go go1.24.4 (the user-level go1.27.2 SDK named in `AGENTS.md` is a dangling link at `~/.local/bin/go` today).

## Tests first (plan box 1)

- `TestShowContextTarget` (show_test.go), `TestReportContextTargetRow` (report_test.go), the context lines of `TestNumbersOnly`, the `--context-target` lines of `TestMetricsVerb`, four rows of `TestMetricsUsageErrorsExit2` and one of `TestUsageErrorsExit2` were written before the code: `go vet ./internal/metrics` → `numbers_test.go:31: sum.B.SessionsOverContextTarget undefined` (red for the task's reason), then green after the code.
- One expectation was corrected during the red-to-green pass, not the code: the Antigravity fixture's `t0` comment said 2026-10-08 06:40Z, its epoch value is 2026-10-07 10:00Z (`date -u -d @1791367200`); the comment in `agy_test.go` now says so.

## Go checks (guide § 1 and § 3g), bare

- `gofmt -l cmd internal` → empty; `go vet ./...` → exit 0.
- § 3g tier: `go test -race -count=1 -timeout 30m ./internal/metrics ./internal/trail ./internal/dream ./internal/search ./cmd/sh-agent` → exit 0 (`.agents/scratch/metrics-phase2/go-test-3g.txt`).
- Full suite: `go test -race -count=1 -timeout 30m ./...` → exit 0, every package ok (`.agents/scratch/metrics-phase2/go-test-all.txt`).
- The canary tests of § 3g (`TestClaudeIngestStoresNoContent`, `TestAgyIngestShape`, `TestVerbDropsContent`) ran green inside the tier; the ingesters and `Verb` are untouched by this change, so their must-fail control was not re-run.

## Real data from copies (guide § 3g, plan box 3)

Copy: `~/.claude/projects` (598 MB, 56 main transcripts) and `~/.gemini/antigravity-cli` (17 MB, one conversation) into `.agents/scratch/metrics-home/`; binary built from this tree (`go build -o .agents/scratch/metrics-phase2/sh-agent ./cmd/sh-agent`).

| step | command | result |
|---|---|---|
| ingest, first | `env -u CLAUDE_CONFIG_DIR sh-agent metrics ingest --home <copy>` | `316 sessions, 44145 steps, 855 events added; 0 lines skipped; 0 sources failed` |
| ingest, second | the same | `0 sessions, 0 steps, 0 events added` |
| show | `metrics show --json --since 2026-09-01 --home <copy>` | `context_target` 300000; `sessions_over_context_target` claude **33**, agy null; `session_peaks.claude` 54 entries, largest first (967274, 966899, 965941); `peak_context.claude` 967274; note `agy: no main session in the window carries a context value; sessions over context target is n/a` |
| independent count | `python3 -I .agents/scratch/metrics-phase2/independent_count.py <copy>/.claude/projects 2026-09-01 300000` — reads the transcripts, not the store: per session the max over assistant messages of input + cache read + cache creation, each message id once under the first session by start, sessions in the window by first timestamp | `main sessions in window: 56; with a peak: 54; over 300000: 33 -> 33 of 54`; the same top three ids and peaks |
| text table | `metrics show --since 2026-09-01` | row `over context target 300000   33 of 54   n/a` under `peak context` |
| refusals | `show --context-target 0`; `ingest --context-target 5` | `--context-target takes a token count above 0`, exit 2; `--context-target belongs to show or report`, exit 2 |
| report | `metrics report --since 2026-09-01 --home <copy> --out …/report.html` | summary row `<td>sessions over context target (300000)</td><td>33 of 54</td><td>n/a</td>` |
| browser | headless Chromium (`/usr/bin/chromium --headless=new`) at 1100 px light, 1100 px with `--force-dark-mode`, and 400 px | the row is drawn with `33 of 54` and `n/a` in all three; screenshots `.agents/scratch/metrics-phase2/report-{light,dark,400}.png`. The dark switch was validated first on a two-colour test page: white body without it, black with it (must-pass and must-fail of the check). |

- Baseline for the E2E criterion (plan D13): the research brief counted 31 of 49 main Claude Code sessions past 300K since 2026-09-01 on a copy made at 12:26 today; this copy, made at 17:12, holds 56 main sessions, 54 with a peak, 33 over. The sessions this plan's handoffs name are among the 54; their peaks are read at Phase 6 by `Session ID:`.
- Antigravity on this box: the one conversation has no step timestamps and no `gen_metadata` rows (its session row has no start and an ended_at of 0), so it is in no window and carries no context value. The number path for Antigravity is pinned by `TestShowContextTarget` (the fixture's C1 at 42000: 0 of 1 at the default target, 1 of 1 at 40000).
- Observed, not changed (outside this phase): with no Antigravity session in the window the per-day charts still draw an Antigravity line at 0 and the summary table shows `0` for its main sessions and peak context, as before this change.
- The copy was deleted after the run (`rm -r .agents/scratch/metrics-home`, 0 tracked files); the export made for the Antigravity check was deleted with it.

## Not run on purpose

- No behavioural runs (§ 4): the change touches no `context/`, `agents/` or `skills/` file.
- No real-home install or ingest (R6; the operator's).
- No `verifier`, `reviewer` or `security` dispatch: Phase 6 covers the whole feature (D22: metrics arithmetic alone does not make `security` due).
