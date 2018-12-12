package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

// These constants indicate the possible swizzle state.
const (
	swizzleDone = iota
	swizzleSetup
	swizzleOngoing
)

// Constants representing possible persistent memory initialization states
const (
	initNotDone = iota // Persistent memory not initialiazed
	initOngoing        // Persistent memory initialization ongoing
	initDone           // Persistent memory initialization completed
)

const (
	isNotPersistent = 0
	isPersistent    = 1

	// maxMemTypes represents the memory types supported - persistent memory
	// and volatile memory.
	maxMemTypes = 2
)

var (
	memTypes = []int{isPersistent, isNotPersistent}
)

// The structure of the persistent memory file header region
type pHeader struct {
	magic        int
	mappedSize   int
	rootPointer  uintptr
	swizzleState int
}

// Strucutre of a persistent memory arena
type pArena struct {
	// To identify this is a go-pmem recognized arena. This can either be a
	// magic constant or something like a checksum.
	magic int

	// Size of the persistent memory arena
	size int

	// Address at which the region corresponding to this arena is mapped
	mapAddr uintptr

	// The delta value to be used for pointer swizzling
	delta int

	// The number of bytes of data in this arena that have already been swizzled
	numBytesSwizzled int

	// The following data members are for supporting a minimal per-arena undo log
	numLogEntries int         // Number of valid entries in the log section
	logs          [2]logEntry // The actual log data
}

// A volatile data-structure which stores all the necessary information about
// the persistent memory region.
var pmemInfo struct {
	// The persistent memory backing file name
	fname string

	// isPmem stores whether the backing file is on a persistent memory medium
	// and supports direct access (DAX)
	isPmem bool

	// Persistent memory initialization state
	// This is used to prevent concurrent/multiple persistent memory initialization
	initState uint32
}

// PmemInit is the persistent memory initialization function.
// It returns the application root pointer and an error value to indicate if
// initialization was successful.
// fname is the path to the file that has to be used as the persistent memory
// medium.
func PmemInit(fname string) (unsafe.Pointer, error) {
	if GOOS != "linux" || GOARCH != "amd64" {
		return nil, error(errorString("Unsupported architecture"))
	}

	// Change persistent memory initialization state from not-done to ongoing
	if !atomic.Cas(&pmemInfo.initState, initNotDone, initOngoing) {
		return nil, error(errorString(`Persistent memory is already initialized
				or initialization is ongoing`))
	}

	// Set the persistent memory file name. This will be used to map the file
	// into memory in growPmemRegion().
	pmemInfo.fname = fname

	// Set persistent memory as initialized
	atomic.Store(&pmemInfo.initState, initDone)

	return nil, nil
}

func logHeapBits(addr uintptr, startByte, endByte *byte) {
	// todo
}

func clearHeapBits(addr uintptr, size uintptr) {
	// todo
}

// Function to check that 'addr' is an address in the persistent memory range
func InPmem(addr uintptr) bool {
	// todo
	return false
}

// Function to log a span allocation.
func logSpanAlloc(s *mspan) {
	// todo
}

// Function to log that a span has been completely freed.
func logSpanFree(s *mspan) {
	// todo
}
