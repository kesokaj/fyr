package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
)

// write stores each proposal as a global, queued `proposal` record about
// the project (brain plan D15), and every valid draft skill in its own
// directory under proposals/dream/drafts/, which a record names; the model's
// text is scrubbed again first.
func (o Options) write(conf string, c Collected, r response, cli string, t0 time.Time) error {
	day := t0.Format("2006-01-02")
	dir := filepath.Join(conf, "proposals", "dream")
	var drafts []draft
	var dropped []string
	for _, d := range r.Skills {
		if skillName.MatchString(d.Name) {
			drafts = append(drafts, d)
		} else {
			dropped = append(dropped, d.Name)
		}
	}
	var draftLines []string
	for _, d := range drafts {
		path, err := unique(filepath.Join(dir, "drafts"), day+"-"+c.Name+"-"+d.Name, "")
		if err != nil {
			return err
		}
		desc, _ := json.Marshal(o.clean(d.Description))
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", d.Name, desc, strings.TrimSpace(o.clean(d.Body)))
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		if err := harness.WriteAtomic(filepath.Join(path, "SKILL.md"), []byte(body), 0o600); err != nil {
			return err
		}
		draftLines = append(draftLines, "Draft skill: "+filepath.Join(dir, "drafts", filepath.Base(path), "SKILL.md"))
	}
	for _, n := range dropped {
		draftLines = append(draftLines, fmt.Sprintf("Draft skill %q dropped: not a kebab-case name", o.line(n)))
	}
	head := o.header(c, cli, day)
	var ps []brain.Proposal
	for _, p := range r.Proposals {
		// The target leads the title, so /retro tells a project's proposals
		// from the harness's without opening each (security review of the
		// dream over records, finding 3).
		target := "project"
		if p.Target == "harness" {
			target = "harness"
		}
		ps = append(ps, brain.Proposal{Title: "Dream, " + target + " — " + o.line(p.Title), Body: head + o.section(p)})
	}
	if len(draftLines) > 0 {
		ps = append(ps, brain.Proposal{Title: "Dream, harness — draft skills for " + c.Name, Body: head + strings.Join(draftLines, "\n") + "\n"})
	}
	_, err := propose(o.Home, c.Root, ps, t0)
	return err
}

// propose is brain.ProposeAll; a test replaces it.
var propose = brain.ProposeAll

// header is what every proposal of one project's dream carries: where it came
// from and what the model read.
func (o Options) header(c Collected, cli, day string) string {
	counts := map[string]int{}
	for _, in := range c.Inputs {
		counts[in.Kind]++
	}
	var inputs []string
	for _, k := range promptOrder {
		inputs = append(inputs, fmt.Sprintf("%s %d", k, counts[k]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Dream of %s, %s, by %s with no tool granted, from redacted inputs: %s.\n", o.line(c.Root), day, cli, strings.Join(inputs, ", "))
	if len(c.Dropped) > 0 {
		fmt.Fprintf(&b, "Left out: %s\n", o.line(strings.Join(c.Dropped, "; ")))
	}
	b.WriteString("\n")
	return b.String()
}

// section is one proposal's text. The model's text is indented four spaces,
// line by line, so it reads as quoted data, never as headings or fields of
// the record (audit 2026-09-30, SD3).
func (o Options) section(p proposal) string {
	target := "project"
	if p.Target == "harness" {
		target = "harness"
	}
	return fmt.Sprintf("Target: %s   Kind: %s   File: %s\n\n%s\n\nEvidence: %s\n",
		target, o.line(p.Kind), o.line(p.File), indent(strings.TrimSpace(o.clean(p.Text))), o.line(p.Evidence))
}

func (o Options) clean(s string) string { return scrub(s, o.Home) }

// indent prefixes every line of s with four spaces.
func indent(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// line is a one-line field: scrubbed, and its line breaks folded, so a model's
// title can never open a second **Status:** line or a fake section in the
// Status-driven queue (security review 2026-09-30).
func (o Options) line(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(o.clean(s))), " ")
}

// unique is dir/base+ext, or with -2, -3 … when that exists.
func unique(dir, base, ext string) (string, error) {
	for n := 1; n < 1000; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		p := filepath.Join(dir, name+ext)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
	}
	return "", fmt.Errorf("no free name for %s in %s", base, dir)
}
