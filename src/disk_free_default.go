// +build !linux,!darwin,!freebsd,!windows

package main

import "math"

func getDiskFreeSpace(path string) (uint64, error) {
	_ = path
	return math.MaxUint64, nil
}
