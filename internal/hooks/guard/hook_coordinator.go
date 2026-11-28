package guard

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/vet"
)

// CoordinatorInput carries workspace and prompt fields needed by coordinator guards.
type CoordinatorInput struct {
	Home           string
	Dir            string
	ConversationID string
	WorkspacePaths []string
	Cwd            string
	LastUserInput  string
}

// IsRegularFileFast reports whether path exists as a non-symlink regular file.
func IsRegularFileFast(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

// IsCoordinatorOnlyMode reports whether repo-local or global coordinator-only marker files are present.
func IsCoordinatorOnlyMode(home, root string) bool {
	if root != "" && trail.CheckScratchNoSymlinks(root) == nil {
		if IsRegularFileFast(filepath.Join(root, ".agents", "scratch", "coordinator-only")) {
			return true
		}
	}
	if h := strings.TrimSpace(home); h != "" {
		stateDir := filepath.Join(h, harness.StateDir)
		if fi, err := os.Lstat(stateDir); err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir() {
			if IsRegularFileFast(filepath.Join(stateDir, "coordinator-only")) {
				return true
			}
		}
	}
	return false
}

// ResolveExplicitHookDirs returns the resolved git root and first explicit workspace directory.
func ResolveExplicitHookDirs(in CoordinatorInput) (string, string) {
	firstDir := ""
	cands := append(append([]string{}, in.WorkspacePaths...), in.Cwd, in.Dir)
	for _, raw := range cands {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if firstDir == "" {
			firstDir = p
		}
		if root, _, ok := FindGitRootFast(p); ok {
			return root, root
		}
	}
	return "", firstDir
}

// ParseWorkerFilesHeader extracts leased file paths from a worker brief's Files: header.
func ParseWorkerFilesHeader(prompt string) ([]string, bool) {
	var files []string
	seen := map[string]bool{}
	hasHeader, inBullets := false, false
	for _, raw := range strings.Split(strings.ReplaceAll(prompt, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(strings.ToLower(line), "files:") {
			hasHeader = true
			rest := strings.TrimSpace(line[len("files:"):])
			inBullets = rest == ""
			appendWorkerFileTokens(rest, seen, &files)
			continue
		}
		inBullets = stepWorkerFilesBullet(line, inBullets, seen, &files)
	}
	return files, hasHeader
}

func appendWorkerFileTokens(rest string, seen map[string]bool, files *[]string) {
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		addWorkerFileToken(part, seen, files)
	}
}

func stepWorkerFilesBullet(line string, inBullets bool, seen map[string]bool, files *[]string) bool {
	if inBullets && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")) {
		if fields := strings.Fields(line[2:]); len(fields) > 0 {
			addWorkerFileToken(fields[0], seen, files)
		}
		return true
	}
	if line != "" {
		return false
	}
	return inBullets
}

func addWorkerFileToken(tok string, seen map[string]bool, files *[]string) {
	clean := vet.NormalizeSlashPath(strings.Trim(tok, "`'\",;()[]"))
	low := strings.ToLower(clean)
	if clean == "" || low == "none" || low == "<none>" || low == "(none)" || seen[clean] {
		return
	}
	seen[clean] = true
	*files = append(*files, clean)
}

// CheckCoordinatorWriteGuard returns a denial reason if direct edits are blocked for the Coordinator.
func CheckCoordinatorWriteGuard(in CoordinatorInput, dir, toolDir, target string, openLedgerDB func(root string) (*sql.DB, func(), error)) string {
	root, commonDir, isGit := resolveWriteGuardGitRoot(toolDir, dir, target)
	if isCoordinatorWriteExemptPath(root, toolDir, target) {
		return ""
	}
	if isGit && filepath.Clean(commonDir) != filepath.Clean(filepath.Join(root, ".git")) {
		return ""
	}
	if hasBangOrWorkerSignal(in.LastUserInput) {
		return ""
	}
	lastVet := readLatestVetHistoryFast(root, in.ConversationID, openLedgerDB)
	if lastVet != nil && hasBangOrWorkerSignal(lastVet.RawPrompt) {
		return ""
	}
	if lastVet != nil && (lastVet.Directive == "COORDINATE_SUBTASKS" || lastVet.SubtaskCount >= 2) && !isSinglePackageNonParallelVet(lastVet) {
		label := fmt.Sprintf("multi-subtask turn (%d subtasks)", max(2, lastVet.SubtaskCount))
		if lastVet.Directive == "COORDINATE_SUBTASKS" {
			label = fmt.Sprintf("active COORDINATE_SUBTASKS turn (%d subtasks)", max(2, lastVet.SubtaskCount))
		}
		return fmt.Sprintf("[COORDINATOR_GUARD] %s blocks direct Coordinator source edits; dispatch an isolated worker (invoke_subagent / swarm with Files: and workspace=\"share\") or prefix prompt with `!` to bypass.", label)
	}
	if IsCoordinatorOnlyMode(in.Home, root) {
		return "[COORDINATOR_GUARD] coordinator-only mode blocks direct Coordinator source edits; dispatch an isolated worker (invoke_subagent / swarm with Files: and workspace=\"share\") or prefix prompt with `!` to bypass."
	}
	return ""
}

