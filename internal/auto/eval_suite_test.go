package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
)

// TestReadCheckoutSuiteTrackedFirst: the auto gate reads the suite `fyr eval`
// reads, the tracked file when the checkout has one (ADR-500), else the
// GUIDE-eval record.
func TestReadCheckoutSuiteTrackedFirst(t *testing.T) {
	home, root := t.TempDir(), idleProject(t)
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: time.Now}, brain.AddInput{
		ID: "GUIDE-eval", Kind: "guide", Status: "current", Title: "GUIDE-eval", Body: strings.NewReader(testHarnessSuite),
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := readCheckoutSuite(home, root)
	if err != nil || len(rows) != 4 {
		t.Fatalf("the record: %d rows, %v", len(rows), err)
	}
	path := filepath.Join(root, filepath.FromSlash(eval.SuitePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := "| id | setup | prompt | prompt2 | check | agent | grants |\n|---|---|---|---|---|---|---|\n| `tracked` |  | p |  | `true` |  | named |\n"
	if err := os.WriteFile(path, []byte(tracked), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err = readCheckoutSuite(home, root)
	if err != nil || len(rows) != 1 || rows[0].ID != "tracked" {
		t.Errorf("the tracked file: %+v, %v", rows, err)
	}
	if err := os.WriteFile(path, []byte("no table\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCheckoutSuite(home, root); err == nil {
		t.Error("a tracked file that does not parse fell back to the record")
	}
}
