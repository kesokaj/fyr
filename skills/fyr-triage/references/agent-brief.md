# Writing Durable Agent Briefs

An **Agent Brief** is the self-contained contract written when an issue, PR, backlog item, or `BUG-*`/`PROPOSAL-*` record reaches `ready-for-agent`. An autonomous session (`fyr work` worker, `fyr-feature`, or `fyr-bug`) works from this brief: the raw issue thread is background context, while the Agent Brief is the specification.

> [!NOTE]
> Adapted from `<upstream>/skills/engineering/triage/AGENT-BRIEF.md` in `mattpocock/skills` at `4ae2317` (MIT, `../LICENSE.txt`), extended with `/fyr-work` task/Kickoff mapping and `brain.db` conventions (`PRD.md` R7).

## Principles

### 1. Durability Over Brittle Line Numbers

An item may stay in `ready-for-agent` across several commits while files split or lines shift. Write the brief so a fresh agent running `fyr search --slice` converges even after refactors:

- **Do** name exported types, function/method signatures, CLI flags, SQL tables, or `SPEC-*` / `ADR-*` records.
- **Do** describe observable contracts, inputs, outputs, and invariants.
- **Don't** hardcode raw line numbers as instructions (`"add a field on line 42"`).
- **Don't** dictate mechanical edits where behavioural specification lets the worker follow current repo structure.

### 2. Behavioural, Not Procedural

State **what** must hold when done, not step-by-step keystrokes:

- **Good:** "`fyr report --project --global` must exit with code `2` and print `--project and --global exclude each other`."
- **Bad:** "Add an `if` statement on line 54 of `verb_report.go`."

### 3. Testable Acceptance Criteria & Explicit Out-of-Scope

Every brief carries independently checkable `- [ ]` criteria and an explicit **Out of scope** list so an unattended worker never gold-plates adjacent surfaces.

## Agent Brief Template

```markdown
## Agent Brief

**Category:** bug | enhancement
**Summary:** <one-line description of the required outcome>

**Current behavior:**
<What happens today. For a bug, the broken behavior and reproduction; for an enhancement, the status quo; for a PR, what the contributor's diff already does and what gaps remain.>

**Desired behavior:**
<What must hold after the change, including edge cases, exit codes, and error conditions.>

**Key interfaces & constraints:**
- `<TypeOrFunc>` / `<CLI verb or flag>`: <contract change and why>
- `PRD.md` / `ADR-NNN`: <invariants that must stay intact>

**Acceptance criteria:**
- [ ] <Specific, testable criterion 1>
- [ ] <Specific, testable criterion 2>
- [ ] <Regression / unit test that fails without the change>

**Out of scope:**
- <Adjacent surface or follow-up explicitly excluded>
```

<details>
<summary>Examples: Bug, Enhancement, PR & Anti-Pattern</summary>

### Good Brief (Bug)

```markdown
## Agent Brief

**Category:** bug
**Summary:** Skill description truncation splits mid-word instead of breaking on word boundaries

**Current behavior:**
Descriptions exceeding 1,024 characters are sliced at byte 1,024 regardless of whitespace, producing cut-off words in CLI listings.

**Desired behavior:**
Descriptions over 1,024 characters break at the last whitespace boundary at or before `1,021` characters and append `"..."`, never exceeding 1,024 characters total.

**Key interfaces & constraints:**
- `Skill` frontmatter validation/truncation helper: preserve `<= 1024` descriptions byte-for-byte.
- Single continuous word > 1,021 chars falls back to hard cut at 1,021 + `"..."`.

**Acceptance criteria:**
- [ ] Descriptions `<= 1024` chars remain unchanged
- [ ] Multi-word descriptions `> 1024` chars break at the last word boundary and end with `"..."`
- [ ] Total length never exceeds 1,024 chars
- [ ] Table-driven unit test covers under-limit, boundary, multi-word overflow, and single-token overflow

**Out of scope:**
- Raising the 1,024-character limit (`TestLintDescriptionLimit`)
```

### Good Brief (Enhancement)

```markdown
## Agent Brief

**Category:** enhancement
**Summary:** Add `--json` output mode to `fyr record list` for machine-readable queue inspection

**Current behavior:**
`fyr record list` prints tab/space-aligned text lines only; callers scripting over open `bug` or `proposal` records must parse human text.

**Desired behavior:**
Passing `--json` to `fyr record list` writes a JSON array of record summary objects (`id`, `kind`, `status`, `updatedAt`, `title`) to stdout (`[]` when empty) while keeping exit codes and default human output unchanged.

**Key interfaces & constraints:**
- `runRecord` / `recordList` flag set and `brain.Store.List` result slice
- `PRD.md` R4: stdlib `encoding/json` only; `--home` isolation respected

**Acceptance criteria:**
- [ ] `fyr record list --json --home <scratch>` emits valid JSON array (`[]` when no rows match)
- [ ] Default output without `--json` is byte-for-byte unchanged
- [ ] CLI test covers empty store, filtered `--open --kind bug`, and error exit codes

**Out of scope:**
- Changing `brain.db` schema or `.agents/trail.jsonl` export format
```

### Good Brief (External PR)

```markdown
## Agent Brief

**Category:** enhancement
**Summary:** Finish contributor PR #42 (`--md` flag on `fyr report`) with error-path and overwrite tests

**Current behavior:**
PR #42 implements `RenderMarkdownReport` on the happy path, but `--md --out <existing-file>` truncates existing files instead of refusing with `O_EXCL`, and no CLI test covers `--md`.

**Desired behavior:**
`--md --out FILE` creates `FILE` with `O_WRONLY|O_CREATE|O_EXCL` at mode `0600` (refusing existing paths like HTML `WriteReport`), with unit and CLI tests covering stdout and `--out` refusal.

**Key interfaces & constraints:**
- Reuse `WriteMarkdownReport`; keep `RenderMarkdownReport` pure
- `ADR-516`: read-only over `existingMetricsStore()`

**Acceptance criteria:**
- [ ] `fyr report --md --out <existing>` exits non-zero without modifying `<existing>`
- [ ] New `--out` file is created with mode `0600`
- [ ] Unit + CLI tests pass under `go test -race`

**Out of scope:**
- Changing the 5 report section headings introduced in PR #42
```

### Anti-Pattern (Bad Brief)

- Vague goal (`"Fix the triage bug"`), hardcoded stale line numbers (`"edit handler.go line 150"`), no acceptance criteria, and no out-of-scope boundary.

</details>

## Mapping an Agent Brief to `/fyr-work`

When the operator approves running one or more `ready-for-agent` briefs right away via `/fyr-work` (task-list mode), condense each brief into a row in `/fyr-work`'s proposal table after running `fyr search --slice` to pin current repo-relative `Files:`:

| Field | How to derive from the Agent Brief |
|---|---|
| **Task** | One-sentence summary + desired behavior + `PRD`/`ADR` constraint |
| **`Files:`** | Repo-relative files/dirs resolved right before dispatch via `fyr search --slice` |
| **`Model:`** | `gemini-4-argon` (`[pro]`) or `gemini-3.8-flash` (`[flash]`) per `rules/agy-dispatch.md` |
| **`Done when:`** | Concrete `go test -race <pkg>` command verifying the brief's acceptance criteria |
