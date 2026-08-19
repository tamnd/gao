//go:build !unix && !windows

package harvest

// alive cannot be answered here, so no lock is ever broken automatically and
// the refusal says which file to remove.
func alive(int) bool { return true }
