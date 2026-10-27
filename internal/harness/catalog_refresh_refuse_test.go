package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pinRefusals refuses the pins that are not a commit's full id.
func (r *refreshRig) pinRefusals() {
	t, tmp, gskills, pinA, pinG, good := r.t, r.tmp, r.gskills, r.pinA, r.pinG, r.good
	if out, code := r.run(pinA, "0000000000000000000000000000000000000000"); code != 1 || !strings.Contains(out, "does not resolve in the google-skills source") || r.snapshot() != good {
		t.Errorf("unresolvable pin: want exit 1 and an untouched destination, got exit %d\n%s", code, out)
	}
	// A pin is a full commit id. git checks a tag or branch named like a
	// shorter one out before the abbreviated id, and a prefix check after the
	// checkout cannot tell a commit made to share the prefix: whoever names
	// refs at the source would choose the content (security reviews of
	// 01255bc..4487a7a and 4487a7a..5bbd789). Both shapes are refused before
	// any clone: a tag named in hex digits, and the real commit's short id.
	r.git(gskills, "tag", "20261001", pinG)
	for _, short := range []string{"20261001", pinG[:7], pinG[:39]} {
		if out, code := r.run(pinA, short); code != 1 || !strings.Contains(out, "a pin must be a full commit id (40 hex digits)") || r.snapshot() != good {
			t.Errorf("a short pin %q: want exit 1, the shape refusal and an untouched destination, got exit %d\n%s", short, code, out)
		}
	}
	r.git(gskills, "tag", "-d", "20261001")
	// A full id is no protection by itself: `git checkout --detach <id>`
	// takes a local branch of that exact name first, and a clone makes the
	// source's default branch local. A source whose default branch is named
	// like the pin, and points elsewhere, must not choose the content
	// (security review of 5bbd789..42670ff).
	{
		was := r.git(gskills, "symbolic-ref", "--short", "HEAD")
		r.put(gskills, map[string]string{"skills/cloud/gamma/PLANTED.md": "content of another commit\n"})
		other := r.commit(gskills)
		r.git(gskills, "branch", pinG, other)
		r.git(gskills, "symbolic-ref", "HEAD", "refs/heads/"+pinG)
		r.git(gskills, "reset", "-q", "--hard", other)
		bDest, bWork := filepath.Join(tmp, "branch-dest"), filepath.Join(tmp, "branch-work")
		out, err := r.workRun(bDest, bWork, pinA, pinG)
		if _, statErr := os.Stat(filepath.Join(bDest, "gamma", "PLANTED.md")); statErr == nil {
			t.Errorf("a default branch named like the pin chose the content: the other commit's file was vendored (err %v)\n%s", err, out)
		} else if err != nil {
			t.Errorf("the pinned commit is in the source and must be vendored whatever a branch is called: %v\n%s", err, out)
		} else if _, statErr := os.Stat(filepath.Join(bDest, "gamma", "SKILL.md")); statErr != nil {
			t.Errorf("the run vendored nothing: %v\n%s", statErr, out)
		}
		r.git(gskills, "symbolic-ref", "HEAD", "refs/heads/"+was)
		r.git(gskills, "branch", "-D", pinG)
		r.git(gskills, "reset", "-q", "--hard", pinG)
		r.git(gskills, "clean", "-qfd")
	}
	// The id of an annotated tag is forty hex digits and peels to a commit:
	// it is not that commit's id, and the manifest would record a pin that
	// names no commit. The comparison after the checkout refuses it.
	r.git(gskills, "tag", "-a", "annotated", "-m", "a tag object", pinG)
	tagObject := r.git(gskills, "rev-parse", "annotated")
	if tagObject == pinG {
		t.Fatal("fixture: the annotated tag's id is the commit's id")
	}
	if out, code := r.run(pinA, tagObject); code != 1 || !strings.Contains(out, "did not check out as itself in the google-skills source") || r.snapshot() != good {
		t.Errorf("a pin that is a tag object's id: want exit 1, the comparison's refusal and an untouched destination, got exit %d\n%s", code, out)
	}
	r.git(gskills, "tag", "-d", "annotated")
}

