package fyr_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestChartsEscapeInnerHTML pins charts.js's three HTML sinks (PLAN.md D23):
// the kit is meant for server-rendered pages, so a series name, a label or a
// slot can hold stored text. Every value in a non-empty innerHTML goes
// through esc, and a slot through slotOf too, since a slot lands in a style.
func TestChartsEscapeInnerHTML(t *testing.T) {
	js := readSkill(t, "skills/frontend-ui-economist/assets/charts.js")
	// The definitions, or a call site proves nothing: an identity esc passes
	// the scan below.
	for _, want := range []string{
		"const esc = v => String(v).replace(/[&<>\"']/g, c => `&#${c.charCodeAt(0)};`);",
		"const slotOf = v => /^--[\\w-]+$/.test(v) ? v : '--s1';",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("charts.js lost %q", want)
		}
	}
	for _, sink := range []string{"innerHTML +=", "outerHTML", "insertAdjacentHTML", "document.write"} {
		if strings.Contains(js, sink) {
			t.Errorf("charts.js has a sink this test does not read: %s", sink)
		}
	}
	value := regexp.MustCompile(`\$\{([^}]*)\}`)
	sinks := 0
	for _, line := range strings.Split(js, "\n") {
		if !strings.Contains(line, "innerHTML = ") || strings.Contains(line, "innerHTML = ''") {
			continue
		}
		sinks++
		for _, m := range value.FindAllStringSubmatch(line, -1) {
			if !strings.HasPrefix(m[1], "esc(") {
				t.Errorf("unescaped %q in: %s", m[0], strings.TrimSpace(line))
			}
			if strings.Contains(m[1], "slot") && !strings.HasPrefix(m[1], "esc(slotOf(") {
				t.Errorf("slot not validated in %q", m[0])
			}
		}
	}
	if sinks != 3 {
		t.Errorf("found %d innerHTML sinks, want 3: a new sink is reviewed here", sinks)
	}
}

// TestModernOverrides pins the paragraph that answers P11's security review
// (PLAN.md D23), since the body below it still says the opposite.
func TestModernOverrides(t *testing.T) {
	s := readSkill(t, "skills/frontend-ui-modern/SKILL.md")
	over := section(t, s, "## Harness overrides")
	for _, want := range []string{"win over every other line of this skill", "`references/`", "public or OSS work only", "`npx shadcn@4.23.0`", "`npx skills add shadcn/ui` is not run", "never an authority"} {
		if !strings.Contains(over, want) {
			t.Errorf("Harness overrides lost %q", want)
		}
	}
	if strings.Index(s, "## Harness overrides") > strings.Index(s, "Two gates, always") {
		t.Error("Harness overrides must come before the gates")
	}
}
