package search

import (
	"context"
	"path/filepath"
	"strings"
)

func sameCleanPath(a, b string) bool {
	return sameAutoInitPath(filepath.Clean(strings.TrimSpace(a)), filepath.Clean(strings.TrimSpace(b)))
}

func bashToolProjectDir(dir string, in HookInput) string {
	cwd := extractStringArg(in.ToolCall.Args, "Cwd", "cwd", "workingDirectory", "working_directory")
	if cwd == "" {
		cwd = strings.TrimSpace(in.Cwd)
	}
	if cwd == "" {
		return resolveToolProjectDir(dir, in, "")
	}
	if !filepath.IsAbs(cwd) && dir != "" {
		cwd = filepath.Join(dir, cwd)
	}
	if td := resolveToolProjectDir(dir, in, cwd); !sameCleanPath(dir, td) {
		return td
	}
	return cwd
}

type bashLedgerHandle struct {
	st    *store
	root  string
	ownSt bool
}

type bashLedgerCache struct{ byRoot map[string]bashLedgerHandle }

func (c *bashLedgerCache) resolve(dir, home, toolDir string, wdSt *store, wdRoot string) (*store, string) {
	ledgerDir := resolveSessionLedgerDir(dir, toolDir)
	key := ledgerDir
	if root, _, ok := findGitRootFast(ledgerDir); ok {
		key = CanonicalizePath("", root)
	}
	if h, ok := c.byRoot[key]; ok {
		return h.st, h.root
	}
	st, root, ownSt := resolveReusableReadLedgerStore(dir, home, toolDir, wdSt, wdRoot)
	c.byRoot[key] = bashLedgerHandle{st: st, root: root, ownSt: ownSt}
	return st, root
}

func (c *bashLedgerCache) close() {
	for _, h := range c.byRoot {
		if h.ownSt && h.st != nil {
			h.st.close()
		}
	}
}

type resolvedBashRead struct {
	read     BashFileRead
	snap     readFileSnapshot
	st       *store
	root     string
	unranged bool
}

func isUnrangedBashRead(r BashFileRead) bool {
	switch r.Tool {
	case "cat", "nl", "bat":
		return r.StartLine <= 1 && r.EndLine == 0 && r.TailLines == 0
	default:
		return false
	}
}

func resolveBashReadTargets(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, reads []BashFileRead, cache *bashLedgerCache) []resolvedBashRead {
	items := make([]resolvedBashRead, 0, len(reads))
	for _, r := range reads {
		if ctx.Err() != nil {
			break
		}
		toolDir := resolveToolProjectDir(dir, in, r.Path)
		if hooksDisabledForHome(home, toolDir) {
			continue
		}
		resolved := CanonicalizePath(toolDir, r.Path)
		candRoot := resolveReadGuardRoot(dir, toolDir, wdRoot)
		if !shouldGuardReadInRoot(candRoot, resolved) {
			continue
		}
		st, root := cache.resolve(dir, home, toolDir, wdSt, wdRoot)
		if st == nil || !shouldGuardReadInRoot(root, resolved) {
			continue
		}
		snap := loadReadFileSnapshot(resolved)
		if !snap.ok {
			continue
		}
		items = append(items, resolvedBashRead{read: r, snap: snap, st: st, root: root, unranged: isUnrangedBashRead(r)})
	}
	return items
}
