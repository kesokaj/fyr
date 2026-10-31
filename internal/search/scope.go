package search

import (
	"fmt"
	"strings"
	"time"
)

// scope is an open, reconciled index and the projects a query reads.
type scope struct {
	st       *store
	projects []string // the checkout a file query covers; none under --global
	files    int
	changed  int
	elapsed  time.Duration
	leftOut  []string // directories a noindex marker takes out (project scope only)
}

func (sc *scope) close() {
	if sc.st != nil {
		sc.st.close()
	}
}

func (sc *scope) keys() []any {
	out := make([]any, len(sc.projects))
	for i, p := range sc.projects {
		out[i] = p
	}
	return out
}

func (sc *scope) in() string {
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(sc.projects)), ",") + ")"
}

func (sc *scope) status() string {
	s := fmt.Sprintf("index current: %d files, reconciled %d changed in %d ms", sc.files, sc.changed, sc.elapsed.Milliseconds())
	if len(sc.leftOut) > 0 {
		// The names are the project's own text: printable, like every hit.
		s += "; left out by a noindex marker: " + printable(strings.Join(sc.leftOut, ", "))
	}
	return s
}

func (sc *scope) kind(project, path string) string {
	return kindOf(path, "")
}

func (sc *scope) kindMatches(o Options, project, path string) bool {
	return o.Kind == "" || sc.kind(project, path) == o.Kind
}

func (sc *scope) label(project, path string, line int) string {
	return printable(sc.rawLabel(project, path, line))
}

func (sc *scope) rawLabel(project, path string, line int) string {
	return fmt.Sprintf("%s:%d  %s", path, line, sc.kind(project, path))
}
