package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Adopt copies a skill installed somewhere else — by hand, or by
// `npx skills add -g` into ~/.agents/skills — into the repo's skills/, so it
// is versioned and deployed on the next install. src is a path or a bare
// skill name looked up in the usual global locations.
func Adopt(repo, home, src string, force bool) (string, error) {
	from, err := findSkill(home, src)
	if err != nil {
		return "", err
	}
	name, dest, err := adoptDest(repo, src, from, force)
	if err != nil {
		return "", err
	}
	staging, err := stageAdopt(repo, name, from)
	if err != nil {
		return "", err
	}
	// NO `defer os.RemoveAll(staging)`: on every failure path below the
	// prepared copy is what the operator is told to recover from, so deleting
	// it on the way out would make each of those messages name a path that no
	// longer exists (round-2 release review). staging is removed only when it
	// has been renamed into place, which is when it no longer exists anyway.
	if err := swapIn(dest, staging); err != nil {
		return "", err
	}
	return dest, nil
}

// findSkill resolves src, a path or a bare skill name looked up in the usual
// global locations, to the absolute directory holding its SKILL.md.
func findSkill(home, src string) (string, error) {
	candidates := []string{
		src,
		filepath.Join(home, ".agents", "skills", src),
		filepath.Join(home, AgyRoot, "skills", src),
	}
	var from string
	for _, c := range candidates {
		resolved, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(resolved, "SKILL.md")); err == nil {
			from = resolved
			break
		}
	}
	if from == "" {
		return "", fmt.Errorf("no skill directory with a SKILL.md found for %q", src)
	}

	// EvalSymlinks leaves a relative path relative, so ".", ".." and
	// "skills/<name>" reached Base unchanged and named the deletion root
	// itself, or the source (BUG-037).
	return filepath.Abs(from)
}

// adoptDest names the skill and its destination in the repo's skills/, and
// refuses a destination that overlaps the source or exists without force.
func adoptDest(repo, src, from string, force bool) (name, dest string, err error) {
	name = filepath.Base(from)
	if err := validName("skill", src, name); err != nil {
		return "", "", err
	}
	dest, err = filepath.Abs(filepath.Join(repo, "skills", name))
	if err != nil {
		return "", "", err
	}
	// from is resolved, so dest is compared resolved too: through a linked
	// repo the two would never share a prefix and --force could replace a
	// skill with its own subdirectory (IN42 of the 2026-10-27 audit).
	cmp := dest
	if rs, err := filepath.EvalSymlinks(filepath.Dir(dest)); err == nil {
		cmp = filepath.Join(rs, name)
	}
	if within(from, cmp) || within(cmp, from) {
		return "", "", fmt.Errorf("%s and %s overlap; adopt copies a skill directory from outside the repo's skills/", from, dest)
	}
	_, err = os.Stat(dest)
	switch {
	case err == nil && !force:
		return "", "", fmt.Errorf("%s already exists; pass --force to replace it", dest)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", "", err
	}
	return name, dest, nil
}

// stageAdopt copies from into a fresh staging directory and returns it.
func stageAdopt(repo, name, from string) (string, error) {
	// Copy into a staging directory and rename it into place, so a copy that
	// fails part-way — a symlink refused mid-walk, a read error, a full disk
	// — leaves the existing skill untouched instead of a half-written one.
	// Nothing is removed until the replacement is whole.
	//
	// Staging lives in .agents/scratch, NOT beside dest inside skills/: a
	// process killed between the copy and the rename would otherwise leave a
	// directory holding a complete SKILL.md whose frontmatter name matches
	// the real skill, and loadSkills would then carry two entries of the same
	// name into every install, status and diff (code review 2026-09-16). It
	// also means no real skill directory can ever collide with the staging
	// name. Same filesystem as skills/, so the rename stays atomic.
	scratch := filepath.Join(repo, ".agents", "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", err
	}
	staging := filepath.Join(scratch, fmt.Sprintf("adopt-%s-%d", name, os.Getpid()))
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	if err := copyTree(from, staging); err != nil {
		// Nothing has been moved yet, so a partial copy is the only debris.
		os.RemoveAll(staging)
		return "", err
	}
	return staging, nil
}

// swapIn renames staging into dest, moving an existing dest aside first.
func swapIn(dest, staging string) error {
	// Move the existing skill aside rather than deleting it, and put it back
	// if the replacement cannot be moved into place. The earlier shape —
	// RemoveAll(dest) then Rename, with RemoveAll(staging) on the error path
	// — could delete BOTH the original and its prepared replacement when the
	// rename failed, for instance across a filesystem boundary (release
	// review of v1.0.0, finding 3).
	backup := staging + ".previous"
	hadPrevious := false
	if _, err := os.Stat(dest); err == nil {
		if err := os.RemoveAll(backup); err != nil {
			return err
		}
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("could not move the existing %s aside: %w; it is untouched and the prepared copy is at %s", dest, err, staging)
		}
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, dest); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, dest); restoreErr != nil {
				return fmt.Errorf("could not install %s (%v) and could not put the previous skill back (%v); the previous copy is at %s and the new one at %s — move one of them into place by hand", dest, err, restoreErr, backup, staging)
			}
			return fmt.Errorf("could not install %s: %w; the previous skill is back in place and the prepared copy is at %s", dest, err, staging)
		}
		return fmt.Errorf("could not install %s: %w; there was no previous skill of that name and the prepared copy is at %s", dest, err, staging)
	}
	os.RemoveAll(backup)
	return nil
}

// within reports whether child is parent itself or lies inside it. Adopt
// removes dest before copying, so neither path may contain the other.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		// Same refusal as Plan's walk: a link would be copied as its target's
		// bytes under the link's mode (security review 2026-09-14).
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink — refused", path)
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == ".DS_Store" {
			return nil
		}
		// Refuse a credential at the ENTRANCE too, not only in Plan's deploy
		// walk: adopt copies a directory into skills/, where it is committed
		// and pushed, and the install-time refusal would then fire on every
		// consumer rather than here, after the secret had already been
		// published (security review of v1.0.0, round 2).
		if SecretShaped(d.Name()) {
			return fmt.Errorf("%s is a secret-shaped file — adopt refuses to copy it into the repo; remove it from the skill first", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// As plan.go does for every deployed file: an adopted 0664 would sit
		// group-writable in the tree every install ships, and git records only
		// the x bit (security audit 2026-09-18).
		return WriteAtomic(target, data, info.Mode().Perm()&0o755|0o600)
	})
}
