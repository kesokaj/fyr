package metrics

import (
	"reflect"
	"strings"
	"testing"
)

func TestVerb(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"git status", []string{"git status"}},
		{"git add -A && git commit -m 'fix: x; y'", []string{"git add -A", "git commit -m"}},
		{"git add .", []string{"git add ."}},
		{"git add internal/x.go", []string{"git add"}},
		{"gofmt -l cmd internal | head", []string{"gofmt -l", "head"}},
		{"FOO=1 BAR=x sudo -n go test -race -count=1 ./...", []string{"go test -race -count"}},
		{"go run ./cmd/sh-agent search --refs internal/x.go --kind bug", []string{"sh-agent search --refs --kind"}},
		{"sh-agent search --refs x 2>/dev/null", []string{"sh-agent search --refs"}},
		{"env -u SH_AGENT_REPO sh-agent metrics ingest --home h", []string{"sh-agent metrics ingest --home"}},
		{"echo 'a; b && c' ; ls -la", []string{"echo", "ls -la"}},
		{"git push --force-with-lease=origin/main", []string{"git push --force-with-lease"}},
		{"cat > f.md <<'EOF'\nsecret line one\nrm -rf everything\nEOF\ngit diff", []string{"cat", "git diff"}},
		{"cat <<-END\n\tbody words\n\tEND", []string{"cat"}},
		{"grep -rn needle . || rg needle", []string{"grep -rn", "rg"}},
		{"x=$(git rev-parse HEAD)", []string{"git rev-parse"}},
		{"python3 -I - <<'EOF'\nimport os\nEOF", []string{"python3 -I"}},
		{"go test ./... 2>&1 | tail -3; ls &>/dev/null", []string{"go test", "tail", "ls"}},
		{"sleep 5 & wait", []string{"sleep", "wait"}},
		{"for f in a b; do git add $f; done", []string{"git add"}},
		{"if grep -q x f; then echo y; else ls; fi", []string{"grep -q", "echo", "ls"}},
		{"while read l; do echo $l; done < f", []string{"read", "echo"}},
		{"! git diff --quiet", []string{"git diff --quiet"}},
		{"go test -race -count=1 -run TestX ./...", []string{"go test -race -count -run"}},
		{"grep -rn x . ; ls -la; tar -xzvf a.tgz; head -20 f; cut -c1-200", []string{"grep -rn", "ls -la", "tar -x", "head", "cut -c"}},
		{"mysql -psecret db", []string{"mysql -p"}},
		{"echo ${A:-b} $((1+2)) $(git rev-parse HEAD)", []string{"echo", "git rev-parse"}},
		{"case $k in a) git status;; b|c) ls;; esac", []string{"git status", "ls"}},
		{"# just a comment\ngit log --oneline # trailing", []string{"git log --oneline"}},
		{"git push -f origin main", []string{"git push -f"}},
		{"sh-agent metrics show --json --since 2026-10-01", []string{"sh-agent metrics show --json --since"}},
		{"sh-agent memory add --type user < x", []string{"sh-agent memory add --type"}},
		{"sh-agent metrics secretword --json", []string{"sh-agent metrics --json"}},
		{">/dev/null git status; > out.txt ls; 2>err cat f", []string{"git status", "ls", "cat"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := Verb(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Verb(%q)\n got %q\nwant %q", c.cmd, got, c.want)
		}
	}
}

// No positional word or value reaches a verb: the canary sits in each place a
// command carries content.
func TestVerbDropsContent(t *testing.T) {
	const c = "SECRETCANARY123"
	for _, cmd := range []string{
		"echo " + c,
		"git commit -m " + c,
		"git commit -m '" + c + " more'",
		"curl --header=" + c + " x",
		"cat <<EOF\n" + c + "\nEOF",
		"cat <<EOF\nok\n" + c + " ls\nEOF",
		"go run ./x " + c,
		"TOKEN=" + c + " ./run.sh",
		"echo ${X:-" + c + "}",
		"h=${p#" + c + "}; ls",
		"n=$((" + c + "+1))",
		"case $x in *" + c + "*) ls;; " + c + ") echo;; esac",
		"# " + c + " comment\nls",
		"ls # " + c,
		"mysql -p" + c + " db",
		"cut -c" + c,
		"tar -x" + c + "f",
		"cat " + c + ".md | wc -l",
		"for f in " + c + "; do echo; done",
		">" + c + " ls",
		"> " + c + " ls",
	} {
		for _, v := range Verb(cmd) {
			if strings.Contains(v, c) {
				t.Errorf("Verb(%q) kept the canary: %q", cmd, v)
			}
		}
	}
}

