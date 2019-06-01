// +build linux
// +build amd64

package runtime

// Check whether the path points to a device dax
func isFileDevDax(path string) bool {
	// todo
	return false
}
func unlinkFile(path string) int32 {
	// todo
	return -1
}

func ftruncate(fd, len uintptr) int32 {
	// todo
	return -1
}

func fstat(fd, stat uintptr) int32 {
	// todo
	return -1
}

func readlink(path, buf, len uintptr) int32 {
	// todo
	return -1
}
