package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

const (
	// The size of the global header section in persistent memory file
	pmemHeaderSize = unsafe.Sizeof(pHeader{})

	// The size of the per-arena metadata excluding the span and type bitmap
	pArenaHeaderSize = unsafe.Sizeof(pArena{})
)

// These constants indicate the possible swizzle state.
const (
	swizzleDone = iota
	swizzleSetup
	swizzleOngoing
)

// Constants representing possible persistent memory initialization states
const (
	initNotDone = iota // Persistent memory not initialiazed
	initOngoing        // Persistent memory initialization ongoing
	initDone           // Persistent memory initialization completed
)

const (
	isNotPersistent = 0
	isPersistent    = 1

	// maxMemTypes represents the memory types supported - persistent memory
	// and volatile memory.
	maxMemTypes = 2

	// The maximum span class of a small span
	maxSmallSpanclass = 133

	// The maximum value that will logged in span bitmap corresponding to a small span.
	// This is when the spanclass of the span is 133 and its needzero parameter
	// is 1.
	maxSmallSpanLogVal = (maxSmallSpanclass << 1) + 1
)

var (
	memTypes   = []int{isPersistent, isNotPersistent}
	pmemHeader *pHeader
)

// The structure of the persistent memory file header region
type pHeader struct {
	magic        int
	mappedSize   uintptr
	rootPointer  uintptr
	swizzleState int
}

// Strucutre of a persistent memory arena
type pArena struct {
	// To identify this is a go-pmem recognized arena. This can either be a
	// magic constant or something like a checksum.
	magic int

	// Size of the persistent memory arena
	size uintptr

	// Address at which the region corresponding to this arena is mapped
	mapAddr uintptr

	// The delta value to be used for pointer swizzling
	delta int

	// Offset indicates if any space in the beginning of the arena is reserved,
	// and cannot be used to store the metadata or used by the allocator.
	offset uintptr

	// The number of bytes of data in this arena that have already been swizzled
	numBytesSwizzled int

	// The following data members are for supporting a minimal per-arena undo log
	numLogEntries int         // Number of valid entries in the log section
	logs          [2]logEntry // The actual log data
}

// A volatile data-structure which stores all the necessary information about
// the persistent memory region.
var pmemInfo struct {
	// The persistent memory backing file name
	fname string

	// isPmem stores whether the backing file is on a persistent memory medium
	// and supports direct access (DAX)
	isPmem bool

	// Persistent memory initialization state
	// This is used to prevent concurrent/multiple persistent memory initialization
	initState uint32

	// nextMapOffset stores the offset in the persistent memory file at which
	// it should be mapped into memory next.
	nextMapOffset uintptr

	// The application root pointer. Root pointer is the pointer through which
	// the application accesses all data in the persistent memory region. This
	// variable is used only during reconstruction. It ensures that there is at
	// least one variable pointing to the application root before invoking
	// garbage collection.
	root unsafe.Pointer

	// A lock to protect modifications to the root pointer
	rootLock mutex
}

// PmemInit is the persistent memory initialization function.
// It returns the application root pointer and an error value to indicate if
// initialization was successful.
// fname is the path to the file that has to be used as the persistent memory
// medium.
func PmemInit(fname string) (unsafe.Pointer, error) {
	if GOOS != "linux" || GOARCH != "amd64" {
		return nil, error(errorString("Unsupported architecture"))
	}

	// Change persistent memory initialization state from not-done to ongoing
	if !atomic.Cas(&pmemInfo.initState, initNotDone, initOngoing) {
		return nil, error(errorString(`Persistent memory is already initialized
				or initialization is ongoing`))
	}

	// Set the persistent memory file name. This will be used to map the file
	// into memory in growPmemRegion().
	pmemInfo.fname = fname

	// Map the header section of the file to identify if this is a first-time
	// initialization.
	mapAddr, isPmem, err := mapFile(fname, int(pmemHeaderSize), fileCreate,
		_DEFAULT_FMODE, 0, nil)
	if err != 0 {
		return nil, error(errorString("Mapping persistent memory file failed"))
	}
	pmemHeader = (*pHeader)(mapAddr)
	pmemInfo.isPmem = isPmem

	if pmemHeader.magic != hdrMagic {
		// First time initialization
		// Store the mapped size in the header section
		pmemHeader.mappedSize = pmemHeaderSize
		PersistRange(unsafe.Pointer(&pmemHeader.mappedSize),
			unsafe.Sizeof(pmemHeaderSize))

		// Store the magic constant in the header section
		pmemHeader.magic = hdrMagic
		PersistRange(unsafe.Pointer(&pmemHeader.magic),
			unsafe.Sizeof(hdrMagic))
		println("First time initialization")
	} else {
		println("Not a first time intialization")
		err := verifyMetadata()
		if err != nil {
			unmapHeader()
			return nil, err
		}

		// Disable garbage collection during persistent memory initialization
		setGCPercent(-1)

		// Map all arenas found in the persistent memory file to memory. This
		// function also creates spans for the in-use regions of memory in the
		// file, and restores the heap type bits for the 'recreated' spans.
		err = mapArenas()
		if err != nil {
			unmapHeader()
			return nil, err
		}

		// Before invoking garbage collection, the runtime should ensure that
		// there is at least one variable pointing to the application root data.
		// Hence, save a copy of the root pointer in pmemInfo.root.
		pmemInfo.root = GetRoot()
	}

	// Set persistent memory as initialized
	atomic.Store(&pmemInfo.initState, initDone)

	return pmemInfo.root, nil
}

