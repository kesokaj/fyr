package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
)

// recordAdd serves `record add`: the new record's ID, and the redactor
// warning when the given ID, the stored title or the body would trip it.
func recordAdd(c *runtimeCtx, o brain.Options, kind string, set map[string]bool) error {
	opts := recordOptsOf(c)
	body, closeBody, err := c.resolveRecordAddInput(set)
	if err != nil {
		return err
	}
	defer closeBody()
	var seen strings.Builder
	id, err := brain.Add(o, brain.AddInput{Kind: kind, Title: opts.title, Status: opts.status, ID: opts.id, Project: opts.project, Body: teeBodyFrom(body, &seen)})
	if err == nil {
		fmt.Fprintln(c.out, id)
		warnRedactable(opts.id, opts.title, seen.String())
	}
	return recordErr(c, err)
}

func (c *runtimeCtx) resolveRecordAddInput(set map[string]bool) (io.Reader, func(), error) {
	opts := recordOptsOf(c)
	noop := func() {}
	if opts.jsonIn {
		if set["body-file"] || set["title-file"] {
			return nil, noop, usageErr(c.out, "record", "fyr record add: --json-in takes no --body-file or --title-file")
		}
		doc, err := brain.ParseRecordJSON(stdin)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		applyRecordStdinDoc(opts, doc, set)
		return strings.NewReader(doc.Body), noop, nil
	}
	if opts.titleFile != "" {
		if opts.title != "" {
			return nil, noop, usageErr(c.out, "record", "fyr record add takes --title or --title-file, not both")
		}
		t, err := readTitleFile(opts.titleFile)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		opts.title = t
	}
	body, closeBody, err := openBodyReader(opts.bodyFile, set["body-file"])
	if err != nil {
		return nil, noop, recordErr(c, err)
	}
	if !set["body-file"] {
		doc, rest, perr := brain.ParseRecordStdin(body)
		if perr != nil {
			closeBody()
			return nil, noop, recordErr(c, perr)
		}
		applyRecordStdinDoc(opts, doc, set)
		body = rest
	}
	return body, closeBody, nil
}

func applyRecordStdinDoc(opts *recordOpts, doc brain.RecordStdinDoc, set map[string]bool) {
	if !set["title"] && opts.title == "" && doc.Title != "" {
		opts.title = doc.Title
	}
	if !set["status"] && doc.Status != "" {
		opts.status = doc.Status
	}
	if !set["id"] && doc.ID != "" {
		opts.id = doc.ID
	}
	if !set["project"] && doc.Project != "" {
		opts.project = doc.Project
	}
}

// recordEdit serves `record edit`: only what the command line set changes;
// a new title or body gets the redactor warning, a kept one does not.
func recordEdit(c *runtimeCtx, o brain.Options, id string, set map[string]bool) error {
	opts := recordOptsOf(c)
	if opts.body && set["body-file"] {
		return usageErr(c.out, "record", "fyr record edit takes --body or --body-file, not both")
	}
	if opts.jsonIn && (opts.body || set["body-file"]) {
		return usageErr(c.out, "record", "fyr record edit: --json-in takes no --body or --body-file")
	}
	in := brain.EditInput{ID: id}
	if set["status"] {
		in.Status = &opts.status
	}
	if set["title"] {
		in.Title = &opts.title
	}
	var seen strings.Builder
	closeBody, err := c.resolveRecordEditInput(&in, &seen, set)
	if err != nil {
		return err
	}
	defer closeBody()
	err = brain.Edit(o, in)
	if err == nil {
		title := ""
		if in.Title != nil {
			title = *in.Title
		}
		warnRedactable("", title, seen.String())
	}
	return recordErr(c, err)
}

func (c *runtimeCtx) resolveRecordEditInput(in *brain.EditInput, seen *strings.Builder, set map[string]bool) (func(), error) {
	opts := recordOptsOf(c)
	noop := func() {}
	if opts.jsonIn {
		return noop, c.resolveRecordEditJSON(in, seen)
	}
	if !opts.body && !set["body-file"] {
		return noop, nil
	}
	body, closeBody, err := openBodyReader(opts.bodyFile, set["body-file"])
	if err != nil {
		return noop, recordErr(c, err)
	}
	if !set["body-file"] {
		rest, perr := applyRecordEditStdin(in, body)
		if perr != nil {
			closeBody()
			return noop, recordErr(c, perr)
		}
		body = rest
	}
	in.Body = teeBodyFrom(body, seen)
	return closeBody, nil
}

func (c *runtimeCtx) resolveRecordEditJSON(in *brain.EditInput, seen *strings.Builder) error {
	doc, err := brain.ParseRecordEditJSON(stdin)
	if err != nil {
		return recordErr(c, err)
	}
	if in.Status == nil && doc.Status != nil {
		in.Status = doc.Status
	}
	if in.Title == nil && doc.Title != nil {
		in.Title = doc.Title
	}
	if doc.Body != nil {
		in.Body = teeBodyFrom(strings.NewReader(*doc.Body), seen)
	}
	return nil
}

func applyRecordEditStdin(in *brain.EditInput, body io.Reader) (io.Reader, error) {
	doc, rest, err := brain.ParseRecordStdin(body)
	if err != nil {
		return nil, err
	}
	if in.Title == nil && doc.Title != "" {
		t := doc.Title
		in.Title = &t
	}
	if in.Status == nil && doc.Status != "" {
		s := doc.Status
		in.Status = &s
	}
	return rest, nil
}

// maxTitleFile caps what --title-file reads: past brain's 512-byte title,
// so an over-long title is refused by the store's own check, not cut here.
const maxTitleFile = 4 << 10

// readTitleFile is --title-file's title: the file's text with one trailing
// newline dropped, so a title the /work lead copies from a worker's
// records never passes through a shell (ADR-501). Every other check is the
// store's, as for --title.
func readTitleFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTitleFile+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxTitleFile {
		return "", fmt.Errorf("--title-file %s is over %d bytes", path, maxTitleFile)
	}
	s := strings.TrimSuffix(string(b), "\n")
	return strings.TrimSuffix(s, "\r"), nil
}
