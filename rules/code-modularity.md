---
trigger: always_on
description: "Code modularity: file and function size limits, split before growing a large file, paradigm-aware splits, no dumping-ground files, an acyclic import graph, and an audit table approved before any refactor of a file over 600 LOC."
---

# Code modularity

Enforce strict code modularity and file-size boundaries across all programming languages:

1. **Line Limits:** Target 100–350 LOC per file (hard ceiling: 600 LOC; UI components: 400 LOC; functions/methods: 50 LOC with max cyclomatic complexity of 10; test files: 600 LOC). Machine-generated files (`*.pb.*`, `*_gen.*`, migrations, lockfiles) are exempt and must never be edited.
2. **Pre-Edit Check:** Before adding code to any file > 400 LOC, place new logic in a new, cohesively named file and wire it via composition or imports. When editing a legacy file > 600 LOC, extract the touched functional cluster into a dedicated file first.
3. **Paradigm-Aware Splitting:**
   - **Package-Scoped (Go, Rust):** Keep primary types/constructors in `<entity>.go`/`.rs` and split large method sets into `<entity>_<concern>.go` or child submodules without widening public visibility.
   - **OOP (Java, C#, Kotlin, C++):** Enforce 1 public class/interface per file; decompose classes with > 7 injected dependencies into narrower collaborating services.
   - **Module-Scoped (Python, TypeScript, JS):** Convert bloated files into directory modules (`<module>/`) with cohesive sub-files, re-exporting only the public contract via `__init__.py` or `index.ts`.
   - **UI Frameworks (React, Vue, SwiftUI, Flutter):** Strictly separate presentational markup, state/logic hooks/controllers, and data/network calls into distinct files.
4. **Architectural Constraints:** Never create generic dumping-ground files (`utils.*`, `helpers.*`, `common.*`, `misc.*`)—name every file after its specific domain capability. Maintain a strict Directed Acyclic Graph (DAG) with zero circular dependencies, and never widen private/unexported visibility solely to split a file.
5. **Refactoring Protocol (For Existing Files > 600 LOC):** Do not move code immediately. First output a Markdown table auditing responsibility clusters, line counts, and shared state, followed by a zero-regression extraction blueprint where all target files are < 400 LOC. Wait for user approval, then extract one file at a time, running build, typecheck, and test verification after each step.
