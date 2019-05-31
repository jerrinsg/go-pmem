package runtime

import "unsafe"

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
	memTypes = []int{isPersistent, isNotPersistent}
)

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