func mapArenas() (err error) {
	// A slice containing addresses of all mapped arenas, in case we need to
	// unmap them
	var arenas []*pArena

	if pmemHeader.mappedSize == pmemHeaderSize {
		// The persistent memory file contains only the header section and does
		// not contain any arenas.
		return
	}

	var mapped uintptr
	for mapped < pmemHeader.mappedSize {
		// Map the header section of the arena to get the size of the arena and
		// the map address. Then unmap the mapped region, and map the entire
		// arena at the map address.
		var offset uintptr
		if mapped == 0 {
			offset = pmemHeaderSize
		}
		mapAddr, _, err := mapFile(pmemInfo.fname, int(pArenaHeaderSize+offset),
			fileCreate, _DEFAULT_FMODE, int(mapped), nil)
		if err != 0 {
			unmapArenas(arenas)
			return error(errorString("Arena mapping failed"))
		}

		// Point at the arena header
		parena := (*pArena)(unsafe.Pointer(uintptr(mapAddr) + offset))
		arenaMapAddr := unsafe.Pointer(parena.mapAddr)
		arenaSize := parena.size
		munmap(mapAddr, pArenaHeaderSize+offset)

		// Try mapping the arena at the exact address it was mapped previously
		_, _, err = mapFile(pmemInfo.fname, int(arenaSize), fileCreate,
			_DEFAULT_FMODE, int(mapped), arenaMapAddr)
		if err != 0 {
			// We do not support swizzling yet, so just return an error
			unmapArenas(arenas)
			return error(errorString("Arena mapping failed"))
		}

		// Create the volatile memory arena datastructures for the newly mapped
		// heap regions. Each volatile arena datastructure contains the runtime
		// heap type bitmap and span table for the region it manages.
		mheap_.createArenaMetadata(arenaMapAddr, arenaSize)

		mapped += arenaSize

		// Point the arena header at the beginning of the mapped region
		parena = (*pArena)(unsafe.Pointer(uintptr(arenaMapAddr) + offset))
		arenas = append(arenas, parena)
	}

	// Update the next offset at which the persistent memory file should be mapped
	pmemInfo.nextMapOffset = mapped

	for _, ar := range arenas {
		ar.reconstruct()
	}

	return
}

// A helper function that iterates the arena slice and unmaps all of them
func unmapArenas(arenas []*pArena) {
	for _, ar := range arenas {
		mapAddr := unsafe.Pointer(ar.mapAddr)
		mapSize := ar.size
		munmap(mapAddr, mapSize)
	}
}

// A helper function that unmaps the header section of the persistent memory
// file in case any errors happen during the reconstruction process.
func unmapHeader() {
	munmap(unsafe.Pointer(pmemHeader), pmemHeaderSize)
}

