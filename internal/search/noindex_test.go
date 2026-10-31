package search

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A directory holding a `.fyr-noindex` file is left out of the index,
// files below it included, tracked or not: vendored data a project
// carries (the harness's Google skills catalog) would otherwise crowd the
// project's own files out of the hits (recall 0.633 -> 0.596 on the fixture,
// 2026-10-01). Files beside the directory stay in.
func TestNoIndexMarkerSkipsTheDirectory(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/data/.fyr-noindex", "")
	writeFile(t, root, "vendor/data/a/entry.md", "an ocelot in vendored data\n")
	writeFile(t, root, "vendor/beside.md", "an ocelot beside the marked directory\n")
	commitAll(t, root, "add")
	writeFile(t, root, "vendor/data/untracked.md", "an untracked ocelot in vendored data\n")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	for _, want := range []string{"docs/own.md:", "vendor/beside.md:"} {
		if !strings.Contains(out, want) {
			t.Errorf("a file outside the marked directory is missing (%s):\n%s", want, out)
		}
	}
	// A hit line starts with its path; the header names the directory itself.
	if strings.Contains(out, "\nvendor/data/") {
		t.Errorf("the marked directory is in the index:\n%s", out)
	}
	// A marker that is not committed yet counts too, and a reconcile drops
	// what was indexed before it appeared.
	writeFile(t, root, "docs/.fyr-noindex", "")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "docs/own.md") || !strings.Contains(out, "vendor/beside.md:") {
		t.Errorf("an uncommitted marker did not take docs/ out of the index:\n%s", out)
	}
	// A marker at the root of the checkout is a directory like any other:
	// nothing of the project is indexed.
	writeFile(t, root, ".fyr-noindex", "")
	var buf bytes.Buffer
	Search(Options{Dir: root, Home: home, Words: []string{"ocelot"}}, &buf) // no hit is an error here; the output is what matters
	if strings.Contains(buf.String(), "vendor/beside.md") || strings.Contains(buf.String(), "docs/own.md") {
		t.Errorf("a root marker left files in the index:\n%s", buf.String())
	}
}

// TestNoIndexMarkerDeletedCountsNoMore: a tracked marker removed from the
// work tree, before the removal is committed, already gives its directory
// back to the index (code review round 3: the branch had no test).
func TestNoIndexMarkerDeletedCountsNoMore(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/data/.fyr-noindex", "")
	writeFile(t, root, "vendor/data/entry.md", "an ocelot in vendored data\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "\nvendor/data/") {
		t.Fatalf("the marked directory is in the index before the marker is removed:\n%s", out)
	}
	if err := os.Remove(filepath.Join(root, "vendor/data/.fyr-noindex")); err != nil {
		t.Fatal(err)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "vendor/data/entry.md:") {
		t.Errorf("a marker deleted from the work tree still hides its directory:\n%s", out)
	}
}

// TestHeaderNamesLeftOutDirectories: a marker hides a directory, and the
// header of a words search and of --open says which, so an empty answer is
// not read as "nothing there" (the v2.1.0 gate's L6). The global header
// covers many projects and stays as it was.
func TestHeaderNamesLeftOutDirectories(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/beside.md", "an ocelot beside\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "left out") {
		t.Errorf("no marker, yet the header names one:\n%s", out)
	}
	writeFile(t, root, "vendor/data/.fyr-noindex", "")
	writeFile(t, root, "vendor/data/entry.md", "an ocelot in vendored data\n")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "; left out by a noindex marker: vendor/data/)") {
		t.Errorf("one marker: the header does not name it:\n%s", out)
	}
	writeFile(t, root, "attic/.fyr-noindex", "")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "; left out by a noindex marker: attic/, vendor/data/)") {
		t.Errorf("two markers: want them sorted in the header:\n%s", out)
	}
	var buf bytes.Buffer
	Search(Options{Dir: root, Home: home, Global: true, Words: []string{"ocelot"}}, &buf)
	if strings.Contains(buf.String(), "left out") {
		t.Errorf("--global: the header names one project's markers:\n%s", buf.String())
	}
	// A directory name is the project's own text: control characters in it
	// must not reach the terminal or forge a line of output (code review of
	// v2.1.0..a62eb9d).
	writeFile(t, root, "ev\x1bil\nfake-hit.md:1/.fyr-noindex", "")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\nfake-hit.md:1/") || !strings.Contains(out, "ev?il?fake-hit.md:1/") {
		t.Errorf("a marked directory's name reached the header raw:\n%q", out)
	}
	if err := os.RemoveAll(filepath.Join(root, "ev\x1bil\nfake-hit.md:1")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, ".fyr-noindex", "")
	buf.Reset()
	Search(Options{Dir: root, Home: home, Words: []string{"ocelot"}}, &buf) // no hit is an error here; the header is what matters
	if !strings.Contains(buf.String(), "left out by a noindex marker: ./, attic/, vendor/data/") {
		t.Errorf("a root marker: want ./ first in the header:\n%s", buf.String())
	}
}

