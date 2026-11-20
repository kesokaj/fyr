package search

import (
	"path/filepath"
	"slices"
	"strings"
)

func containsAnySubstr(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func extractPromptAndSliceFiles(prompt string, sliceFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range sliceFiles {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, m := range fileTokenRe.FindAllString(prompt, -1) {
		if m = strings.TrimSpace(strings.Trim(m, "`'\".,;:()")); m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func containsAnyWord(low string, words ...string) bool {
	tokens := strings.FieldsFunc(low, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
	})
	for _, t := range tokens {
		if slices.Contains(words, t) {
			return true
		}
	}
	return false
}

func hasExt(files []string, exts ...string) bool {
	return countExt(files, exts...) > 0
}

func countExt(files []string, exts ...string) int {
	n := 0
	for _, f := range files {
		if slices.Contains(exts, strings.ToLower(filepath.Ext(f))) {
			n++
		}
	}
	return n
}

func promptHasExt(lowPrompt string, exts ...string) bool {
	for _, m := range fileTokenRe.FindAllString(lowPrompt, -1) {
		m = strings.Trim(m, "`'\".,;:()")
		if slices.Contains(exts, strings.ToLower(filepath.Ext(m))) {
			return true
		}
	}
	return false
}

func hasBaseOrExt(files []string, subs ...string) bool {
	for _, f := range files {
		low := strings.ToLower(filepath.Base(f))
		for _, sub := range subs {
			if strings.Contains(low, sub) {
				return true
			}
		}
	}
	return false
}

func defaultSkillCatalog() []SkillMeta {
	return []SkillMeta{
		{Name: "fyr-bug", Path: "skills/fyr-bug/SKILL.md", Digest: "Workflow for fixing broken behavior: problem note, goldfish diagnosis on symptom/repro only, failing test before the fix, smallest fix, review, bug record."},
		{Name: "fyr-feature", Path: "skills/fyr-feature/SKILL.md", Digest: "Workflow for feature work: scope confirmation, plan record with phases and task checkboxes, cold-reader check before code, one phase per checkpoint, e2e verification."},
		{Name: "fyr-feedback-log", Path: "skills/fyr-feedback-log/SKILL.md", Digest: "Passive correction logger: when the operator corrects or overrides agent behavior, record a structured global feedback record (`fyr record add feedback --global`)."},
		{Name: "fyr-project-memory", Path: "skills/fyr-project-memory/SKILL.md", Digest: "Save and load project memory through `fyr memory add` and `fyr memory show` when the operator states a preference, correction, or project constraint."},
		{Name: "fyr-review", Path: "skills/fyr-review/SKILL.md", Digest: "Review working tree, git range, PR, or paths through reviewer, security, verifier, and explorer subagents (`--code`, `--security`, `--docs`, `--tests`)."},
		{Name: "fyr-preflight", Path: "skills/fyr-preflight/SKILL.md", Digest: "Release gate and release path (`/fyr-preflight vX.Y.Z`): Definition of Done, reviewer and security dispatch over release diff, SHIP/NO-SHIP report, annotated tag."},
		{Name: "golang", Path: "skills/golang/SKILL.md", Digest: "Go standards: `cmd/<app>` + `internal/`, stdlib first, `log/slog` JSON, table-driven tests with `-race`, hand-written fakes, functions <= 50 lines."},
		{Name: "typescript", Path: "skills/typescript/SKILL.md", Digest: "TypeScript standards: strict tsconfig, narrowing, ESM module boundaries, runtime validation at boundaries, Vitest and Playwright tiers."},
		{Name: "bash", Path: "skills/bash/SKILL.md", Digest: "Bash and shell scripting: `set -euo pipefail`, safe quoting, `jq`/`rg`/`fd` pipelines, shellcheck compliance, and interactive setup wizards (`set_gcp_secret`)."},
		{Name: "testing-strategy", Path: "skills/testing-strategy/SKILL.md", Digest: "Decide test tiers owed by a change: unit with `-race`, integration, API `httptest`, e2e, and `GUIDE-testing` maintenance."},
		{Name: "fyr-docs", Path: "skills/fyr-docs/SKILL.md", Digest: "Airy, visual-first documentation standard and docset audit: BLUF (Bottom Line Up Front), short prose, Mermaid diagrams, and `<details>` folding."},
		{Name: "fyr-triage", Path: "skills/fyr-triage/SKILL.md", Digest: "Triage GitHub issues/PRs and local `fyr` queues (`TODO.md` Backlog, `BUG-*`, `PROPOSAL-*`) via redundancy and `PRD.md`/ADR checks into Agent Briefs or `/fyr-swarm` Kickoffs."},
		{Name: "fyr-spec", Path: "skills/fyr-spec/SKILL.md", Digest: "Write or update a `SPEC-<app>-<topic>` record concrete enough that two implementers converge and `fyr check` verifies it."},
		{Name: "frontend-ui-economist", Path: "skills/frontend-ui-economist/SKILL.md", Digest: "Editorial, report and airy UI in plain HTML/CSS/SVG: six themes on one token contract, an SVG chart kit, a white-space method."},
		{Name: "frontend-ui-modern", Path: "skills/frontend-ui-modern/SKILL.md", Digest: "Modern web-app UI on shadcn/ui, Tailwind and React: dashboards, tables, forms, theming, dark mode."},
		{Name: "docker", Path: "skills/docker/SKILL.md", Digest: "Container-based development with Docker and Colima: building images, compose services, Dockerfiles, volumes and networking."},
		{Name: "cloud-run-deploy", Path: "skills/cloud-run-deploy/SKILL.md", Digest: "Google Cloud Run deployment mechanics: build locally, push to Artifact Registry, deploy, verify, roll back."},
		{Name: "data-gcp", Path: "skills/data-gcp/SKILL.md", Digest: "Cloud SQL PostgreSQL for Go on Cloud Run: pgx over /cloudsql socket, golang-migrate job, owner/runtime role split, indexes."},
		{Name: "api-auth-gcp", Path: "skills/api-auth-gcp/SKILL.md", Digest: "Cloud Run caller verification: IAP, Firebase Auth, OIDC identity tokens for service-to-service, Scheduler, Tasks, and Pub/Sub."},
		{Name: "json-api-style", Path: "skills/json-api-style/SKILL.md", Digest: "Google AIP JSON API design: lowerCamelCase keys, RFC 3339 timestamps, string enums, pageSize/pageToken, structured errors."},
		{Name: "mermaid-syntax", Path: "skills/mermaid-syntax/SKILL.md", Digest: "Error-free Mermaid v11 diagrams: flowcharts, sequence, state, class, ER, and XY charts with quoted special-character labels."},
		{Name: "fyr-swarm", Path: "skills/fyr-swarm/SKILL.md", Digest: "Route parallel multi-file work into isolated `Files:`-leased worktrees or `run_workflow` stages and land each with `fyr gate` and `fyr land`."},
		{Name: "fyr-handoff", Path: "skills/fyr-handoff/SKILL.md", Digest: "Compact the session into an immutable handoff record with a Kickoff block, commit, push, and fyr metrics ingest."},
		{Name: "fyr-retro", Path: "skills/fyr-retro/SKILL.md", Digest: "Session retrospective: analyze corrections, memory promotions, bugs, and metrics to propose minimal rule/skill improvements."},
		{Name: "fyr-dream", Path: "skills/fyr-dream/SKILL.md", Digest: "Interactive project memory consolidation: assign verdicts (keep, graduated, stale, broken, merge, promote) to memory records."},
		{Name: "fyr-harness-audit", Path: "skills/fyr-harness-audit/SKILL.md", Digest: "Audit the harness checkout area by area with read-only agents and write a severity-ranked report and action plan."},
	}
}

func mergeSkillCatalog(custom []SkillMeta) map[string]SkillMeta {
	byName := map[string]SkillMeta{}
	for _, s := range defaultSkillCatalog() {
		byName[s.Name] = s
	}
	for _, s := range custom {
		if s.Name == "" {
			continue
		}
		if s.Digest == "" {
			if def, ok := byName[s.Name]; ok {
				s.Digest = def.Digest
			} else {
				s.Digest = s.Description
			}
		}
		if s.Path == "" {
			s.Path = "skills/" + s.Name + "/SKILL.md"
		}
		byName[s.Name] = s
	}
	return byName
}
