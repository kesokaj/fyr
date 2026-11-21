package search

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

type catalogModelEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Effort      string `json:"effort"`
}

type modelsCatalogFile struct {
	UpdatedAt string                      `json:"updated_at"`
	Entries   []catalogModelEntry         `json:"entries"`
	Picks     map[string]modelRoutingPick `json:"picks"`
}

const maxHookConvDBBytes = 8 << 20

var modelSuffixThinkingMap = []struct {
	suffix   string
	thinking string
}{
	{"-xhigh", "XHIGH"},
	{"-high", "HIGH"},
	{"-medium", "MEDIUM"},
	{"-low", "LOW"},
	{"-minimal", "LOW"},
	{"-max", "MAX"},
	{"-tiered", "TIERED"},
}

// ResolveModelAndThinking translates a raw Antigravity model ID or display name
// into an exact human model name and an UPPER_SNAKE_CASE thinking level using
// cached models.json metadata under home/harness.StateDir with a dynamic fallback.
func ResolveModelAndThinking(home, rawModel string) (model, thinking string) {
	trimmed := strings.TrimSpace(printable(rawModel))
	if trimmed == "" || strings.EqualFold(trimmed, "auto") {
		return "", ""
	}
	cat, ok := loadModelsCatalog(home)
	return resolveModelAndThinkingWithCatalog(cat, ok, trimmed)
}

func resolveModelAndThinkingWithCatalog(cat modelsCatalogFile, hasCat bool, rawModel string) (string, string) {
	trimmed := strings.TrimSpace(printable(rawModel))
	if trimmed == "" || strings.EqualFold(trimmed, "auto") {
		return "", ""
	}
	if hasCat {
		if name, eff, ok := matchCatalogModelEntry(cat.Entries, trimmed); ok {
			return name, eff
		}
	}
	return parseModelFallback(trimmed)
}

func loadModelsCatalog(home string) (modelsCatalogFile, bool) {
	if strings.TrimSpace(home) == "" {
		return modelsCatalogFile{}, false
	}
	p := filepath.Join(home, harness.StateDir, "models.json")
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 4<<20 {
		return modelsCatalogFile{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return modelsCatalogFile{}, false
	}
	var cat modelsCatalogFile
	if json.Unmarshal(data, &cat) != nil {
		return modelsCatalogFile{}, false
	}
	return cat, true
}

func matchCatalogModelEntry(entries []catalogModelEntry, rawModel string) (string, string, bool) {
	for _, e := range entries {
		if !strings.EqualFold(e.ID, rawModel) && !strings.EqualFold(e.DisplayName, rawModel) {
			continue
		}
		think := normalizeThinking(e.Effort, e.ID)
		name := stripEffortSuffix(e.DisplayName, e.Effort)
		if name == "" || think == "" {
			fbName, fbThink := parseModelFallback(rawModel)
			if name == "" {
				name = fbName
			}
			if think == "" {
				think = fbThink
			}
		}
		return name, think, true
	}
	return "", "", false
}

func normalizeThinking(effort, id string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high", "xhigh", "max", "tiered", "fast":
		return strings.ToUpper(strings.TrimSpace(effort))
	case "minimal":
		return "LOW"
	case "orchestrator":
		return "DYNAMIC"
	}
	_, thinking := parseModelFallback(id)
	return thinking
}

func stripEffortSuffix(displayName, effort string) string {
	name := strings.TrimSpace(printable(displayName))
	if name == "" {
		return ""
	}
	eff := strings.ToLower(strings.TrimSpace(effort))
	if eff == "" || eff == "orchestrator" {
		return name
	}
	suffix := " (" + eff + ")"
	if len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
		return strings.TrimSpace(name[:len(name)-len(suffix)])
	}
	return name
}

func parseModelFallback(rawModel string) (string, string) {
	trimmed := strings.TrimSpace(printable(rawModel))
	low := strings.ToLower(strings.TrimPrefix(trimmed, "models/"))
	if low == "" || low == "auto" {
		return "", ""
	}
	if low == "gemini-flash-worker-agy" {
		return "Gemini Flash Worker", "FAST"
	}
	if name, ok := formatOrchestratorFallback(low); ok {
		return name, "DYNAMIC"
	}
	stem, thinking := stripThinkingSuffix(strings.TrimPrefix(trimmed, "models/"))
	if strings.Contains(stem, " ") {
		return strings.TrimSpace(stem), thinking
	}
	return humanizeModelStem(stem), thinking
}

func formatOrchestratorFallback(low string) (string, bool) {
	if low == "gemini-pro-orchestrator" {
		return "Orchestrator (Carbon)", true
	}
	if variant, ok := strings.CutPrefix(low, "gemini-pro-orchestrator-"); ok && variant != "" {
		return fmt.Sprintf("Orchestrator (%s)", humanizeModelStem(variant)), true
	}
	if strings.Contains(low, "orchestrator") {
		return humanizeModelStem(low), true
	}
	return "", false
}

func stripThinkingSuffix(raw string) (string, string) {
	low := strings.ToLower(raw)
	for _, item := range modelSuffixThinkingMap {
		if strings.HasSuffix(low, item.suffix) && len(raw) > len(item.suffix) {
			return raw[:len(raw)-len(item.suffix)], item.thinking
		}
		paren := " (" + strings.TrimPrefix(item.suffix, "-") + ")"
		if strings.HasSuffix(low, paren) && len(raw) > len(paren) {
			return strings.TrimSpace(raw[:len(raw)-len(paren)]), item.thinking
		}
	}
	return raw, ""
}

