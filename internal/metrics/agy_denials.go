package metrics

import (
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

var hookDenialMarkers = []string{
	"[CLAMPED]",
	"[UNCHANGED]",
	"[COORDINATOR_GUARD]",
	"[POLL_LOOP]",
	"[TOKEN_LOOP]",
	"[SUBAGENT_BUDGET]",
	"[MODEL_OVERRIDE]",
	"[AUTO_SUBAGENT_PRO]",
	"[INTERACTIVE_STDIN]",
}

// isHookDenial reports whether a tool step represents a pre-tool hook denial
// rather than an execution failure.
func isHookDenial(status int, f fields, meta, errDetails []byte) bool {
	return classifyHookDenial(status, f, meta, errDetails) != ""
}

// classifyHookDenial classifies a tool step's pre-tool hook denial into
// "clamped", "ledger", "guard", or "" when not denied.
func classifyHookDenial(status int, f fields, meta, errDetails []byte) string {
	if strings.EqualFold(strings.TrimSpace(f.text(34, 4)), "deny") {
		if k := hookDenialKind(f.text(34, 5)); k != "" {
			return k
		}
		return "guard"
	}
	if status != agyFailed {
		return ""
	}
	if k := hookDenialKind(f.text(34, 5)); k != "" {
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
		return hookDenialKind(string(blob))
	}
	best := ""
	for _, fld := range flds {
		if fld.Wire != 2 || (skipToolCall && len(fld.Path) > 0 && fld.Path[0] == 4) {
			continue
		}
		switch k := hookDenialKind(string(fld.Bytes)); k {
		case "clamped", "ledger":
			return k
		case "guard":
			best = k
		}
	}
	return best
}

func hookDenialKind(s string) string {
	if s == "" {
		return ""
	}
	if strings.Contains(s, "[CLAMPED]") {
		return "clamped"
	}
	if strings.Contains(s, "[UNCHANGED]") {
		return "ledger"
	}
	for _, m := range hookDenialMarkers[2:] {
		if strings.Contains(s, m) {
			return "guard"
		}
	}
	if strings.Contains(strings.ToLower(s), "denied by pre-tool hook") {
		return "guard"
	}
	return ""
}
