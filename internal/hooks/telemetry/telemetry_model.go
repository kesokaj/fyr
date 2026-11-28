package telemetry

import (
	"encoding/json"
	"github.com/kesokaj/fyr/internal/vet"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

type CatalogModelEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Effort      string `json:"effort"`
}

type ModelsCatalogFile struct {
	UpdatedAt string                      `json:"updated_at"`
	Entries   []CatalogModelEntry         `json:"entries"`
	Picks     map[string]ModelRoutingPick `json:"picks"`
}

type ResolvedModelInfo struct {
	ID       string
	Display  string
	Thinking string
}

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

// ResolveModelDetails translates a raw Antigravity model ID or display name
// into its exact canonical agy model ID, unmodified agy display name (when known),
// and UPPER_SNAKE_CASE thinking level.
func ResolveModelDetails(home, rawModel string) (modelID, modelDisplay, thinking string) {
	trimmed := CleanRawModelID(rawModel)
	if trimmed == "" {
		return "", "", ""
	}
	cat, ok := LoadModelsCatalog(home)
	return ResolveModelDetailsWithCatalog(cat, ok, trimmed)
}

func ResolveModelDetailsWithCatalog(cat ModelsCatalogFile, hasCat bool, rawModel string) (string, string, string) {
	trimmed := CleanRawModelID(rawModel)
	if trimmed == "" {
		return "", "", ""
	}
	if hasCat {
		if id, disp, think, ok := matchCatalogModelEntry(cat.Entries, trimmed); ok {
			if strings.EqualFold(disp, id) {
				disp = ""
			}
			return id, disp, think
		}
	}
	return trimmed, "", inferModelThinking(trimmed)
}

func CleanRawModelID(raw string) string {
	trimmed := strings.TrimSpace(Printable(raw))
	if len(trimmed) >= 7 && strings.EqualFold(trimmed[:7], "models/") {
		trimmed = strings.TrimSpace(trimmed[7:])
	}
	if trimmed == "" || strings.EqualFold(trimmed, "auto") {
		return ""
	}
	return trimmed
}

func LoadModelsCatalog(home string) (ModelsCatalogFile, bool) {
	if strings.TrimSpace(home) == "" {
		return ModelsCatalogFile{}, false
	}
	p := filepath.Join(home, harness.StateDir, "models.json")
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 4<<20 {
		return ModelsCatalogFile{}, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ModelsCatalogFile{}, false
	}
	var cat ModelsCatalogFile
	if json.Unmarshal(data, &cat) != nil {
		return ModelsCatalogFile{}, false
	}
	return cat, true
}

func matchCatalogModelEntry(entries []CatalogModelEntry, rawModel string) (string, string, string, bool) {
	for _, e := range entries {
		cleanID := CleanRawModelID(e.ID)
		if !strings.EqualFold(cleanID, rawModel) && !strings.EqualFold(e.ID, rawModel) &&
			(e.DisplayName == "" || !strings.EqualFold(e.DisplayName, rawModel)) {
			continue
		}
		if cleanID == "" {
			cleanID = rawModel
		}
		disp := vet.CollapseOneLine(Printable(e.DisplayName), 120)
		think := NormalizeThinking(e.Effort, cleanID)
		return cleanID, disp, think, true
	}
	return "", "", "", false
}

func NormalizeThinking(effort, id string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high", "xhigh", "max", "tiered", "fast":
		return strings.ToUpper(strings.TrimSpace(effort))
	case "minimal":
		return "LOW"
	case "orchestrator":
		return "DYNAMIC"
	}
	return inferModelThinking(id)
}

func inferModelThinking(raw string) string {
	low := strings.ToLower(CleanRawModelID(raw))
	if low == "" {
		return ""
	}
	if low == "gemini-flash-worker-agy" {
		return "FAST"
	}
	if strings.Contains(low, "orchestrator") {
		return "DYNAMIC"
	}
	for _, item := range modelSuffixThinkingMap {
		if strings.HasSuffix(low, item.suffix) && len(low) > len(item.suffix) {
			return item.thinking
		}
		paren := " (" + strings.TrimPrefix(item.suffix, "-") + ")"
		if strings.HasSuffix(low, paren) && len(low) > len(paren) {
			return item.thinking
		}
	}
	return ""
}