func resolveWriteGuardGitRoot(toolDir, dir, target string) (string, string, bool) {
	var cands []string
	if strings.TrimSpace(target) != "" {
		if resolved := ResolveTargetWithAncestors(toolDir, target); resolved != "" {
			if anc := ExistingAncestorDir(filepath.FromSlash(resolved)); anc != "" {
				cands = append(cands, anc)
			}
		}
	}
	cands = append(cands, toolDir, dir)
	for _, d := range cands {
		if strings.TrimSpace(d) == "" {
			continue
		}
		if root, commonDir, ok := FindGitRootFast(d); ok {
			return root, commonDir, true
		}
	}
	return "", "", false
}

// ExistingAncestorDir walks upward from path until an existing directory is found.
func ExistingAncestorDir(path string) string {
	for cur := filepath.Clean(path); ; {
		if fi, err := os.Stat(cur); err == nil && fi.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func isCoordinatorWriteExemptPath(root, toolDir, target string) bool {
	base := root
	if base == "" {
		base = toolDir
	}
	if base == "" {
		return false
	}
	relBase := toolDir
	if relBase == "" {
		relBase = base
	}
	resolved := ResolveTargetWithAncestors(relBase, target)
	canonBase := readledger.CanonicalizePath("", base)
	r, err := filepath.Rel(filepath.FromSlash(canonBase), filepath.FromSlash(resolved))
	if err != nil {
		return false
	}
	rel := filepath.ToSlash(filepath.Clean(r))
	if rel != "TODO.md" && rel != ".agents/scratch" && !strings.HasPrefix(rel, ".agents/scratch/") {
		return false
	}
	if HasSymlinkSegmentUnder(base, relBase, target) || HasSymlinkSegmentUnder(canonBase, canonBase, filepath.FromSlash(resolved)) {
		return false
	}
	if trail.CheckScratchNoSymlinks(base) != nil {
		return rel == "TODO.md"
	}
	return true
}

// HasSymlinkSegmentUnder reports whether any path component of target under root is a symlink.
func HasSymlinkSegmentUnder(root, relBase, target string) bool {
	p := strings.TrimSpace(target)
	if !filepath.IsAbs(p) && relBase != "" {
		p = filepath.Join(relBase, p)
	}
	p = filepath.Clean(p)
	rel, err := filepath.Rel(filepath.Clean(root), p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	cur := filepath.Clean(root)
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// ResolveTargetWithAncestors resolves target relative to toolDir, evaluating existing symlink ancestors.
func ResolveTargetWithAncestors(toolDir, target string) string {
	p := strings.TrimSpace(target)
	if !filepath.IsAbs(p) && toolDir != "" {
		p = filepath.Join(toolDir, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	for cur, tail := p, ""; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.ToSlash(filepath.Join(real, tail))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.ToSlash(p)
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

func hasBangOrWorkerSignal(raw string) bool {
	trimmed := vet.StripAnySandboxPreamble(raw)
	return trimmed != "" && (strings.HasPrefix(trimmed, "!") || vet.IsWorkerBriefPrompt(trimmed))
}

func isSinglePackageNonParallelVet(row *vet.HistoryRow) bool {
	if row == nil || vet.HasExplicitMultiPhaseOrParallelCues(strings.ToLower(row.RawPrompt)) {
		return false
	}
	files := vet.ExtractPromptAndSliceFiles(row.RawPrompt, nil)
	if len(files) == 0 {
		files = row.PivotFiles
	}
	if len(files) == 0 {
		return false
	}
	dirs := map[string]bool{}
	for _, f := range files {
		if n := vet.NormalizeSlashPath(f); n != "" {
			dirs[vet.PackageDirKey(n)] = true
		}
	}
	return len(dirs) == 1
}

func readLatestVetHistoryFast(root, convID string, openLedgerDB func(root string) (*sql.DB, func(), error)) *vet.HistoryRow {
	if root == "" || openLedgerDB == nil || trail.CheckScratchNoSymlinks(root) != nil || !IsRegularFileFast(ProjectDB(root)) {
		return nil
	}
	db, cleanup, err := openLedgerDB(root)
	if err != nil || db == nil {
		return nil
	}
	if cleanup != nil {
		defer cleanup()
	}
	row, err := vet.LastHistoryCtx(context.Background(), db, convID, 0)
	if err != nil {
		return nil
	}
	return row
}
