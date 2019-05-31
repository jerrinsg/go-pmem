// +build !linux !amd64

package runtime

import "unsafe"

const (
	fileCreate = 0
)

func mapFile(path string, len, flags, mode int, off uintptr,
	mapAddr unsafe.Pointer) (addr unsafe.Pointer, isPmem bool, err int) {
	throw("Not implemented")
	return
}
