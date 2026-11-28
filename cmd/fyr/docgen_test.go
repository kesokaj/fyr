package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/redact"
)

const (
	cliRefRelPath    = "docs/cli-reference.md"
	cliRefTableLimit = 12
)

var (
	simFlagArgRe  = regexp.MustCompile(`--([a-z][a-z0-9-]*)(?:\s+([A-Z][A-Z0-9_-]*))?`)
	destructiveRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])(?:rm\s+-rf|git\s+reset\s+--hard)\b|\bdrop\s+table\b|(?:^|[\s\[\(])--?(?:auto-approve|force)(?:\b|=|$)`)
)

type cliVerbMeta struct{ group, summary string }
type cliFlagRow struct{ command, arg, desc string }
type simHelpInfo struct {
	synopsis []string
	args     map[string]string
	actions  map[string][]string
}

func TestCLIDocsFresh(t *testing.T) {
	want := renderCLIReference()
	if again := renderCLIReference(); want != again {
		t.Fatalf("renderCLIReference is non-deterministic:\n%s", firstLineDiff(want, again))
	}
	assertNoRedactionHits(t, cliRefRelPath, want)

	docPath := filepath.Join("..", "..", filepath.FromSlash(cliRefRelPath))
	if os.Getenv("FYR_UPDATE_DOCS") == "1" {
		if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(docPath), err)
		}
		if err := os.WriteFile(docPath, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", cliRefRelPath, err)
		}
	}
	gotBytes, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("%s missing (%v); regenerate with: FYR_UPDATE_DOCS=1 go test ./cmd/fyr -run TestCLIDocsFresh", cliRefRelPath, err)
	}
	if got := string(gotBytes); got != want {
		t.Fatalf("%s is out of date (%s); regenerate with: FYR_UPDATE_DOCS=1 go test ./cmd/fyr -run TestCLIDocsFresh",
			cliRefRelPath, firstLineDiff(want, got))
	}
}

func TestCLIDocsStandaloneCheckAndDrift(t *testing.T) {
	doc, vocab := renderCLIReference(), vocabulary()
	if missing := missingCLIDocEntries(doc, vocab); len(missing) > 0 {
		t.Fatalf("generated %s missing entries: %v", cliRefRelPath, missing)
	}
	dropped := strings.Replace(doc, "`fyr search --slice`", "`fyr search --removed`", 1)
	if missing := missingCLIDocEntries(dropped, vocab); len(missing) == 0 {
		t.Fatal("drift check missed dropped flag `fyr search --slice`")
	}
	if diff := firstLineDiff(doc, dropped); diff == "" {
		t.Fatal("firstLineDiff returned empty string for drifted document")
	}

	rep := runStandaloneCheck(t, doc, vocab)
	if len(rep.Findings) > 0 {
		t.Fatalf("standalone check on %s found %d findings: %+v", cliRefRelPath, len(rep.Findings), rep.Findings)
	}
	for _, note := range rep.Notes {
		if strings.HasPrefix(note, "docs-style-note:") {
			t.Fatalf("standalone check emitted style note: %s", note)
		}
	}

	repDrift := runStandaloneCheck(t, strings.ReplaceAll(doc, "--slice", "--help"), vocab)
	foundSliceUndoc := false
	for _, f := range repDrift.Findings {
		if f.Rule == "flag-undoc" && strings.Contains(f.Text, "`--slice`") {
			foundSliceUndoc = true
		}
	}
	if !foundSliceUndoc {
		t.Fatalf("expected flag-undoc finding for dropped --slice, got: %+v", repDrift.Findings)
	}
	if b, err := os.ReadFile(filepath.Join("..", "..", "docs", "glossary.md")); err == nil {
		assertNoRedactionHits(t, "docs/glossary.md", string(b))
	}
}

func runStandaloneCheck(t *testing.T, cliDoc string, vocab check.Vocabulary) check.Report {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	for rel, body := range map[string]string{cliRefRelPath: cliDoc, "cmd/fyr/stub.go": buildFlagStubSource(vocab)} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"},
		{"add", "-A"}, {"commit", "-q", "-m", "fixture"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	rep, err := check.Run(root, vocab, home)
	if err != nil {
		t.Fatalf("check.Run: %v", err)
	}
	return rep
}

func buildFlagStubSource(vocab check.Vocabulary) string {
	seen := map[string]bool{}
	for _, spec := range vocab {
		maps.Copy(seen, spec.Flags)
		for _, subFlags := range spec.Subverbs {
			maps.Copy(seen, subFlags)
		}
	}
	var b strings.Builder
	b.WriteString("package main\n\nimport \"flag\"\n\nfunc registerStub(fs *flag.FlagSet) {\n")
	for _, name := range sortedNonGlobalFlags(seen) {
		fmt.Fprintf(&b, "\tfs.String(%q, \"\", \"\")\n", name)
	}
	b.WriteString("}\n")
	return b.String()
}

func assertNoRedactionHits(t *testing.T, label, content string) {
	t.Helper()
	for i, line := range strings.Split(content, "\n") {
		if redact.Redact(line) != line {
			t.Errorf("%s:%d triggered redact.Redact: %q", label, i+1, line)
		}
	}
}

func firstLineDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < min(len(wl), len(gl)); i++ {
		if wl[i] != gl[i] {
			return fmt.Sprintf("first diff at line %d: want %q, got %q", i+1, wl[i], gl[i])
		}
	}
	if len(wl) != len(gl) {
		return fmt.Sprintf("line count differs: want %d, got %d", len(wl), len(gl))
	}
	return ""
}

func missingCLIDocEntries(doc string, vocab check.Vocabulary) []string {
	var missing []string
	require := func(tok string) {
		if !strings.Contains(doc, tok) {
			missing = append(missing, tok)
		}
	}
	for _, tok := range []string{"`-C`", "`--dir`", "`--help`", "`--home`", "`--repo`"} {
		require(tok)
	}
	for _, verb := range sortedMapKeys(vocab) {
		require(fmt.Sprintf("### `fyr %s`", verb))
		spec := vocab[verb]
		if len(spec.Subverbs) == 0 {
			for _, f := range sortedNonGlobalFlags(spec.Flags) {
				require(fmt.Sprintf("`fyr %s --%s`", verb, f))
			}
			continue
		}
		for _, sub := range sortedMapKeys(spec.Subverbs) {
			require(fmt.Sprintf("#### `fyr %s %s`", verb, sub))
			for _, f := range sortedNonGlobalFlags(spec.Subverbs[sub]) {
				require(fmt.Sprintf("`fyr %s %s --%s`", verb, sub, f))
			}
		}
	}
	return missing
}

func renderCLIReference() string {
	vocab, meta, simInfo := vocabulary(), parseShortPageMeta(), parseAutoSIMHelp()
	verbNames := sortedMapKeys(vocab)
	var b strings.Builder
	b.WriteString("<!-- Code generated by cmd/fyr/docgen_test.go; DO NOT EDIT. Regenerate with: FYR_UPDATE_DOCS=1 go test ./cmd/fyr -run TestCLIDocsFresh -->\n")
	b.WriteString("# CLI Reference — `fyr`\n\n")
	b.WriteString("This reference documents every `fyr` command, subcommand, and flag. ")
	b.WriteString("It is generated from `cmd/fyr` flag registrations and help metadata. ")
	b.WriteString("Run `fyr help <command>` for full terminal help.\n\n")
	writeGlobalFlagsSection(&b)
	writeCommandSummarySection(&b, verbNames, meta)
	b.WriteString("## Commands\n\nCommands are sorted alphabetically with exact usage and flags.\n")
	for _, name := range verbNames {
		writeVerbSection(&b, name, vocab[name], meta[name], simInfo)
	}
	return b.String()
}

func writeGlobalFlagsSection(b *strings.Builder) {
	b.WriteString("## Global Flags\n\n")
	b.WriteString("Every `fyr` command accepts these flags before or after the command name. ")
	b.WriteString("Without `--home`, builds outside `~/.local/bin/fyr` run only `install`, `sync`, `update`, `version`, `list`, `render`, and `diff`.\n\n")
	fs := flag.NewFlagSet("global", flag.ContinueOnError)
	registerGlobalFlags(fs, nil)
	var rows []cliFlagRow
	fs.VisitAll(func(f *flag.Flag) {
		arg, use := flag.UnquoteUsage(f)
		rows = append(rows, cliFlagRow{formatFlagToken(f.Name), formatArgCell(arg), cleanTableCell(use)})
	})
	rows = append(rows, cliFlagRow{"`--help`", "—", "Print the help page for the command (`-h` alias)."})
	sort.Slice(rows, func(i, j int) bool { return rows[i].command < rows[j].command })
	writeFlagTable(b, rows, "Global flags")
	b.WriteString("\n")
}

func writeCommandSummarySection(b *strings.Builder, verbNames []string, meta map[string]cliVerbMeta) {
	b.WriteString("## Command Summary\n\nUse this index to jump to any `fyr` command.\n\n")
	if len(verbNames) > cliRefTableLimit {
		fmt.Fprintf(b, "<details>\n<summary>All commands (%d)</summary>\n\n", len(verbNames))
	}
	b.WriteString("| Command | Group | Summary |\n|---|---|---|\n")
	for _, name := range verbNames {
		m := meta[name]
		if m.group == "" {
			m.group = "General"
		}
		fmt.Fprintf(b, "| [`fyr %s`](#fyr-%s) | %s | %s |\n", name, name, cleanTableCell(m.group), cleanTableCell(m.summary))
	}
	if len(verbNames) > cliRefTableLimit {
		b.WriteString("\n</details>\n")
	}
	b.WriteString("\n")
}

func writeVerbSection(b *strings.Builder, name string, spec check.VerbSpec, m cliVerbMeta, simInfo simHelpInfo) {
	fmt.Fprintf(b, "\n### `fyr %s`\n\n%s\n\n", name, formatVerbBLUF(name, m.summary))
	syn := verbSynopsis(name)
	if caution := verbCautionText(name, syn); caution != "" {
		fmt.Fprintf(b, "> **Caution:** %s\n\n", caution)
	}
	b.WriteString("```text\n" + strings.Join(syn, "\n") + "\n```\n\n")
	flagMeta := collectVerbFlagMeta(name)
	if len(spec.Subverbs) == 0 {
		rows := buildFlagRows(name, "", sortedNonGlobalFlags(spec.Flags), flagMeta, simInfo)
		if len(rows) == 0 {
			b.WriteString("This command takes only global flags.\n")
			return
		}
		writeFlagTable(b, rows, fmt.Sprintf("Flags (%d)", len(rows)))
		return
	}
	covered := map[string]bool{}
	for i, sub := range sortedMapKeys(spec.Subverbs) {
		if i > 0 {
			b.WriteString("\n")
		}
		writeSubverbSection(b, name, sub, spec.Subverbs[sub], flagMeta, simInfo)
		maps.Copy(covered, spec.Subverbs[sub])
	}
	var extra []string
	for _, f := range sortedNonGlobalFlags(spec.Flags) {
		if !covered[f] {
			extra = append(extra, f)
		}
	}
	if len(extra) > 0 {
		b.WriteString("\n#### Top-level flags\n\n")
		writeFlagTable(b, buildFlagRows(name, "", extra, flagMeta, simInfo), fmt.Sprintf("Flags (%d)", len(extra)))
	}
}

func writeSubverbSection(b *strings.Builder, verb, sub string, flags map[string]bool, flagMeta map[string][2]string, simInfo simHelpInfo) {
	fmt.Fprintf(b, "#### `fyr %s %s`\n\n", verb, sub)
	if verb == "auto" && sub == "sim" {
		b.WriteString("Manage the global Self-Improvement Memory (SIM) ledger in `brain.db`.\n\n")
		b.WriteString("```text\n" + strings.Join(simInfo.synopsis, "\n") + "\n```\n\n")
	}
	rows := buildFlagRows(verb, sub, sortedNonGlobalFlags(flags), flagMeta, simInfo)
	if len(rows) == 0 {
		b.WriteString("This subcommand takes only global flags.\n")
		return
	}
	writeFlagTable(b, rows, fmt.Sprintf("Flags (%d)", len(rows)))
}

func buildFlagRows(verb, sub string, names []string, flagMeta map[string][2]string, simInfo simHelpInfo) []cliFlagRow {
	rows := make([]cliFlagRow, 0, len(names))
	prefix := "fyr " + verb
	if sub != "" {
		prefix += " " + sub
	}
	for _, f := range names {
		var arg, desc string
		if verb == "auto" && sub == "sim" {
			arg, desc = simInfo.args[f], formatSIMFlagDesc(f, simInfo.args[f], simInfo.actions[f])
		} else if pair, ok := flagMeta[f]; ok {
			arg, desc = pair[0], pair[1]
		}
		rows = append(rows, cliFlagRow{fmt.Sprintf("`%s --%s`", prefix, f), formatArgCell(arg), cleanTableCell(desc)})
	}
	return rows
}

func writeFlagTable(b *strings.Builder, rows []cliFlagRow, summary string) {
	if len(rows) > cliRefTableLimit {
		fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n", summary)
	}
	b.WriteString("| Flag | Argument | Description |\n|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| %s | %s | %s |\n", r.command, r.arg, r.desc)
	}
	if len(rows) > cliRefTableLimit {
		b.WriteString("\n</details>\n")
	}
}

func collectVerbFlagMeta(verb string) map[string][2]string {
	out := map[string][2]string{}
	vb, ok := allVerbs()[verb]
	if !ok {
		return out
	}
	newCtx(verb, vb, io.Discard).fs.VisitAll(func(f *flag.Flag) {
		if !isGlobalFlag(f.Name) {
			arg, use := flag.UnquoteUsage(f)
			out[f.Name] = [2]string{arg, use}
		}
	})
	return out
}

func parseShortPageMeta() map[string]cliVerbMeta {
	out := map[string]cliVerbMeta{
		"help": {"Harness", "print the short command list or one command help page"},
		"sync": {"Harness", "alias of install"},
	}
	var group, lastVerb string
	started := false
	for _, line := range strings.Split(shortPage, "\n") {
		if strings.HasPrefix(line, "Usage:") {
			started = true
			continue
		}
		if !started {
			continue
		}
		if strings.HasPrefix(line, "Every command takes") {
			break
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			lastVerb = ""
		case !strings.HasPrefix(line, " "):
			group, lastVerb = trimmed, ""
		case strings.HasPrefix(line, "   ") && lastVerb != "":
			m := out[lastVerb]
			m.summary += " " + trimmed
			out[lastVerb] = m
		default:
			if name, rest, ok := strings.Cut(trimmed, " "); ok {
				out[name] = cliVerbMeta{group, strings.TrimSpace(rest)}
				lastVerb = name
			}
		}
	}
	return out
}

func parseAutoSIMHelp() simHelpInfo {
	info := simHelpInfo{args: map[string]string{}, actions: map[string][]string{}}
	curAction := ""
	for _, line := range strings.Split(autoSIMHelp(), "\n") {
		if strings.TrimSpace(line) == "" {
			break
		}
		info.synopsis = append(info.synopsis, line)
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "usage:"))
		if rest, ok := strings.CutPrefix(trimmed, "fyr auto sim "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				curAction = fields[0]
			}
		}
		for _, m := range simFlagArgRe.FindAllStringSubmatch(line, -1) {
			if m[2] != "" && info.args[m[1]] == "" {
				info.args[m[1]] = m[2]
			}
			if curAction != "" && !slices.Contains(info.actions[m[1]], curAction) {
				info.actions[m[1]] = append(info.actions[m[1]], curAction)
			}
		}
	}
	return info
}

func formatSIMFlagDesc(name, arg string, actions []string) string {
	scope := "SIM flag"
	if len(actions) > 0 {
		scope = "Used by `auto sim " + strings.Join(actions, "`, `auto sim ") + "`"
	}
	switch {
	case name == "json":
		return scope + ": print output as JSON."
	case name == "json-in":
		return scope + ": read SIM record JSON payload from stdin."
	case arg == "":
		return scope + "."
	default:
		return fmt.Sprintf("%s: pass %s value.", scope, arg)
	}
}

func verbSynopsis(verb string) []string {
	if verb == "help" {
		return []string{helpSynopsis}
	}
	if verb == "sync" {
		p, _ := pageOf("install")
		out := make([]string, len(p.synopsis))
		for i, l := range p.synopsis {
			out[i] = strings.Replace(l, "fyr install", "fyr sync", 1)
		}
		return out
	}
	p, _ := pageOf(verb)
	return p.synopsis
}

func verbCautionText(verb string, synopsis []string) string {
	switch verb {
	case "uninstall":
		return "Removes files written by `fyr install` and any auto timer. Preview changes with `--dry-run` first."
	case "adopt":
		return "Passing `--force` replaces an existing skill of the same name in `skills/`."
	case "auto":
		return "Passing `--force` to `fyr auto run` bypasses the 4-hour model-run cooldown."
	case "graph":
		return "Passing `--force` overwrites an existing output file."
	}
	for _, line := range synopsis {
		if destructiveRe.MatchString(line) {
			return "This command includes a destructive or force flag. Preview changes before running it."
		}
	}
	return ""
}

func formatVerbBLUF(verb, summary string) string {
	if verb == "sync" {
		return "Alias of `fyr install`."
	}
	if summary == "" {
		return fmt.Sprintf("Run the `fyr %s` command.", verb)
	}
	s := strings.TrimSpace(summary)
	if len(s) > 0 && s[0] >= 'a' && s[0] <= 'z' {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func formatFlagToken(name string) string {
	if len(name) == 1 {
		return "`-" + name + "`"
	}
	return "`--" + name + "`"
}

func formatArgCell(arg string) string {
	if strings.TrimSpace(arg) == "" {
		return "—"
	}
	return "`" + cleanTableCell(arg) + "`"
}

func cleanTableCell(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", `\|`)
}

func sortedMapKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func sortedNonGlobalFlags(flags map[string]bool) []string {
	var out []string
	for f := range flags {
		if !isGlobalFlag(f) && f != "help" {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}
