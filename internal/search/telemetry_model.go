package search

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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

var exactModelFallbackMap = map[string][2]string{
	"gemini-pro-orchestrator":       {"Orchestrator (Carbon)", "DYNAMIC"},
	"gemini-pro-orchestrator-argon": {"Orchestrator (Argon)", "DYNAMIC"},
	"gemini-flash-worker-agy":       {"Gemini Flash Worker", "FAST"},
	"gemini-3.8-flash-tiered":       {"Gemini 3.8 Flash", "TIERED"},
}

var stemModelNameMap = map[string]string{
	"claude-opus-5-5":   "Claude Opus 5.5",
	"claude-sonnet-5-5": "Claude Sonnet 5.5",
	"gemini-4-carbon":   "Gemini 4 Carbon",
	"gemini-4-argon":    "Gemini 4 Argon",
	"gemini-4-hydrogen": "Gemini 4 Hydrogen",
	"gemini-3.8-flash":  "Gemini 3.8 Flash",
	"gemini-3-8-flash":  "Gemini 3.8 Flash",
	"gpt-oss-120b":      "GPT-OSS 120B",
}

var modelSuffixThinkingMap = []struct {
	suffix   string
	thinking string
}{
	{"-xhigh", "XHIGH"},
	{"-high", "HIGH"},
	{"-medium", "MEDIUM"},
	{"-low", "LOW"},
	{"-max", "MAX"},
	{"-tiered", "TIERED"},
}

// ResolveModelAndThinking translates a raw Antigravity model ID or display name
// into an exact human model name and an UPPER_SNAKE_CASE thinking level using
// cached models.json metadata under home/harness.StateDir with a pure-stdlib fallback.
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
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 1<<20 {
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
	low := strings.ToLower(trimmed)
	if pair, ok := exactModelFallbackMap[low]; ok {
		return pair[0], pair[1]
	}
	stem, thinking := trimmed, ""
	for _, item := range modelSuffixThinkingMap {
		if strings.HasSuffix(low, item.suffix) {
			thinking = item.thinking
			stem = trimmed[:len(trimmed)-len(item.suffix)]
			break
		}
	}
	if mapped, ok := stemModelNameMap[strings.ToLower(stem)]; ok {
		return mapped, thinking
	}
	return strings.TrimSpace(stem), thinking
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
	var blob []byte
	if err := db.QueryRow(`SELECT data FROM gen_metadata ORDER BY idx DESC LIMIT 1`).Scan(&blob); err != nil {
		return "", err
	}
	for _, f := range ProtoFields(blob) {
		if f.Wire == 2 && len(f.Path) == 2 && f.Path[0] == 1 && f.Path[1] == 19 {
			return strings.TrimSpace(string(f.Bytes)), nil
		}
	}
	return "", nil
}
