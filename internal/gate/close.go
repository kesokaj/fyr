package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// CloseOptions configures deterministic handoff recording after verification.
type CloseOptions struct {
	Dir        string
	Home       string
	ID         string
	Title      string
	Body       string
	Provenance string
	Now        func() time.Time
}

// CloseReport summarizes a verified Close handoff record creation.
type CloseReport struct {
	OK         bool   `json:"ok"`
	ID         string `json:"id"`
	Title      string `json:"title"`
	TreeSHA256 string `json:"treeSha256"`
	UpdatedAt  string `json:"updatedAt"`
	StampPath  string `json:"stampPath"`
}

// Close validates a fresh matching verify.stamp, embeds a Verification Digest
// section into the handoff body, and writes one handoff record without
// modifying or deleting any existing records.
func Close(ctx context.Context, opts CloseOptions) (CloseReport, error) {
	if strings.TrimSpace(opts.Title) == "" {
		return CloseReport{}, refusef("missing handoff title")
	}
	if strings.TrimSpace(opts.Body) == "" {
		return CloseReport{}, refusef("missing handoff body")
	}
	top, st, err := loadFreshStamp(ctx, opts.Dir)
	if err != nil {
		return CloseReport{}, err
	}
	if err := validateCloseKickoff(ctx, top, opts); err != nil {
		return CloseReport{}, err
	}
	body := appendVerificationDigest(opts.Body, st)
	id, err := brain.Add(
		brain.Options{Home: opts.Home, Dir: top, Now: opts.Now},
		brain.AddInput{
			Kind:       "handoff",
			ID:         opts.ID,
			Title:      opts.Title,
			Provenance: opts.Provenance,
			Body:       strings.NewReader(body),
		},
	)
	if err != nil {
		return CloseReport{}, err
	}
	return CloseReport{
		OK:         true,
		ID:         id,
		Title:      opts.Title,
		TreeSHA256: st.TreeSHA256,
		UpdatedAt:  st.UpdatedAt,
		StampPath:  filepath.Join(top, filepath.FromSlash(StampRelPath)),
	}, nil
}

func loadFreshStamp(ctx context.Context, dir string) (string, Stamp, error) {
	if dir == "" {
		dir = "."
	}
	_, top, err := trail.Roots(dir)
	if err != nil {
		return "", Stamp{}, refusef("resolve checkout root: %v", err)
	}
	st, err := ReadStamp(top)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", Stamp{}, refusef("missing %s; run `fyr verify` first", StampRelPath)
		}
		return "", Stamp{}, err
	}
	cur, err := search.ComputeGoTreeState(top)
	if err != nil {
		return "", Stamp{}, err
	}
	if !st.OK || st.TreeSHA256 == "" || st.TreeSHA256 != cur.TreeSHA256 {
		return "", Stamp{}, refusef("stale %s; re-run `fyr verify` before closing", StampRelPath)
	}
	if head, err := gitLine(ctx, top, "rev-parse", "HEAD"); err == nil && st.Head == "" {
		st.Head = head
	}
	return top, st, nil
}

func appendVerificationDigest(body string, st Stamp) string {
	trimmed := strings.TrimRight(body, "\n")
	var b strings.Builder
	b.WriteString(trimmed)
	b.WriteString("\n\n## Verification Digest\n\n")
	fmt.Fprintf(&b, "- stamp: `%s`\n", StampRelPath)
	fmt.Fprintf(&b, "- treeSha256: `%s`\n", st.TreeSHA256)
	fmt.Fprintf(&b, "- updatedAt: `%s`\n", st.UpdatedAt)
	if st.Branch != "" {
		fmt.Fprintf(&b, "- branch: `%s`\n", st.Branch)
	}
	if st.Head != "" {
		fmt.Fprintf(&b, "- head: `%s`\n", st.Head)
	}
	return b.String()
}
