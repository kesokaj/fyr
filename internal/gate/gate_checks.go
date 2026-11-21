package gate

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/redact"
)

var privateKeyHeaderRe = regexp.MustCompile(`-----BEGIN ([A-Z0-9 ]+ )?PRIVATE KEY-----`)

func runAllGateChecks(ctx context.Context, tgt gateTarget, lease []string, maxFiles, maxLines int, rep *GateReport) error {
	deleted, err := checkRawChanges(ctx, tgt, lease, rep)
	if err != nil {
		return err
	}
	if err := checkNumstat(ctx, tgt, maxFiles, maxLines, deleted, rep); err != nil {
		return err
	}
	if err := checkAddedSecrets(ctx, tgt, rep); err != nil {
		return err
	}
	return checkCommitTrailers(ctx, tgt, rep)
}

func checkRawChanges(ctx context.Context, tgt gateTarget, lease []string, rep *GateReport) (map[string]bool, error) {
	out, err := gitBytes(ctx, tgt.mainRoot, "diff", "--raw", "-z", "--no-renames", "--no-ext-diff", tgt.mergeBase, tgt.head)
	if err != nil {
		return nil, err
	}
	deleted := map[string]bool{}
	tokens := bytes.Split(out, []byte{0})
	for i := 0; i+1 < len(tokens); i += 2 {
		meta := string(tokens[i])
		rel := string(tokens[i+1])
		if meta == "" || rel == "" {
			continue
		}
		if evaluateRawEntry(meta, rel, lease, rep) {
			deleted[rel] = true
		}
	}
	return deleted, nil
}

func evaluateRawEntry(meta, rel string, lease []string, rep *GateReport) bool {
	fields := strings.Fields(meta)
	if len(fields) >= 2 {
		modeNew := fields[1]
		if modeNew == "120000" {
			rep.Findings = append(rep.Findings, GateFinding{Check: "lease", Path: rel, Detail: "symlink mode 120000 disallowed"})
		} else if modeNew == "160000" {
			rep.Findings = append(rep.Findings, GateFinding{Check: "lease", Path: rel, Detail: "gitlink mode 160000 disallowed"})
		}
	}
	if isSecretFilename(rel) {
		rep.Findings = append(rep.Findings, GateFinding{Check: "lease", Path: rel, Detail: "secret file name disallowed"})
	}
	if len(lease) > 0 && !matchesAnyLease(lease, rel) {
		rep.Findings = append(rep.Findings, GateFinding{Check: "lease", Path: rel, Detail: "file outside lease"})
	}
	return len(fields) >= 5 && fields[1] == "000000" && fields[4] == "D"
}

