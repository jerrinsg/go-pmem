package runtime

import (
	"unsafe"
)

const (
	fileCreate        = (1 << 0)
	fileExcl          = (1 << 1)
	fileAllFlags      = fileCreate | fileExcl
	fileDaxValidFlags = fileCreate

	_O_RDRW = 0x0002 // open for reading and writing
	_O_EXCL = 0x0800 // exclusive mode - error if file already exists
)

// PmapFile creates or opens the file passed as argument and maps it to memory.
// It returns the address  at which the file was mapped, a boolean value to
// indicate if the path is on a persistent memory device, and an error value.
// 'path' points to the file to be mapped, 'len' is the file length to be mapped
// in memory, 'flags' and 'mode' are the values to be passed to the file open
// system call. Supported flags are: fileCreate and fileExcl
// 'mapAddr' is the address at which the caller wants to map the file. It can be
// set as nil if the caller has no preference on the mapping address.
func PmapFile(path string, len, flags, mode int, mapAddr unsafe.Pointer) (addr unsafe.Pointer, isPmem bool, err int) {
	openFlags := _O_RDRW
	delFileOnErr := false
	err = _EINVAL

	if flags & ^fileAllFlags != 0 {
		println("Invalid flags specified")
		return
	}

	devDax := isFileDevDax(path)
	if devDax {
		if flags & ^fileDaxValidFlags != 0 {
			println("Flag unsupported for Device DAX")
			return
		}
		// ignore all of the flags for devdax
		flags = 0
	}

	if flags&fileCreate != 0 {
		if len < 0 {
			println("Invalid file length")
			return
		}
		openFlags |= _O_CREAT
	}

	if flags&fileExcl != 0 {
		openFlags |= _O_EXCL
	}

	if (len != 0) && (flags&fileCreate == 0) {
		println("Non-zero 'len' not allowed without fileCreate flag")
		return
	}

	if (len == 0) && (flags&fileCreate != 0) {
		println("Zero 'len' not allowed with fileCreate flag")
		return
	}

	pathArray := []byte(path)
	fd := open(&pathArray[0], int32(openFlags), int32(mode))
	if fd < 0 {
		println("File open failed")
		return
	}

	if devDax {
		actualLen := utilGetFileSize(int(fd))
		if actualLen < 0 {
			println("Unable to read Device DAX size")
			return
		}
		if len != 0 && len != actualLen {
			println("Device DAX length must be either 0 or the exact size of the device")
			return
		}
	}

	if (flags&fileCreate != 0) && (flags&fileExcl != 0) {
		delFileOnErr = true
	}

	addr, isPmem, err = mapFd(fd, flags, len, mapAddr)
	if err != 0 && delFileOnErr {
		unlinkFile(path)
	}
	closefd(fd)
	return
}

func mapFd(fd int32, flags, len int, mapAddr unsafe.Pointer) (addr unsafe.Pointer, isPmem bool, err int) {
	if flags&fileCreate != 0 {
		// set the length of the file to 'len'
		// extend or truncate existing file
		if err = int(ftruncate(uintptr(fd), uintptr(len))); err != 0 {
			println("mapFd: ftruncate() failed")
			return
		}
		if err = int(fallocate(uintptr(fd), 0, 0, uintptr(len))); err != 0 {
			println("mapFd: fallocate() failed")
			return
		}
	} else {
		actualLen := utilGetFileSize(int(fd))
		if actualLen < 0 {
			println("mapFd: Get file size failed")
			err = actualLen
			return
		}
		len = actualLen
	}

	return utilMap(mapAddr, fd, len, __MAP_SHARED, false)
}
