// +build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

func getDiskFreeSpace(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	if abs == "" {
		abs = "."
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		vol = "C:"
	}
	vol += "\\"
	kernel := syscall.MustLoadDLL("kernel32.dll")
	getDiskFreeSpaceEx := kernel.MustFindProc("GetDiskFreeSpaceExW")
	var free, total, avail uint64
	ptr, _ := syscall.UTF16PtrFromString(vol)
	r1, _, err := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&avail)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)),
	)
	if r1 == 0 {
		return 0, err
	}
	return free, nil
}
