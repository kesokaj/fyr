package relay

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type gitSnapshot struct {
	head       string
	tags       map[string]bool
	remoteRefs map[string]string
	upstream   string
}

func snapshotGitBefore(ctx context.Context, top string) (gitSnapshot, error) {
	head, err := gitHead(ctx, top)
	if err != nil {
		return gitSnapshot{}, refusef("resolve HEAD: %v", err)
	}
	tags, err := gitTags(ctx, top)
	if err != nil {
		return gitSnapshot{}, err
	}
	remoteRefs, err := gitRemoteRefs(ctx, top)
	if err != nil {
		return gitSnapshot{}, err
	}
	return gitSnapshot{
		head:       head,
		tags:       tags,
		remoteRefs: remoteRefs,
		upstream:   gitUpstreamRef(ctx, top),
	}, nil
}

func gitRemoteRefs(ctx context.Context, root string) (map[string]string, error) {
	lines, err := gitLines(ctx, root, "for-each-ref", "--format=%(refname) %(objectname)", "refs/remotes/")
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string, len(lines))
	for _, line := range lines {
		if name, oid, ok := strings.Cut(line, " "); ok && name != "" {
			refs[name] = strings.TrimSpace(oid)
		}
	}
	return refs, nil
}

func gitUpstreamRef(ctx context.Context, root string) string {
	out, err := gitBytes(ctx, root, "rev-parse", "--verify", "-q", "@{u}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func changedRemoteRefs(before, after map[string]string, upBefore, upAfter string) []string {
	var diffs []string
	for k, v := range after {
		if bv, ok := before[k]; !ok || bv != v {
			diffs = append(diffs, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			diffs = append(diffs, k+" (deleted)")
		}
	}
	if upBefore != upAfter {
		diffs = append(diffs, "@{u}")
	}
	sort.Strings(diffs)
	return diffs
}

func gitIsAncestor(ctx context.Context, root, ancestor, descendant string) (bool, error) {
	_, err := gitBytes(ctx, root, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func outOfLeaseFiles(changed, patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	var outside []string
	for _, f := range changed {
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(f), "./"))
		if !matchesAnyLease(patterns, rel) {
			outside = append(outside, rel)
		}
	}
	sort.Strings(outside)
	return outside
}

func matchesAnyLease(patterns []string, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimPrefix(relPath, "./"))
	for _, pat := range patterns {
		if matchLeasePattern(pat, relPath) {
			return true
		}
	}
	return false
}

func matchLeasePattern(pat, relPath string) bool {
	if strings.HasSuffix(pat, "/") {
		return relPath == strings.TrimSuffix(pat, "/") || strings.HasPrefix(relPath, pat)
	}
	if !strings.Contains(pat, "**") {
		ok, err := path.Match(pat, relPath)
		return err == nil && ok
	}
	return matchLeaseSegments(strings.Split(pat, "/"), strings.Split(relPath, "/"))
}

func matchLeaseSegments(pats, segs []string) bool {
	if len(pats) == 0 {
		return len(segs) == 0
	}
	if pats[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchLeaseSegments(pats[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pats[0], segs[0])
	return err == nil && ok && matchLeaseSegments(pats[1:], segs[1:])
}
