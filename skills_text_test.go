package fyr_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func readSkill(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section returns the text from the heading line that starts with head up to
// the next heading of the same or a higher level.
func section(t *testing.T, text, head string) string {
	t.Helper()
	i := strings.Index(text, "\n"+head)
	if i < 0 {
		t.Fatalf("no heading %q", head)
	}
	rest := text[i+1:]
	level := len(head) - len(strings.TrimLeft(head, "#"))
	next := regexp.MustCompile(`(?m)^#{1,` + string(rune('0'+level)) + `} `)
	if loc := next.FindStringIndex(rest[len(head):]); loc != nil {
		return rest[:len(head)+loc[0]]
	}
	return rest
}

// TestHandoffReportEndsWithTheKickoffInline: the operator said "Give it as a
// prompt always" (2026-10-25, project memory next-session-prompt-inline), and
// two sessions still closed with a pointer to the handoff file instead of the
// block. The handoff skill's report step prints the Kickoff in full, in a
// fenced block, as its last item, and says a pointer does not replace it.
func TestHandoffReportEndsWithTheKickoffInline(t *testing.T) {
	step := section(t, readSkill(t, "skills/fyr-handoff/SKILL.md"), "## Step 7")
	for _, want := range []string{"Kickoff block in full", "in a fenced block in the reply", "ready to paste", "Never a pointer to the file"} {
		if !strings.Contains(step, want) {
			t.Errorf("handoff Step 7 does not say %q", want)
		}
	}
	items := regexp.MustCompile(`(?m)^\d+\. .*$`).FindAllString(step, -1)
	if len(items) == 0 || !strings.Contains(items[len(items)-1], "Kickoff") {
		t.Errorf("the Kickoff is not the report's last numbered item: %q", items)
	}
}

// TestPushStepsRunTheTrailerCheck: BUG-177's rule graduated into a script
// (plan 2026-10-25-harness-audit-fix D13); both skills that push run it
// before the push, by its installed path.
func TestPushStepsRunTheTrailerCheck(t *testing.T) {
	for path, head := range map[string]string{
		"skills/fyr-handoff/SKILL.md":   "## Step 6",
		"skills/fyr-preflight/SKILL.md": "## Step 6",
	} {
		step := section(t, readSkill(t, path), head)
		if !strings.Contains(step, "scripts/check-trailers.sh") || !strings.Contains(step, "~/.gemini/config/skills/fyr-handoff") {
			t.Errorf("%s %s does not run check-trailers.sh by its installed path", path, head)
		}
		run, push := strings.Index(step, "check-trailers.sh"), strings.LastIndex(step, "push")
		if run < 0 || push < run {
			t.Errorf("%s %s: the trailer check must come before the push", path, head)
		}
	}
}

// TestWorkflowSkillsCarryNoStaleNames pins Phase 3's grep expects (WS23,
// WS24, WS27, WS28): the agent format of ADR-062, the global context's
// section name, one CLI, and one Session form.
func TestWorkflowSkillsCarryNoStaleNames(t *testing.T) {
	gone := map[string][]string{
		"skills/fyr-retro/SKILL.md":                    {"`agents/<name>.md`", "`.agents/agents/<name>.md`"},
		"skills/fyr-harness-audit/references/areas.md": {"derive.go", "`agents/*.md`"},
		"skills/fyr-handoff/SKILL.md":                  {"Resume Protocol", "either vendor", "Session ID"},
		"skills/review-standards/SKILL.md":             {"the six criteria below"},
		"skills/fyr-review/SKILL.md":                   {"all six criteria"},
		"skills/fyr-preflight/SKILL.md":                {"Step 2 item 1"},
		"skills/fyr-feature/SKILL.md":                  {"a tool-call budget the dispatch names"},
		"skills/fyr-bug/SKILL.md":                      {"a tool-call budget the dispatch names"},
	}
	for path, words := range gone {
		text := readSkill(t, path)
		for _, w := range words {
			if strings.Contains(text, w) {
				t.Errorf("%s still says %s", path, w)
			}
		}
	}
	const form = "<first 8 chars of the conversation id>"
	for _, path := range []string{"skills/fyr-handoff/SKILL.md", "skills/fyr-feedback-log/SKILL.md", "skills/fyr-retro/SKILL.md"} {
		if !strings.Contains(readSkill(t, path), form) {
			t.Errorf("%s does not use the one Session form %s", path, form)
		}
	}
	if !strings.Contains(readSkill(t, "skills/fyr-retro/SKILL.md"), "<name>/agent.md") {
		t.Error("retro's subagent row does not name <name>/agent.md")
	}
}

// TestFeatureSkillEmptyRepoAndWorkersDRow pins Item 18 and I4 of
// PLAN-2026-11-02-1806-fyr-speed: feature Step 1 skips explorer in a
// repository that has no source files yet, and Step 3 shows the workers D-row
// shape that the work skill reads.
func TestFeatureSkillEmptyRepoAndWorkersDRow(t *testing.T) {
	text := readSkill(t, "skills/fyr-feature/SKILL.md")
	step1 := section(t, text, "## Step 1: Scope")
	for _, want := range []string{
		"repository already has source files",
		"no source files yet",
		"skip `explorer`",
		"no source yet",
	} {
		if !strings.Contains(step1, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 1: Scope does not say %q", want)
		}
	}
	step3 := section(t, text, "## Step 3: Cold-reader check")
	for _, want := range []string{
		"D<n> — <date> — workers: yes | no, <why> — by: planner",
	} {
		if !strings.Contains(step3, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check does not say %q", want)
		}
	}
}

// TestColdReaderBudget pins Item 19 of PLAN-2026-11-02-1806-fyr-speed: the
// plan's cold reader gets a 15-tool-call budget and a 10-finding/question
// output cap (< 80 lines), and skips review-standards code-diff references.
func TestColdReaderBudget(t *testing.T) {
	step3 := section(t, readSkill(t, "skills/fyr-feature/SKILL.md"), "## Step 3: Cold-reader check")
	for _, want := range []string{
		"a budget of 15 tool calls",
		"at most 10 findings and questions",
		"under 80 lines",
	} {
		if !strings.Contains(step3, want) {
			t.Errorf("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check does not say %q", want)
		}
	}
	if strings.Contains(step3, "a budget of 30 tool calls") {
		t.Error("skills/fyr-feature/SKILL.md ## Step 3: Cold-reader check still says 30 tool calls")
	}

	rev := readSkill(t, "agents/reviewer/agent.md")
	for _, want := range []string{
		"15 tool calls",
		"10 findings and questions",
		"under 80 lines",
		"skip on a plan cold-read",
	} {
		if !strings.Contains(rev, want) {
			t.Errorf("agents/reviewer/agent.md does not say %q", want)
		}
	}
}

// TestResumeAndGitCheckAndSingleCommit pins Items 20, 28, and 29 of
// PLAN-2026-11-02-1806-fyr-speed: context/AGENTS.md uses fyr resume and fyr
// git-check, and context/AGENTS.md, skills/fyr-feature/SKILL.md, and
// skills/fyr-handoff/SKILL.md commit, push, and ingest once per conversation.
func TestResumeAndGitCheckAndSingleCommit(t *testing.T) {
	ctx := readSkill(t, "context/AGENTS.md")
	for _, want := range []string{
		"`fyr resume`",
		"`fyr git-check`: an unnamed path, commit or tag is a stop sign.",
		"commits, pushes and runs `fyr metrics ingest` once",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context/AGENTS.md does not say %q", want)
		}
	}
	for _, gone := range []string{
		"as three separate commands, never joined on one line",
		"(Checkpoint box, `change` record, commit, push, `fyr metrics ingest`)",
	} {
		if strings.Contains(ctx, gone) {
			t.Errorf("context/AGENTS.md still says %q", gone)
		}
	}

	feat := readSkill(t, "skills/fyr-feature/SKILL.md")
	if strings.Contains(feat, "commit and push as separate calls, then the handoff") {
		t.Error("skills/fyr-feature/SKILL.md still commits and pushes before the handoff")
	}
	if !strings.Contains(feat, "commits, pushes and runs `fyr metrics ingest` once") {
		t.Error("skills/fyr-feature/SKILL.md does not state that /fyr-handoff commits, pushes and ingests once")
	}

	handoff := section(t, readSkill(t, "skills/fyr-handoff/SKILL.md"), "## Step 6")
	if !strings.Contains(handoff, "one commit, one push and one `fyr metrics ingest` per conversation") {
		t.Error("skills/fyr-handoff/SKILL.md Step 6 does not state one commit, push and ingest per conversation")
	}
}

