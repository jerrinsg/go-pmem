package runtime

import "runtime/internal/sys"

const (
	// Number of bytes of data described by one byte of heap type bitmap
	bytesPerBitmapByte = wordsPerBitmapByte * sys.PtrSize

	// The number of bytes needed to log a span allocation in the span bitmap.
	// To log allocation of a small span s, the value recorded is
	// ((s.spanclass) << 1 | s.needzero).
	// spanClass for a small allocation vary from 4 to 133. For a large
	// allocation that uses 'npages' pages and has spanClass 'spc', the value
	// recorded is: ((66+npages-4) << 2 | spc << 1 | s.needzero).
	// A large span uses 5 or more pages, and its spanClass is always 0 or 1.
	spanBytesPerPage = 4
)

// Computes the size of the persistent memory metadata section necessary
// for an arena of size 'size'. The metadata occupies 80 bytes to store the
// arena header and a variable number of bytes to store the heap type bitmap and
// the span bitmap. See pArena struct.
func metadataSize(size uintptr) uintptr {
	if size%pageSize != 0 {
		throw("size has to a multiple of page size")
	}
	// Size required for the heap type bitmap
	heapBitmapSize := size / bytesPerBitmapByte

	// Size required for the span bitmap
	spanBitmapSize := (size / pageSize) * spanBytesPerPage

	return uintptr(80 + heapBitmapSize + spanBitmapSize)
}

// Given a persistent memory arena of 'size' bytes, this function computes
// how the arena should be divided into two regions - the metadata region (X bytes)
// and the actual allocator usable region (Y bytes). The allocator usable region
// has to be page aligned.
// size = X + Y
// 'offset' indicates if any space in the beginning of the arena has to be reserved
// and hence unavailable to be used as metadata or data region.
// The following methodology is used for this computation.
// S' = size-offset
// The maximal possible allocator space is computed as:
// Y + metadataSize(Y) = S'
// The metadata size computed from this equation is then rounded up to a multiple
// of page size to find the required sizes.
//
// TODO: this calculations in this function needs to be further improved
func arenaLayout(size, offset uintptr) (uintptr, uintptr) {
	// Y + metadataSize(Y) = S'
	// Y + (80 + y/32 + y/2048) + y = S'
	// Y = ((2048 * S') - (80*2048)) / (64 + 1 + 2048)

	availSize := size - offset
	Y := (2048*availSize - 80*2048) / (64 + 1 + 2048)

	rem := size - Y
	remRound := round(rem+offset, pageSize)

	usable := size - remRound
	return remRound, usable
}
