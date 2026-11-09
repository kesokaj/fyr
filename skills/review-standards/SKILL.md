---
name: review-standards
description: "Per-language code-excellence standards for Go and TypeScript, loaded by the reviewer agent, and by /fyr-preflight when it reads a small diff itself. Load when applying the project review criteria: dead code, function flow, directory layout, simplicity, security-exempt DIY rule, decoupling. Not a review workflow — `/fyr-review` dispatches the reviewing."
---

# Review Standards

A standards **library**, not an actor. Whoever is reviewing (the `reviewer` agent, or `/fyr-preflight` reading a diff below the reviewer threshold itself; `/fyr-review` never reviews by itself) loads this file and judges the diff against criteria 1–6 below and the rules in §§ 7–8. Every finding gets a severity (high / medium / low) and a confidence level, and lands in a `review` record (the reviewer's draft, stored by the main thread) — never chat-only.

Language deep-dives (load only for the languages present in the diff):

- [references/go-review.md](references/go-review.md) — Go checklist
- [references/typescript-review.md](references/typescript-review.md) — TypeScript checklist

Lens reference (not a language; loaded only when `/fyr-review --prompts` names it in the reviewer's brief):

- [references/prompt-audit/prompt-audit.md](references/prompt-audit/prompt-audit.md) — Anthropic's prompt-audit reference (vendored verbatim, Apache-2.0, `skills/VENDORED.md`): dated prompt patterns, brittle instruction files, redundant sub-agents

## 1. Dead code — flag for REMOVAL, not for a comment

- [ ] Exported functions, types, and constants with zero callers outside their own tests.
- [ ] Unused dependencies (`go mod tidy` diff; `npm ls`/depcheck for TS) and unused imports.
- [ ] Commented-out code blocks — version control remembers; the file must not.
- [ ] Unreachable branches: conditions that can never be true, code after unconditional return/throw, feature flags that no longer vary.
- [ ] Leftover debug output and temporary scaffolding added for the change (prints, `console.log`, debug logs).
- [ ] Verdict is always "delete", with the evidence (no callers, no references). Never suggest wrapping dead code in a comment or a TODO.

## 2. Flow tracing — every changed function, caller to callee

Installers count as callers. A change that an installer, updater or converge step must act on (a unit `[Install]` section, a new flag, a changed default) is traced to EVERY such step, and to the question "which version of it runs the first time after this ships?" — a self-updating tool runs its OLD code on the tick that installs the change, so a step guarded by "only when something changed" never runs. Require the step to be unconditional or idempotent, and a post-deploy check by the exact command (`systemctl is-enabled …`), not by reading the unit file.

- [ ] Trace each changed function from its callers down through its callees; confirm the change is consistent at every hop (arguments, invariants, locking, units).
- [ ] Error paths propagate: Go — every `err` wrapped with context (`fmt.Errorf("...: %w", err)`) or explicitly handled, never discarded with `_` or an empty branch; TS — no floating promises (every promise awaited, returned, or explicitly voided with a reason), caught errors typed `unknown` and narrowed.
- [ ] Context and cancellation flow end-to-end: Go — `context.Context` passed down, honored in loops and I/O, goroutines have an exit path; TS — `AbortSignal` reaches fetches/long operations that outlive a request or component.
- [ ] Resource lifetimes: everything opened is closed on every path, including the error paths.
- [ ] Every new test can FAIL: would it still pass if the behaviour it names were reverted? Reject tests that assert on source text rather than behaviour, that re-assert a constant against itself, or that exercise a path the test setup makes unreachable. A test that cannot fail is worse than no test — it reports coverage that does not exist. One exception, named in the test's own comment: an ORDER no behavioural test can observe (close before rename; sync before rename) may be pinned by reading the source, code lines only, never comments — and the pin is the finding's evidence only when the comment says what cannot be observed and why.
- [ ] A change that consolidates two copies of a step sequence (write, sync, close, rename; check, then act) keeps each original's order: compare the merged version against each copy line by line, and flag any step that moved — a fix on one copy has already been ported by hand once when the copies existed.
- [ ] A must-fail MUTATION cited as evidence followed the protocol: the target text was unique in the file before the write (occurrences counted, exactly one), the file was restored from a saved copy rather than by a reverse replacement, `git diff` was read afterwards to confirm nothing else moved, and it counts only when the mutated code COMPILED and a named test failed — a build failure ("declared and not used", a missing return) is printed as such and is no evidence, since a test that never ran fails nothing. A test that sets up an unusual condition proves the condition HOLDS before it is trusted: run the command by hand in a scratch fixture and read its output. A self-hosting suite refuses to start when its port is already bound, takes the port from an overridable variable, and probes the process it started rather than "something answers".
- [ ] A write that can fail halfway leaves no partial state; a check-then-act on a file or row is not a TOCTOU race; a fallback does not mask the error it replaces; empty, NaN, Unicode and timezone inputs are handled where the input allows them.
- [ ] A source change whose generated counterpart (route tree, protobuf, `go generate` output, embedded build) is absent from the diff. A test that a generated or embedded artifact matches its source asserts BOTH directions — extra content is as much a defect as missing content — and an artifact built from the working tree rather than from git is pinned to what git tracks (`go:embed` can compile a gitignored `.env` into a release binary while every test stays green).
- [ ] An assertion changed in an existing test traces to a behaviour change in the source diff; a suppression directive, skip, `.only` or lint-config exclusion the diff adds names its rule or condition and a reason other than making the check pass — otherwise it is a finding.

**Silence is not evidence:** before trusting a monitor, watcher or filter that reports nothing, confirm it can emit at all; a green run is evidence only when it demonstrably hit the thing under test; a filter that fires is read before it counts, since a needle matches its own negation; a check is validated once in the must-pass and once in the must-fail state before its verdict is cited, and a test that sets up an unusual condition proves the condition HOLDS before it is trusted.

## 3. Standards conformance — the language skills are the authority

- [ ] Go changes conform to the `golang` skill; TypeScript to the `typescript` skill; UI work to the `frontend-ui-modern` or `frontend-ui-economist` skill. Cite the specific rule when flagging.
- [ ] Where this file and a language skill overlap, the language skill wins for idiom; this file wins for review scope and severity.
- [ ] Formatting/lint gates already automated (gofmt, vet, eslint) are verified as run, not re-litigated line-by-line.
- [ ] Comments, doc-comments, test names and spec rows that name behaviour the diff changed still describe it — the global context's describing-text rule; a stale one is a finding, not a comment request.
- [ ] A rule, ban, threshold or trigger the diff changes has every other restatement of it in the repository changed too: grep its subject nouns (not only its old wording) across `context/`, `skills/*/SKILL.md`, `agents/`, `README.md`; a restatement left standing is a finding naming both file:line. Beside the grep, `fyr search <subject nouns>` ranks restatements worded differently (working tree only: a past revision stays with `git grep <rev>`).

## 4. Directory layout

- [ ] Go: entrypoints in `cmd/<app>/`, private code in `internal/`, `pkg/` only for deliberately public libraries — not as a dumping ground.
- [ ] Frontend fully contained in one `web/` or `ui/` directory inside the Go project; no stray JS/TS at the repo root.
- [ ] Tests live beside the code they cover (`foo_test.go` next to `foo.go`, `foo.test.ts` next to `foo.ts`) — not in a parallel tree.
- [ ] New files land where an unfamiliar engineer would look for them first; flag surprising placement even when it compiles.

## 5. Simplicity — stdlib-first, with one hard exception

- [ ] Prefer the standard library; every new dependency is justified in the PR description or the changelog entry — "saves 20 lines" is not a justification.
- [ ] Flag speculative abstraction: interfaces with one implementation and no test double, config for values that never vary, layers that only forward calls.
- [ ] **Security exemption (mandatory, overrides stdlib-first):** never hand-rolled auth, crypto, password hashing, session handling, or JWT/OIDC — vetted libraries only. DIY code in these areas is a high-severity finding regardless of quality. Hand-rolled means a primitive or protocol implemented or composed here — a cipher, hash, MAC, signature, key derivation, random-number generator, token or session format, an auth flow — and the finding names it. Consuming the standard library's `crypto/*` as designed — bytes or an identifier from `crypto/rand`, a `sha256` checksum, `crypto/tls` — is stdlib use, not DIY, and is no finding. A finding that asks for a new dependency names the stdlib alternative it rejected and why it does not do (Go 1.24: `crypto/rand.Text()` for identifiers).
- [ ] The simplest correct solution wins; complexity must buy something observable (performance, correctness, a real second caller).

## 6. Decoupling

- [ ] Small interfaces at the seams — use the `codebase-design` skill's vocabulary (module, interface, seam, adapter, depth): prefer deep modules, flag shallow ones whose interface is as complex as their implementation.
- [ ] Interfaces declared where they are consumed, sized to what the consumer needs — not exported wholesale from the implementing package.
- [ ] No cross-package reach-ins: no importing another package's internals, no reaching through a struct to a third package's state, no test importing another package's unexported helpers.
- [ ] Files independently editable: two engineers changing adjacent features should not collide in the same file; flag god-files and grab-bag `util` packages that force serialized edits.

## 7. Restatements — a changed claim has every copy changed

The two shapes that hide best.

- [ ] A value joining a VOCABULARY (a signal, a status, a category) has every enumeration of it (schema, template, table, help text) changed in the same edit.
- [ ] A CLAIM the diff reverses has every restatement of it found by one grep over its key words across every living file BEFORE the first review round, because each round otherwise finds only the copy the last fix pointed at. The `fyr-review` skill's `--docs` lens runs the `fyr-review` skill's `fyr-review/scripts/restatement-grep.sh` for this; name the files it listed.
- [ ] Claims of test COVERAGE name what is not covered; "covered" without the gap named is a finding.

## 8. Code-shape rules — a review checks them, an author applies them before claiming done

The `golang`, `typescript` and `bash` skills point here.

- [ ] A guard in front of a destructive action keeps the data when its check ERRORS: `if err != nil` never falls through to the delete, and the test covers the case where the check itself cannot run.
- [ ] When a command has a read-only mode (`--check`, `--dry-run`, "report only"), a new side effect goes BELOW that mode's early returns, and the test asserts the read-only mode does NOT perform it, not only that the normal mode does. The read-only mode also resolves every guard and path exactly as the real run does, or it is not a dry run of it: a path handed to a child process started with another working directory is made absolute before the handoff.
- [ ] A test that a generated or embedded artifact matches its source asserts BOTH directions — extra content is as much a defect as missing content — and an artifact built from the working tree rather than from git is pinned to what git tracks.
- [ ] A verification command is never piped into a filter inside a `&&` chain that leads to a commit: the chain then guards the filter's exit status, not the command's. Run the command bare, read its status, filter its captured output afterwards, and commit in its own call. Staging is a verification step too: `git add` by name from a path list regenerated right before it, read `git diff --cached --stat`, then commit in a separate call — never `add … ; commit`. (The graduation target of every "chained check and commit" bug record.)
- [ ] A must-fail mutation cited as evidence COMPILED and a named test failed — a build failure is no evidence (the protocol is § 2).
- [ ] A check that guards what ANOTHER parser will read (a lint in front of a YAML, shell or SQL reader) refuses everything it cannot itself read the same way. Whatever it skips as harmless — indentation, a character it does not treat as a line break, a line it calls a continuation, a trimmed run where one terminator was meant — is where the other parser may find the forbidden thing: each skip rule has a must-refuse test with the forbidden thing inside the skipped text, in every kind the check covers. A test for a guard also keeps the guard's ABSENCE harmless, since writing it red-first runs it without the guard: the forbidden target is a stand-in under the test's temporary directory, never the real checkout, home or `/`.
