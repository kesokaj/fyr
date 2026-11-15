---
trigger: model_decision
description: "Apply only when creating new source files, writing functions > 40 LOC, or editing/splitting files > 350 LOC (skip on small localized edits to short files)."
---

# Code modularity

1. **Line Limits:** Target 100–350 LOC per file (hard ceiling: 600 LOC; UI components: 400 LOC; functions/methods: 50 LOC, complexity 10; tests: 600 LOC). Generated (`*.pb.*`, `*_gen.*`, migrations, lockfiles) and vendored files are exempt.
2. **Pre-Edit Check:** Before adding to a > 400 LOC file, place new logic in a new cohesive file. Before editing a > 600 LOC file, extract the touched cluster first.
3. **Paradigm-Aware Splitting:** Go/Rust: keep core types in `<entity>.go`/`.rs` and split concern methods into `<entity>_<concern>.go` or child packages without widening visibility. OOP: 1 public type/file; split > 7 dependencies. Python/TS/JS: split into `<module>/` re-exporting public API only via `__init__.py`/`index.ts`. UI: separate markup, hooks/state, and data calls.
4. **Constraints:** Never create `utils.*`/`helpers.*`/`common.*`/`misc.*` files; name by domain capability. Keep a strict import DAG (zero cycles) and never widen private visibility solely to split.
5. **Refactoring (> 600 LOC):** Present a cluster table (LOC, shared state) + extraction plan (< 400 LOC/file), await approval, then extract one file at a time running build, typecheck, and tests after each.
