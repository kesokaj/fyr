package check

import (
	"reflect"
	"sort"
	"testing"
)

func TestCollectFlagTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "double-dash lower and mixed case",
			text: "Use `--dry-run`, `--FlagName`, `--camelCase=1`, and (--HTTP2-port).",
			want: []string{"FlagName", "HTTP2-port", "camelCase", "dry-run"},
		},
		{
			name: "single-dash short and named flags",
			text: "Run `fyr -C /tmp -x -v` or [-N|--FlagName] and -h/--help and -n=5.",
			want: []string{"C", "FlagName", "N", "h", "help", "n", "v", "x"},
		},
		{
			name: "trailing single-dash flag at EOF",
			text: "fyr -x",
			want: []string{"x"},
		},
		{
			name: "markdown italic and bold flags",
			text: "*--flag*, **-x**, and **--FlagName** plus (*--nested*).",
			want: []string{"FlagName", "flag", "nested", "x"},
		},
		{
			name: "must-not-match markdown emphasized hyphenated prose",
			text: "Use *fyr*-based and **fyr**-based or ***v9***-based workflows.",
			want: nil,
		},
		{
			name: "must-not-match negative numbers and hyphenated prose",
			text: "A well-known read-only single-dash v9-11-0 score is -1, -42, -0.5, --99, -1-foo, `fyr`-based, (foo)-bar, [a]-b, foo_bar-baz, ---triple, <!--comment-->, --opt=-val.\n- bullet item\n-- bare separator\n",
			want: []string{"opt"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMap := map[string]bool{}
			collectFlagTokens(tc.text, gotMap)
			var got []string
			for k := range gotMap {
				got = append(got, k)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("collectFlagTokens(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestUndocumentedFlagsMixedCaseAndShortFlag(t *testing.T) {
	v := Vocabulary{
		"help": {Flags: map[string]bool{"home": true, "repo": true, "help": true}},
		"run":  {Flags: map[string]bool{"home": true, "repo": true, "help": true, "FlagName": true, "x": true, "known": true}},
	}
	cmdSrc := "package main\nimport \"flag\"\nfunc reg(fs *flag.FlagSet) {\n\tfs.Bool(\"FlagName\", false, \"f\")\n\tfs.Bool(\"x\", false, \"x\")\n\tfs.Bool(\"known\", false, \"k\")\n}\n"

	// Must-fail: prose hyphenated "well-known" and negative "-1" must not document --known,
	// and undocumented --FlagName / -x are flagged.
	undoc := repo(t, map[string]string{
		"cmd/fyr/main.go": cmdSrc,
		"README.md":       "A well-known limit is -1.\n",
	})
	rep, err := Run(undoc, v)
	if err != nil {
		t.Fatalf("Run undoc: %v", err)
	}
	same(t, rep.Findings,
		"cmd/fyr/main.go:4 flag-undoc",
		"cmd/fyr/main.go:5 flag-undoc",
		"cmd/fyr/main.go:6 flag-undoc",
	)

	// Passing case: --FlagName, -x, and --known documented across README and help_text.
	doc := repo(t, map[string]string{
		"cmd/fyr/main.go":      cmdSrc,
		"cmd/fyr/help_text.go": "package main\nconst help = \"fyr run -x --known\"\n",
		"README.md":            "Pass `--FlagName` to enable.\n",
	})
	repClean, err := Run(doc, v)
	if err != nil {
		t.Fatalf("Run clean: %v", err)
	}
	same(t, repClean.Findings)
}
