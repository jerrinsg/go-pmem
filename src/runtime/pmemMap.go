// +build linux
// +build amd64

package runtime

import "unsafe"

const (
	fileCreate   = (1 << 0)
	fileExcl     = (1 << 1)
	fileAllFlags = fileCreate | fileExcl

	// The valid file open modes that can be passed to the open system call are
	// 0400, 0200, etc (see http://man7.org/linux/man-pages/man2/open.2.html).
	// setuid, setgid, and setting sticky bit has an effect only on executable
	// files and directories, and hence not supported. validFileModes constant
	// is used to verify that the file mode passed to mapFile is supported.
	validFileModes = 0777

	oRdRw = 0x0002 // open for reading and writing
	oExcl = 0x0800 // exclusive mode - error if file already exists

	// the physical page size
	sysPageSize = 4096

	// A magic constant that will be written to the first 8 bytes of the
	// persistent memory region. This constant will then help to differentiate
	// between a first run and subsequent runs.
	hdrMagic = 0x73E85840266B4B1E
)

// mapFile creates or opens the file passed as argument and maps it to memory.
// It returns the address  at which the file was mapped, a boolean value to
// indicate if the path is on a persistent memory device, and an error value.
// 'path' points to the file to be mapped, 'len' is the file length to be mapped
// in memory, 'flags' and 'mode' are the values to be passed to the file open
// system call. Supported flags are: fileCreate and fileExcl
// 'off' is the offset in the file.
// 'mapAddr' is the address at which the caller wants to map the file. It can be
// set as nil if the caller has no preference on the mapping address.
// If the file length is less than the region requested to be mapped, then the
// file will be extended to accommodate the map request.
// Some of the code layout taken from PMDK's libpmem library.
func mapFile(path string, len, flags, mode int, off uintptr,
	mapAddr unsafe.Pointer) (addr unsafe.Pointer, isPmem bool, err int) {
	openFlags := oRdRw
	delFileOnErr := false
	err = _EINVAL

	if flags & ^fileAllFlags != 0 {
		println("Invalid flags specified")
		return
	}

	if off%sysPageSize != 0 {
		println("Offset must be a multiple of page size")
		return
	}

	devDax := isFileDevDax(path)
	if devDax {
		println("Device DAX not supported")
		return
	} else {
		if flags&fileCreate != 0 {
			if len < 0 {
				println("Invalid file length")
				return
			}
			openFlags |= _O_CREAT
		}

		// Verify that the mode passed to mapFile is valid. mode is used by the
		// open system call only when O_CREAT flag is specified.
		if flags&fileCreate != 0 {
			if mode & ^validFileModes != 0 {
				println("Invalid file mode")
				return
			}
		}

		if flags&fileExcl != 0 {
			openFlags |= oExcl
		}

		if (len != 0) && (flags&fileCreate == 0) {
			println("Non-zero 'len' not allowed without fileCreate flag")
			return
		}

		if (len == 0) && (flags&fileCreate != 0) {
			println("Zero 'len' not allowed with fileCreate flag")
			return
		}

		if (flags&fileCreate != 0) && (flags&fileExcl != 0) {
			delFileOnErr = true
		}
	}

	pathArray := []byte(path)
	fd := open(&pathArray[0], int32(openFlags), int32(mode))
	if fd < 0 {
		println("File open failed")
		return
	}
	fsize := getFileSizeFd(fd)
	if fsize < 0 {
		println("Unable to read file size")
		closefd(fd)
		return
	}

	addr, isPmem, err = mapHelper(fd, flags, len, off, mapAddr, fsize)
	if err != 0 && delFileOnErr {
		unlinkFile(path)
	}

	closefd(fd)
	return
}

func mapHelper(fd int32, flags, len int, off uintptr,
	mapAddr unsafe.Pointer, fsize int) (addr unsafe.Pointer, isPmem bool, err int) {
	if fsize < (int(off) + len) {
		// Need to extend the file to map the file
		// set the length of the file to 'off+len'
		if err = int(ftruncate(uintptr(fd), uintptr(len)+off)); err != 0 {
			println("mapHelper: ftruncate() failed")
			return
		}
		if err = int(fallocate(uintptr(fd), 0, off, uintptr(len))); err != 0 {
			println("mapHelper: fallocate() failed")
			return
		}
	}

	return utilMap(mapAddr, fd, len, mapShared, off, false)
}
