package runtime

import (
	"unsafe"
)

const (
	__MAP_SHARED         = 0x1
	_MAP_SHARED_VALIDATE = 0x03
	_MAP_SYNC            = 0x80000
	_EOPNOTSUPP          = 95
)

// A utility function to map a persistent memory file in the address space.
// This function first tries to map the file with MAP_SYNC flag. This succeeds
// only if the device the file is on supports direct-access (DAX). If this
// fails, then a normal mapping of the file is done.
func utilMap(mapAddr unsafe.Pointer, fd int32, len, flags int, rdonly bool) (unsafe.Pointer, bool, int) {
	protection := _PROT_READ
	if !rdonly {
		protection |= _PROT_WRITE
	}
	if mapAddr != nil {
		flags |= _MAP_FIXED
	}

	p, err := mmap(mapAddr, uintptr(len), int32(protection),
		int32(flags|_MAP_SHARED_VALIDATE|_MAP_SYNC), fd, 0)
	if err == 0 {
		// Mapping with MAP_SYNC succeeded. Return the mapped address and a boolean
		// value 'true' to indicate this file is indeed on a persistent memory device.
		return p, true, err
	} else if err == _EOPNOTSUPP || err == _EINVAL {
		p, err = mmap(mapAddr, uintptr(len), int32(protection), int32(flags), fd, 0)
		return p, false, err
	}

	return p, false, err
}
