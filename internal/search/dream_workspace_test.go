package search

import (
	"os"
	"path/filepath"
	"testing"
)

// workspaces.judge keeps a conversation whose every file workspace is root,
// a directory in root's checkout, or one since deleted below it; it leaves
// out, and names, one in a checkout nested under root or one that also has a
// workspace elsewhere, a remote or unparsable one included; another project's, a sibling with root's name as a
// prefix, a non-file URI and bad JSON are left out unnamed (ledger item 77).
func TestWorkspacesJudge(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	nested := filepath.Join(root, "vendor", "nested")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, nested, "init", "-q")
	u := func(p string) string { return `"file://` + p + `"` }
	w := newWorkspaces(root, nil)
	for _, c := range []struct {
		uris string
		keep bool
		why  string
	}{
		{"[" + u(root) + "]", true, ""},
		{"[" + u(filepath.Join(root, "sub")) + "]", true, ""},
		{"[" + u(filepath.Join(root, "gone", "deeper")) + "]", true, ""},
		{"[" + u(nested) + "]", false, whyNested},
		{"[" + u(filepath.Join(nested, "pkg")) + "]", false, whyNested},
		{"[" + u(root) + "," + u(nested) + "]", false, whyNested},
		{"[" + u("/elsewhere") + "," + u(root) + "]", false, whyOutside},
		{"[" + u("/elsewhere") + "]", false, ""},
		{"[" + u(root+"-other") + "]", false, ""},
		{`["https://` + root + `"]`, false, ""},
		{"[" + u(root) + `,"vscode-remote://ssh-remote+h/srv/x"]`, false, whyOutside},
		{"[" + u(root) + `,"%zz"]`, false, whyOutside},
		{"not json", false, ""},
	} {
		if keep, why := w.judge(c.uris); keep != c.keep || why != c.why {
			t.Errorf("%s: keep %v why %q, want %v %q", c.uris, keep, why, c.keep, c.why)
		}
	}
	// A non-git directory keeps its own workspaces and subdirectories, while
	// still excluding nested git checkouts and outside workspaces.
	plain := t.TempDir()
	plainNested := filepath.Join(plain, "nested-git")
	if err := os.MkdirAll(filepath.Join(plain, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(plainNested, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, plainNested, "init", "-q")
	pw := newWorkspaces(plain, nil)
	for _, c := range []struct {
		uris string
		keep bool
		why  string
	}{
		{"[" + u(plain) + "]", true, ""},
		{"[" + u(filepath.Join(plain, "sub")) + "]", true, ""},
		{"[" + u(filepath.Join(plain, "gone", "deeper")) + "]", true, ""},
		{"[" + u(plainNested) + "]", false, whyNested},
		{"[" + u("/elsewhere") + "," + u(plain) + "]", false, whyOutside},
	} {
		if keep, why := pw.judge(c.uris); keep != c.keep || why != c.why {
			t.Errorf("plain %s: keep %v why %q, want %v %q", c.uris, keep, why, c.keep, c.why)
		}
	}

	// GlobalWorkspace matches only empty workspace_uris ([] or "").
	gw := newWorkspaces(GlobalWorkspace, nil)
	for _, c := range []struct {
		uris string
		keep bool
	}{
		{"[]", true},
		{"", true},
		{"[" + u(root) + "]", false},
		{`["vscode-remote://ssh-remote+h/srv/x"]`, false},
		{"not json", false},
	} {
		if keep, why := gw.judge(c.uris); keep != c.keep || why != "" {
			t.Errorf("global %q: keep %v why %q, want %v %q", c.uris, keep, why, c.keep, "")
		}
	}
}
