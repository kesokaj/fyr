package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseBashFileReadsV910All18Patterns(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []BashFileRead
	}{
		{"01 cat", "cat a.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"02 cat -n", "cat -n a.go", []BashFileRead{{Tool: "cat", Path: "a.go", StartLine: 1}}},
		{"03 head -n", "head -n 50 a.go", []BashFileRead{{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 50}}},
		{"04 head -N", "head -25 a.go", []BashFileRead{{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 25}}},
		{"05 tail -n", "tail -n 30 a.go", []BashFileRead{{Tool: "tail", Path: "a.go", StartLine: -30, TailLines: 30}}},
		{"06 tail +N", "tail -n +15 a.go", []BashFileRead{{Tool: "tail", Path: "a.go", StartLine: 15}}},
		{"07 sed range", "sed -n '1,120p' a.go", []BashFileRead{{Tool: "sed", Path: "a.go", StartLine: 1, EndLine: 120}}},
		{"08 sed quit", "sed -n '10,50p;51q' a.go", []BashFileRead{{Tool: "sed", Path: "a.go", StartLine: 10, EndLine: 50}}},
		{"09 nl", "nl -ba a.go", []BashFileRead{{Tool: "nl", Path: "a.go", StartLine: 1}}},
		{"10 bat range", "bat --paging=never -r 10:80 a.go", []BashFileRead{{Tool: "bat", Path: "a.go", StartLine: 10, EndLine: 80}}},
		{"11a multi-range sed", "sed -n '1,10p;20,30p' a.go", []BashFileRead{
			{Tool: "sed", Path: "a.go", StartLine: 1, EndLine: 10},
			{Tool: "sed", Path: "a.go", StartLine: 20, EndLine: 30},
		}},
		{"11b multi -e sed", "sed -n -e '1,10p' -e '20,30p;31q' a.go", []BashFileRead{
			{Tool: "sed", Path: "a.go", StartLine: 1, EndLine: 10},
			{Tool: "sed", Path: "a.go", StartLine: 20, EndLine: 30},
		}},
		{"12a awk NR range", "awk 'NR>=1 && NR<=10' a.go", []BashFileRead{
			{Tool: "awk", Path: "a.go", StartLine: 1, EndLine: 10},
		}},
		{"12b awk NR range with print", `awk 'NR>=10 && NR<=25 {print NR": "$0}' a.go`, []BashFileRead{
			{Tool: "awk", Path: "a.go", StartLine: 10, EndLine: 25},
		}},
		{"12c awk comma range", "awk 'NR==5,NR==15' a.go", []BashFileRead{
			{Tool: "awk", Path: "a.go", StartLine: 5, EndLine: 15},
		}},
		{"12d awk full print", "awk '{print}' a.go", []BashFileRead{
			{Tool: "awk", Path: "a.go", StartLine: 1},
		}},
		{"13a python3 open", `python3 -c "print(open('a.go').read())"`, []BashFileRead{
			{Tool: "python", Path: "a.go", StartLine: 1},
		}},
		{"13b python3 open slice", `python3 -c "print(''.join(open('a.go').readlines()[10:30]))"`, []BashFileRead{
			{Tool: "python", Path: "a.go", StartLine: 11, EndLine: 30},
		}},
		{"13c python3 Path read_text", `python3 -c "from pathlib import Path; print(Path('sub/b.go').read_text())"`, []BashFileRead{
			{Tool: "python", Path: "sub/b.go", StartLine: 1},
		}},
		{"13d python3 open encoding", `python3 -c "print(open('a.go', encoding='ascii').read())"`, []BashFileRead{
			{Tool: "python", Path: "a.go", StartLine: 1},
		}},
		{"14a grep -n empty", "grep -n '' a.go", []BashFileRead{
			{Tool: "grep", Path: "a.go", StartLine: 1},
		}},
		{"14b grep -n caret", "grep -n '^' a.go", []BashFileRead{
			{Tool: "grep", Path: "a.go", StartLine: 1},
		}},
		{"14c rg -n empty", "rg -n '' a.go", []BashFileRead{
			{Tool: "rg", Path: "a.go", StartLine: 1},
		}},
		{"15a stderr redirect", "cat a.go 2>/dev/null", []BashFileRead{
			{Tool: "cat", Path: "a.go", StartLine: 1},
		}},
		{"15b stderr space and 2>&1", "head -n 20 a.go 2> /dev/null && sed -n '1,10p' b.go 2>&1", []BashFileRead{
			{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 20},
			{Tool: "sed", Path: "b.go", StartLine: 1, EndLine: 10},
		}},
		{"16a trailing or true", "cat a.go || true", []BashFileRead{
			{Tool: "cat", Path: "a.go", StartLine: 1},
		}},
		{"16b combined stderr and or true", "grep -n '' a.go 2>/dev/null || true", []BashFileRead{
			{Tool: "grep", Path: "a.go", StartLine: 1},
		}},
		{"17a multi-file head", "head -n 20 a.go b.go", []BashFileRead{
			{Tool: "head", Path: "a.go", StartLine: 1, EndLine: 20},
			{Tool: "head", Path: "b.go", StartLine: 1, EndLine: 20},
		}},
		{"17b multi-file tail", "tail -n 15 a.go b.go", []BashFileRead{
			{Tool: "tail", Path: "a.go", StartLine: -15, TailLines: 15},
			{Tool: "tail", Path: "b.go", StartLine: -15, TailLines: 15},
		}},
		{"18a simple subshell", "(cd sub && cat a.go)", []BashFileRead{
			{Tool: "cat", Path: "sub/a.go", StartLine: 1},
		}},
		{"18b scoped subshell cd", "(cd sub && head -n 20 a.go) && cat b.go", []BashFileRead{
			{Tool: "head", Path: "sub/a.go", StartLine: 1, EndLine: 20},
			{Tool: "cat", Path: "b.go", StartLine: 1},
		}},
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

func TestParseBashFileReadsV910MustFailSafety(t *testing.T) {
	mustIgnore := []string{
		"sed -n '1,10p;1w out.txt' a.go",
		"sed -n '1,10p;1e id' a.go",
		`awk 'NR>=1 && NR<=10 {print > "out.txt"}' a.go`,
		`awk 'BEGIN{system("id")} NR>=1 && NR<=10' a.go`,
		"awk -f prog.awk a.go",
		"awk '/func/' a.go",
		`python3 -c "open('a.go', 'w').write('x')"`,
		`python3 -c "open('a.go', 'a').write('x')"`,
		`python3 -c "open('a.go', 'r+').read()"`,
		`python3 -c "from pathlib import Path; Path('a.go').write_text('x')"`,
		`python3 -c "import subprocess; open('a.go').read(); subprocess.run(['ls'])"`,
		`python3 -c "open('$FILE').read()"`,
		"grep -n 'func' a.go",
		"grep -nv '' a.go",
		"grep -c '' a.go",
		"grep -l '' a.go",
		"grep -F -n '^' a.go",
		"cat a.go 2> err.log",
		"cat a.go 1>/dev/null",
		"cat a.go > out.txt",
		"cat a.go || cat b.go",
		"cat a.go || echo fail",
		"cat a.go || true extra",
		"cd sub || true; cat a.go",
		"(cd sub && cat $(echo a.go))",
		"(for f in a.go; do cat a.go; done)",
		"(cd sub && cat a.go | head -n 5)",
		"(cd sub && cat a.go) | head -n 5",
		"((cd sub && cat a.go))",
		"(cd sub && cat a.go) > out.txt",
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty (must-fail)", cmd, got)
		}
	}
}

func TestPreToolUseV910BashReadDedupAndClamp(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	var dense strings.Builder
	dense.WriteString("package sample\n\nfunc DenseBash() int {\n")
	for i := 4; i < 350; i++ {
		prefix := fmt.Sprintf("\t// line %03d ", i)
		dense.WriteString(prefix + strings.Repeat("b", 116-len(prefix)) + "\n")
	}
	dense.WriteString("\treturn 1\n}\n")
	writeFile(t, dir, "dense_bash.go", dense.String())
	writeFile(t, dir, "small.go", "package sample\n\nfunc Small() int { return 1 }\n")
	writeFile(t, dir, "sub/c.go", "package sub\n\nfunc C() int { return 2 }\n")
	commitAll(t, dir, "add bash test fixtures")

	callTool := func(conv, tool string, args map[string]any) HookDecisionOutput {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": tool, "args": args},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(payload)}, &out); err != nil {
			t.Fatalf("pre-tool-use (%s): %v", tool, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	// Unranged grep -n '' on 350-line ~39.9KB file clamps via dual-gate D7.
	if d := callTool("conv-b-clamp", "run_command", map[string]any{"CommandLine": "grep -n '' dense_bash.go"}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[CLAMPED]\n") {
		t.Fatalf("grep -n '' dense_bash.go = %+v, want deny [CLAMPED]", d)
	}
	// Multi-range sed stamps 1..10 and 20..30 without falsely marking gap 11..19 as read.
	if d := callTool("conv-b-sed", "run_command", map[string]any{"CommandLine": "sed -n '1,10p;20,30p' dense_bash.go"}); d.Decision != "allow" {
		t.Fatalf("1st multi-range sed = %+v, want allow", d)
	}
	if d := callTool("conv-b-sed", "read_file", map[string]any{"Path": "dense_bash.go", "StartLine": 11, "EndLine": 19}); d.Decision != "allow" {
		t.Fatalf("unread gap 11..19 after multi-range sed = %+v, want allow", d)
	}
	if d := callTool("conv-b-sed", "read_file", map[string]any{"Path": "dense_bash.go", "StartLine": 20, "EndLine": 30}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]") {
		t.Fatalf("re-read 20..30 after multi-range sed = %+v, want deny [UNCHANGED]", d)
	}
	// awk, python3 -c, and simple subshell all dedupe on 2nd identical read.
	for _, tc := range []struct{ conv, cmd string }{
		{"conv-b-awk", "awk 'NR>=1 && NR<=10' dense_bash.go 2>/dev/null || true"},
		{"conv-b-py", `python3 -c "print(open('small.go').read())"`},
		{"conv-b-sub", "(cd sub && cat c.go)"},
	} {
		if d := callTool(tc.conv, "run_command", map[string]any{"CommandLine": tc.cmd}); d.Decision != "allow" {
			t.Fatalf("1st %s = %+v, want allow", tc.cmd, d)
		}
		if d := callTool(tc.conv, "run_command", map[string]any{"CommandLine": tc.cmd}); d.Decision != "deny" || !strings.HasPrefix(d.Reason, "[UNCHANGED]") {
			t.Fatalf("2nd %s = %+v, want deny [UNCHANGED]", tc.cmd, d)
		}
	}
}
