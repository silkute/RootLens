package platform

import "golang.org/x/sys/windows"

func FreeSpace(path string) (uint64, error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	var free, total, available uint64
	e = windows.GetDiskFreeSpaceEx(p, &free, &total, &available)
	return free, e
}
