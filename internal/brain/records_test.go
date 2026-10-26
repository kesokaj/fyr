package brain

import (
	"strings"
	"testing"
)

// add prints the ID, get returns header and body, list names it, and edit
// without a body keeps the body (D9) while edit with one replaces it.
func TestAddGetEditList(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	id := add(t, o, "bug", "trailer slipped into a commit", "Symptom: a trailer.\n\nRule: check the message.\n")
	if id != "BUG-001" {
		t.Fatalf("first bug id %q, want BUG-001", id)
	}
	recs, missing, err := Get(o, []string{id})
	if err != nil || len(missing) != 0 || len(recs) != 1 {
		t.Fatalf("get: %v %v %v", recs, missing, err)
	}
	r := recs[0]
	if r.Kind != "bug" || r.Status != "open" || r.Date != "2026-10-25" || r.Title != "trailer slipped into a commit" {
		t.Errorf("record = %+v", r)
	}
	if r.Body != "Symptom: a trailer.\n\nRule: check the message." {
		t.Errorf("body = %q", r.Body)
	}
	if err := Edit(o, EditInput{ID: id, Status: str("fixed"), Title: str("trailer in a commit")}); err != nil {
		t.Fatal(err)
	}
	recs, _, _ = Get(o, []string{id})
	if recs[0].Status != "fixed" || recs[0].Title != "trailer in a commit" || recs[0].Body != r.Body {
		t.Errorf("edit without --body changed the body or missed metadata: %+v", recs[0])
	}
	if err := Edit(o, EditInput{ID: id, Body: strings.NewReader("Rule: new.\n")}); err != nil {
		t.Fatal(err)
	}
	recs, _, _ = Get(o, []string{id})
	if recs[0].Body != "Rule: new." {
		t.Errorf("edit --body: %q", recs[0].Body)
	}
	list, err := List(o, ListFilter{})
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %v %v", list, err)
	}
	if got := FormatLine(list[0], false); got != "BUG-001  bug  fixed  2026-10-25  trailer in a commit" {
		t.Errorf("list line %q", got)
	}
	_, missing, err = Get(o, []string{"BUG-404"})
	if !IsRefusal(err) || len(missing) != 1 {
		t.Errorf("get of a missing id: %v %v, want a refusal naming it", missing, err)
	}
	if err := Edit(o, EditInput{ID: "BUG-404", Status: str("fixed")}); !IsRefusal(err) {
		t.Errorf("edit of a missing id: %v, want a refusal", err)
	}
}

// An empty body is refused (D9), a body over 1 MiB is refused, and one of
// exactly 1 MiB is taken.
func TestBodyLimits(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for name, body := range map[string]string{"empty": "", "blank": " \n\t\n", "over 1 MiB": strings.Repeat("a", 1<<20+1)} {
		if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader(body)}); !IsRefusal(err) {
			t.Errorf("%s body: %v, want a refusal", name, err)
		}
	}
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader(strings.Repeat("a", 1<<20))}); err != nil {
		t.Errorf("a body of exactly 1 MiB: %v", err)
	}
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("a\x00b")}); !IsRefusal(err) {
		t.Errorf("a NUL in the body: %v, want a refusal", err)
	}
	id := add(t, o, "learning", "keep", "kept")
	if err := Edit(o, EditInput{ID: id, Body: strings.NewReader("\n")}); !IsRefusal(err) {
		t.Errorf("edit --body with an empty body: %v, want a refusal", err)
	}
}

