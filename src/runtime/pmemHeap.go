package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

const (
	// The size of the global header section in persistent memory file
	pmemHeaderSize = unsafe.Sizeof(pHeader{})
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

	// The effective permission of the created persistent memory file is
	// (mode & ~umask) where umask is the system wide umask.
	_DEFAULT_FMODE = 0666
)

var (
	memTypes   = []int{isPersistent, isNotPersistent}
	pmemHeader *pHeader
)

// The structure of the persistent memory file header region
type pHeader struct {
	// A magic constant that is used to distinguish between the first time and
	// subsequent initialization of the persistent memory file.
	magic int

	// The size of the file that is currently mapped into memory. This is used
	// during reinitialization to identify if the file was externally truncated
	// and to correctly map the file into memory.
	mappedSize uintptr

	// The offset from the beginning of the file of the application root pointer.
	// An offset is stored instead of the actual root pointer because, during
	// reinitialization, the arena map address can change causing the pointer
	// value to be invalid.
	rootOffset uintptr

	// If pointers are currently being swizzled, swizzleState captures the current
	// swizzling state.
	swizzleState int
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

	// nextMapOffset stores the offset in the persistent memory file at which
	// it should be mapped into memory next.
	nextMapOffset uintptr

	// The application root pointer. Root pointer is the pointer through which
	// the application accesses all data in the persistent memory region. This
	// variable is used only during reconstruction. It ensures that there is at
	// least one variable pointing to the application root before invoking
	// garbage collection.
	root unsafe.Pointer

	// A lock to protect modifications to the root pointer
	rootLock mutex
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

	// Map the header section of the file to identify if this is a first-time
	// initialization.
	mapAddr, isPmem, err := mapFile(fname, int(pmemHeaderSize), fileCreate,
		_DEFAULT_FMODE, 0, nil)
	if err != 0 {
		return nil, error(errorString("Mapping persistent memory file failed"))
	}
	pmemHeader = (*pHeader)(mapAddr)
	pmemInfo.isPmem = isPmem

	// TODO - Set persistent memory as initialized

	return nil, error(errorString("Persistent memory initialization not fully supported"))
}

// InPmem checks whether 'addr' is an address in the persistent memory range
func InPmem(addr uintptr) bool {
	// TODO
	return false
}
