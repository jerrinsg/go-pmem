// +build linux
// +build amd64

package runtime

import (
	"unsafe"
)

const (
	_AT_FDCWD = -0x64 // file descriptor that points to the current working directory
)

// Check whether the path points to a device dax
func isFileDevDax(path string) bool {
	// todo
	return false
}
func unlinkFile(path string) int32 {
	return unlinkFileAt(path, _AT_FDCWD)
}

func unlinkFileAt(path string, fd int) int32 {
	pathArray := []byte(path)
	pathAddr := uintptr(unsafe.Pointer(&pathArray[0]))
	return unlinkat(uintptr(fd), pathAddr, 0)
}
