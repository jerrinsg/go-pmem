// +build linux
// +build amd64

package runtime

import (
	"unsafe"
)

const (
	__MAP_SHARED         = 0x1
	_MAP_SHARED_VALIDATE = 0x03
	_MAP_SYNC            = 0x80000
	_EOPNOTSUPP          = 95
	S_IFMT               = 0xf000
	S_IFCHR              = 0x2000
	PATH_MAX             = 256
)

type timespec_t struct {
	tv_sec  int64
	tv_nsec int64
}

// definitions from syscall/ztypes_linux_amd64.go
type stat_t struct {
	dev       uint64
	ino       uint64
	nlink     uint64
	mode      uint32
	uid       uint32
	gid       uint32
	x__pad0   int32
	rdev      uint64
	size      int64
	blksize   int64
	blocks    int64
	atim      timespec_t
	mtim      timespec_t
	ctim      timespec_t
	x__unused [3]int64
}

var (
	// runtime package cannot have local variables escape to the heap. Hence
	// pathBuf is kept as a global buffer for various APIs that need a byte
	// array.
	pathBuf [PATH_MAX]byte
)

// Most functions in this file are called as a result of a persistent memory
// allocation request for the first time (through mallocgc). Therefore, those
// functions must ensure that it does not create a memory allocation side-effect
// as this will result in a malloc deadlock.

// A utility function to map a persistent memory file in the address space.
// This function first tries to map the file with MAP_SYNC flag. This succeeds
// only if the device the file is on supports direct-access (DAX). If this
// fails, then a normal mapping of the file is done.
func utilMap(mapAddr unsafe.Pointer, fd int32, len, flags int, off uintptr,
	rdonly bool) (unsafe.Pointer, bool, int) {
	protection := _PROT_READ
	if !rdonly {
		protection |= _PROT_WRITE
	}

	if mapAddr != nil {
		flags |= _MAP_FIXED
	}

	p, err := mmap(mapAddr, uintptr(len), int32(protection),
		int32(flags|_MAP_SHARED_VALIDATE|_MAP_SYNC), fd, off)
	if err == 0 {
		// Mapping with MAP_SYNC succeeded. Return the mapped address and a
		// boolean value 'true' to indicate this file is indeed on a persistent
		//  memory device.
		return p, true, err
	} else if err == _EOPNOTSUPP || err == _EINVAL {
		p, err = mmap(mapAddr, uintptr(len), int32(protection), int32(flags), fd, off)
		return p, false, err
	}
	return p, false, err
}

func getFileSize(fname string) int {
	openFlags := _O_RDONLY
	pathArray := []byte(fname)
	fd := open(&pathArray[0], int32(openFlags), 0)
	if fd < 0 {
		return -1
	}
	fsize := getFileSizeFd(fd)
	closefd(fd)
	return fsize
}

func getFileSizeFd(fd int32) int {
	devDax := utilIsFdDevDax(fd)
	if devDax {
		return utilDevDaxSize(fd)
	}
	return utilFileSize(fd)
}

// A helper function to get file size
func utilFileSize(fd int32) int {
	var st stat_t
	if ret := fstat(uintptr(fd), uintptr(unsafe.Pointer(&st))); ret < 0 {
		return int(ret)
	}
	return int(st.size)
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

func utilIsFdDevDax(fd int32) bool {
	var st stat_t
	var mj, mn [8]byte
	if fstat(uintptr(fd), uintptr(unsafe.Pointer(&st))) < 0 {
		println("utilIsFdDevDax: Error fstat of file")
		return false
	}

	if !isCharDev(st.mode) {
		// file is not a character device
		return false
	}

	// The below code block checks if fd is a device that supports direct-access
	// (DAX). It checks whether /sys/dev/char/M:N/subsystem is a symlink to
	// /sys/class/dax, where M and N are the major and minor number of the
	// character device. There is no implementation of realpath() API in golang
	// runtime. Hence, the code below uses a workaround by calling readlink()
	// to read the contents of the symlink. The content of the symlink will be
	// a relative path to /sys/class/dax. It then verifies that the last 9
	// characters equal 'class/dax'.
	// See util_fd_is_device_dax() in https://github.com/pmem/pmdk/blob/master/src/common/file.c
	mb := uintToBytes(majorNum(st.rdev), mj[:])
	m2b := uintToBytes(minorNum(st.rdev), mn[:])
	combineBytes(pathBuf[:], []byte("/sys/dev/char/"), mj[:mb], []byte(":"),
		mn[:m2b], []byte("/subsystem"))

	return isClassDax(pathBuf[:])
}

// A helper function to get the size of a device dax
func utilDevDaxSize(fd int32) int {
	var st stat_t
	var mj, mn [8]byte

	if fstat(uintptr(fd), uintptr(unsafe.Pointer(&st))) < 0 {
		println("utilDevDaxSize: Error fstat of file")
		return -1
	}

	mb := uintToBytes(majorNum(st.rdev), mj[:8])
	m2b := uintToBytes(minorNum(st.rdev), mn[:8])
	combineBytes(pathBuf[:], []byte("/sys/dev/char/"), mj[:mb], []byte(":"),
		mn[:m2b], []byte("/size"))

	sFd := open(&pathBuf[0], _O_RDONLY, 0)
	if sFd < 0 {
		println("Error opening device dax size file")
		return -1
	}

	n := read(sFd, unsafe.Pointer(&pathBuf[0]), PATH_MAX)
	sz := bytesToInt(pathBuf[:n])
	return sz
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

// readLink is a helper function to call the readlink system call. It casts the
// arguments to the required datatype and invokes the system call.
func isClassDax(path []byte) bool {
	var b [PATH_MAX]byte
	ret := readlink(uintptr(unsafe.Pointer(&path[0])),
		uintptr(unsafe.Pointer(&b[0])), PATH_MAX)
	if ret < 0 {
		return false // read link failed
	}

	// Find the length of the path to return. This is done by finding the first
	// byte that is 0 in the byte array.
	len := 0
	for len = 0; len < PATH_MAX; len++ {
		if b[len] == 0 {
			break
		}
	}
	if len < 9 {
		return false
	}

	return compareBytes(b[len-9:len], []byte("class/dax"))
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
