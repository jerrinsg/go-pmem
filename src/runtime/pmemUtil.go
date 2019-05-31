// +build linux
// +build amd64

package runtime

import (
	"unsafe"
)

const (
	__MAP_SHARED         = 0x1
)
// A utility function to map a persistent memory file in the address space.
// This function first tries to map the file with MAP_SYNC flag. This succeeds
// only if the device the file is on supports direct-access (DAX). If this
// fails, then a normal mapping of the file is done.
func utilMap(mapAddr unsafe.Pointer, fd int32, len, flags int, off uintptr,
	rdonly bool) (unsafe.Pointer, bool, int) {
	// todo
	return nil, false, 0
}


func getFileSizeFd(fd int32) int {
	// todo
	return 0
}