func TestCombinedContextRulesAndRepoAgentsUnder15KB(t *testing.T) {
	const budget = 15000
	var paths []string
	for _, pat := range []string{"context/*.md", "rules/*.md"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
	}
	paths = append(paths, "AGENTS.md")
	var total int
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		total += len(b)
	}
	if total == 0 || total > budget {
		t.Fatalf("combined context/*.md + rules/*.md + AGENTS.md = %d B, want 1..%d B", total, budget)
	}
	t.Logf("combined context/*.md + rules/*.md + AGENTS.md = %d B (budget %d B)", total, budget)
}

func parseSkillFrontmatterDescription(t *testing.T, path string) string {
	t.Helper()
	raw := readSkill(t, path)
	if !strings.HasPrefix(raw, "---\n") {
		t.Fatalf("%s: missing opening frontmatter fence", path)
	}
	front, _, ok := strings.Cut(raw[4:], "\n---\n")
	if !ok {
		t.Fatalf("%s: missing closing frontmatter fence", path)
	}
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		rest, ok := strings.CutPrefix(lines[i], "description:")
		if !ok {
			continue
		}
		val := strings.TrimSpace(rest)
		if val == "|" || val == "|-" || val == ">" || val == ">-" {
			var cont []string
			for j := i + 1; j < len(lines) && (strings.HasPrefix(lines[j], "  ") || lines[j] == ""); j++ {
				if trimmed := strings.TrimSpace(lines[j]); trimmed != "" {
					cont = append(cont, trimmed)
				}
			}
			return strings.Join(cont, " ")
		}
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			if u, err := strconv.Unquote(val); err == nil {
				return u
			}
		}
		if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
			return strings.ReplaceAll(val[1:len(val)-1], "''", "'")
		}
		return val
	}
	t.Fatalf("%s: missing description in frontmatter", path)
	return ""
}

