package hooks

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/readledger"
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
		return ResolveToolProjectDir(dir, in, "")
	}
	if !filepath.IsAbs(cwd) && dir != "" {
		cwd = filepath.Join(dir, cwd)
	}
	if td := ResolveToolProjectDir(dir, in, cwd); !sameCleanPath(dir, td) {
		return td
	}
	return cwd
}

type bashLedgerHandle struct {
	st    *Store
	root  string
	ownSt bool
}

type bashLedgerCache struct{ byRoot map[string]bashLedgerHandle }

func (c *bashLedgerCache) resolve(d Deps, dir, home, toolDir string, wdSt *Store, wdRoot string) (*Store, string) {
	ledgerDir := d.ResolveSessionLedgerDir(dir, toolDir)
	k := ledgerDir
	if root, _, ok := guard.FindGitRootFast(ledgerDir); ok {
		k = readledger.CanonicalizePath("", root)
	}
	if h, ok := c.byRoot[k]; ok {
		return h.st, h.root
	}
	st, root, ownSt := resolveReusableReadLedgerStore(d, dir, home, toolDir, wdSt, wdRoot)
	c.byRoot[k] = bashLedgerHandle{st: st, root: root, ownSt: ownSt}
	return st, root
}

func (c *bashLedgerCache) close() {
	for _, h := range c.byRoot {
		if h.ownSt && h.st != nil {
			h.st.Close()
		}
	}
}

type resolvedBashRead struct {
	read     readledger.BashFileRead
	snap     readFileSnapshot
	st       *Store
	root     string
	unranged bool
}

func isUnrangedBashRead(r readledger.BashFileRead) bool {
	switch r.Tool {
	case "cat", "nl", "bat", "awk", "python", "grep", "rg":
		return r.StartLine <= 1 && r.EndLine == 0 && r.TailLines == 0
	default:
		return false
	}
}

func resolveBashReadTargets(ctx context.Context, d Deps, home, dir string, wdSt *Store, wdRoot string, in HookInput, reads []readledger.BashFileRead, cache *bashLedgerCache) []resolvedBashRead {
	items := make([]resolvedBashRead, 0, len(reads))
	for _, r := range reads {
		if ctx.Err() != nil {
			break
		}
		toolDir := ResolveToolProjectDir(dir, in, r.Path)
		if HooksDisabledForHome(home, toolDir) {
			continue
		}
		resolved := readledger.CanonicalizePath(toolDir, r.Path)
		candRoot := resolveReadGuardRoot(d, dir, toolDir, wdRoot)
		if !shouldGuardReadInRoot(d, candRoot, resolved) {
			continue
		}
		st, root := cache.resolve(d, dir, home, toolDir, wdSt, wdRoot)
		if st == nil || !shouldGuardReadInRoot(d, root, resolved) {
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
