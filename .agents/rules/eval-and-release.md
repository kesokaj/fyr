---
trigger: model_decision
description: "Apply when running tests, fyr eval, editing GUIDE-eval.md, checking real-home installs, or cutting a release with /fyr-preflight."
---

# Eval, testing, and release rules

- **Testing:** `go test` only (three root-package pins in `TDD.md` § Testing strategy). Follow `GUIDE-testing` § 1 for every change, § 3a–3l by package, § 2 and § 4 for `context/`, `agents/`, and `skills/` (run `fyr eval <row>...` per changed agent or rule; report `n of N`), and § 5 after any real-home install.
- **`GUIDE-eval.md` edits:** `TestSuiteHarness` pins every row by id, agent and grant, and `TestSuiteChecks` tests every check against good and bad outcomes (`internal/eval/suite_outcomes_test.go`). Update the matching `want` entry and outcomes in the same commit after `go test ./internal/eval` (`BUG-183`, `BUG-191`, `ADR-500`).
- **Docs review & binary check:** Changes to `context/`, `agents/`, or `skills/` require `/fyr-review --docs` over the range (search `skills/` directly because `.agents/skills` is a symlink). After changes under `cmd/` or `internal/`, an install is complete only when `fyr status` against the target home prints a `binary` line with no warning (`TDD.md` § T6). On a machine whose `DEPLOY-local` § Operator steps grants standing permission, session checkpoints install into the real home from a clean, pushed tree (`fyr-feature` workers defer to closing handoff).
- **Releases:** Annotated `vX.Y.Z` tags on `main` are cut only via `/fyr-preflight vX.Y.Z` (Steps 1–5 gate) from the operator's turn in this checkout; unsigned (`ADR-053`), verified before pushing (`DEPLOY-remote` § Releases), no version file (`preflight` Step 6.2), with Step 6's bridge-release wait (`BUG-161`). Blocking condition: if "What was not checked" names an unrun Antigravity check of a changed skill/agent (or misses `TEST-2026-09-03-verify-real-home`), status is NO-SHIP until verified.
