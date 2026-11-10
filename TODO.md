# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 4 — `fyr-docs`, `/fyr-triage` & Bash Wizard Skills Integration (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)
- [ ] Create `skills/fyr-docs/SKILL.md` and `skills/fyr-docs/references/docs-auditing.md` (forked from `google-gemini/gemini-cli` `.gemini/skills/docs-writer` at `1930f25`, Apache-2.0) enforcing BLUF (Bottom Line Up Front), short airy explanations, `<details>` folding for dense tables/schemas, and Mermaid visuals (`mermaid-syntax`), and wire `fyr-docs` into `agents/documentation/agent.md`, `skills/fyr-explain-project/SKILL.md`, `skills/fyr-spec/SKILL.md`, `skills/fyr-feature/SKILL.md`, and `skills/fyr-review/SKILL.md` (`--docs` lens) — verify: unit, `go test -race ./internal/harness/...`, who: AI — expect: `PASS` on `TestLint*` and `TestSkillListingBudget` — docs: `skills/fyr-docs/SKILL.md`, `agents/documentation/agent.md`
- [ ] Create `skills/fyr-triage/SKILL.md` and `skills/fyr-triage/references/agent-brief.md` (forked from `mattpocock/skills` `skills/engineering/triage` at `4ae2317`, MIT) to triage GitHub Issues/PRs (`gh issue`, `gh pr`) and local `fyr` queues (`TODO.md` Backlog, open `BUG-*` and global `PROPOSAL-*` records) with redundancy + `brain.db`/`PRD.md` prior-rejection checks into durable Agent Briefs / `/fyr-work` Kickoffs — verify: unit, `go test -race ./internal/harness/...`, who: AI — expect: `PASS` — docs: `skills/fyr-triage/SKILL.md`
- [ ] Add `skills/bash/references/wizard.md` and `skills/bash/assets/wizard-template.sh` (forked from `mattpocock/skills` `skills/engineering/wizard` at `4ae2317`, MIT, defaulting ephemeral scripts to `.agents/scratch/wizard-<slug>.sh` and adding `set_gcp_secret` via `gcloud secrets versions add --data-file=-`), reference it in `skills/bash/SKILL.md`, record all 3 forks in `skills/VENDORED.md` § Forks, and add `fyr-docs` and `fyr-triage` triggers in `internal/search/vet_skills.go` (`scoreSkillSurface`) — verify: unit, `go test -race ./internal/harness/... ./internal/search/...`, who: AI — expect: `PASS` on `TestVendoredRowsCarryAPin`, `TestSkillListingBudget`, and `TestMatchSkills*` — docs: `skills/bash/SKILL.md`, `skills/VENDORED.md`
- [ ] Checkpoint — expect: all new/updated skills and `VENDORED.md` pass `go test -race ./internal/harness/... ./internal/search/...` and `go run ./cmd/fyr list` — evidence: command and exit status, date

## Next

- Phase 5 — End-to-End Verification, Reviews & Release Readiness (`PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep`)

## Backlog
