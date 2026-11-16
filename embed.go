// Package fyr carries the harness's own files inside the binary, so a
// release binary installs without a checkout (ADR-033).
package fyr

import "embed"

// Files holds context/, skills/, agents/, commands/, rules/, and workflows/.
//
//go:embed all:context all:skills all:agents all:commands all:rules all:workflows
var Files embed.FS
