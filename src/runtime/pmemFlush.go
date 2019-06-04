// +build linux
// +build amd64

package runtime

const (
	FLUSH_ALIGN = 64 // cache line size
)

func flushClflush(addr, len uintptr) {
	// Loop through cache-line-size (typically 64B) aligned chunks
	// covering the given range.
	for uptr := addr &^ (FLUSH_ALIGN - 1); uptr < (addr + len); uptr += FLUSH_ALIGN {
		clflush(uptr)
	}
}

func flushClflushopt(addr, len uintptr) {
	// Loop through cache-line-size (typically 64B) aligned chunks
	// covering the given range.
	for uptr := addr &^ (FLUSH_ALIGN - 1); uptr < (addr + len); uptr += FLUSH_ALIGN {
		clflushopt(uptr)
	}
}

func flushClwb(addr, len uintptr) {
	// Loop through cache-line-size (typically 64B) aligned chunks
	// covering the given range.
	for uptr := addr &^ (FLUSH_ALIGN - 1); uptr < addr+len; uptr += FLUSH_ALIGN {
		clwb(uptr)
	}
}

func fenceEmpty() {
	// nothing to do
}

func memoryBarrier() {
	sfence()
}

func sfence()
func clwb(ptr uintptr)
func clflush(ptr uintptr)
func clflushopt(ptr uintptr)

func msyncRange(addr, len uintptr) {
       // TODO
}
