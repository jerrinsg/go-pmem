package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

// logEntry is the structure used to store one log entry.
type logEntry struct {
	// Offset of the address to be logged from the arena map address
	off uintptr
	// The value to be logged
	val int
}

// Function to log a span allocation.
func logSpanAlloc(s *mspan) {
	if s.memtype == isNotPersistent {
		throw("Invalid span passed to logSpanAlloc")
	}

	// The address at which the span value has to be logged
	logAddr := spanLogAddr(s)

	// The value that should be logged
	logVal := spanLogValue(s)

	bitmapVal := *logAddr
	if bitmapVal != 0 {
		// The span bitmap already has an entry corresponding to this span.
		// We clear the span bitmap when a span is freed. Since the entry still
		// exists, this means that the span is getting reused. Hence, the first
		// 31 bits of the entry should match with the corresponding value to be
		// logged. The last bit need not be the same as needzero bit can change
		// as spans get reused.
		// compare the first 31 bits
		if bitmapVal>>1 != logVal>>1 {
			throw("Logged span information mismatch")
		}
		// compare the last bit
		if bitmapVal&1 == logVal&1 {
			// all bits are equal, need not store the value again
			return
		}
	}

	atomic.Store(logAddr, logVal)
	PersistRange(unsafe.Pointer(logAddr), unsafe.Sizeof(*logAddr))
}

// Function to log that a span has been completely freed. This is done by
// writing 0 to the bitmap entry corresponding to this span.
func logSpanFree(s *mspan) {
	if s.memtype == isNotPersistent {
		throw("Invalid span passed to logSpanAlloc")
	}

	logAddr := spanLogAddr(s)
	atomic.Store(logAddr, 0)
	PersistRange(unsafe.Pointer(logAddr), unsafe.Sizeof(*logAddr))
}

// A helper function to compute the value that should be logged to record the
// allocation of span s.
// For a small span, the value logged is -
// ((s.spc) << 1 | s.needzero) and for a large span the value logged is -
// ((66+s.npages-4) << 2 | s.spc << 1 | s.needzero)
func spanLogValue(s *mspan) uint32 {
	var logVal uintptr
	if s.elemsize > maxSmallSize { // large allocation
		npages := s.elemsize >> pageShift
		logVal = (66+npages-4)<<2 | uintptr(s.spanclass)<<1 | uintptr(s.needzero)
	} else {
		logVal = uintptr(s.spanclass)<<1 | uintptr(s.needzero)
	}
	return uint32(logVal)
}

// A helper function to compute the address at which the span log has to be
// written.
func spanLogAddr(s *mspan) *uint32 {
	pArena := (*pArena)(unsafe.Pointer(s.pArena))
	mdSize, allocSize := pArena.layout()
	arenaStart := pArena.mapAddr + mdSize

	offset := uintptr(0)
	if pArena.fileOffset == 0 {
		// Account the space occupied by the common persistent memory header
		// present in the first arena.
		offset = pmemHeaderSize
	}

	// Add offset, arena header, and heap typebitmap size to get the address of span bitmap
	spanBitmap := pArena.mapAddr + offset + pArenaHeaderSize + allocSize/bytesPerBitmapByte

	// Index of the first page of this span within the persistent memory arena
	pageOffset := (s.base() - arenaStart) >> pageShift

	logAddr := spanBitmap + (pageOffset * spanBytesPerPage)
	return (*uint32)(unsafe.Pointer(logAddr))
}

