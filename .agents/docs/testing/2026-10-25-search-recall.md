---
owner: main
date: 2026-10-25
---
# Search recall re-measure (audit SD13, plan 2026-10-25-harness-audit-fix D16)

Command: `FYR_RECALL=1 go test -count=1 -run TestRecall -v ./internal/search/` on `/usr/bin/go` go1.24.4 linux/amd64 (D16: `~/.local/bin/go` is a dangling link on this box), on a worktree of 3797da5 with the Phase 5 edits applied. Exit 0; `TestRecallQuestionsAreWellFormed` PASS.

Fixture changes first (`internal/search/testdata/recall.tsv`):
- q41 `open plan`: the gold moved from `plans/2026-09-30-audit-fix.md`, which is closed, to the two plans `fyr search --open` lists today, `2026-10-24-fyr-agy-only.md` and `2026-10-25-harness-audit-fix.md`.
- q33 `open bug` had one gold, BUG-139. This phase marks BUG-139 `accepted` (TR14), so no bug is open and the question has no answer. It becomes `refs BUG-171`, and its gold is the six records that define or fix BUG-171. The classes are now 26 words, 10 drift, 4 refs and 1 open.

| Arm | words (26) | drift (10) | refs (4) | open (1) | all (41) |
|---|---|---|---|---|---|
| index | 0.538 | 0.170 | 1.000 | 1.000 | 0.505 |
| grep (one pass) | 0.115 | 0.172 | 1.000 | 1.000 | 0.237 |

Compared with 2026-10-01 (index 0.633, grep 0.296, on the old fixture), the all-question figure fell for both arms. On drift the index no longer beats grep: 0.170 against 0.172. The open and refs arms score the fixture, not the ranking. Their gold decays as plans close and bugs change state. So the words and drift columns are the comparison that carries over from one measurement to the next. The per-question lines were in `.agents/scratch/p5/recall.txt` (scratch, not committed).
