---
trigger: always_on
description: "Code modularity: file/function size limits, split before growing >400 LOC files, paradigm-aware splits, no utils dumping grounds, acyclic imports, and an approved audit table before refactoring >600 LOC files."
---

# Code modularity

Enforce strict modularity and file-size bounds across all languages:

1. **Line Limits:** Target 100–350 LOC per file (hard ceiling: 600 LOC; UI components: 400 LOC; functions/methods: 50 LOC, max cyclomatic complexity 10; tests: 600 LOC). Machine-generated (`*.pb.*`, `*_gen.*`, migrations, lockfiles) and vendored files are exempt and never edited.
2. **Pre-Edit Check:** Before adding to a file > 400 LOC, put new logic in a new cohesive file. Before editing a file > 600 LOC, extract the touched cluster first.
3. **Paradigm-Aware Splitting:**
   - **Package-Scoped (Go, Rust):** Keep core types/constructors in `<entity>.go`/`.rs`; split method sets into `<entity>_<concern>.go` or child packages without widening visibility.
   - **OOP (Java, C#, Kotlin, C++):** 1 public class/interface per file; split classes with > 7 injected dependencies.
   - **Module-Scoped (Python, TypeScript, JS):** Split large files into `<module>/` sub-files, re-exporting only the public surface via `__init__.py` or `index.ts`.
   - **UI (React, Vue, SwiftUI, Flutter):** Separate markup, state/controller hooks, and data/network calls into distinct files.
4. **Architectural Constraints:** Never create dumping-ground files (`utils.*`, `helpers.*`, `common.*`, `misc.*`); name files by domain capability. Keep a strict DAG (zero circular imports) and never widen private visibility just to split a file.
5. **Refactoring Protocol (> 600 LOC):** Output a Markdown table of responsibility clusters, line counts and shared state, plus a zero-regression extraction plan (< 400 LOC per target file). Wait for approval, then extract one file at a time, running build, typecheck and tests after each.
