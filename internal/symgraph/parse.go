package symgraph

import (
	"path"
	"regexp"
	"strings"
)

// ChunkLines is the most lines one chunk holds.
const ChunkLines = 40

type Chunk struct {
	Line    int // first line, 1-based
	Heading string
	Body    string
}

var atxHeading = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)

// fence tracks fenced code blocks the way the handoff skill's scan does: a
// run of three or more backticks opens a block, and a line holding only a
// run of the same length closes it.
type Fence struct{ open int }

var fenceLine = regexp.MustCompile("^(`{3,})")
var fenceClose = regexp.MustCompile("^`+\\s*$")

// step reports whether line is a fence line (and so part of no scan).
func (f *Fence) Step(line string) bool {
	m := fenceLine.FindString(line)
	if m == "" {
		return false
	}
	if f.open == 0 {
		f.open = len(m)
	} else if len(m) == f.open && fenceClose.MatchString(line) {
		f.open = 0
	}
	return true
}

func (f *Fence) Inside() bool { return f.open != 0 }

func SplitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// chunkText splits a file into chunks: Markdown at each ATX heading outside
// fenced blocks, everything else in windows; no chunk is longer than
// ChunkLines, and a chunk of only blank lines is dropped.
func ChunkText(p, text string) []Chunk {
	lines := SplitLines(text)
	md := strings.EqualFold(path.Ext(p), ".md")
	var out []Chunk
	start, heading := 0, ""
	flush := func(end int) {
		for s := start; s < end; s += ChunkLines {
			e := min(s+ChunkLines, end)
			body := strings.Join(lines[s:e], "\n")
			if strings.TrimSpace(body) != "" {
				out = append(out, Chunk{Line: s + 1, Heading: heading, Body: body})
			}
		}
	}
	var f Fence
	for i, l := range lines {
		if !md {
			continue
		}
		if f.Step(l) || f.Inside() {
			continue
		}
		if m := atxHeading.FindStringSubmatch(l); m != nil {
			flush(i)
			start, heading = i, m[1]
		}
	}
	flush(len(lines))
	return out
}

var Unticked = regexp.MustCompile(`^- \[ \]`)

type Ref struct {
	Line   int
	Target string
}

var (
	IDRefRe = regexp.MustCompile(`\b(BUG|ADR)-\d{3}[a-z]?\b`)
	// pathToken: a path with a slash, or a bare name ending in one of
	// RefExtensions (one list, so the prefilter below cannot drift from it).
	PathRefRe = regexp.MustCompile(`[A-Za-z0-9_.@-]+(?:/[A-Za-z0-9_.@-]+)+/?|[A-Za-z0-9_-]+\.(?:` + strings.Join(RefExtensions, "|") + `)\b`)
)

// RefExtensions are the file extensions a bare name may end in to count as
// a path reference.
var RefExtensions = []string{"go", "md", "ts", "tsx", "js", "json", "yaml", "yml", "sh", "py", "toml", "mod", "sum", "sql", "tf"}

func hasRefExtension(l string) bool {
	for _, e := range RefExtensions {
		if strings.Contains(l, "."+e) {
			return true
		}
	}
	return false
}

// normalizeRef trims what surrounds a path in prose: trailing punctuation, a
// leading ./ and a diff's a/ or b/ prefix.
func NormalizeRef(t string) string {
	t = strings.TrimRight(t, ".,;:)")
	for _, p := range []string{"./", "a/", "b/"} {
		if strings.HasPrefix(t, p) && len(t) > len(p) {
			t = t[len(p):]
			break
		}
	}
	return t
}

func appendLineRef(out []Ref, seen *map[string]bool, lineNum int, target string) []Ref {
	if target == "" {
		return out
	}
	if *seen == nil {
		*seen = map[string]bool{}
	}
	if (*seen)[target] {
		return out
	}
	(*seen)[target] = true
	return append(out, Ref{Line: lineNum, Target: target})
}

func appendLineIDRefs(out []Ref, seen *map[string]bool, lineNum int, line string) []Ref {
	if !strings.Contains(line, "BUG-") && !strings.Contains(line, "ADR-") {
		return out
	}
	for _, m := range IDRefRe.FindAllString(line, -1) {
		out = appendLineRef(out, seen, lineNum, m)
	}
	return out
}

func appendLinePathRefs(out []Ref, seen *map[string]bool, lineNum int, line string) []Ref {
	if !strings.Contains(line, "/") && !hasRefExtension(line) {
		return out
	}
	for _, m := range PathRefRe.FindAllString(line, -1) {
		if t := NormalizeRef(m); strings.ContainsAny(t, "/.") && !IDRefRe.MatchString(t) {
			out = appendLineRef(out, seen, lineNum, t)
		}
	}
	return out
}

// parseRefs lists the record IDs and path-shaped tokens each line names, one
// per line and target.
func ParseRefs(text string) []Ref {
	var out []Ref
	var seen map[string]bool
	for i, l := range SplitLines(text) {
		if len(seen) > 0 {
			clear(seen)
		}
		// The regular expressions are the cost of a build (a quarter of it
		// here): each helper skips lines that cannot match.
		out = appendLineIDRefs(out, &seen, i+1, l)
		out = appendLinePathRefs(out, &seen, i+1, l)
	}
	return out
}

// Kinds a path can have, in the order `fyr help search` lists them.
// kinds are the --kind values. A checkout's file is code or doc; every other
// kind is answered by brain records alone (ADR-063, ADR-086, ADR-087).
var Kinds = []string{"bug", "adr", "plan", "change", "handoff", "review", "testing", "retro", "spec", "deploy", "trail", "code", "doc", "feedback", "proposal", "learning", "guide", "memory"}

var trailKinds = map[string]string{
	"bugs": "bug", "architecture": "adr", "plans": "plan", "changes": "change",
	"handoffs": "handoff", "reviews": "review", "testing": "testing", "retros": "retro",
	"specs": "spec", "deploy": "deploy",
}

var codeExt = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true,
	".py": true, ".sh": true, ".rs": true, ".java": true, ".kt": true, ".swift": true,
	".rb": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".cs": true,
	".sql": true, ".tf": true,
}

// kindOf classifies a path. Search passes no docs root; docsRoot is for
// KindOf, which metrics reads with the project's (ADR-086).
func KindOf(p, docsRoot string) string {
	if rel, ok := docsRelative(p, docsRoot); ok {
		dir, _, _ := strings.Cut(rel, "/")
		if k, ok := trailKinds[dir]; ok && strings.Contains(rel, "/") {
			return k
		}
		return "trail"
	}
	if codeExt[strings.ToLower(path.Ext(p))] {
		return "code"
	}
	return "doc"
}

func docsRelative(p, docsRoot string) (string, bool) {
	if docsRoot == "" {
		return "", false
	}
	prefix := docsRoot + "/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	return strings.TrimPrefix(p, prefix), true
}
