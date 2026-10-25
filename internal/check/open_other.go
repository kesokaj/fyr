//go:build !unix

package check

// nonBlock is no flag where O_NONBLOCK does not exist; the release targets
// are unix (DEPLOY-remote § Releases).
const nonBlock = 0
