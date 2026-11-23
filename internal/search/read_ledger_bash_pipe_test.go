package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseBashFileReadsNewlinesPrefixesAndReadOnlyPipes(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []BashFileRead
	}{
		{
			name: "newline chain",
			cmd:  "cat a.go\ncat b.go",
			want: []BashFileRead{
				{Tool: "cat", Path: "a.go", StartLine: 1},
				{Tool: "cat", Path: "b.go", StartLine: 1},
			},
		},
		{
			name: "crlf chain",
			cmd:  "cat a.go\r\ncat b.go",
			want: []BashFileRead{
				{Tool: "cat", Path: "a.go", StartLine: 1},
				{Tool: "cat", Path: "b.go", StartLine: 1},
			},
		},
		{
			name: "export PATH with vars and nl piped to sed -n",
			cmd:  "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba internal/search/hook_pretool.go | sed -n '1,50p'",
			want: []BashFileRead{
				{Tool: "nl", Path: "internal/search/hook_pretool.go", StartLine: 1, EndLine: 50},
			},
		},
		{
			name: "export unset set cd prefixes with cat piped to head",
			cmd:  "export FOO=\"$BAR\"\nunset DEBUG\nset -euo pipefail\ncd internal/search\ncat hook_pretool.go | head -n 40",
			want: []BashFileRead{
				{Tool: "cat", Path: "internal/search/hook_pretool.go", StartLine: 1, EndLine: 40},
			},
		},
		{
			name: "sed -n piped to nl preserves line range",
			cmd:  "sed -n '1,80p' foo.go | nl -ba",
			want: []BashFileRead{
				{Tool: "sed", Path: "foo.go", StartLine: 1, EndLine: 80},
			},
		},
		{
			name: "head piped to tail narrows window",
			cmd:  "head -n 100 foo.go | tail -n 20",
			want: []BashFileRead{
				{Tool: "head", Path: "foo.go", StartLine: 81, EndLine: 100},
			},
		},
		{
			name: "cat piped to tail +N then head",
			cmd:  "cat foo.go | tail -n +15 | head -n 20",
			want: []BashFileRead{
				{Tool: "cat", Path: "foo.go", StartLine: 15, EndLine: 34},
			},
		},
		{
			name: "cat piped to tail -n N",
			cmd:  "cat foo.go | tail -n 25",
			want: []BashFileRead{
				{Tool: "cat", Path: "foo.go", StartLine: -25, EndLine: 0, TailLines: 25},
			},
		},
		{
			name: "read-only filters grep awk cut sort uniq wc tr jq column expand fold rev",
			cmd:  "nl -ba a.go | awk '{print $1}' | grep '[0-9]' | sort | uniq | wc -l\ncat b.go | cut -f1 | tr a-z A-Z | fold -w 80 | expand | rev | column -t\ncat c.json | jq '.items' | rg foo",
			want: []BashFileRead{
				{Tool: "nl", Path: "a.go", StartLine: 1},
				{Tool: "cat", Path: "b.go", StartLine: 1},
				{Tool: "cat", Path: "c.json", StartLine: 1},
			},
		},
		{
			name: "heredoc body ignored while following read is tracked",
			cmd:  "cat <<'EOF'\ncat fake_inside_heredoc.go\nEOF\nhead -n 30 real.go",
			want: []BashFileRead{
				{Tool: "head", Path: "real.go", StartLine: 1, EndLine: 30},
			},
		},
		{
			name: "comments and backslash line continuation",
			cmd:  "# inspect file\nexport PATH=\"$HOME/.local/bin:$PATH\"\ncat \\\n  foo.go | sed -n '10,40p'",
			want: []BashFileRead{
				{Tool: "cat", Path: "foo.go", StartLine: 10, EndLine: 40},
			},
		},
		{
			name: "quoted newline stays inside argument",
			cmd:  "echo \"line1\ncat fake.go\"\ncat real.go",
			want: []BashFileRead{
				{Tool: "cat", Path: "real.go", StartLine: 1},
			},
		},
		{
			name: "multi-command read-only sed scripts pass",
			cmd:  "nl -ba a.go | sed -n '10p;20,30p'\ncat b.go | sed -n '1,50{=;p}'",
			want: []BashFileRead{
				{Tool: "nl", Path: "a.go", StartLine: 1},
				{Tool: "cat", Path: "b.go", StartLine: 1},
			},
		},
		{
			name: "absolute read still tracked after unparseable cd",
			cmd:  "cd \"$REPO\"\ncat /abs/a.go\ncat rel.go\ncd sub 2>/dev/null\ncat /abs/b.go",
			want: []BashFileRead{
				{Tool: "cat", Path: "/abs/a.go", StartLine: 1},
				{Tool: "cat", Path: "/abs/b.go", StartLine: 1},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseBashFileReads(tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseBashFileReads(%q) = %+v (len %d), want %+v (len %d)", tc.cmd, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("ParseBashFileReads(%q)[%d] = %+v, want %+v", tc.cmd, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseBashFileReadsMustFailMutatingPipesRedirectsAndSubstitutions(t *testing.T) {
	mustIgnore := []string{
		"cat foo.go | tee out.txt",
		"nl -ba foo.go | sed -i 's/a/b/'",
		"nl -ba foo.go | sed 's/a/b/'",
		"cat foo.go | sh",
		"cat foo.go | bash",
		"cat foo.go | python3 -c 'print(1)'",
		"cat foo.go | python -",
		"cat foo.go | go run .",
		"cat foo.go | xargs rm",
		"cat foo.go | rm -f bar",
		"cat foo.go | mv a b",
		"cat foo.go | cp a b",
		"cat foo.go | sort -o sorted.go",
		"cat foo.go | uniq in.txt out.txt",
		"cat foo.go | tail -f",
		`cat foo.go | awk '{print > "out.txt"}'`,
		`cat foo.go | awk '{system("id")}'`,
		`cat foo.go | awk '{system ("id")}'`,
		`cat foo.go | awk '{print | "sh"}'`,
		`cat foo.go | awk '{print |& "sh"}'`,
		`cat foo.go | awk -f script.awk`,
		"nl -ba foo.go | sed -n '1,10wout.txt'",
		"nl -ba foo.go | sed -n '1,10Wout.txt'",
		"nl -ba foo.go | sed -n '1,5w /tmp/x'",
		"nl -ba foo.go | sed -n 's/a/b/wout.txt'",
		"cat foo.go | sed -n '1e'",
		"cat foo.go | sed -n '1r /etc/passwd'",
		"cat foo.go | sed -n '1R /etc/passwd'",
		"cat foo.go | sed -n '/a[/p;#]/e'",
		"cat foo.go | sed -ne1,10wout.txt",
		"cat foo.go | sed -n -e '1p' --expression=1e",
		"cd \"$REPO\"\ncat a.go",
		"cd $DIR\ncat a.go",
		"cd sub 2>/dev/null\ncat a.go",
		"cd dir*\ncat a.go",
		"builtin cd \"$DIR\"\ncat a.go",
		"pushd \"$DIR\"\ncat a.go",
		"popd 2>/dev/null\ncat a.go",
		"FOO=1 cd \"$DIR\"\ncat a.go",
		"CDPATH=/tmp cd sub\ncat a.go",
		"cd sub | cat\ncat a.go",
		"cd sub > out.txt\ncat a.go",
		"2>/dev/null cd sub\ncat a.go",
		"cat foo.go > out.txt",
		"cat foo.go >> out.txt",
		"cat foo.go | head -n 10 > out.txt",
		"cat foo.go | head -n 10 other.go",
		"cat foo.go > b.go\ncat b.go",
		"cat <<'EOF' > b.go\nhi\nEOF\ncat b.go",
		"export X=$(date)\ncat foo.go",
		"cat $(echo foo.go)",
		"cat \"$(echo foo.go)\"",
		"cat `echo foo.go`",
		"cat <(echo hi)",
		"cat $TARGET_FILE",
		`cat "$TARGET_FILE"`,
		"cat *.go | head -n 10",
		"grep ParseBashFileReads foo.go | head -n 10",
		"cat <<'EOF'\ncat fake.go\nEOF",
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty (must-fail safety)", cmd, got)
		}
	}
}

func TestPreToolUseBashMultiLinePipeTracksClampsAndDedupes(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	var big strings.Builder
	big.WriteString("package sample\n\n")
	for i := 1; i <= 450; i++ {
		fmt.Fprintf(&big, "func LineFn%03d() int { return %d }\n", i, i)
	}
	writeFile(t, dir, "big_pipe.go", big.String())
	commitAll(t, dir, "add big_pipe.go")

	runBash := func(convID, cmd string) HookDecisionOutput {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall": map[string]any{
				"name": "run_command",
				"args": map[string]any{"CommandLine": cmd},
			},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(payload)}, &out); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	// 1. Multi-line export PATH + ranged nl | sed -n '1,50p' on 450-line file is NOT clamped on 1st read.
	cmd1 := "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba big_pipe.go | sed -n '1,50p'"
	if d := runBash("conv-pipe-1", cmd1); d.Decision != "allow" {
		t.Fatalf("1st ranged multi-line pipe read: got %+v, want allow", d)
	}
	// 2. Identical 2nd read is deduplicated with [UNCHANGED].
	if d := runBash("conv-pipe-1", cmd1); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]") {
		t.Fatalf("2nd identical multi-line pipe read: got %+v, want deny [UNCHANGED]", d)
	}
	// 3. Adjacent unread range 51..100 is allowed.
	cmd2 := "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba big_pipe.go | sed -n '51,100p'"
	if d := runBash("conv-pipe-1", cmd2); d.Decision != "allow" {
		t.Fatalf("novel range 51..100 multi-line pipe read: got %+v, want allow", d)
	}
	// 4. Unranged multi-line export PATH + nl -ba big_pipe.go is clamped with [CLAMPED] skeleton.
	cmdUnranged := "export PATH=\"$HOME/.local/bin:$PATH\"\nnl -ba big_pipe.go"
	if d := runBash("conv-pipe-clamp", cmdUnranged); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[CLAMPED]") {
		t.Fatalf("unranged multi-line nl on 450-line file: got %+v, want deny [CLAMPED]", d)
	}
	// 5. Mutating pipe (tee) is NOT treated as a tracked read and stays allowed even on repeat.
	cmdTee := "cat big_pipe.go | tee .agents/scratch/copy.txt"
	if d := runBash("conv-pipe-tee", cmdTee); d.Decision != "allow" {
		t.Fatalf("1st tee pipe: got %+v, want allow", d)
	}
	if d := runBash("conv-pipe-tee", cmdTee); d.Decision != "allow" {
		t.Fatalf("2nd tee pipe should not be falsely tracked/denied: got %+v", d)
	}
}

