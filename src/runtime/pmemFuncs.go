// +build linux
// +build amd64

package runtime

import (
	"unsafe"
)

type flushFunc func(addr, len uintptr)
type fenceFunc func()

var pmemFuncs struct {
	flush flushFunc
	fence fenceFunc
}

const (
	// Block device compatibility mode
	// In order to let application developers use our package even in those
	// machines that do not have pmem, we are adding a block device
	// compatibility mode. This mode gives a reduced consistency guarantee
	// that the pmem application is resilient to application crashes or
	// restart, but data can get irrecoverably corrupted in case of a host
	// crash or restart. Therefore no need to msync data writes.
	// See https://vmware.github.io/persistent-memory-projects/Block-Device-Compatibility/
	blockDeviceCompatibility = true
)

// The init function runs even before the main() function of the application is run.
// This function is used to set the flush and fence functions to be used according
// to CPU capabilities.
func init() {
	// default functions
	pmemFuncs.flush = flushClflush
	// clflush does not require a fence, hence set default fence function as an
	// empty function.
	pmemFuncs.fence = fenceEmpty

	// overwrite default functions depending on CPU features
	if isCPUClfushoptPresent() {
		pmemFuncs.flush = flushClflushopt
		pmemFuncs.fence = memoryBarrier
	}

	if isCPUClwbPresent() {
		pmemFuncs.flush = flushClwb
		pmemFuncs.fence = memoryBarrier
	}
}

// Flushing and fencing APIs exported

// PersistRange - make any cached changes to a range of memory address persistent
// 'addr' is the memory address to be flushed and 'len' is the length of the memory
// address range to be flushed.
// Depening on pmemInfo.isPmem. CPU flush instructions such as clflush() or the memory
// flush function msync() will be called.
func PersistRange(addr unsafe.Pointer, len uintptr) {
	if pmemInfo.isPmem {
		pmemFuncs.flush(uintptr(addr), len)
		pmemFuncs.fence()
	} else {
		if blockDeviceCompatibility == false {
			msyncRange(uintptr(addr), len)
		}
	}
}

// FlushRange - flush a range of persistent memory address
func FlushRange(addr unsafe.Pointer, len uintptr) {
	if pmemInfo.isPmem {
		pmemFuncs.flush(uintptr(addr), len)
	} else {
		if blockDeviceCompatibility == false {
			msyncRange(uintptr(addr), len)
		}
	}
}

// Fence - invoke a fence instruction
func Fence() {
	pmemFuncs.fence()
}
