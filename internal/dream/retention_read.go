package dream

import (
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

// ProjectWatermark returns the stored dream watermark for root from
// dream-state.json without creating or modifying the state file. Corrupt state
// fails closed with an error.
func ProjectWatermark(home, root string) (time.Time, error) {
	st, err := readState(home)
	if err != nil {
		return time.Time{}, err
	}
	key := search.GlobalWorkspace
	if root != search.GlobalWorkspace {
		key = filepath.Clean(root)
	}
	return st.Projects[key], nil
}