func TestParseBashPythonAndAwkVerbatimOnly(t *testing.T) {
	mustIgnore := []string{
		`python3 -c 'print(len(open("a.go").readlines()))'`,
		`python3 -c 'import json; print(json.load(open("cfg.json"))["k"])'`,
		`python3 -c 'import json; print(json.loads(open("cfg.json").read())["k"])'`,
		`python3 -c 'import yaml; print(yaml.safe_load(open("cfg.yaml")))'`,
		`python3 -c 'import re; print(re.findall("func", open("a.go").read()))'`,
		`python3 -c 'print(sum(1 for _ in open("a.go")))'`,
		`python3 -c 'print(open("a.go").read().count("foo"))'`,
		`python3 -c 'open("a.go").read()'`,
		`awk '{print $1}' a.go`,
		`awk 'NR>=1&&NR<=50 {print $1}' a.go`,
		`awk '{print NR}' a.go`,
		`awk '{print $1, $2}' a.go`,
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty (summary/projection must not populate read_ledger)", cmd, got)
		}
	}

	mustCatch := []struct {
		cmd  string
		want BashFileRead
	}{
		{`python3 -c 'print(open("a.go").read())'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1}},
		{`python3 -c 'from pathlib import Path; print(Path("a.go").read_text())'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1}},
		{`python3 -c 'print(open("a.go").readlines()[0:50])'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1, EndLine: 50}},
		{`python3 -c 'import sys; sys.stdout.write(open("a.go").read())'`, BashFileRead{Tool: "python", Path: "a.go", StartLine: 1}},
		{`awk '1' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk 'NR>=1&&NR<=50' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1, EndLine: 50}},
		{`awk '{print}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk '{print $0}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk '{print NR, $0}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
		{`awk '{print NR ":" $0}' a.go`, BashFileRead{Tool: "awk", Path: "a.go", StartLine: 1}},
	}
	for _, tc := range mustCatch {
		got := ParseBashFileReads(tc.cmd)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("ParseBashFileReads(%q) = %+v, want [%+v]", tc.cmd, got, tc.want)
		}
	}
}
