# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 6 — Prompt Vetting, Intent Router, Skill Prefetcher & Turn Contract (after: Phase 4, Phase 5)
- [ ] Implement cryptographic per-turn data fencing (`Fence`, `New`, `Data` with `crypto/rand` `=====<18-hex>=====` boundary) in `internal/search/fence.go` and wrap all prefetched `MEMORY-*`, `BUG-*`, `ADR-*`, and AST `--slice` payloads inside `PreInvocation` as data — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: `fence.New` never collides with payload text and wraps all retrieved records/slices with author provenance and non-instruction notice
- [ ] Implement the `< 10 ms` deterministic 9-mode Intent Classifier (`ClassifyIntent`), `crew`-style 6-Anchor Prompt Vetter (`VetPrompt`: `PASS_THROUGH`, `ENRICH_AND_PROCEED`, `VET_FIRST` with `!` and `/slash` bypasses), Skill Prefetch Matcher (`MatchSkills`, top `1–2` skills `<= 600` B digest + `SKILL.md` path + mandatory `security` subagent flag on auth/crypto/secret/frontmatter surfaces), and `BuildTurnEnvelope` (`user_markdown` operator tips banner + `[FYR_TURN_CONTRACT]`) in `internal/search/vet.go` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: `APPROVAL`/`TRIVIAL`/`/slash`/`!` prompts get `PASS_THROUGH`, well-anchored `TASK`/`BUG`/`QUESTION`/`CORRECTION` get `ENRICH_AND_PROCEED` (`ANSWER_ONLY` for questions, `feedback-log`+`project-memory` for corrections), and underspecified `FEATURE`/`BUG` or `ADR-*` conflicts get `VET_FIRST`
- [ ] Wire `Options.BrainTurnContext` (full-body `MEMORY-*` matching on prompt words/paths, open `BUG-*` prevention rules, `ADR-*` constraints, active `PLAN-*` phase) and `Options.SkillCatalog` in `cmd/fyr/verb_search_memory.go` into `fyr search --hook pre-invocation` and add `fyr search --vet <prompt>` in `cmd/fyr/ctx_verbs.go` and `cmd/fyr/help_text.go` (`<= 79` runes/line, `<= 10` `about` lines, backticked metavar) — verify: unit, `go test -race ./internal/search/... ./cmd/fyr/... .`, who: AI — expect: `fyr search --vet` and `--hook pre-invocation` complete in `< 25 ms` and pass `TestHelpLinesFit`, `TestHelpFlagsAreTheFlagSet`, `TestHelpFormsAgree`, `TestHelpPageShape`, and `TestFunctionLength` — docs: cmd/fyr/help_text.go
- [ ] Checkpoint — expect: every `PreInvocation` hook call classifies intent, vets anchors, prefetches skills and matched full-body memories/ADRs/bugs inside a cryptographic fence, emits the zero-token `user_markdown` operator tip banner, and enforces `[FYR_TURN_CONTRACT]` — evidence: `go test -race ./...`, pending

## Next

- Phase 7 — Cross-Agent Citation Rules, Docs & Byte-Budget Verification (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T1, T2, T5, T8)

## Backlog
