// +build linux
// +build amd64

package runtime

import (
	"unsafe"
)

const (
	atFdCwd = -0x64 // file descriptor that points to the current working directory
)

// Check whether the path points to a device dax
func isFileDevDax(path string) bool {
	pathArray := []byte(path)
	fd := open(&pathArray[0], _O_RDONLY, 0)
	if fd < 0 {
		return false
	}

	ret := utilIsFdDevDax(fd)
	closefd(fd)
	return ret
}

func unlinkFile(path string) int32 {
	return unlinkFileAt(path, atFdCwd)
}

func unlinkFileAt(path string, fd int) int32 {
	pathArray := []byte(path)
	pathAddr := uintptr(unsafe.Pointer(&pathArray[0]))
	return unlinkat(uintptr(fd), pathAddr, 0)
}
