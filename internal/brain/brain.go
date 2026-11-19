package brain

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"sort"
	"strings"
	"time"
)

// Options is what one call runs with.
type Options struct {
	Home   string           // the home whose ~/.config/fyr holds brain.db (--home)
	Dir    string           // the working directory: its checkout is the project
	Repo   string           // optional source checkout whose scope Export reads (--repo)
	Global bool             // the global scope instead of the project's
	Now    func() time.Time // the clock; nil is time.Now
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// AddInput is `record add`'s arguments.
type AddInput struct {
	Kind, Title, Status, ID string
	Project                 string // --project: the project a global record is about
	Provenance              string // operator, derived or unknown on memory and feedback
	Body                    io.Reader
}

// EditInput is `record edit`'s arguments: nil leaves a field as it is.
type EditInput struct {
	ID            string
	Status, Title *string
	Body          io.Reader // --body (stdin) or --body-file FILE: the new body
}

// ListFilter is `record list`'s flags.
type ListFilter struct {
	Kind    string
	Open    bool
	Project string // with Global: the global records about this project
	All     bool   // every scope
}

// Add stores one record and returns its ID. It writes the store only: a
// project's trail.jsonl is written on demand by `fyr record export` (ADR-091).
func Add(o Options, in AddInput) (string, error) {
	kind := in.Kind
	title, err := checkAdd(o, in)
	if err != nil {
		return "", err
	}
	body, err := readBody(in.Body)
	if err != nil {
		return "", err
	}
	status, err := resolveStatus(kind, in.Status, body)
	if err != nil {
		return "", err
	}
	prov, err := resolveProvenance(kind, in.Provenance)
	if err != nil {
		return "", err
	}
	s, err := openSession(o)
	if err != nil {
		return "", err
	}
	defer s.close()
	project := ""
	if in.Project != "" {
		project = canonicalProject(in.Project)
	}
	now := o.now()
	var id string
	err = s.write(func(c *sql.Conn) error {
		var werr error
		id, werr = insertRecord(c, s, in, status, title, body, project, prov, now)
		return werr
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func insertRecord(c *sql.Conn, s *session, in AddInput, status, title, body, project, prov string, now time.Time) (string, error) {
	ctx := context.Background()
	id := in.ID
	if id == "" {
		var err error
		if id, err = s.allocate(c, in.Kind, title, now); err != nil {
			return "", err
		}
	} else if taken, err := exists(c, s.scope, id); err != nil {
		return "", err
	} else if taken {
		return "", refuse("%s already exists in this scope", id)
	}
	stamp := now.UTC().Format(time.RFC3339)
	memKind, val, verAt := "", 1.0, ""
	if in.Kind == "memory" {
		rec := Record{
			Scope: s.scope, ID: id, Kind: in.Kind, Status: status,
			Title: title, Body: body, Project: project, Provenance: prov,
			VerifiedAt: stamp, Updated: stamp,
		}
		memKind = resolveMemoryKind(rec)
		rec.MemoryKind = memKind
		verAt = stamp
		feedbacks, err := queryFeedbacksConn(ctx, c, s.scope)
		if err != nil {
			return "", err
		}
		val = ComputeMemoryValidity(s.top, rec, CountMemoryContradictions(rec, feedbacks), now)
	}
	_, err := c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.scope, id, in.Kind, status, now.Format("2006-01-02"), title, body, project, prov, memKind, val, verAt, stamp)
	return id, err
}

// Get returns the records named, in the order named; any not in the scope
// are returned as missing, with a refusal naming them.
func Get(o Options, ids []string) ([]Record, []string, error) {
	s, err := openSession(o)
	if err != nil {
		return nil, nil, err
	}
	defer s.close()
	var recs []Record
	var missing []string
	for _, id := range ids {
		r, err := scanRecord(s.st.db.QueryRow(`SELECT `+recordCols+` FROM records WHERE scope = ? AND id = ?`, s.scope, id))
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, id)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		recs = append(recs, r)
	}
	if len(missing) > 0 {
		return recs, missing, refuse("no record %s in this scope", strings.Join(missing, ", "))
	}
	return recs, nil, nil
}

// Edit changes a record's status, title or body (D9: without a body only the
// metadata changes); a plan's status follows its new body.
func Edit(o Options, in EditInput) error {
	title, body, err := editFields(in)
	if err != nil {
		return err
	}
	s, err := openSession(o)
	if err != nil {
		return err
	}
	defer s.close()
	now := o.now()
	return s.write(func(c *sql.Conn) error {
		ctx := context.Background()
		r, err := scanRecord(c.QueryRowContext(ctx, `SELECT `+recordCols+` FROM records WHERE scope = ? AND id = ?`, s.scope, in.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return refuse("no record %s in this scope", in.ID)
		}
		if err != nil {
			return err
		}
		if in.Status != nil {
			if r.Status, err = resolveStatus(r.Kind, *in.Status, r.Body); err != nil {
				return err
			}
		}
		if in.Title != nil {
			r.Title = title
		}
		if in.Body != nil {
			r.Body = body
			if r.Kind == "plan" {
				r.Status = planStatus(body)
			}
		}
		_, err = c.ExecContext(ctx, `UPDATE records SET status = ?, title = ?, body = ?, updated = ? WHERE scope = ? AND id = ?`,
			r.Status, r.Title, r.Body, now.UTC().Format(time.RFC3339), s.scope, in.ID)
		return err
	})
}

// List returns the scope's records, newest first: with All every scope's.
func List(o Options, f ListFilter) ([]Record, error) {
	if err := checkList(o, f); err != nil {
		return nil, err
	}
	var s *session
	var err error
	if f.All {
		s, err = openAll(o)
	} else {
		s, err = openSession(o)
	}
	if err != nil {
		return nil, err
	}
	defer s.close()
	q, args := listQuery(f, s.scope)
	rows, err := s.st.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanRecordsRows(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
	return out, nil
}

func recordDescLess(a, b Record) bool {
	if a.Date != b.Date {
		return a.Date > b.Date
	}
	if a.Updated != b.Updated {
		return a.Updated > b.Updated
	}
	if a.ID != b.ID {
		return idLess(b.ID, a.ID)
	}
	return a.Scope < b.Scope
}
