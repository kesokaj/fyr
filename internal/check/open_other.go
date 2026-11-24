//go:build !unix

package check

// nonBlock and noFollow are zero where O_NONBLOCK/O_NOFOLLOW do not exist;
// the release targets are unix (DEPLOY-remote § Releases).
const (
	nonBlock = 0
	noFollow = 0
)
