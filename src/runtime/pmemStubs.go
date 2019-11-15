// +build !linux !amd64

package runtime

func mapFile(path string, len, flags, mode int, off uintptr,
	mapAddr unsafe.Pointer) (addr unsafe.Pointer, isPmem bool, err int) {
	throw("Not implemented")
	return
}

func getFileSize(fname string) (size int) {
	throw("Not implemented")
	return
}
