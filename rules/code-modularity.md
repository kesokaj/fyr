---
trigger: model_decision
description: "Apply when creating source files, writing functions > 40 LOC, or editing files > 350 LOC."
---

# Code modularity

1. **Limits:** Target 100–350 LOC/file (ceiling: 600 LOC; UI: 400 LOC; functions: 40–50 LOC, complexity 10; tests: 600 LOC). Generated and vendored files are exempt.
2. **Pre-Edit:** Put new logic in a new cohesive file before growing a > 400 LOC file; extract the touched cluster before editing a > 600 LOC file.
3. **Splitting:** Go/Rust keep core types in `<entity>.go`/`.rs` and concern methods in `<entity>_<concern>.go` without widening visibility. OOP: 1 public type/file. TS/JS: `<module>/index.ts`. UI: separate markup, state, and data.
4. **Boundaries:** Never create `utils.*`/`helpers.*`/`common.*`/`misc.*` files; keep a strict import DAG (zero cycles).
5. **Refactoring (> 600 LOC):** Present a cluster table + extraction plan (< 400 LOC/file), await approval, then extract one file at a time with green tests.
