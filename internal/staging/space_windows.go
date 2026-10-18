package staging

import (
	"golang.org/x/sys/windows"
)

func Free(path string) (uint64, error) {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	err = windows.GetDiskFreeSpaceEx(value, &free, nil, nil)
	return free, err
}