func humanizeModelStem(stem string) string {
	rawToks := strings.FieldsFunc(strings.TrimSpace(stem), func(r rune) bool { return r == '-' || r == '_' })
	if len(rawToks) == 0 {
		return strings.TrimSpace(stem)
	}
	merged := mergeNumericModelTokens(rawToks)
	out := make([]string, 0, len(merged))
	for i := 0; i < len(merged); i++ {
		if i+1 < len(merged) && strings.EqualFold(merged[i], "gpt") && strings.EqualFold(merged[i+1], "oss") {
			out = append(out, "GPT-OSS")
			i++
			continue
		}
		out = append(out, formatModelWord(merged[i]))
	}
	return strings.Join(out, " ")
}

func mergeNumericModelTokens(toks []string) []string {
	var out []string
	for _, tok := range toks {
		if len(out) > 0 && isAllDigits(out[len(out)-1]) && isAllDigits(tok) {
			out[len(out)-1] = out[len(out)-1] + "." + tok
			continue
		}
		out = append(out, tok)
	}
	return out
}

func formatModelWord(tok string) string {
	low := strings.ToLower(tok)
	switch low {
	case "gpt", "oss", "ai", "llm":
		return strings.ToUpper(low)
	}
	if len(low) >= 2 && isAllDigits(low[:len(low)-1]) && strings.ContainsRune("bkm", rune(low[len(low)-1])) {
		return strings.ToUpper(low)
	}
	runes := []rune(low)
	if len(runes) > 0 && unicode.IsLetter(runes[0]) {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

func effectiveHookSessionModel(home string, in HookInput) string {
	if m := resolveHookSessionModelForHome(home, in); m != "" {
		return m
	}
	return strings.TrimSpace(in.ModelName)
}

func resolveHookSessionModel(in HookInput) string {
	return resolveHookSessionModelForHome("", in)
}

func resolveHookSessionModelForHome(home string, in HookInput) string {
	if m := strings.TrimSpace(in.ModelName); m != "" && !strings.EqualFold(m, "auto") {
		return m
	}
	dbPath, brainSubdir := resolveHookConvDBPathForHome(home, in)
	if dbPath == "" || brainSubdir == "" {
		return ""
	}
	return queryConvGenMetadataModel(dbPath, brainSubdir)
}

func resolveHookConvDBPath(in HookInput) (string, string) {
	return resolveHookConvDBPathForHome("", in)
}

func resolveHookConvDBPathForHome(home string, in HookInput) (string, string) {
	target := resolveProcessArtifactPathForHome(home, in)
	if target == "" {
		return "", ""
	}
	brainSubdir := filepath.Dir(target)
	convID := strings.TrimSpace(in.ConversationID)
	if convID == "" {
		convID = filepath.Base(brainSubdir)
	}
	if convID == "" || strings.ContainsAny(convID, `/\`) || !filepath.IsLocal(convID+".db") {
		return "", ""
	}
	appDataDir := filepath.Dir(filepath.Dir(brainSubdir))
	convDir := filepath.Join(appDataDir, "conversations")
	if st, err := os.Lstat(convDir); err != nil || st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return "", ""
	}
	dbPath := filepath.Join(convDir, convID+".db")
	if !regularBoundedSQLiteFile(dbPath, maxHookConvDBBytes) {
		return "", ""
	}
	return dbPath, brainSubdir
}

func regularBoundedSQLiteFile(dbPath string, maxBytes int64) bool {
	st, err := os.Lstat(dbPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxBytes {
		return false
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if cst, err := os.Lstat(dbPath + suffix); err == nil && (!cst.Mode().IsRegular() || cst.Size() > maxBytes) {
			return false
		}
	}
	return true
}

func queryConvGenMetadataModel(dbPath, brainSubdir string) string {
	for attempt := 0; attempt < 2; attempt++ {
		model, err := readConvGenMetadataModelOnce(dbPath, brainSubdir)
		if err == nil || !isUnreadable(err) {
			return model
		}
	}
	return ""
}

func readConvGenMetadataModelOnce(dbPath, brainSubdir string) (string, error) {
	tmp, err := os.MkdirTemp(brainSubdir, ".fyr-conv-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	cp, err := copyDB(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", roDSN(cp))
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT data FROM gen_metadata ORDER BY idx DESC LIMIT 4`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return "", err
		}
		if m := extractGenMetadataModel(blob); m != "" {
			return m, nil
		}
	}
	return "", rows.Err()
}

func extractGenMetadataModel(blob []byte) string {
	var fallback string
	for _, f := range ProtoFields(blob) {
		if f.Wire != 2 || len(f.Path) != 2 {
			continue
		}
		val := strings.TrimSpace(printable(string(f.Bytes)))
		if val == "" || strings.EqualFold(val, "auto") {
			continue
		}
		if f.Path[0] == 3 && f.Path[1] == 28 {
			return val
		}
		if f.Path[0] == 1 && f.Path[1] == 19 && fallback == "" {
			fallback = val
		}
	}
	return fallback
}
