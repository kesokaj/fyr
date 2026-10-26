package harness

import (
	"regexp"
	"strings"
)

// earlyExitConsumer matches the start of a pipeline stage that stops reading
// before its producer is done: `head`, `grep -q/-m/-l` (short or long form),
// a `sed` script with a `q` or `Q` command (after an address, a `!`, a flag,
// a `;` or a brace, with or without an exit code), an `awk` program with
// `exit`, `read`; by name or by path.
var earlyExitConsumer = regexp.MustCompile(`^&?\s*(\S*/)?(head\b|grep\b.*(\s-[a-zA-Z]*[qml]|\s--(quiet|silent|max-count|files-with-matches)\b)|sed\b.*[^a-z_][qQ][0-9]*([}'";\s)]|$)|awk\b.*\bexit\b|read\b)`)

// pipeStages cuts one logical shell line at its single pipes and returns what
// follows each. Not a pipe: `||`, an escaped `\|`, a `|` inside single
// quotes, a `|` inside double quotes (an alternation in a pattern), anything
// after an unquoted ` #`. A command substitution is code again, also inside
// double quotes: `v="$(find . | head -1)"` has a pipe, and the stage it
// starts ends at the substitution's closing parenthesis.
func pipeStages(line string) []string {
	var stages []string
	var quote byte   // 0, '\'' or '"'
	var saved []byte // the quote state each open `$(` returns to
	var depth []int  // plain parentheses open inside each `$(`
	start := -1
	end := func(i int) {
		if start >= 0 {
			stages = append(stages, line[start:i])
			start = -1
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' {
			i++
			continue
		}
		if c == '$' && i+1 < len(line) && line[i+1] == '(' {
			saved, depth, quote = append(saved, quote), append(depth, 0), 0
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				end(len(line))
				return stages
			}
		case '(':
			if len(depth) > 0 {
				depth[len(depth)-1]++
			}
		case ')':
			if n := len(depth); n > 0 {
				if depth[n-1] > 0 {
					depth[n-1]--
				} else {
					end(i)
					quote, saved, depth = saved[n-1], saved[:n-1], depth[:n-1]
				}
			}
		case '|':
			if i+1 < len(line) && line[i+1] == '|' {
				end(i)
				i++
				continue
			}
			end(i)
			start = i + 1
		}
	}
	end(len(line))
	return stages
}

// earlyExitPipeProblem is the first logical line of a shell script that pipes
// into such a consumer without a guard. Under `set -o pipefail` the producer
// is killed by SIGPIPE once its output outgrows a pipe and the pipeline's
// status is 141: `set -e` ends the script with no message, and an `||` or an
// `if` on the pipeline takes the wrong branch (BUG-155, BUG-157; BUG-138 and
// BUG-148 are the same family). A producer stops itself instead (`find
// -quit`, `awk '{…; exit}'` reading a file), a test reads a here-string or
// asks git directly; a line that only prints an excerpt ends `|| true`.
//
// It is a static check with limits, in the refusing direction and the
// passing one: a `case` label spelled like a consumer (`a|head)`) is refused;
// a `sed` stage is refused when a `q` or `Q` follows anything but a lowercase
// letter, which takes in some harmless ones (`sed 's|a|b|' FAQ`, a label
// `:q`, `s/x/"Q"/`): restructure such a line, or end it `|| true`; and a
// consumer reached through a function or a variable is not seen.
func earlyExitPipeProblem(script string) string {
	// One logical line: a backslash continuation, and a line ending in a
	// pipe, continue on the next.
	script = strings.ReplaceAll(script, "\\\n", " ")
	script = regexp.MustCompile(`([^|])\|[ \t]*\n`).ReplaceAllString(script, "$1| ")
	for _, l := range strings.Split(script, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") || strings.HasSuffix(t, "|| true") {
			continue
		}
		for _, stage := range pipeStages(t) {
			if earlyExitConsumer.MatchString(stage) {
				return t
			}
		}
	}
	return ""
}

// loopTailTest is a statement `[ … ] && cmd`, `[[ … ]] && cmd` or
// `test … && cmd` with no `||` to catch the false test.
var loopTailTest = regexp.MustCompile(`^(\[\[?|test)\s.*&&`)

// loopTailTestProblem is the first statement of a shell script that ends the
// body of a loop which is a pipeline stage in `test && cmd` (BUG-148, TR13).
// When the last test is false the loop returns 1, and under pipefail and
// `set -e` the script ends there with no message; the same loop fed by a
// redirection does not end the script, since bash exempts the && list. A
// static check with limits: statements split on `;` and lines outside
// quotes, loops matched by their keywords, and a loop reached through a
// function or a here-document is not seen.
func loopTailTestProblem(script string) string {
	script = strings.ReplaceAll(script, "\\\n", " ")
	script = regexp.MustCompile(`([^|])\|[ \t]*\n`).ReplaceAllString(script, "$1| ")
	var stmts []string
	for _, line := range strings.Split(script, "\n") {
		var quote byte
		start := 0
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case quote != 0:
				if c == quote {
					quote = 0
				}
			case c == '\'' || c == '"':
				quote = c
			case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
				stmts = append(stmts, line[start:i])
				start = len(line)
				i = len(line)
			case c == ';':
				stmts = append(stmts, line[start:i])
				start = i + 1
			}
		}
		if start < len(line) {
			stmts = append(stmts, line[start:])
		}
	}
	keyword := func(s string) bool {
		for _, k := range []string{"while ", "until ", "for "} {
			if strings.HasPrefix(s, k) {
				return true
			}
		}
		return false
	}
	var piped []bool // one per open loop: is it a pipeline stage
	last := ""
	for _, s := range stmts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if s == "do" {
			continue
		}
		s = strings.TrimSpace(strings.TrimPrefix(s, "do "))
		if s == "done" || strings.HasPrefix(s, "done ") {
			if n := len(piped); n > 0 {
				if piped[n-1] && loopTailTest.MatchString(last) && !strings.Contains(last, "||") {
					return last
				}
				piped = piped[:n-1]
			}
			last = s
			continue
		}
		if keyword(s) {
			piped = append(piped, false)
			continue
		}
		// pipeStages returns what follows each pipe, so a loop found there
		// is a pipeline stage.
		opened := false
		for _, stage := range pipeStages(s) {
			if keyword(strings.TrimSpace(stage)) {
				piped = append(piped, true)
				opened = true
				break
			}
		}
		if !opened {
			last = s
		}
	}
	return ""
}
