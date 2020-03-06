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

const (
	// The maximum number of entries that can be logged in the arena header
	maxLogEntries = 2

	logEntrySize = unsafe.Sizeof(logEntry{})
)

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

	ai := arenaIndex(addr)
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	pArena := (*pArena)(unsafe.Pointer(arena.pArena))
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

	ai := arenaIndex(addr)
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	pArena := (*pArena)(unsafe.Pointer(arena.pArena))
	heapBitsAddr := pmemHeapBitsAddr(addr, pArena)
	numTypeBytes := size / bytesPerBitmapByte
	memclrNoHeapPointers(heapBitsAddr, numTypeBytes)
	PersistRange(heapBitsAddr, numTypeBytes)
}

// pmemHeapBitsAddr returns the address in persistent memory where heap type
// bitmap will be logged corresponding to virtual address 'x'
func pmemHeapBitsAddr(x uintptr, pa *pArena) unsafe.Pointer {
	off := uintptr(0)
	if pa.fileOffset == 0 {
		// Account the space occupied by the common persistent memory header
		// present in the first arena.
		off = pmemHeaderSize
	}
	pu := uintptr(unsafe.Pointer(pa))
	mdSize, _ := pa.layout()
	arenaStart := pu - off + mdSize
	allocOffset := (x - arenaStart) / 32

	typeBitsAddr := pu + pArenaHeaderSize
	return unsafe.Pointer(typeBitsAddr + allocOffset)
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

	//bitmapVal := *logAddr
	// jerrin XXX TODO
	//if bitmapVal != 0 {
	// The span bitmap already has an entry corresponding to this span.
	// We clear the span bitmap when a span is freed. Since the entry still
	// exists, this means that the span is getting reused. Hence, the first
	// 31 bits of the entry should match with the corresponding value to be
	// logged. The last bit need not be the same as needzero bit can change
	// as spans get reused.
	// compare the first 31 bits
	//if bitmapVal>>1 != logVal>>1 {
	//throw("Logged span information mismatch")
	//}
	// compare the last bit
	//if bitmapVal&1 == logVal&1 {
	// all bits are equal, need not store the value again
	//return
	//}
	//}

	if uintptr(unsafe.Pointer(logAddr)) == 0 {
		println("span base = ", hex(s.base()))
		println("logging addr = ", unsafe.Pointer(logAddr))
		println("log value = ", logVal)
		throw("logging error")
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
// ((67+s.npages-4) << 2 | s.spc << 1 | s.needzero)
// TODO XXX jerrin should the needzero parameter be always set after reconstruction?
func spanLogValue(s *mspan) uint32 {
	var logVal uintptr
	if s.elemsize > maxSmallSize { // large allocation
		npages := s.elemsize >> pageShift
		logVal = (67+npages-4)<<2 | uintptr(s.spanclass)<<1 | uintptr(s.needzero)
	} else {
		logVal = uintptr(s.spanclass)<<1 | uintptr(s.needzero)
	}
	return uint32(logVal)
}

// A helper function to compute the address at which the span log has to be
// written.
func spanLogAddr(s *mspan) *uint32 {
	ai := arenaIndex(s.base())
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	pArena := (*pArena)(unsafe.Pointer(arena.pArena))
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

// The following functions help implement a minimal undo logging in the runtime
// using persistent memory arena header undo buffers.
// Each arena support storing two data items. Both data items are stored as a
// signed int value. The only unsigned value logged here is the arena map address
// (mapAddr). But since Go uses only 48 bits for heap address (see comment about
// heapAddrBits in malloc.go), it is safe to store and retrieve mapAddr as a
// signed value.

// Function to log a value in the arena header. Each arena supports logging up
// to 'maxLogEntries' number of entries.
func (pa *pArena) logEntry(addr unsafe.Pointer) {
	// Store the offset from the beginning of the arena instead of the
	// actual address
	off := uintptr(addr) - uintptr(unsafe.Pointer(pa))
	if off >= pa.size {
		throw("Invalid arena logging request")
	}

	ind := pa.numLogEntries
	if ind == maxLogEntries {
		throw("No more space in the arena to log values")
	}

	val := *(*int)(addr)
	pa.logs[ind].off = off
	pa.logs[ind].val = val
	PersistRange(unsafe.Pointer(&pa.logs[ind]), logEntrySize)

	pa.numLogEntries = ind + 1
	PersistRange(unsafe.Pointer(&pa.numLogEntries), intSize)
}

// Copies the logged data back back to persistent memory
func (pa *pArena) revertLog() {
	if pa.numLogEntries == 0 {
		// No log entries to revert
		return
	}

	for i := 0; i < pa.numLogEntries; i++ {
		addr := unsafe.Pointer(pa.logs[i].off + uintptr(unsafe.Pointer(pa)))
		ai := (*int)(addr)
		*ai = pa.logs[i].val
		PersistRange(addr, intSize)
	}

	pa.numLogEntries = 0
	PersistRange(unsafe.Pointer(&pa.numLogEntries), intSize)
}

// Discards all log entries without copying any data
func (pa *pArena) resetLog() {
	pa.numLogEntries = 0
	PersistRange(unsafe.Pointer(&pa.numLogEntries), intSize)
}

// Discards the log entries by setting numLogEntries as 0. It also flushes the
// persistent memory addresses into which data were written.
func (pa *pArena) commitLog() {
	for i := 0; i < pa.numLogEntries; i++ {
		addr := pa.logs[i].off + uintptr(unsafe.Pointer(pa))
		PersistRange(unsafe.Pointer(addr), intSize)
	}
	pa.numLogEntries = 0
	PersistRange(unsafe.Pointer(&pa.numLogEntries), intSize)
}

// CollectPtrs collects pointers found within the 'objPtr' object and adds them
// to the ptrArray slice.
func CollectPtrs(objPtr uintptr, objSize int, ptrArray []unsafe.Pointer) []unsafe.Pointer {
	s := spanOfUnchecked(objPtr)
	h := heapBitsForAddr(objPtr)
	if s.spanclass.noscan() {
		// This is a noscan span, so there no pointers within it
		return ptrArray
	}

	// h.morePointers() shouldn't be called on the second word of an object.
	// Hence handle the first and second word separately below.
	if h.isPointer() { // First 8 bytes
		ptrArray = append(ptrArray, unsafe.Pointer(objPtr))
	}
	if !h.morePointers() {
		return ptrArray
	}
	objSize -= 8
	if objSize == 0 {
		return ptrArray
	}
	objPtr += 8
	h = h.next()

	if h.isPointer() { // Second 8 bytes
		ptrArray = append(ptrArray, unsafe.Pointer(objPtr))
	}

	// Rest of the objects
	for {
		objSize -= 8
		if objSize == 0 {
			break
		}

		objPtr += 8
		h = h.next()
		if h.isPointer() {
			ptrArray = append(ptrArray, unsafe.Pointer(objPtr))
		}

		if !h.morePointers() {
			break
		}
	}

	return ptrArray
}
