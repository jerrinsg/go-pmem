package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

const (
	isNotPersistent = 0
	isPersistent    = 1

	// maxMemTypes represents the memory types supported - persistent memory
	// and volatile memory.
	maxMemTypes = 2

	// The number of bytes needed to log a span allocation in the span bitmap.
	// The layout of the value logged will be explained when span logging
	// is introduced.
	logBytesPerSpan = 4

	// A magic constant that will be written to the first 8 bytes of the
	// persistent memory region. This constant will then help to differentiate
	// between a first run and subsequent runs
	pmemHdrMagic = 0xABCDCBA

	// Persistent memory region header size in bytes. This includes
	// pmemHdrMagic (8 bytes) and another 8 bytes to record the size of the
	// persistent memory region.
	pmemHdrSize = 16

	// Golang manages its heap in arenas of 64MB. Enforce persistent memory
	// initialization size to be a multiple of 64MB
	pmemInitSize = 64 * 1024 * 1024

	// The number of bytes required to log heap type bits for one page. Golang
	// runtime uses 1 byte of heap type bitmap to record type information of
	// 32 bytes of data.
	heapBytesPerPage = pageSize / 32
)

var (
	memTypes = []int{isPersistent, isNotPersistent}
)

// Constants representing possible persistent memory initialization states
const (
	initNotDone = iota // Persistent memory not initialiazed
	initOngoing        // Persistent memory initialization ongoing
	initDone           // Persistent memory initialization completed
)

// A volatile data-structure which stores all the necessary information about
// the persistent memory region.
var pmemInfo struct {
	// The persistent memory backing file name
	fname string

	// Persistent memory initialization state
	// This is used to prevent concurrent/multiple persistent memory initialization
	initState uint32
}

// Persistent memory initialization function.
// 'fname' is the file on persistent memory device that should be used for
// persistent memory allocations. If the file does not exist on the persistent
// memory device, this implies a first-time initialization and the file is
// created on the device.
// 'size' is the size of the file to be used.
// 'offset' specifies the number of bytes in the beginning of the persistent
// memory region that should be left unmanaged by the runtime. The memory
// allocator and GC will not manage this space. This can be used by the
// application to store any application-specific data that need not be in the
// runtime-managed heap.
// This function returns the address at which the file was mapped.
// On error, a nil value is returned
func PmallocInit(fname string, size, offset int) unsafe.Pointer {
	if (size-offset) < pmemInitSize || size%pmemInitSize != 0 {
		println(`Persistent memory initialization requires a minimum of 64MB
			for initialization (size-offset) and size needs to be a
			multiple of 64MB`)
		return nil
	}

	if offset%pageSize != 0 {
		println("Persistent memory initialization requires offset to be a multiple of page size")
		return nil
	}

	// Change persistent memory initialization state from not-done to ongoing
	if !atomic.Cas(&pmemInfo.initState, initNotDone, initOngoing) {
		println("Persistent memory is already initialized or initialization is ongoing")
		return nil
	}

	// Set the persistent memory file name. This will be used to map the file
	// into memory in growPmemRegion().
	pmemInfo.fname = fname

	// Persistent memory size available to the allocator
	availSize := size - offset
	availPages := availSize >> pageShift

	// Compute the size of the header section. The header section includes the
	// span bitmap, the heap type bitmap, and 'pmemHdrSize' bytes to record the
	// magic constant and persistent memory size.
	heapTypeBitmapSize := availPages / heapBytesPerPage
	spanBitmapSize := availPages * logBytesPerSpan
	headerSize := heapTypeBitmapSize + spanBitmapSize + pmemHdrSize

	reserveSize := uintptr(offset + headerSize)
	reservePages := round(reserveSize, pageSize) >> pageShift
	totalPages := uintptr(size) >> pageShift
	pmemMappedAddr := growPmemRegion(totalPages, reservePages)
	if pmemMappedAddr == nil {
		atomic.Store(&pmemInfo.initState, initNotDone)
		return nil
	}

	// hdrAddr is the address of the header section in persistent memory
	hdrAddr := unsafe.Pointer(uintptr(pmemMappedAddr) + uintptr(offset))
	// Cast hdrAddr as a pointer to a slice to easily do pointer manipulations
	addresses := (*[3]int)(hdrAddr)
	magicAddr := &addresses[0]
	sizeAddr := &addresses[1]
	// addresses[2] will be used later

	firstTime := false
	// Read the first 8 bytes of header section to check for magic constant
	if *magicAddr == pmemHdrMagic {
		println("Not a first time initialization")

		if *sizeAddr != size {
			println("Initialization size does not match")
			// Unmap the mapped region
			sysFree(pmemMappedAddr, uintptr(size), &memstats.heap_sys)
			atomic.Store(&pmemInfo.initState, initNotDone)
			return nil
		}
	} else {
		println("First time initialization")
		firstTime = true
		// record the size of the persistent memory region
		*sizeAddr = size
		// todo persist size written to persistent memory

		// record a header magic to distinguish between first run and subsequent runs
		*magicAddr = pmemHdrMagic
		// todo persist the magic constant written to persistent memory

		// The first run of the application is distinguished from subsequent runs
		// by comparing the header magic value written. Hence if an application is
		// restarted before the header constant is written, then that run of the
		// application will be considered as a first-time initialization.
	}

	if !firstTime {
		// TODO reconstruction
	}

	// Set persistent memory as initialized
	atomic.Store(&pmemInfo.initState, initDone)

	return pmemMappedAddr
}

// growPmemRegion maps the persistent memory file into the process address space
// and returns the address at which the file was mapped.
// npages is the total number of pages to be mapped into memory
// reservePages is the number of pages in the beginning of the mapped region that
// should be left unmanaged by the runtime.
// On error, a nil value is returned.
func growPmemRegion(npages, reservePages uintptr) unsafe.Pointer {
	// code skeleton taken from grow() in mheap.go
	h := &mheap_
	ask := npages << pageShift
	lock(&h.lock)
	v, size := h.sysAlloc(ask, isPersistent)
	if v == nil {
		unlock(&h.lock)
		println("Unable to reserve persistent memory heap")
		return nil
	}
	if size != ask {
		unlock(&h.lock)
		println("Unable to reserve requested size")
		sysFree(v, size, &memstats.heap_sys)
		return nil
	}

	// The persistent memory region address from which allocator can allocate from
	spanBase := uintptr(v) + (reservePages << pageShift)

	// Create a fake span and free it, so that the right coalescing happens.
	s := (*mspan)(h.spanalloc.alloc())
	s.init(spanBase, npages-reservePages)
	s.persistent = isPersistent
	h.setSpan(s.base(), s)
	h.setSpan(s.base()+s.npages*pageSize-1, s)
	s.state = mSpanManual
	h.freeSpanLocked(s, false, true, 0)
	unlock(&h.lock)
	return v
}
