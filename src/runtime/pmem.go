package runtime

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

// A volatile data-structure which stores all the necessary information about the
// persistent memory region.
// All fields are populated during initialization and is never updated during
// program run.
var pmemInfo struct {
	// The persistent memory backing file name
	fname string

	// initialized indicates that persistent memory region has been initialized
	// and persistent memory allocations are allowed.
	initialized bool
}
