// +build linux
// +build amd64

package runtime

import (
	"unsafe"
)

const (
	__MAP_SHARED = 0x1
	S_IFMT               = 0xf000
	S_IFCHR              = 0x2000
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

func isCharDev(mode uint32) bool {
	return mode&S_IFMT == S_IFCHR
}

func majorNum(rdev uint64) uint {
	return uint(rdev / 256)
}

func minorNum(rdev uint64) uint {
	return uint(rdev % 256)
}

// combineBytes appends all the contents in args array into the 'result' buffer,
// and returns the number of bytes copied.
func combineBytes(result []byte, args ...[]byte) int {
	memclrNoHeapPointers(unsafe.Pointer(&result[0]), uintptr(len(result)))
	ind := 0
	for i := range args {
		copy(result[ind:], args[i])
		ind += len(args[i])
	}
	return ind
}

// A utility function that compares two byte arrays and checks if their contents
// are the same.
func compareBytes(b1 []byte, b2 []byte) bool {
	if len(b1) != len(b2) {
		return false
	}
	for i := range b1 {
		if b1[i] != b2[i] {
			return false
		}
	}
	return true
}

// A utility function to convert an unsigned integer number to a byte array.
// The result is written to array 'b' and the function returns the number of
// bytes written.
func uintToBytes(num uint, b []byte) int {
	var ind int
	for num > 0 {
		r := num % 10
		b[ind] = byte('0' + r)
		ind++
		num /= 10
	}
	// The bytes stored in the array is in the inverted order. Reverse the array
	// to get the correct byte stream.
	for i, j := 0, ind-1; i < ind/2; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}

	return ind
}

// A helper function to convert a byte array to an int. This is a simple
// implementation and supports only positive decimal values
func bytesToInt(s []byte) int {
	var n uint64
	for _, b := range s {
		// skip unrecognized characters
		if 48 <= b && b <= 57 {
			v := b - 48
			n *= uint64(10)
			n += uint64(v)
		}
	}
	return int(n)
}
