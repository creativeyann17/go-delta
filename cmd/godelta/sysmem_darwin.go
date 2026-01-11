//go:build darwin

package main

import (
	"syscall"
	"unsafe"
)

// getTotalSystemMemory returns total system RAM in KB (macOS)
func getTotalSystemMemory() (uint64, error) {
	mib := []int32{6, 24} // CTL_HW, HW_MEMSIZE

	var memsize uint64
	n := unsafe.Sizeof(memsize)

	_, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		2,
		uintptr(unsafe.Pointer(&memsize)),
		uintptr(unsafe.Pointer(&n)),
		0,
		0,
	)

	if errno != 0 {
		return 0, errno
	}

	return memsize / 1024, nil // Convert bytes to KB
}