// entryRefusals refuses each kind of entry the catalog does not carry.
func (r *refreshRig) entryRefusals() {
	t, gskills := r.t, r.gskills
	r.refuse("no frontmatter", "bad", map[string]string{"skills/cloud/bad/SKILL.md": "no frontmatter here\n"}, nil, "bad")
	r.refuse("collision", "alpha", map[string]string{"skills/cloud/alpha/SKILL.md": fixtureSkill("alpha", "description: a second alpha\n")}, nil, "")
	r.refuse("symlink", "gamma", nil, func() {
		if err := os.Symlink("SKILL.md", filepath.Join(gskills, "skills/cloud/gamma/link.md")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "gamma")
	r.refuse("go.mod", "delta", map[string]string{"skills/cloud/delta/go.mod": "module x\n"}, nil, "")
	// An entry with very many offending files is refused in the same words:
	// `find | head -1` under pipefail ended the script with status 141 and no
	// message once find wrote more than a pipe holds (security review of
	// 4165f81..7b3e969). 1,500 dotfiles with long names are about 200 KB.
	r.refuse("many dotfiles", "holds a dotfile", nil, func() {
		many := map[string]string{}
		for i := 0; i < 1500; i++ {
			many[fmt.Sprintf("skills/cloud/delta/.a-dotfile-with-a-long-name-to-fill-the-pipe-%04d", i)] = "x\n"
		}
		r.put(gskills, many)
	}, "")
	r.refuse("go file", "epsilon", map[string]string{"skills/developers/epsilon/main.go": "package main\n"}, nil, "")
	// The hint is printed shell-quoted: the name comes from upstream and the
	// operator pastes the hint into a shell (code review round 3).
	r.refuse("name with a space", `(--exclude bad\ name)`, map[string]string{"skills/cloud/bad name/SKILL.md": fixtureSkill("bad-name", "description: a directory name with a space\n")}, nil, "bad name")
	r.refuse("uppercase name", `(--exclude Loud)`, map[string]string{"skills/cloud/Loud/SKILL.md": fixtureSkill("loud", "description: a directory name with a capital\n")}, nil, "Loud")
	r.refuse("shell characters in a name", `(--exclude a\$\(id\))`, map[string]string{"skills/cloud/a$(id)/SKILL.md": fixtureSkill("a-id", "description: a directory name a shell would expand\n")}, nil, "a$(id)")
}

// escapedNames prints an upstream name only shell-quoted.
func (r *refreshRig) escapedNames() {
	t, gskills, pinA, pinG := r.t, r.gskills, r.pinA, r.pinG
	// The refusal prints an upstream name only shell-quoted: a raw escape
	// sequence in a directory name must not reach the operator's terminal
	// (security review of the v2.1.0 gate).
	r.put(gskills, map[string]string{"skills/cloud/a\x1b[31mb/SKILL.md": fixtureSkill("a-b", "description: a directory name with a terminal escape\n")})
	escPin := r.commit(gskills)
	if out, code := r.run(pinA, escPin); code != 1 || strings.Contains(out, "\x1b") || !strings.Contains(out, "not a plain skill name") {
		t.Errorf("a name with an escape sequence: want exit 1 and no raw escape in the message, got exit %d\n%q", code, out)
	}
	r.git(gskills, "reset", "-q", "--hard", pinG)
	r.git(gskills, "clean", "-qfd")
	// The same for a path inside an entry: the dotfile, symlink and Go-file
	// refusals print the path shell-quoted. One fixture per message: each is
	// its own line in the script (the gate's verifier removed the quoting
	// from one and nothing failed).
	for _, c := range []struct {
		label, want string
		make        func()
	}{
		{"dotfile", "holds a dotfile", func() {
			r.put(gskills, map[string]string{"skills/cloud/delta/.x\x1b[31m": "a dotfile whose name holds an escape\n"})
		}},
		{"Go file", "holds a Go file", func() {
			r.put(gskills, map[string]string{"skills/cloud/delta/a\x1b[31m.go": "package main\n"})
		}},
		{"symlink", "holds a symlink", func() {
			if err := os.Symlink("SKILL.md", filepath.Join(gskills, "skills/cloud/delta/l\x1b[31mnk")); err != nil {
				t.Skip("no symlinks here")
			}
		}},
	} {
		c.make()
		pin := r.commit(gskills)
		if out, code := r.run(pinA, pin); code != 1 || strings.Contains(out, "\x1b") || !strings.Contains(out, c.want) {
			t.Errorf("a nested %s path with an escape sequence: want exit 1, %q and no raw escape in the message, got exit %d\n%q", c.label, c.want, code, out)
		}
		r.git(gskills, "reset", "-q", "--hard", pinG)
		r.git(gskills, "clean", "-qfd")
	}
}

// excludeOneName excludes one name, spaces and all.
func (r *refreshRig) excludeOneName() {
	t, gskills, dest, pinA, pinG, good := r.t, r.gskills, r.dest, r.pinA, r.pinG, r.good
	// An excluded name is one name: excluding "delta epsilon" must not also
	// exclude the entries delta and epsilon.
	r.put(gskills, map[string]string{"skills/cloud/delta epsilon/SKILL.md": fixtureSkill("delta-epsilon", "description: two entry names with a space between\n")})
	wordsPin := r.commit(gskills)
	if out, code := r.run("--exclude", "delta epsilon", pinA, wordsPin); code != 0 {
		t.Errorf("--exclude \"delta epsilon\" failed (exit %d)\n%s", code, out)
	}
	for _, name := range []string{"delta", "epsilon"} {
		if _, err := os.Stat(filepath.Join(dest, name, "SKILL.md")); err != nil {
			t.Errorf("excluding \"delta epsilon\" also excluded the entry %s", name)
		}
	}
	r.git(gskills, "reset", "-q", "--hard", pinG)
	r.git(gskills, "clean", "-qfd")
	if out, code := r.run(pinA, pinG); code != 0 || r.snapshot() != good {
		t.Fatalf("could not restore the good catalog after the two-word exclusion (exit %d)\n%s", code, out)
	}
}

// moreRefusals refuses a dotfile and the symlinked shapes of an entry.
func (r *refreshRig) moreRefusals() {
	t, gskills := r.t, r.gskills
	r.refuse("dotfile", "delta", map[string]string{"skills/cloud/delta/.gitattributes": "* export-ignore\n"}, nil, "")
	r.refuse("symlinked entry", "zeta", nil, func() {
		if err := os.Symlink("gamma", filepath.Join(gskills, "skills/cloud/zeta")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "")
	r.refuse("symlinked source set", "leaves the clone through a symlink", nil, func() {
		// skills/developers becomes a link to a directory elsewhere in the clone.
		if err := os.Rename(filepath.Join(gskills, "skills/developers"), filepath.Join(gskills, "elsewhere")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../elsewhere", filepath.Join(gskills, "skills/developers")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "")
	r.refuse("symlinked LICENSE", "LICENSE", nil, func() {
		os.Remove(filepath.Join(gskills, "LICENSE"))
		if err := os.Symlink("skills/cloud/gamma/SKILL.md", filepath.Join(gskills, "LICENSE")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "")
}