// This function goes through the span bitmap found in the arena header, and
// recreates spans one by one. For each recreated span, it copies the heap type
// bits from the persistent memory arena header to the runtime arena datastructure.
func (ar *pArena) reconstruct() {
	h := &mheap_
	mdata, allocSize := ar.layout()
	allocPages := allocSize >> pageShift
	spanBase := ar.mapAddr + mdata

	// Create a span for this arena and free it so that it will be added to
	// the mheap free list.
	lock(&h.lock)
	s := (*mspan)(h.spanalloc.alloc())
	s.init(spanBase, allocPages)
	s.memtype = isPersistent
	s.pArena = (uintptr)(unsafe.Pointer(ar))
	s.needzero = 1
	h.setSpan(s.base(), s)
	h.setSpan(s.base()+s.npages*pageSize-1, s)
	s.state = mSpanManual
	h.freeSpanLocked(s, false, true, 0)
	unlock(&h.lock)

	// Compute the address of the span bitmap within the arena header.
	typeEntries := allocSize / bytesPerBitmapByte
	typeBitsAddr := ar.mapAddr + ar.offset + pArenaHeaderSize
	spanBitsAddr := unsafe.Pointer(typeBitsAddr + typeEntries)
	spanBitmap := (*(*[1 << 28]uint32)(spanBitsAddr))[:allocPages]

	// Iterate over the span bitmap log and recreate spans one by one
	for i, sVal := range spanBitmap {
		if sVal != 0 {
			baseAddr := spanBase + uintptr(i<<pageShift)
			s := createSpan(sVal, baseAddr)
			restoreSpanHeapBits(s)
		}
	}
}

// createSpan figures out the properties of the span to be reconstructed such as
// spanclass, number of pages, the needzero value, etc. and calls the core
// reconstruction function createSpanCore.
func createSpan(sVal uint32, baseAddr uintptr) *mspan {
	var npages int
	var spc spanClass
	large := false
	needzero := ((sVal & 1) == 1)
	if sVal > maxSmallSpanLogVal { // large allocation
		large = true
		noscan := ((sVal >> 1 & 1) == 1)
		npages = int((sVal >> 2) - 66 + 4)
		spc = makeSpanClass(0, noscan)
	} else {
		npages = int(class_to_allocnpages[sVal>>2])
		spc = spanClass(sVal >> 1)
	}
	return createSpanCore(spc, baseAddr, npages, large, needzero)
}

// createSpanCore creates a span corresponding to memory region beginning at
// address 'base' and containing 'npages' number of pages. It first searches the
// mheap free list/treap to find a large span to carve this span from. It then
// trims out any unnecessary regions from the span obtained from the search, sets
// the necessary metadata for the span, and restores the heap type bit information
// for this span.
func createSpanCore(spc spanClass, base uintptr, npages int, large, needzero bool) *mspan {
	h := &mheap_
	// lock mheap before searching for the required span
	lock(&h.lock)
	s := h.searchSpanLocked(base, npages)
	if s == nil {
		println("Unable to reconstruct span for address ", hex(base))
		throw("Unable to complete persistent memory metadata reconstruction")
	}

	// set span state as mSpanManual so that the spans freed below (corresponding
	// to the trailing and leading pages) do not coalesce with s.
	s.state = mSpanManual
	// The span we found from the search might contain more pages than necessary.
	// Trim the leading and trailing pages of the span.
	leadpages := (base - s.base()) >> pageShift
	if leadpages > 0 {
		freeSpan(leadpages, s.base(), s.needzero, s.pArena)
	}

	trailpages := s.npages - leadpages - uintptr(npages)
	if trailpages > 0 {
		freeSpan(trailpages, base+uintptr(npages*pageSize), s.needzero, s.pArena)
	}

	// Initialize s with the correct base address and number of pages
	s.init(base, uintptr(npages))
	h.setSpans(s.base(), s.npages, s)

	// Initialize other metadata of s
	s.state = mSpanInUse
	s.memtype = isPersistent
	s.spanclass = spc

	// copying span initialization code from alloc_m() in mheap.go
	if sizeclass := spc.sizeclass(); sizeclass == 0 { // indicates a large span
		s.elemsize = s.npages << pageShift
		s.divShift = 0
		s.divMul = 0
		s.divShift2 = 0
		s.baseMask = 0
	} else {
		s.elemsize = uintptr(class_to_size[sizeclass])
		m := &class_to_divmagic[sizeclass]
		s.divShift = m.shift
		s.divMul = m.mul
		s.divShift2 = m.shift2
		s.baseMask = m.baseMask
	}
	s.needzero = uint8(bool2int(needzero))

	if large == false {
		size := uintptr(class_to_size[spc.sizeclass()])
		n := uintptr(npages<<pageShift) / size
		s.limit = s.base() + size*n
	} else {
		s.limit = s.base() + (uintptr(npages) << pageShift)
	}

	_, ln, _ := s.layout()

	// During reconstruction, we mark the span as being completely used. This is
	// done by setting the allocCount and freeindex as the number of elements in
	// the span, and allocCache as 0 (allocCache holds the complement of allocBits).
	// The expectation is that GC will later do the required cleanup.

	// copying code block from initSpan() here
	// Init the markbit structures
	s.nelems = ln
	s.allocCount = uint16(ln) // set span as being fully allocated
	s.freeindex = ln
	s.allocCache = 0 // all 0s indicating all objects in the span are in-use
	s.allocBits = newAllocBits(s.nelems)
	s.gcmarkBits = newMarkBits(s.nelems)

	// Put span s in the appropriate memory allocator list
	if large {
		h.busy[isPersistent].insertBack(s)
	} else {
		// In-use and empty (no free objects) small spans are stored in the empty
		// list in mcentral. Since the span is empty, it will not be cached in
		// mcache.
		c := &mheap_.central[isPersistent][spc].mcentral
		lock(&c.lock)
		c.empty.insertBack(s)
		unlock(&c.lock)
	}

	// Set the sweep generation(SG) of the reconstructed span as the global SG.
	// Global SG will not change during the reconstruction process as GC is
	// disabled.
	s.sweepgen = h.sweepgen

	// From mheap.go: sweepSpans contains two mspan stacks: one of swept in-use
	// spans, and one of unswept in-use spans. These two trade roles on each GC
	// cycle. Since the sweepgen increases by 2 on each cycle, this means the
	// swept spans are in sweepSpans[sweepgen/2%2] and the unswept spans are in
	// sweepSpans[1-sweepgen/2%2]. Sweeping pops spans from the unswept stack
	// and pushes spans that are still in-use on the swept stack. Likewise,
	// allocating an in-use span pushes it  on the swept stack.

	// Place the reconstructed spans in the swept in-use stack of sweepSpans.
	// During the next full GC cycle, these spans would all be considered to be
	// unswept, and will be swept by the GC.
	h.sweepSpans[(h.sweepgen / 2 % 2)].push(s)
	unlock(&h.lock)

	return s
}

