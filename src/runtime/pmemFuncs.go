package runtime

type flushFunc func(addr, len uintptr)
type fenceFunc func()

var pmemFuncs struct {
	flush flushFunc
	fence fenceFunc
}

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
// address range to be flushed. 'isPmem' indicates if the address range is persistent
// memory. Accordingly, CPU flush instructions such as clflush() or the memory
// flush function msync() will be called.
func PersistRange(addr, len uintptr, isPmem bool) {
	if isPmem {
		pmemFuncs.flush(addr, len)
		pmemFuncs.fence()
	} else {
		msync(addr, len)
	}
}

// FlushRange - flush a range of persistent memory address
func FlushRange(addr, len uintptr, isPmem bool) {
	if isPmem {
		pmemFuncs.flush(addr, len)
	} else {
		msync(addr, len)
	}
}

// Fence - invoke a fence instruction
func Fence() {
	pmemFuncs.fence()
}
