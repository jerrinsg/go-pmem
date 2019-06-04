// +build linux
// +build amd64

package runtime

const (
	FLUSH_ALIGN = 64 // cache line size
	MS_SYNC     = 4
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

// msyncRange() flushes changes made to the in-core copy of a file that was
// mapped into memory using mmap(2) back to the filesystem.
func msyncRange(addr, len uintptr) (ret int) {
	// msync requires len to be a multiple of pagesize, so adjust addr and len
	// to represent the full 4k chunks covering the given range.

	// increase len by the amount we gain when we round addr down
	len += (addr & (physPageSize - 1))

	// round addr down to page boundary
	uptr := uintptr(int(addr) & ^(int(physPageSize) - 1))

	// msync accepts addresses aligned to page boundary, so we may sync more and
	// part of it may have been marked as undefined/inaccessible.  Msyncing such
	// memory is not a bug.

	if ret = int(msync(uptr, len, MS_SYNC)); ret < 0 {
		println("msync failed")
	}

	return ret
}
