//go:build unix && !openbsd

package may

import "syscall"

// freeBytes is the disk available to this user on the filesystem holding path.
//
// Available rather than free, because the difference on a Linux filesystem is
// the root reserve, and a stage that is not running as root cannot spend it.
//
// The two conversions are needed on some of these platforms and redundant on
// others, because the block count and the block size are signed on one unix and
// unsigned on the next and thirty two bits on a third. Writing it any other way
// means one file per operating system for one multiply.
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	//nolint:unconvert // see above: Bsize is int64 on Linux and uint32 on Darwin
	return int64(st.Bavail) * int64(st.Bsize), nil
}
