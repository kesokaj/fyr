package ingest

import (
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

var hookLedgerMarkers = []string{
	"[UNCHANGED]",
	"[LEDGER]",
	"[MODIFIED]",
}

var hookGuardMarkers = []string{
	"[COORDINATOR_GUARD]",
	"[POLL_LOOP]",
	"[TOKEN_LOOP]",
	"[ERROR_LOOP]",
	"[SUBAGENT_BUDGET]",
	"[MODEL_OVERRIDE]",
	"[AUTO_SUBAGENT_PRO]",
	"[INTERACTIVE_STDIN]",
	"[VERIFY_STAMP]",
	"[ASK_GUARD]",
	"[KICKOFF_GUARD]",
}

// ClassifyHookDenial classifies a tool step's pre-tool hook denial into
// "clamped", "ledger", "guard", or "" when not denied.
func ClassifyHookDenial(status int, f Fields, meta, errDetails []byte) string {
	if strings.EqualFold(strings.TrimSpace(f.Text(34, 4)), "deny") {
		if k := HookDenialKind(f.Text(34, 5)); k != "" {
			return k
		}
		return "guard"
	}
	if status != AgyFailed {
		return ""
	}
	if k := HookDenialKind(f.Text(34, 5)); k != "" {
		return k
	}
	if k := protoHookDenialKind(errDetails, false); k != "" {
		return k
	}
	return protoHookDenialKind(meta, true)
}

func protoHookDenialKind(blob []byte, skipToolCall bool) string {
	if len(blob) == 0 {
		return ""
	}
	flds := search.ProtoFields(blob)
	if len(flds) == 0 {
		return HookDenialKind(string(blob))
	}
	best := ""
	for _, fld := range flds {
		if fld.Wire != 2 || (skipToolCall && len(fld.Path) > 0 && fld.Path[0] == 4) {
			continue
		}
		switch k := HookDenialKind(string(fld.Bytes)); k {
		case "clamped", "ledger":
			return k
		case "guard":
			best = k
		}
	}
	return best
}

// HookDenialKind classifies a pre-tool hook denial message.
func HookDenialKind(s string) string {
	if s == "" {
		return ""
	}
	if strings.Contains(s, "[CLAMPED]") {
		return "clamped"
	}
	for _, m := range hookLedgerMarkers {
		if strings.Contains(s, m) {
			return "ledger"
		}
	}
	for _, m := range hookGuardMarkers {
		if strings.Contains(s, m) {
			return "guard"
		}
	}
	low := strings.ToLower(s)
	if strings.Contains(low, "denied by pre-tool hook") ||
		strings.Contains(low, "pre-tool hook denied") ||
		strings.Contains(low, "blocked by pre-tool hook") {
		return "guard"
	}
	return ""
}
