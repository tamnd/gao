package may

import "syscall"

// freeBytes is the disk available to this user on the filesystem holding path.
//
// Same call and same reasoning as the other unixes, in its own file because
// OpenBSD's syscall.Statfs_t names its fields F_bavail and F_bsize where the
// rest name them Bavail and Bsize. Nothing on the fleet runs OpenBSD. The
// release builds for it, so a field name is a build failure rather than a note
// somebody makes later.
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.F_bavail * int64(st.F_bsize), nil
}
