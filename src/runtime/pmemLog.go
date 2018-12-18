package runtime

import "unsafe"

// logEntry is the structure used to store one log entry.
type logEntry struct {
	ptr uintptr
	val uintptr
}

// logHeapBits is used to log the heap type bits set by the memory allocator during
// a persistent memory allocation request.
// 'addr' is the start address of the allocated region.
// The heap type bits to be copied from are between addresses 'startByte' and 'endByte'.
// This type bitmap will be restored during subsequent run of the program
// and will help GC identify which addresses in the reconstructed persistent memory
// region has pointers.
func logHeapBits(addr uintptr, startByte, endByte *byte) {
	span := spanOf(addr)
	if span.memtype != isPersistent {
		throw("Invalid heap type bits logging request")
	}

	pArena := (*pArena)(unsafe.Pointer(span.pArena))
	numHeapBytes := uintptr(unsafe.Pointer(endByte)) - uintptr(unsafe.Pointer(startByte)) + 1
	dstAddr := pmemHeapBitsAddr(addr, pArena)

	// From heapBitsSetType():
	// There can only be one allocation from a given span active at a time,
	// and the bitmap for a span always falls on byte boundaries,
	// so there are no write-write races for access to the heap bitmap.
	// Hence, heapBitsSetType can access the bitmap without atomics.
	memmove(dstAddr, unsafe.Pointer(startByte), numHeapBytes)
	PersistRange(dstAddr, numHeapBytes)
}

// clearHeapBits clears the logged heap type bits for the object allocated at
// address 'addr' and occupying 'size' bytes.
// The allocator tries to reuse memory regions if possible to satisfy allocation
// requests. If the reused regions do not contain pointers, then the heap type
// bits need to be cleared. This is because for swizzling pointers, the runtime
// need to be exactly sure what regions are static data and what regions contain
// pointers.
// This function expects size to be a multiple of bytesPerBitmapByte.
func clearHeapBits(addr uintptr, size uintptr) {
	span := spanOf(addr)
	if span.memtype != isPersistent {
		throw("Invalid heap type bits logging request")
	}

	pArena := (*pArena)(unsafe.Pointer(span.pArena))
	heapBitsAddr := pmemHeapBitsAddr(addr, pArena)
	numTypeBytes := size / bytesPerBitmapByte
	memclrNoHeapPointers(heapBitsAddr, numTypeBytes)
	PersistRange(heapBitsAddr, numTypeBytes)
}

// pmemHeapBitsAddr returns the address in persistent memory where heap type
// bitmap will be logged corresponding to virtual address 'x'
func pmemHeapBitsAddr(x uintptr, pa *pArena) unsafe.Pointer {
	arenaOffset := pa.offset
	typeBitsAddr := pa.mapAddr + arenaOffset + pArenaHeaderSize

	mdSize, _ := pa.pArenaLayout()
	arenaStart := pa.mapAddr + mdSize

	allocOffset := (x - arenaStart) / 32
	return unsafe.Pointer(typeBitsAddr + allocOffset)
}