func isSecretFilename(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	if base == ".credentials.json" || base == "antigravity-oauth-token" {
		return true
	}
	switch base {
	case "id_rsa", "id_ed25519", "id_ecdsa", "id_dsa":
		return true
	}
	if strings.HasSuffix(base, ".tfstate") || strings.Contains(base, ".tfstate.") {
		return true
	}
	if strings.HasSuffix(base, ".tfvars") || strings.HasSuffix(base, ".tfvars.json") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

func checkNumstat(ctx context.Context, tgt gateTarget, maxFiles, maxLines int, deleted map[string]bool, rep *GateReport) error {
	out, err := gitBytes(ctx, tgt.mainRoot, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", tgt.mergeBase, tgt.head)
	if err != nil {
		return err
	}
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		parseNumstatRecord(string(rec), deleted, rep)
	}
	if rep.FilesChanged == 0 {
		rep.Findings = append(rep.Findings, GateFinding{Check: "numstat", Detail: "empty diff"})
		return nil
	}
	if rep.FilesChanged > maxFiles {
		rep.Findings = append(rep.Findings, GateFinding{
			Check:  "numstat",
			Detail: fmt.Sprintf("changed %d files exceeds limit %d", rep.FilesChanged, maxFiles),
		})
	}
	if total := rep.LinesAdded + rep.LinesDeleted; total > maxLines {
		rep.Findings = append(rep.Findings, GateFinding{
			Check:  "numstat",
			Detail: fmt.Sprintf("changed %d lines exceeds limit %d", total, maxLines),
		})
	}
	return nil
}

func parseNumstatRecord(rec string, deleted map[string]bool, rep *GateReport) {
	parts := strings.SplitN(rec, "\t", 3)
	if len(parts) != 3 {
		return
	}
	rep.FilesChanged++
	if parts[0] == "-" || parts[1] == "-" {
		if !deleted[parts[2]] {
			rep.Findings = append(rep.Findings, GateFinding{Check: "numstat", Path: parts[2], Detail: "binary diff disallowed"})
		}
		return
	}
	add, errA := strconv.Atoi(parts[0])
	del, errD := strconv.Atoi(parts[1])
	if errA != nil || errD != nil {
		rep.Findings = append(rep.Findings, GateFinding{Check: "numstat", Path: parts[2], Detail: "unparseable numstat entry"})
		return
	}
	rep.LinesAdded += add
	rep.LinesDeleted += del
}

type diffSecretCursor struct {
	curFile        string
	nextLine       int
	inHunk         bool
	sawMinusHeader bool
}

func checkAddedSecrets(ctx context.Context, tgt gateTarget, rep *GateReport) error {
	out, err := gitBytes(ctx, tgt.mainRoot, "diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", tgt.mergeBase, tgt.head)
	if err != nil {
		return err
	}
	var cur diffSecretCursor
	for _, line := range strings.Split(string(out), "\n") {
		cur.step(line, rep)
	}
	return nil
}

func (c *diffSecretCursor) step(line string, rep *GateReport) {
	if strings.HasPrefix(line, "diff --git ") {
		*c = diffSecretCursor{}
		return
	}
	if !c.inHunk {
		if strings.HasPrefix(line, "--- ") {
			c.sawMinusHeader = true
			return
		}
		if c.sawMinusHeader && strings.HasPrefix(line, "+++ ") {
			c.curFile = parsePlusFileHeader(line[4:])
			c.nextLine = 0
			c.sawMinusHeader = false
			return
		}
	}
	if strings.HasPrefix(line, "@@ ") {
		c.inHunk = true
		c.sawMinusHeader = false
		c.nextLine = parseHunkNewStart(line)
		return
	}
	if c.inHunk && strings.HasPrefix(line, "+") {
		if lineHasSecret(line[1:]) {
			rep.Findings = append(rep.Findings, GateFinding{
				Check:  "secrets",
				Path:   c.curFile,
				Line:   c.nextLine,
				Detail: "added line matched secret pattern",
			})
		}
		c.nextLine++
	}
}

func parsePlusFileHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(raw, `"`) {
		if u, err := strconv.Unquote(raw); err == nil {
			raw = u
		}
	}
	if !strings.HasPrefix(raw, "b/") {
		return ""
	}
	return strings.TrimPrefix(raw, "b/")
}

func parseHunkNewStart(header string) int {
	_, rest, ok := strings.Cut(header, "+")
	if !ok {
		return 0
	}
	numStr, _, _ := strings.Cut(rest, " ")
	numStr, _, _ = strings.Cut(numStr, ",")
	n, err := strconv.Atoi(numStr)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func lineHasSecret(s string) bool {
	if s == "" {
		return false
	}
	return redact.Redact(s) != s || privateKeyHeaderRe.MatchString(s)
}

func checkCommitTrailers(ctx context.Context, tgt gateTarget, rep *GateReport) error {
	out, err := gitBytes(ctx, tgt.mainRoot, "log", "--format=%H%x00%B%x00", tgt.mergeBase+".."+tgt.head)
	if err != nil {
		return err
	}
	parts := bytes.Split(out, []byte{0})
	for i := 0; i+1 < len(parts); i += 2 {
		sha := strings.TrimSpace(string(parts[i]))
		body := string(parts[i+1])
		if sha == "" {
			continue
		}
		if key, bad := hasForbiddenTrailer(body); bad {
			short := sha
			if len(short) > 8 {
				short = short[:8]
			}
			rep.Findings = append(rep.Findings, GateFinding{
				Check:  "trailers",
				Path:   short,
				Detail: fmt.Sprintf("commit %s has forbidden trailer %s:", short, key),
			})
		}
	}
	return nil
}