// Function to search the memory allocator free treap to find if it contains a
// large-enough span that can contain the required span with start address
// 'baseAddr' and 'npages' number of pages
func treapSearch(root *treapNode, baseAddr uintptr, npages uintptr) *mspan {
	if root == nil {
		return nil
	}
	key := root.spanKey

	// Check if key points to a span that has base address less than or equal
	// to 'baseAddr' and has the required number of pages
	if key.base() <= baseAddr && (key.base()+key.npages*pageSize) >=
		baseAddr+uintptr(npages*pageSize) {
		mheap_.free[isPersistent].removeNode(root)
		return key
	}

	// Recursively search the left subtree
	if root.left != nil && npages <= key.npages {
		if sl := treapSearch(root.left, baseAddr, npages); sl != nil {
			return sl
		}
	}

	// Recursively search the right subtree
	if root.right != nil {
		if sr := treapSearch(root.right, baseAddr, npages); sr != nil {
			return sr
		}
	}

	return nil
}

// Function that searches the mheap free large treap and free lists to find a
// large span that can contain the required span with 'npages' number of spans
// starting  at address 'baseAddr'.
// mheap must be locked before calling this function.
func (h *mheap) searchSpanLocked(baseAddr uintptr, npages int) *mspan {
	// Check the free treap to see if it has a large span that can
	// contain the required span
	treapRoot := h.free[isPersistent].treap
	var s *mspan
	systemstack(func() {
		s = treapSearch(treapRoot, baseAddr, uintptr(npages))
	})
	return s
}

// freeSpan() is used to put back trimmed out regions of a span back into the
// memory allocator free list/treap. 'npages' is the number of pages in the trimmed
// region, and 'base' is its start address.
// mheap must be locked before calling this function
func freeSpan(npages, base uintptr, needzero uint8, parena uintptr) {
	h := &mheap_
	t := (*mspan)(h.spanalloc.alloc())
	t.init(base, npages)
	t.memtype = isPersistent
	t.pArena = parena

	h.setSpan(t.base(), t)
	h.setSpan(t.base()+t.npages*pageSize-1, t)
	t.needzero = needzero
	t.state = mSpanManual
	h.freeSpanLocked(t, false, false, 0)
}

