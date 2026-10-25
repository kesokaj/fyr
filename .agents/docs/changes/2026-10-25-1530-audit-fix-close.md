---
owner: main
date: 2026-10-25
---
# Audit fix closed; brain store planned and approved   (2026-10-25)

## What changed
- **Phase 0b, the retro.** Record `retros/2026-10-25-retro.md` and the dreamer's `retros/2026-10-25-dreamer.md`. A new bullet in `rules/session-sizing.md`: a pasted report that points at a Kickoff means go. The documentation agent writes the README for a human first. The handoff report prints the Kickoff inline.
- **Phases 1, 2, 2c, 3 and 4** ran in parallel, one worktree each, and were merged (D21):
  - **Phase 1, agents:** the data clause, write-first, AG23's one docs-root home, six new kit rows.
  - **Phase 2, the binary:** a frontmatter allow list per kind, doc-name checks, `fyr diff` that writes nothing, exit codes, managed roots in `LoadState`, FIFO safety, the old timer name, and a `kept` status.
  - **Phase 2c, dream, memory and trail:** partial-clone dates, name folding, `memory.Root` resolving the work-tree top, `fit` rendering once, a dry-run summary.
  - **Phase 3, workflow skills:** the trailer check script `skills/handoff/scripts/check-trailers.sh`, which handoff and preflight run before a push; numbered budgets; the preflight order; the WS fixes; WS37's vendor facts; the feature gates.
  - **Phase 4, technical skills:** `find-skills` forked as `discover-skills`, `gcloud-setup` uses `--project`, `gke-basics` sheds its `~/.claude` path and `us-central1`, dated pins.
  - **Phase 5** stopped after one commit: the context, AGENTS.md, the docs sweep, DECISIONS status lines, recall re-measured, and part of the matrix. Its rest, and Phase 6, were dropped (audit-fix D22).
- **Merge fixups.** A test moved to Phase 2's unexported names. The AGENTS.md lint text was updated. The SD16 restatements were fixed.
- **Checkpoint fixes:**
  - `memory.Root` refuses a linked worktree the repository does not register.
  - `readPlain` opens with `O_NOFOLLOW|O_NONBLOCK`.
  - The stale "Resume Protocol" name is gone.
  - The statedir walk skips `.claude/`.
- **The brain store, planned.** Plan `plans/2026-10-25-sqlite-trail.md`, answered by its cold read (D5–D22). The operator approved R0, R3, R4, R7 and R8 word for word (D23), and ADR-063 was written (0aaaf0a). Phase 1 is being built.

## Agents dispatched
- general-purpose ×6, one per phase, in worktrees;
- `dreamer` for the retro;
- `reviewer` for the brain plan's cold read and for the audit-fix range;
- `security` for the audit-fix range;
- `verifier`.

## Verification
- `testing/2026-10-25-verify-audit-fix-checkpoint.md`: PASS on 0a1bd0c, on go1.24.4.
  - 704 tests passed, 0 failed, 5 skipped.
  - Size: rules block 18,141 of 22,000 bytes; hand-written context 14,463 of 15,000; skill listing 19,177 of 19,500.
  - The scratch install shows a `binary` line with no warning.
- Antigravity rows ran per phase, n of N:
  - Phase 1: 13 PASS, distill BLOCKED;
  - Phase 3: 4 of 4;
  - Phase 4: 2 of 2;
  - Phase 5: ctx-deploy and canary.

## Not done
- **WS34:** `grilling` is vendored.
- **Phase 2b:** the dream opt-in marker, moved to the operator.
- **IN19:** two matrix rows.
- The distill kit row.
- **Security lows 4 and 5:** deferred to the brain plan's Phase 4.
