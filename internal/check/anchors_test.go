package check

import (
	"strings"
	"testing"
)

func TestAnchorLinksMustPassAndFail(t *testing.T) {
	readme := strings.Join([]string{
		"# Top Title",
		"",
		"- [T7](#t7--the-brain-store)",
		"- [Chars](#a--b-cde-f_g)",
		"- [Dup0](#dup) [Dup1](#dup-1) [Dup2](#dup-2)",
		"- [Cross rel](docs/sub.md#sub-heading)",
		"- [Missing anchor](#no-such-heading)",
		"- [Missing file](docs/missing.md#heading)",
		"- [Bad cross](docs/sub.md#wrong)",
		"- Skip: [url](https://example.com/a#b) [mail](mailto:a@b.c#x) [code](cmd/app/main.go#L10) [tmpl](<path>#sec)",
		"- Inline code skip: `[bad](#no-such-heading)`",
		"",
		"```markdown",
		"[fenced](#no-such-heading)",
		"```",
		"",
		"~~~md",
		"[tilde](#no-such-heading)",
		"~~~",
		"",
		"## T7 — The brain store",
		"## A & B: c.d/e (f_g)",
		"## Dup",
		"## Dup",
		"## Dup",
	}, "\n")
	sub := strings.Join([]string{
		"# Sub",
		"",
		"## Sub Heading",
		"",
		"- [Fallback root](README.md#top-title)",
		"- [Relative up](../README.md#t7--the-brain-store)",
	}, "\n")
	dir := repo(t, map[string]string{
		"README.md":   readme,
		"docs/sub.md": sub,
	})
	rep := run(t, dir)
	same(t, rep.Findings, "README.md:7 anchor", "README.md:8 anchor", "README.md:9 anchor")
}

func TestSectionRefsC50AndMatching(t *testing.T) {
	rootAgents := strings.Join([]string{
		"# AGENTS.md — root",
		"",
		"## Setup commands",
		"## Layout",
	}, "\n")
	ctxAgents := strings.Join([]string{
		"# AGENTS.md — global",
		"",
		"## Git & hard rules",
	}, "\n")
	assetAgents := strings.Join([]string{
		"# AGENTS.md — consumer",
		"",
		"## Setup Commands",
	}, "\n")
	tdd := strings.Join([]string{
		"# TDD",
		"",
		"## T1 — First",
		"## T6 — The installer",
		"## T10 — Tenth",
	}, "\n")
	readme := strings.Join([]string{
		"# README",
		"",
		"## How it works",
		"## Known limits",
		"",
		"Valid: `TDD.md § T6`, `TDD.md` § T1, `README.md` § How it works is the flow, `AGENTS.md` § Setup names commands.",
		"Possessive boundary: `README.md` § Known limits' 22,000-byte cap.",
		"Table bare skip: | `AGENTS.md` § | and `AGENTS.md` §, and `AGENTS.md § <section title>`.",
		"C50 fail 1: `AGENTS.md` § How the harness works.",
		"C50 fail 2: `AGENTS.md` § Release.",
		"Valid T10 and missing T99: `TDD.md` § T10 is valid, `TDD.md` § T99.",
		"Prose non-continuation fails: `AGENTS.md` § Setup banana.",
		"Missing file fails: `NOPE.md` § Missing.",
		"Context AGENTS section not in root AGENTS fails on root doc: `AGENTS.md` § Git & hard rules.",
	}, "\n")
	skill := strings.Join([]string{
		"# Skill",
		"",
		"Consumer setup: `AGENTS.md` § Setup Commands.",
		"Global rules: `AGENTS.md § Git & hard rules`.",
		"Sibling skill ref: `other/references/ref.md` § Database item 3.",
		"Prose named skill ref: the other skill's `ref.md` §4.",
		"Own skill ref missing fails: `missing.md` § Nope.",
	}, "\n")
	otherSkill := "# Other\n\n## Dockerfile: Go multi-stage\n\n### 4. Smoke test\n"
	otherRef := "# Ref\n\n## Database: Postgres\n\n### 4. Pooling\n"
	dockerSetup := "# Setup\n\nSee `SKILL.md` § Dockerfile rule 4 points here, and `SKILL.md` §4.\n"
	trail := trailLine(t, "SPEC-live", "spec", "current", "Code `internal/a.go`, `TDD.md` § T6, [bad](#missing-in-spec), [ok](#spec-head).\n\n## Spec head\n")
	dir := repo(t, map[string]string{
		"internal/a.go":     "package a\n",
		"AGENTS.md":         rootAgents,
		"context/AGENTS.md": ctxAgents,
		"skills/fyr-init-project/assets/AGENTS.md": assetAgents,
		"TDD.md":                           tdd,
		"README.md":                        readme,
		"skills/demo/SKILL.md":             skill,
		"skills/other/SKILL.md":            otherSkill,
		"skills/other/references/ref.md":   otherRef,
		"skills/other/references/setup.md": dockerSetup,
		".agents/trail.jsonl":              trail,
	})
	rep := run(t, dir)
	same(t, rep.Findings,
		"README.md:9 anchor",
		"README.md:10 anchor",
		"README.md:11 anchor",
		"README.md:12 anchor",
		"README.md:13 anchor",
		"README.md:14 anchor",
		"skills/demo/SKILL.md:7 anchor",
		"SPEC-live:1 anchor",
	)
}

func TestAnchorBoundaryRejectsT1MatchingT10(t *testing.T) {
	dir := repo(t, map[string]string{
		"TDD.md":    "# TDD\n\n## T10 — Tenth\n",
		"README.md": "Bad ref: `TDD.md § T1`.\n",
	})
	same(t, run(t, dir).Findings, "README.md:1 anchor")
}
