package harness

// ManagedDirs are the source's top-level directories an install reads:
// checkManagedTree's boundary, and what eval's harness key compares when it
// marks a tree dirty (PLAN.md item 53). workflows/ is read as commands.
var ManagedDirs = []string{"context", "agents", "commands", "rules", "workflows", "skills"}
