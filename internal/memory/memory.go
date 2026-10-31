// Package memory resolves a project's root, which every worktree of a
// repository shares. `fyr memory` writes and reads memory records in the
// brain store (internal/brain, D14); `fyr record import` migrates the old
// store.
package memory

import (
	"errors"
	"fmt"

	"github.com/kesokaj/fyr/internal/trail"
)

// refusal is a store the command will not touch, or input it will not take;
// the caller exits 2 with its message.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return &refusal{msg: fmt.Sprintf(format, a...)} }

// IsRefusal reports whether err is a refusal.
func IsRefusal(err error) bool {
	var r *refusal
	return errors.As(err, &r)
}

// Root is the main worktree's top level for the checkout that holds dir, so
// every worktree of a repository shares one store (D26). The resolution and
// its checks are trail.Roots'; here a failure is a refusal.
func Root(dir string) (string, error) {
	main, _, err := trail.Roots(dir)
	if err != nil {
		return "", refuse("%s", err)
	}
	return main, nil
}
