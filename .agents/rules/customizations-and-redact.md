---
trigger: model_decision
description: "Apply when editing internal/harness/, internal/redact/, skills/, agents/, commands/, or rules/ (allow lists, knownTools, vendored skills, catalog refreshes, redaction needles)."
---

# Customizations and redaction

- **Tools & frontmatter lint:** Adding a tool to an agent's `tools:` list requires adding it to `knownTools` (`internal/harness/inventory.go`); an unknown tool hangs Antigravity subagents and fails install. `fyr install` lints every skill, agent, command and rule (`TDD.md` § T6, `TestLint*`, `TestAgentDescriptionRules`). A new frontmatter key joins an allow list manually under a D-row (never a permission key or `disallowedTools`, `ADR-056`, `ADR-056a`).
- **File drop & listing budget:** Adding a skill, agent, command or rule is a file drop only, unless it needs a new allowlisted frontmatter key. If `TestSkillListingBudget` fails (`TDD.md` § T2), shorten a description; never raise the budget.
- **Size pins (`internal/harness/size_test.go`):** Changes to `context/*.md` or `rules/*.md` must pass `TestRenderedContextUnderTarget`, `TestAlwaysOnContextBudget`, and `TestRenderedContextFitsAntigravityBlock` (`TDD.md` § T1); never raise a limit.
- **Vendored skills & catalog:** Third-party skills listed in `skills/VENDORED.md` are vendored verbatim (except named forks); fork under a new name rather than editing in place. Refresh the Google catalog only via its script, security-review the diff before committing, and never install catalog entries as global skills (`skills/VENDORED.md` § Vendored catalogs). Write pins only from `git archive` + `diff -r` (`BUG-111`).
- **Redaction needles (`internal/redact/redact.go`):** Needles grow by kind (regexp for a vendor's length/alphabet, or distinctive prefix only; `BUG-110`). Every needle requires a benign near-miss in `TestRedactShapes` (`TDD.md` § T12).
