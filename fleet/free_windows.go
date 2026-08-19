//go:build windows

package fleet

import "golang.org/x/sys/windows"

// freeBytes is the disk available to this user on the volume holding path.
//
// The first number GetDiskFreeSpaceEx returns is what this user may write,
// which is not the volume's free space when a quota is in force. That is the
// one worth having, since a quota stops a run exactly the way a full disk does.
func freeBytes(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return int64(avail), nil
}
