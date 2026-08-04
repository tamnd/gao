//go:build windows

package gat

import "os"

// alive reports whether a process on this box is still running.
//
// On Windows FindProcess opens a handle to the process, so whether it succeeds
// is the answer, where on Unix it succeeds for any number at all. A handle that
// opens for an exited process is possible and errs toward keeping the lock,
// which is the direction to err in.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