// InPmem checks whether 'addr' is an address in the persistent memory range
func InPmem(addr uintptr) bool {
	s := spanOfHeap(addr)
	if s == nil {
		return false
	}
	return s.memtype == isPersistent
}

// GetRoot returns the root pointer that is stored in the persistent memory
// header region. Swizzling is not yet supported, so the address returned is
// the same as that was stored using SetRoot().
func GetRoot() unsafe.Pointer {
	addr := pmemHeader.rootPointer
	if !InPmem(addr) {
		// The address does not point to a persistent memory address. This may
		// have happened due to some data corruption. Return a nil pointer so
		// that application does not access an unsafe address.
		return nil
	}
	return unsafe.Pointer(addr)
}

// SetRoot stores the application root pointer in the persistent memory header
// region.
func SetRoot(addr unsafe.Pointer) (err error) {
	if !InPmem(uintptr(addr)) {
		return error(errorString("Address not in persistent memory"))
	}
	lock(&pmemInfo.rootLock)
	pmemInfo.root = addr
	pmemHeader.rootPointer = uintptr(addr)
	dstAddr := (unsafe.Pointer)(&pmemHeader.rootPointer)
	PersistRange(dstAddr, unsafe.Sizeof(addr))
	unlock(&pmemInfo.rootLock)
	return
}

// EnableGC runs a full GC cycle as a stop-the-world event. This is written as a
// separate function to PmemInit() so that the application can do any pointer
// manipulation (such as swizzling) before a full GC cycle is run.
// The argumnet gcp specifies garbage collection percentage and controls how
// often GC is run (see https://golang.org/pkg/runtime/debug/#SetGCPercent).
// Default value of gcp is 100.
// Users are required to initialize persistent memory as follows:
// (1) Call PmemInit to initialize persistent memory
// (2) Call EnableGC()
// This function would be removed when runtime supports pointer swizzling.
func EnableGC(gcp int) (err error) {
	if pmemInfo.initState != initDone {
		return error(errorString("Persistent memory uninitialized"))
	}
	setGCPercent(int32(gcp)) // restore GC percentage
	// Run GC so that unused memory in reconstructed spans are reclaimed
	stw := debug.gcstoptheworld
	// set debug.gcstoptheworld as gcForceBlockMode so that GC runs as a
	// stop-the-world event
	debug.gcstoptheworld = int32(gcForceBlockMode)
	GC()
	// restore gcstoptheworld
	debug.gcstoptheworld = stw
	return
}

// Restores the heap type bit information for the reconstructed span 's'.
// The heap type bits is needed for the GC to identify what regions in the
// reconstructed span have pointers in them.
// This function copies the heap type bits that was logged in the persistent
// memory arena header to the volatile memory arena datastructures that holds
// the runtime type bits for this span. The volatile type bits for this span
// may be contained in one or more volatile arenas. Therefore, this function
// copies the heap type bits in a per volatile-memory arena manner.
func restoreSpanHeapBits(s *mspan) {
	// Golang runtime uses 1 byte to record heap type bitmap of 32 bytes of heap
	// total heap type bytes to be copied
	totalBytes := (s.npages << pageShift) / bytesPerBitmapByte

	parena := (*pArena)(unsafe.Pointer(s.pArena))
	spanAddr := s.base()
	spanEnd := spanAddr + (s.npages << pageShift)

	for copied := uintptr(0); copied < totalBytes; {
		// each iteration copies heap type bits corresponding to the heap region
		// between 'spanAddr' and 'endAddr'
		ai := arenaIndex(spanAddr)
		arenaEnd := arenaBase(ai) + heapArenaBytes
		endAddr := arenaEnd
		// Since a span can span across two arenas, the end adress to be used to
		// copy the heap type bits is the minimium of the span end address and the
		// arena end address.
		if spanEnd < endAddr {
			endAddr = spanEnd
		}

		numSpanBytes := (endAddr - spanAddr)
		srcAddr := pmemHeapBitsAddr(spanAddr, parena)
		dstAddr := unsafe.Pointer(heapBitsForAddr(spanAddr).bitp)
		memmove(dstAddr, srcAddr, numSpanBytes/bytesPerBitmapByte)

		copied += (numSpanBytes / bytesPerBitmapByte)
		spanAddr += numSpanBytes
	}
}
