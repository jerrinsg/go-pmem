// +build linux
// +build amd64

package runtime

func sfence()
func clwb(ptr uintptr)
func clflush(ptr uintptr)
func clflushopt(ptr uintptr)
