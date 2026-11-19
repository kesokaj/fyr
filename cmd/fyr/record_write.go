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
func recordAdd(c *ctx, o brain.Options, kind string, set map[string]bool) error {
	body, closeBody, err := c.resolveRecordAddInput(set)
	if err != nil {
		return err
	}
	defer closeBody()
	var seen strings.Builder
	id, err := brain.Add(o, brain.AddInput{Kind: kind, Title: c.recTitle, Status: c.recStatus, ID: c.recID, Project: c.recProject, Body: teeBodyFrom(body, &seen)})
	if err == nil {
		fmt.Fprintln(c.out, id)
		warnRedactable(c.recID, c.recTitle, seen.String())
	}
	return recordErr(c, err)
}

func (c *ctx) resolveRecordAddInput(set map[string]bool) (io.Reader, func(), error) {
	noop := func() {}
	if c.jsonIn {
		if set["body-file"] || set["title-file"] {
			return nil, noop, usageErr(c.out, "record", "fyr record add: --json-in takes no --body-file or --title-file")
		}
		doc, err := brain.ParseRecordJSON(stdin)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		c.applyRecordStdinDoc(doc, set)
		return strings.NewReader(doc.Body), noop, nil
	}
	if c.recTitleFile != "" {
		if c.recTitle != "" {
			return nil, noop, usageErr(c.out, "record", "fyr record add takes --title or --title-file, not both")
		}
		t, err := readTitleFile(c.recTitleFile)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		c.recTitle = t
	}
	body, closeBody, err := openBodyReader(c.recBodyFile, set["body-file"])
	if err != nil {
		return nil, noop, recordErr(c, err)
	}
	if !set["body-file"] {
		doc, rest, perr := brain.ParseRecordStdin(body)
		if perr != nil {
			closeBody()
			return nil, noop, recordErr(c, perr)
		}
		c.applyRecordStdinDoc(doc, set)
		body = rest
	}
	return body, closeBody, nil
}

func (c *ctx) applyRecordStdinDoc(doc brain.RecordStdinDoc, set map[string]bool) {
	if !set["title"] && c.recTitle == "" && doc.Title != "" {
		c.recTitle = doc.Title
	}
	if !set["status"] && doc.Status != "" {
		c.recStatus = doc.Status
	}
	if !set["id"] && doc.ID != "" {
		c.recID = doc.ID
	}
	if !set["project"] && doc.Project != "" {
		c.recProject = doc.Project
	}
}

// recordEdit serves `record edit`: only what the command line set changes;
// a new title or body gets the redactor warning, a kept one does not.
func recordEdit(c *ctx, o brain.Options, id string, set map[string]bool) error {
	if c.recBody && set["body-file"] {
		return usageErr(c.out, "record", "fyr record edit takes --body or --body-file, not both")
	}
	if c.jsonIn && (c.recBody || set["body-file"]) {
		return usageErr(c.out, "record", "fyr record edit: --json-in takes no --body or --body-file")
	}
	in := brain.EditInput{ID: id}
	if set["status"] {
		in.Status = &c.recStatus
	}
	if set["title"] {
		in.Title = &c.recTitle
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

func (c *ctx) resolveRecordEditInput(in *brain.EditInput, seen *strings.Builder, set map[string]bool) (func(), error) {
	noop := func() {}
	if c.jsonIn {
		doc, err := brain.ParseRecordEditJSON(stdin)
		if err != nil {
			return noop, recordErr(c, err)
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
		return noop, nil
	}
	if !c.recBody && !set["body-file"] {
		return noop, nil
	}
	body, closeBody, err := openBodyReader(c.recBodyFile, set["body-file"])
	if err != nil {
		return noop, recordErr(c, err)
	}
	if !set["body-file"] {
		doc, rest, perr := brain.ParseRecordStdin(body)
		if perr != nil {
			closeBody()
			return noop, recordErr(c, perr)
		}
		if in.Title == nil && doc.Title != "" {
			t := doc.Title
			in.Title = &t
		}
		if in.Status == nil && doc.Status != "" {
			s := doc.Status
			in.Status = &s
		}
		body = rest
	}
	in.Body = teeBodyFrom(body, seen)
	return closeBody, nil
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