// TestPrintableDropsLineAndDirectionControls: printable is the one filter
// between project text and the terminal (or a model reading the output). It
// replaces control characters, and also the Unicode line and paragraph
// separators and the bidirectional controls, which can forge a line or
// reorder what is shown (security review of a62eb9d..768efce), and, by
// Unicode property and not by a list, every format character, variation
// selector and other default-ignorable character, which hide text from the
// eye while a model still reads it (BUG-152).
func TestPrintableDropsLineAndDirectionControls(t *testing.T) {
	for in, want := range map[string]string{
		"plain/dir/":          "plain/dir/",
		"a\x1bb\nc":           "a?b?c",
		"a\u2028b\u2029c":     "a?b?c",
		"a\u202eb\u202cc":     "a?b?c",
		"a\u2066b\u2069c":     "a?b?c",
		"a\u200eb\u200fc":     "a?b?c",
		"åäö — ünïcode stays": "åäö — ünïcode stays",
		// The whole format class, not a list: tag characters spell text no
		// terminal shows, and the Arabic letter mark reorders its neighbours.
		"a\U000e0041\U000e0042b": "a??b",
		"a\u200bb\u2060c":        "a?b?c",
		"a\u061cb":               "a?b",
		"\ufeffstart":            "?start",
		"soft\u00adhyphen":       "soft?hyphen",
		// Default-ignorable characters outside the format class: variation
		// selectors, the combining grapheme joiner, the Hangul fillers. An
		// ordinary combining accent stays.
		"a\ufe0fb":        "a?b",
		"a\U000e0101b":    "a?b",
		"a\u034fb":        "a?b",
		"a\u3164b\u115fc": "a?b?c",
		"e\u0301":         "e\u0301",
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

// The marker's old name, from before the rename, still takes its directory
// out (plan fyr F8, D12): it is a privacy control the operator placed, and
// nothing renames the operator's files, so a directory holding only
// `.sh-agent-noindex` stays out of every index the dream reads.
func TestOldNoIndexMarkerStillCounts(t *testing.T) {
	for _, m := range []string{".fyr-noindex", ".sh-agent-noindex"} {
		if !isNoIndexMarker("vendor/" + m) {
			t.Fatalf("%s is not a noindex marker to search", m)
		}
	}
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "docs/own.md", "the ocelot lives here\n")
	writeFile(t, root, "vendor/old/.sh-agent-noindex", "")
	writeFile(t, root, "vendor/old/entry.md", "an ocelot under the old marker\n")
	writeFile(t, root, "vendor/new/.fyr-noindex", "")
	writeFile(t, root, "vendor/new/entry.md", "an ocelot under the new marker\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"ocelot"}})
	if !strings.Contains(out, "docs/own.md:") {
		t.Errorf("a file outside both marked directories is missing:\n%s", out)
	}
	if strings.Contains(out, "\nvendor/old/") || strings.Contains(out, "\nvendor/new/") {
		t.Errorf("a marked directory is in the index:\n%s", out)
	}
	if !strings.Contains(out, "; left out by a noindex marker: vendor/new/, vendor/old/)") {
		t.Errorf("the header does not name both directories:\n%s", out)
	}
}

// The brain's export is searched as records (internal/brain), never as a
// file: indexing it too printed every record twice (brain plan Phase 2).
func TestTrailExportIsNotAFile(t *testing.T) {
	root := newRepo(t)
	writeFile(t, root, ".agents/trail.jsonl", `{"date":"2026-10-25","id":"BUG-001","kind":"bug","status":"open","title":"zebrafinch","updated":"2026-10-25T00:00:00Z","body":"zebrafinch"}`+"\n")
	writeFile(t, root, "notes.md", "zebrafinch\n")
	commitAll(t, root, "trail")
	out := mustSearch(t, Options{Dir: root, Words: []string{"zebrafinch"}})
	if strings.Contains(out, "trail.jsonl") || !strings.Contains(out, "notes.md") {
		t.Errorf("trail.jsonl is listed as a file hit:\n%s", out)
	}
	// Untracked, it is skipped too.
	root = newRepo(t)
	writeFile(t, root, ".agents/trail.jsonl", "zebrafinch\n")
	if out := mustSearch(t, Options{Dir: root, Words: []string{"zebrafinch"}}); strings.Contains(out, "trail.jsonl") {
		t.Errorf("an untracked trail.jsonl is listed:\n%s", out)
	}
}
