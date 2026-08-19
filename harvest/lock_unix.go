//go:build unix

package harvest

import (
	"errors"
	"os"
	"syscall"
)

// alive reports whether a process on this box is still running.
//
// Signal 0 is the portable way to ask. It delivers nothing and returns the
// error the delivery would have produced. EPERM means a live process owned by
// somebody else, which on a box where gao runs both as root and as a user is
// the ordinary case, and reading it as dead would break a lock that is held.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
