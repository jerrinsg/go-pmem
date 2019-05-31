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
