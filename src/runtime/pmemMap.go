// +build linux
// +build amd64

package runtime

import "unsafe"

const (
	fileCreate = (1 << 0)
)

func mapFile(path string, len, flags, mode int, off uintptr,
	mapAddr unsafe.Pointer) (addr unsafe.Pointer, isPmem bool, err int) {
	// todo
	return
}
