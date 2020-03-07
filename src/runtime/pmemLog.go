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

// logHeapBits is used to log the heap type bits set by the memory allocator
// during a persistent memory allocation request.
// 'addr' is the start address of the allocated region. The heap type bits to be
// copied from are between addresses 'startByte' and 'endByte'.
// This type bitmap will be restored during subsequent run of the program and
// will help GC identify which addresses in the reconstructed persistent memory
// region has pointers. The heap type bits logged is different for spans that
// are cached at index 0 and not used for specific type allocation, and for
// spans that are specially cached for a particular type allocation. For a
// specially cached span, logHeapBits logs the type index followed by the
// metdata from the type datastructure. This is done only for the first object.
// For other spans, the heap type bits are copied as-is for each objects.
//
// Specially cached spans:
// +------------+---------+---------+---------+-----------+
// | Type index |   KIND  |   SIZE  | PTRDATA |  GC DATA  |
// |   8 bytes  | 8 bytes | 8 bytes | 8 bytes | var-sized |
// +------------+---------+---------+---------+-----------+
//
// TODO - type metadata need not be saved for each span. It can instead be
// stored in the global header.
func logHeapBits(addr uintptr, startByte, endByte *byte, typ *_type) {
	span := spanOfUnchecked(addr)
	if span.memtype != isPersistent {
		throw("Invalid heap type bits logging request")
	}

	// If this span is used to allocate objects of a particular type then use
	// an optimized logging approach
	optLog := span.typIndex != 0
	ai := arenaIndex(addr)
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	pArena := (*pArena)(unsafe.Pointer(arena.pArena))
	numHeapBytes := uintptr(unsafe.Pointer(endByte)) - uintptr(unsafe.Pointer(startByte)) + 1

	if optLog {
		typAddr := (*int)(pmemHeapBitsAddr(span.base(), pArena))
		// Write the type index (8 bytes) at the beginning of the log followed
		// by the type metadata - kind, size ptrdata, gcdata (see _type structure
		// representation in type.go).
		if *typAddr != span.typIndex {
			*typAddr = span.typIndex
		}

		tu := uintptr(unsafe.Pointer(typAddr))
		kindAddr := (*uint8)(unsafe.Pointer(tu + intSize))
		*kindAddr = typ.kind
		sizeAddr := (*uintptr)(unsafe.Pointer(tu + 16))
		*sizeAddr = typ.size
		ptrAddr := (*uintptr)(unsafe.Pointer(tu + 24))
		*ptrAddr = typ.ptrdata

		// If typ.ptrdata is a multiple of 8, then the below step is not necessary
		numHeapTypeBits := (typ.ptrdata + 7) / 8
		numHeapTypeBytes := (numHeapTypeBits + 7) / 8
		gcDataAddr := unsafe.Pointer(tu + 32)
		memmove(gcDataAddr, unsafe.Pointer(typ.gcdata), numHeapTypeBytes)
		PersistRange(unsafe.Pointer(typAddr), numHeapTypeBytes+32)
	} else {
		logAddr := pmemHeapBitsAddr(addr, pArena)
		// From heapBitsSetType()
		// There can only be one allocation from a given span active at a time,
		// and the bitmap for a span always falls on byte boundaries,
		// so there are no write-write races for access to the heap bitmap.
		// Hence, heapBitsSetType can access the bitmap without atomics.
		memmove(logAddr, unsafe.Pointer(startByte), numHeapBytes)
		PersistRange(logAddr, numHeapBytes)
	}
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

	// TODO jerrin XXX check if any optimization possible in below code
	/*
		bitmapVal := *logAddr
		if bitmapVal != 0 {
			// The span bitmap already has an entry corresponding to this span.
			// We clear the span bitmap when a span is freed. Since the entry still
			// exists, this means that the span is getting reused. Hence, the first
			// 30 bits of the entry should match with the corresponding value to be
			// logged. The last two bits need not be the same as needzero bit or the
			// optTypeLog bit can change as spans get reused.
			// compare the first 30 bits
			if bitmapVal>>2 != logVal>>2 {
				throw("Logged span information mismatch")
			}
			// compare the last two bits
			if bitmapVal&3 == logVal&3 {
				// all bits are equal, need not store the value again
				return
			}
		}
	*/

	atomic.Store(logAddr, logVal)
	// Store fence will be called at the end of mallocgc()
	FlushRange(unsafe.Pointer(logAddr), unsafe.Sizeof(*logAddr))
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
// (s.spc << 2 | optTypeLog << 1 | s.needzero) and for a large span the value
// logged is - ((67+s.npages-4) << 3 | s.spc << 2 | optTypeLog << 1 | s.needzero).
// For a small span, optTypeLog bit indicates that the heap type bits logged for
// this span is an optimized representation - only the first object in the span
// has its type bits logged. All other objects in the span have the same type
// representation.
// optTypeLog bit is currently unused for a large span.
func spanLogValue(s *mspan) uint32 {
	logVal := uintptr(0)
	if s.elemsize > maxSmallSize { // large allocation
		npages := s.elemsize >> pageShift
		logVal = (67+npages-4)<<3 | uintptr(s.spanclass)<<2 | uintptr(s.needzero)
	} else {
		optTypeLog := bool2int(s.typIndex != 0)
		logVal = uintptr(s.spanclass)<<2 | uintptr(optTypeLog)<<1 | uintptr(s.needzero)
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

// The following functions help implement a minimal undo log in the runtime
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