// feedback and proposal are global only (D10, R8): refused in a project,
// taken with --global, and never in the export.
func TestGlobalOnlyKinds(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	for _, k := range []string{"feedback", "proposal"} {
		if _, err := Add(o, AddInput{Kind: k, Title: "t", Body: strings.NewReader("b")}); !IsRefusal(err) {
			t.Errorf("%s in a project: %v, want a refusal", k, err)
		}
	}
	g := o
	g.Global = true
	add(t, g, "feedback", "use the verbs", "said so", "project", dir)
	add(t, g, "proposal", "a rule", "draft", "project", dir)
	if _, err := Add(g, AddInput{Kind: "memory", Title: "t", Body: strings.NewReader("b")}); !IsRefusal(err) {
		t.Errorf("memory --global: %v, want a refusal (memory is project-scoped, D14)", err)
	}
	if _, err := Add(o, AddInput{Kind: "learning", Title: "t", Project: dir, Body: strings.NewReader("b")}); !IsUsage(err) {
		t.Errorf("--project without --global: %v, want a usage error", err)
	}
	list, err := List(g, ListFilter{Kind: "proposal", Project: dir})
	if err != nil || len(list) != 1 || list[0].Status != "queued" || list[0].Project != dir {
		t.Fatalf("list --global --kind proposal --project: %+v %v", list, err)
	}
	add(t, o, "learning", "project one", "b")
	if s := trailText(t, dir); strings.Contains(s, "feedback") || strings.Contains(s, "proposal") {
		t.Errorf("a global record reached the export:\n%s", s)
	}
}

// Status per kind (D6): defaults, the allowed values, plan derived from its
// body, handoff with none.
func TestStatusRules(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	g := o
	g.Global = true
	for _, tc := range []struct {
		kind, status, body string
		global             bool
		want               string // "" with ok=false: refused
		ok                 bool
	}{
		{"bug", "", "b", false, "open", true},
		{"bug", "graduated", "b", false, "graduated", true},
		{"bug", "closed", "b", false, "", false},
		{"adr", "", "b", false, "accepted", true},
		{"adr", "superseded:ADR-047a", "b", false, "superseded:ADR-047a", true},
		{"adr", "superseded:soon", "b", false, "", false},
		{"plan", "", "- [x] done\n- [ ] next\n", false, "open", true},
		{"plan", "", "- [x] done\n```\n- [ ] in a fence\n```\n", false, "closed", true},
		{"plan", "open", "- [ ] x\n", false, "", false},
		{"spec", "", "b", false, "current", true},
		{"guide", "retired", "b", false, "retired", true},
		{"deploy", "open", "b", false, "", false},
		{"proposal", "", "b", true, "queued", true},
		{"proposal", "applied:3797da5", "b", true, "applied:3797da5", true},
		{"proposal", "applied:HEAD", "b", true, "", false},
		{"handoff", "", "b", false, "", true},
		{"handoff", "closed", "b", false, "", false},
		{"review", "", "b", false, "closed", true},
		{"review", "open", "b", false, "", false},
		{"memory", "", "b", false, "closed", true},
	} {
		oo := o
		if tc.global {
			oo = g
		}
		id, err := Add(oo, AddInput{Kind: tc.kind, Title: tc.kind + " " + tc.status, Status: tc.status, Body: strings.NewReader(tc.body)})
		if !tc.ok {
			if err == nil {
				t.Errorf("%s --status %q: taken as %s, want refused", tc.kind, tc.status, id)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s --status %q: %v", tc.kind, tc.status, err)
			continue
		}
		recs, _, err := Get(oo, []string{id})
		if err != nil || recs[0].Status != tc.want {
			t.Errorf("%s --status %q: status %q, want %q (%v)", tc.kind, tc.status, recs[0].Status, tc.want, err)
		}
	}
	// A plan's status follows its body on edit, and --status is refused.
	id := add(t, o, "plan", "the plan", "- [ ] one\n")
	if err := Edit(o, EditInput{ID: id, Body: strings.NewReader("- [x] one\n")}); err != nil {
		t.Fatal(err)
	}
	if recs, _, _ := Get(o, []string{id}); recs[0].Status != "closed" {
		t.Errorf("plan after ticking its last box: %q, want closed", recs[0].Status)
	}
	if err := Edit(o, EditInput{ID: id, Status: str("open")}); err == nil {
		t.Error("edit --status on a plan was taken")
	}
	open, err := List(o, ListFilter{Open: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range open {
		switch {
		case r.Kind == "bug" && r.Status == "open", r.Kind == "plan" && r.Status == "open",
			(r.Kind == "spec" || r.Kind == "guide" || r.Kind == "deploy") && r.Status == "current":
		default:
			t.Errorf("--open listed %s %s %s", r.ID, r.Kind, r.Status)
		}
	}
}
