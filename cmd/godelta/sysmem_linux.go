//go:build linux

package main

import "syscall"

// getTotalSystemMemory returns total system RAM in KB (Linux)
func getTotalSystemMemory() (uint64, error) {
	var si syscall.Sysinfo_t
	err := syscall.Sysinfo(&si)
	if err != nil {
		return 0, err
	}

	// Totalram is in bytes, Unit is a multiplier
	totalBytes := si.Totalram * uint64(si.Unit)
	return totalBytes / 1024, nil // Convert to KB
}
