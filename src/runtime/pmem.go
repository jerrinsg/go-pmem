package runtime

const (
	isNotPersistent = 0
	isPersistent    = 1
)

var (
	memTypes = []int{isPersistent, isNotPersistent}
)