func TestSkillFrontmatterDescriptionsUnder140Chars(t *testing.T) {
	const maxChars = 140
	skills, err := filepath.Glob("skills/*/SKILL.md")
	if err != nil || len(skills) == 0 {
		t.Fatalf("glob skills/*/SKILL.md: %v (n=%d)", err, len(skills))
	}
	for _, p := range skills {
		desc := parseSkillFrontmatterDescription(t, p)
		if desc == "" {
			t.Errorf("%s: empty description", p)
		}
		if n := utf8.RuneCountInString(desc); n > maxChars || len(desc) > maxChars {
			t.Errorf("%s: description is %d chars (%d bytes), want <= %d: %q", p, n, len(desc), maxChars, desc)
		}
	}
}

func TestNoForbiddenStaticCodenamesOrWarningEmoji(t *testing.T) {
	for _, root := range []string{"context", "rules", "agents", "skills"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(b)
			for _, bad := range []string{"Claude Opus 5.5", "⚠"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s contains forbidden token %q", path, bad)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	searchGoRe := regexp.MustCompile(`internal/search/[^` + "`" + `\s)]*\.go`)
	for _, path := range []string{"skills/fyr-review/SKILL.md", "skills/fyr-swarm/SKILL.md"} {
		text := readSkill(t, path)
		if m := searchGoRe.FindString(text); m != "" {
			t.Errorf("%s leaks internal/search implementation path %q", path, m)
		}
		if strings.Contains(text, "gpt-oss") {
			t.Errorf("%s contains stale static codename \"gpt-oss\"", path)
		}
	}
}

type skillTextCase struct {
	name, path, sec string
	want, forbid    []string
}

func assertSkillTextCases(t *testing.T, cases []skillTextCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := readSkill(t, tc.path)
			if tc.sec != "" {
				text = section(t, text, tc.sec)
			}
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("%s missing %q", tc.path, w)
				}
			}
			for _, bad := range tc.forbid {
				if strings.Contains(text, bad) {
					t.Errorf("%s still contains %q", tc.path, bad)
				}
			}
		})
	}
}

// TestV910SkillTemplatesAndR7Alignment pins Phase 16 (F-041, F-015 / D10 / ADR-534):
// runnable distroless + imagetools inspect in docker, two-stage first-apply bootstrap
// in terraform, and PRD.md R7 single-handoff close (optional change) across context,
// fyr-bug, and fyr-handoff.
func TestV910SkillTemplatesAndR7Alignment(t *testing.T) {
	assertSkillTextCases(t, []skillTextCase{
		{
			name:   "docker-distroless-runnable",
			path:   "skills/docker/SKILL.md",
			want:   []string{"gcr.io/distroless/static-debian12:nonroot", "docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot"},
			forbid: []string{"@sha256:<digest>"},
		},
		{
			name:   "terraform-first-apply-bootstrap",
			path:   "skills/terraform/SKILL.md",
			want:   []string{"two-stage bootstrap", "placeholder", "ignore_changes = [template[0].containers[0].image]", "var.git_sha"},
			forbid: []string{"from `var.git_sha`"},
		},
		{
			name:   "context-r7-single-handoff",
			path:   "context/AGENTS.md",
			want:   []string{"(`change` optional)", "self-review small diffs in `handoff`", "batch `adr`/`bug`"},
			forbid: []string{"store `change` and run `/fyr-handoff`", "self-review small diffs in `change`", "batch `change`/`adr`/`bug`"},
		},
		{
			name:   "fyr-bug-r7-single-handoff",
			path:   "skills/fyr-bug/SKILL.md",
			sec:    "## Step 5",
			want:   []string{"single `handoff` record", "optional"},
			forbid: []string{"Store `change` (`fyr record add change"},
		},
		{
			name:   "fyr-handoff-r7-single-handoff",
			path:   "skills/fyr-handoff/SKILL.md",
			want:   []string{"one `handoff` record", "`fyr record add change` is optional"},
			forbid: []string{"no `change` record covers those changes, store one **before** the handoff", "the checkpoint's `change` record, commit and push", "the `change` record, the `handoff` record", "with no `change` record covering it"},
		},
	})
}
