//go:build unix

package may

import "syscall"

// freeBytes is the disk available to this user on the filesystem holding path.
//
// Available rather than free, because the difference on a Linux filesystem is
// the root reserve, and a stage that is not running as root cannot spend it.
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
