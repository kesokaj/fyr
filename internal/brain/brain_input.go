package brain

import "strings"

// The checks on Add's, Edit's and List's arguments, made before the store
// is opened or stdin is read, and List's filter as SQL.

// checkAdd refuses an add whose arguments the store would not take, before
// stdin is read, and returns its title.
func checkAdd(o Options, in AddInput) (string, error) {
	kind := in.Kind
	if !knownKind(kind) {
		return "", usage("unknown kind %q: one of %s", kind, strings.Join(Kinds(), " "))
	}
	title, err := cleanTitle(in.Title)
	if err != nil {
		return "", err
	}
	if in.Project != "" && !o.Global {
		return "", usage("--project names the project a global record is about: it needs --global")
	}
	if in.ID != "" && !validID(kind, in.ID) {
		return "", usage("--id %q is not a %s id (%s)", in.ID, kind, idGrammar(kind))
	}
	// A given status is checked before stdin is read; a plan's is derived
	// from the body, which Add reads after these checks.
	if in.Status != "" || kind == "handoff" {
		if _, err := resolveStatus(kind, in.Status, ""); err != nil {
			return "", err
		}
	}
	switch {
	case o.Global && kind == "memory":
		return "", refuse("memory records are project-scoped (D14): drop --global")
	case !o.Global && globalOnly[kind]:
		return "", refuse("%s records are global only (R8): add --global, and --project <root> for the project it is about", kind)
	}
	return title, nil
}

// editFields refuses an edit that changes nothing or names an empty
// status, and returns its new title and body, read from stdin when given.
func editFields(in EditInput) (title, body string, err error) {
	if in.Status == nil && in.Title == nil && in.Body == nil {
		return "", "", usage("usage: fyr record edit <id> [--status S] [--title T] [--body] needs at least one of them")
	}
	if in.Status != nil && *in.Status == "" {
		return "", "", usage("--status needs a value")
	}
	if in.Title != nil {
		t, err := cleanTitle(*in.Title)
		if err != nil {
			return "", "", err
		}
		title = t
	}
	if in.Body != nil {
		b, err := readBody(in.Body)
		if err != nil {
			return "", "", err
		}
		body = b
	}
	return title, body, nil
}

// checkList refuses a list filter no scope answers.
func checkList(o Options, f ListFilter) error {
	switch {
	case f.Kind != "" && !knownKind(f.Kind):
		return usage("unknown kind %q: one of %s", f.Kind, strings.Join(Kinds(), " "))
	case f.Project != "" && !o.Global:
		return usage("--project lists the global records about a project: it needs --global")
	case f.All && o.Global:
		return usage("--all already covers every scope: drop --global")
	}
	return nil
}

// listQuery is List's SQL and arguments: scope's records, or every scope's
// with All, filtered as f says, newest first.
func listQuery(f ListFilter, scope string) (string, []any) {
	q := `SELECT ` + recordCols + ` FROM records WHERE 1 = 1`
	var args []any
	if !f.All {
		q += ` AND scope = ?`
		args = append(args, scope)
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Open {
		q += ` AND ` + openSQL
	}
	if f.Project != "" {
		q += ` AND project = ?`
		args = append(args, canonicalProject(f.Project))
	}
	return q + ` ORDER BY date DESC, updated DESC, id DESC`, args
}
