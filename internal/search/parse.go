package search

import (
	"path"
	"regexp"
	"strings"
)

// chunkLines is the most lines one chunk holds.
const chunkLines = 40

type chunk struct {
	line    int // first line, 1-based
	heading string
	body    string
}

var atxHeading = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)

// fence tracks fenced code blocks the way the handoff skill's scan does: a
// run of three or more backticks opens a block, and a line holding only a
// run of the same length closes it.
type fence struct{ open int }

var fenceLine = regexp.MustCompile("^(`{3,})")
var fenceClose = regexp.MustCompile("^`+\\s*$")

// step reports whether line is a fence line (and so part of no scan).
func (f *fence) step(line string) bool {
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

func (f *fence) inside() bool { return f.open != 0 }

func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// chunkText splits a file into chunks: Markdown at each ATX heading outside
// fenced blocks, everything else in windows; no chunk is longer than
// chunkLines, and a chunk of only blank lines is dropped.
func chunkText(p, text string) []chunk {
	lines := splitLines(text)
	md := strings.EqualFold(path.Ext(p), ".md")
	var out []chunk
	start, heading := 0, ""
	flush := func(end int) {
		for s := start; s < end; s += chunkLines {
			e := min(s+chunkLines, end)
			body := strings.Join(lines[s:e], "\n")
			if strings.TrimSpace(body) != "" {
				out = append(out, chunk{line: s + 1, heading: heading, body: body})
			}
		}
	}
	var f fence
	for i, l := range lines {
		if !md {
			continue
		}
		if f.step(l) || f.inside() {
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

var unticked = regexp.MustCompile(`^- \[ \]`)

type ref struct {
	line   int
	target string
}

var (
	idToken = regexp.MustCompile(`\b(BUG|ADR)-\d{3}[a-z]?\b`)
	// pathToken: a path with a slash, or a bare name ending in one of
	// refExtensions (one list, so the prefilter below cannot drift from it).
	pathToken = regexp.MustCompile(`[A-Za-z0-9_.@-]+(?:/[A-Za-z0-9_.@-]+)+/?|[A-Za-z0-9_-]+\.(?:` + strings.Join(refExtensions, "|") + `)\b`)
)

// refExtensions are the file extensions a bare name may end in to count as
// a path reference.
var refExtensions = []string{"go", "md", "ts", "tsx", "js", "json", "yaml", "yml", "sh", "py", "toml", "mod", "sum", "sql", "tf"}

func hasRefExtension(l string) bool {
	for _, e := range refExtensions {
		if strings.Contains(l, "."+e) {
			return true
		}
	}
	return false
}

// normalizeRef trims what surrounds a path in prose: trailing punctuation, a
// leading ./ and a diff's a/ or b/ prefix.
func normalizeRef(t string) string {
	t = strings.TrimRight(t, ".,;:)")
	for _, p := range []string{"./", "a/", "b/"} {
		if strings.HasPrefix(t, p) && len(t) > len(p) {
			t = t[len(p):]
			break
		}
	}
	return t
}

// parseRefs lists the record IDs and path-shaped tokens each line names, one
// per line and target.
func parseRefs(text string) []ref {
	var out []ref
	for i, l := range splitLines(text) {
		seen := map[string]bool{}
		add := func(t string) {
			if t != "" && !seen[t] {
				seen[t] = true
				out = append(out, ref{line: i + 1, target: t})
			}
		}
		// The regular expressions are the cost of a build (a quarter of it
		// here): skip the lines that cannot match.
		if strings.Contains(l, "BUG-") || strings.Contains(l, "ADR-") {
			for _, m := range idToken.FindAllString(l, -1) {
				add(m)
			}
		}
		if !strings.Contains(l, "/") && !hasRefExtension(l) {
			continue
		}
		for _, m := range pathToken.FindAllString(l, -1) {
			if t := normalizeRef(m); strings.ContainsAny(t, "/.") && !idToken.MatchString(t) {
				add(t)
			}
		}
	}
	return out
}

// Kinds a path can have, in the order `fyr help search` lists them.
// kinds are the --kind values. A checkout's file is code or doc and a
// ~config file feedback, retro or proposal; every other kind is answered by
// brain records alone (ADR-063, ADR-086).
var kinds = []string{"bug", "adr", "plan", "change", "handoff", "review", "testing", "retro", "spec", "deploy", "trail", "code", "doc", "feedback", "proposal", "learning", "guide", "memory"}

var trailKinds = map[string]string{
	"bugs": "bug", "architecture": "adr", "plans": "plan", "changes": "change",
	"handoffs": "handoff", "reviews": "review", "testing": "testing", "retros": "retro",
	"specs": "spec", "deploy": "deploy",
}

var codeExt = map[string]bool{}

func init() {
	for _, e := range strings.Fields("go ts tsx js jsx mjs py sh rs java kt swift rb c h cc cpp cs sql tf") {
		codeExt["."+e] = true
	}
}

// configProject is the global index's name for ~/.config/fyr/.
const configProject = "~config"

// kindOf classifies a path. Search passes no docs root; docsRoot is for
// KindOf, which metrics reads with the project's (ADR-086).
func kindOf(project, p, docsRoot string) string {
	if project == configProject {
		switch {
		case p == "feedback.jsonl":
			return "feedback"
		case p == "retros.jsonl":
			return "retro"
		default:
			return "proposal"
		}
	}
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