// Git's global options (-C dir, -c k=v, --git-dir=…) come before the
// subcommand and never hide it; a backslash-quoted here-doc is a here-doc.
func TestVerbGitGlobalsAndQuotedHeredoc(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"git -C /p add -A", []string{"git add -A"}},
		{"git -C /p commit -m x && go test ./...", []string{"git commit -m", "go test"}},
		{"git -c user.name=x -c core.fsmonitor=false push --force", []string{"git push --force"}},
		{"git --git-dir=/x --no-pager log --oneline", []string{"git log --oneline"}},
		{"cat > f <<\\EOF\nghp_abcdefABCDEF0123456789\nEOF\nls", []string{"cat", "ls"}},
		{"cat <<-\\END\n\tsk_live_token\n\tEND", []string{"cat"}},
	}
	for _, c := range cases {
		if got := Verb(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Verb(%q)\n got %q\nwant %q", c.cmd, got, c.want)
		}
	}
}

// Content the normaliser could not parse never reaches a verb: a here-doc
// delimiter is read literally to its end (<<"$D" ends at a line $D, as in
// bash), a here-doc with no delimiter word drops the rest, and a
// token-length flag or name is not kept (security review of v2.6.0, part 1).
func TestVerbUnparsedHeredocAndLongWords(t *testing.T) {
	const tok = "ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	for _, cmd := range []string{
		"cat <<\"$D\"\n" + tok + "\nrm -rf x\n$D",
		"cat <<$END\n" + tok + "\n$END",
		"curl --" + tok + " x",
		"npx " + tok,
		"go run ./" + tok,
	} {
		for _, v := range Verb(cmd) {
			if strings.Contains(v, tok) || strings.Contains(v, "rm") {
				t.Errorf("Verb(%q) kept %q", cmd, v)
			}
		}
	}
}

// A quoted delimiter is read whole, to its closing quote: <<'END!' ends only
// at a line END!, never at END (security review of v2.6.0, part 1, round 2).
func TestVerbQuotedDelimiterReadWhole(t *testing.T) {
	cmd := "cat <<'END!'\nEND\nsecretword --flag\nEND!\nls"
	if got := Verb(cmd); !reflect.DeepEqual(got, []string{"cat", "ls"}) {
		t.Fatalf("Verb(%q) = %q, want [cat ls]", cmd, got)
	}
}

// Only words the normaliser knows are command names are kept: a subcommand
// from a fixed list per tool, never npx's package, and a long flag only when
// lowercase letters, digits and dashes (operator, 2026-10-09: allowlists).
func TestVerbAllowlists(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"npx AKIAIOSFODNN7EXAMPLE", []string{"npx"}},
		{"curl --sk_live_abcdefghij x", []string{"curl"}},
		{"curl --Authorization x", []string{"curl"}},
		{"gh hunter2pass", []string{"gh"}},
		{"gh pr view 12 --json title", []string{"gh pr --json"}},
		{"git sparse-checkout set x", []string{"git sparse-checkout"}},
		{"git notarealcmd", []string{"git"}},
		{"go test -race -RACE ./...", []string{"go test -race"}},
		{"docker compose up -d", []string{"docker compose -d"}},
		{"sh-agent metrics show --json", []string{"sh-agent metrics show --json"}},
	}
	for _, c := range cases {
		if got := Verb(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Verb(%q)\n got %q\nwant %q", c.cmd, got, c.want)
		}
	}
}

// Round 4 (operator, 2026-10-09: fix everything). go run keeps its target only
// when that target is a tool the allowlist knows; an unquoted delimiter is the
// whole word up to a shell metacharacter, read literally as bash does.
func TestVerbRound4(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"go run hunter2pass", []string{"go run"}},
		{"go run ./AKIAIOSFODNN7EXAMPLE --x", []string{"go run --x"}},
		{"go run ./cmd/sh-agent search --refs x", []string{"sh-agent search --refs"}},
		{"cat <<END!\nEND\nsecretword --flag\nEND!\nls", []string{"cat", "ls"}},
		{"cat <<$END\nsecretword\n$END\nls", []string{"cat", "ls"}},
		{"cat <<EOF;\nsecretword\nEOF\nls", []string{"cat", "ls"}},
	}
	for _, c := range cases {
		if got := Verb(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Verb(%q)\n got %q\nwant %q", c.cmd, got, c.want)
		}
	}
}

// A delimiter mixing quoted and unquoted parts (E'ND') is not modelled: the
// rest of the command is dropped rather than read with a wrong delimiter.
func TestVerbMixedQuoteDelimiter(t *testing.T) {
	for _, cmd := range []string{
		"cat <<E'ND'\nbody1\nE\nleak_mixed x\nEND",
		"cat <<\"E\"ND\nbody1\nE\nleak_b x\nEND",
		"cat <<'E'\"N\"D\nbody1\nE\nleak_c x\nEND",
	} {
		if got := Verb(cmd); !reflect.DeepEqual(got, []string{"cat"}) {
			t.Errorf("Verb(%q) = %q, want [cat]", cmd, got)
		}
	}
}
