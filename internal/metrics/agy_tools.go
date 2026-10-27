package metrics

import "regexp"

// agyTools maps Antigravity's tool names to the store's vocabulary, the
// names the summary counts by (Bash, Read, Grep; spec § Shared tool
// vocabulary), which the historical rows share. An unmapped name is kept as
// agy:<name>. read_file, write_file and edit_file are agy 1.4.7's names
// beside the older ones (BUG-184).
var agyTools = map[string]string{
	"run_command": "Bash", "view_file": "Read", "write_to_file": "Write",
	"replace_file_content": "Edit", "multi_replace_file_content": "Edit",
	"read_file": "Read", "write_file": "Write", "edit_file": "Edit",
	"grep_search": "Grep", "find_by_name": "Glob", "list_dir": "LS",
	"invoke_subagent": "Agent", "send_message": "SendMessage",
}

// skillPath is a read of an installed skill's SKILL.md; only the name is kept.
var skillPath = regexp.MustCompile(`/skills/([A-Za-z0-9_.-]+)/SKILL\.md$`)

// agyArgs is the only part of a tool call's arguments read, in memory: a
// command line (normalised, hashed) and a path (classified). D28. Each file
// tool sends its path under its own key: view_file AbsolutePath, the 1.4.7
// tools Path, write_to_file and replace_file_content TargetFile (BUG-184).
type agyArgs struct {
	CommandLine  string `json:"CommandLine"`
	AbsolutePath string `json:"AbsolutePath"`
	Path         string `json:"Path"`
	TargetFile   string `json:"TargetFile"`
}

// path is the file the call names, whichever key carried it.
func (a agyArgs) path() string {
	for _, p := range []string{a.AbsolutePath, a.Path, a.TargetFile} {
		if p != "" {
			return p
		}
	}
	return ""
}
