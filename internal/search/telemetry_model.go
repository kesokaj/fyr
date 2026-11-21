package search

import (
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

type resolvedModelInfo struct {
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
	trimmed := cleanRawModelID(rawModel)
	if trimmed == "" {
		return "", "", ""
	}
	cat, ok := loadModelsCatalog(home)
	return resolveModelDetailsWithCatalog(cat, ok, trimmed)
}

func resolveModelDetailsWithCatalog(cat modelsCatalogFile, hasCat bool, rawModel string) (string, string, string) {
	trimmed := cleanRawModelID(rawModel)
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

func cleanRawModelID(raw string) string {
	trimmed := strings.TrimSpace(printable(raw))
	if len(trimmed) >= 7 && strings.EqualFold(trimmed[:7], "models/") {
		trimmed = strings.TrimSpace(trimmed[7:])
	}
	if trimmed == "" || strings.EqualFold(trimmed, "auto") {
		return ""
	}
	return trimmed
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

func matchCatalogModelEntry(entries []catalogModelEntry, rawModel string) (string, string, string, bool) {
	for _, e := range entries {
		cleanID := cleanRawModelID(e.ID)
		if !strings.EqualFold(cleanID, rawModel) && !strings.EqualFold(e.ID, rawModel) &&
			(e.DisplayName == "" || !strings.EqualFold(e.DisplayName, rawModel)) {
			continue
		}
		if cleanID == "" {
			cleanID = rawModel
		}
		disp := collapseOneLine(printable(e.DisplayName), 120)
		think := normalizeThinking(e.Effort, cleanID)
		return cleanID, disp, think, true
	}
	return "", "", "", false
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
	return inferModelThinking(id)
}

func inferModelThinking(raw string) string {
	low := strings.ToLower(cleanRawModelID(raw))
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
