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

// Constants representing possible persistent memory initialization states
const (
	initNotDone = iota // Persistent memory not initialiazed
	initOngoing        // Persistent memory initialization ongoing
	initDone           // Persistent memory initialization completed
)

const (
	isNotPersistent = 0
	isPersistent    = 1

	// Size of a int/uintptr variable
	intSize = unsafe.Sizeof(0)

	// maxMemTypes represents the memory types supported - persistent memory
	// and volatile memory.
	maxMemTypes = 2

	// The maximum span class of a small span
	maxSmallSpanclass = (_NumSizeClasses << 1) - 1

	// The maximum value that will logged in span bitmap corresponding to a
	// small span. This is when the spanclass of the span is maxSmallSpanclass
	// and its needzero parameter is 1.
	maxSmallSpanLogVal = (maxSmallSpanclass << 1) + 1

	// The effective permission of the created persistent memory file is
	// (mode & ~umask) where umask is the system wide umask.
	_DEFAULT_FMODE = 0666
)

var (
	memTypes   = []int{isPersistent, isNotPersistent}
	pmemHeader *pHeader
)

// The structure of the persistent memory file header region
type pHeader struct {
	// A magic constant that is used to distinguish between the first time and
	// subsequent initialization of the persistent memory file.
	magic int

	// The size of the file that is currently mapped into memory. This is used
	// during reinitialization to identify if the file was externally truncated
	// and to correctly map the file into memory.
	mappedSize uintptr

	// The offset from the beginning of the file of the application root pointer.
	// An offset is stored instead of the actual root pointer because, during
	// reinitialization, the arena map address can change causing the pointer
	// value to be invalid.
	rootOffset uintptr

	// If pointers are currently being swizzled, swizzleState captures the current
	// swizzling state.
	swizzleState int
}

// Strucutre of a persistent memory arena header
// Persistent memory arena header pointers used in the runtime point to the
// actual data in the beginning of the persistent memory arena, and not to a
// volatile copy.
type pArena struct {
	// To identify this is a go-pmem recognized arena. This can either be a
	// magic constant or something like a checksum.
	magic int

	// Size of the persistent memory arena
	size uintptr

	// Address at which the region corresponding to this arena is mapped
	// During swizzling mapAddr cannot be reliably used as it might contain the
	// mapping address from a previous run.
	mapAddr uintptr

	// The delta value to be used for pointer swizzling
	delta int

	// The offset of the file region from the beginning of the file at which this
	// arena is mapped at.
	fileOffset uintptr

	// The number of bytes of data in this arena that have already been swizzled
	bytesSwizzled uintptr

	// The following data members are for supporting a minimal per-arena undo log
	numLogEntries int         // Number of valid entries in the log section
	logs          [2]logEntry // The actual log data

	// This is followed by the heap type bits log and the span bitmap log which
	// occupies a variable number of bytes depending on the size of the arena.
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

	var gcp int
	firstInit := pmemHeader.magic != hdrMagic
	if firstInit {
		// First time initialization
		// Store the mapped size in the header section
		pmemHeader.mappedSize = pmemHeaderSize
		PersistRange(unsafe.Pointer(&pmemHeader.mappedSize), intSize)

		// Store the magic constant in the header section
		pmemHeader.magic = hdrMagic
		PersistRange(unsafe.Pointer(&pmemHeader.magic), intSize)
		println("First time initialization")
	} else {
		println("Not a first time intialization")
		err := verifyMetadata()
		if err != nil {
			unmapHeader()
			return nil, err
		}

		// Disable garbage collection during persistent memory initialization
		gcp = int(setGCPercent(-1))

		// Map all arenas found in the persistent memory file to memory. This
		// function creates spans for the in-use regions of memory in the
		// arenas, restores the heap type bits for the 'recreated' spans, and
		// swizzles all pointers in the arenas if necessary.
		err = mapArenas()
		if err != nil {
			unmapHeader()
			return nil, err
		}
	}
	// TODO - Set persistent memory as initialized
	atomic.Store(&pmemInfo.initState, initDone)

	if !firstInit {
		// Enable garbage collection
		enableGC(gcp)
	}

	return pmemInfo.root, nil
}

// Arena information structure which will be used during reconstruction and
// swizzling.
type arenaInfo struct {
	// Pointer to the arena metadata section in persistent memory
	pa *pArena

	// The address at which the arena is mapped in this run. This would be
	// different than pa.mapAddr if the arena mapping address changed.
	mapAddr uintptr
}

func mapArenas() error {
	h := &mheap_
	// A slice containing information about each mapped arena
	var arenas []*arenaInfo

	if pmemHeader.mappedSize == pmemHeaderSize {
		// The persistent memory file contains only the header section and does
		// not contain any arenas.
		return nil
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
			fileCreate, _DEFAULT_FMODE, mapped, nil)
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
		// mapFile() will fail if the file cannot be mapped at the requested address
		mapAddr, _, err = mapFile(pmemInfo.fname, int(arenaSize), fileCreate,
			_DEFAULT_FMODE, mapped, arenaMapAddr)
		if err != 0 {
			// Try mapping the arena again, but at any address
			mapAddr, _, err = mapFile(pmemInfo.fname, int(arenaSize), fileCreate,
				_DEFAULT_FMODE, mapped, nil)
			if err != 0 {
				unmapArenas(arenas)
				return error(errorString("Arena mapping failed"))
			}
		}

		// Create the volatile memory arena datastructures for the newly mapped
		// heap regions. Each volatile arena datastructure contains the runtime
		// heap type bitmap and span table for the region it manages.
		lock(&h.lock)
		h.createArenaMetadata(mapAddr, arenaSize)
		unlock(&h.lock)

		mapped += arenaSize

		// Point the arena header at the actual mapped region
		parena = (*pArena)(unsafe.Pointer(uintptr(mapAddr) + offset))
		ar := &arenaInfo{parena, uintptr(mapAddr)}
		// Reconstruct the spans in this arena
		ar.reconstruct()
		arenas = append(arenas, ar)
	}

	// Update the next offset at which the persistent memory file should be mapped
	pmemInfo.nextMapOffset = mapped

	err := swizzleArenas(arenas)
	if err != nil {
		unmapArenas(arenas)
	}

	return err

}

// A helper function that iterates the arena slice and unmaps all of them
func unmapArenas(arenas []*arenaInfo) {
	for _, ar := range arenas {
		pa := ar.pa
		mapAddr := unsafe.Pointer(pa.mapAddr)
		mapSize := pa.size
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
func (ar *arenaInfo) reconstruct() {
	h := &mheap_
	pa := ar.pa
	mdata, allocSize := pa.layout()
	allocPages := allocSize >> pageShift
	spanBase := ar.mapAddr + mdata

	// We need to add the space occupied by the common persistent memory header
	// for the first arena.
	var off uintptr
	if pa.fileOffset == 0 {
		off = pmemHeaderSize
	}

	// Compute the address of the span bitmap within the arena header.
	typeEntries := allocSize / bytesPerBitmapByte
	typeBitsAddr := ar.mapAddr + off + pArenaHeaderSize
	spanBitsAddr := unsafe.Pointer(typeBitsAddr + typeEntries)
	spanBitmap := (*(*[1 << 28]uint32)(spanBitsAddr))[:allocPages]

	// Iterate over the span bitmap log and recreate spans one by one
	var i, j uintptr
	for i < allocPages {
		sval := spanBitmap[i]
		addr := spanBase + (i << pageShift)
		if sval == 0 {
			for j = i + 1; j < allocPages; j++ {
				if spanBitmap[j] != 0 {
					break
				}
			}
			npages := (j - i)
			lock(&h.lock)
			freeSpan(npages, addr, 1, (uintptr)(unsafe.Pointer(pa)))
			unlock(&h.lock)
			i += npages
		} else {
			s := createSpan(sval, addr)
			// TODO
			// s.pArena = (uintptr)(unsafe.Pointer(pa))
			// TODO xxx jerrin iterate over all the possible arenas
			// can span be bigger than 64 mb?
			// better still would be to do this just at the pmem arena level
			ai := arenaIndex(addr)
			arenaT := mheap_.arenas[ai.l1()][ai.l2()]
			arenaT.pArena = (uintptr)(unsafe.Pointer(pa))
			ar.restoreSpanHeapBits(s)
			i += s.npages
		}
	}
}

// createSpan figures out the properties of the span to be reconstructed such as
// spanclass, number of pages, the needzero value, etc. and calls the core
// reconstruction function createSpanCore.
func createSpan(sVal uint32, baseAddr uintptr) *mspan {
	var npages uintptr
	var spc spanClass
	large := false
	needzero := ((sVal & 1) == 1)
	if sVal > maxSmallSpanLogVal { // large allocation
		large = true
		noscan := ((sVal >> 1 & 1) == 1)
		npages = uintptr((sVal >> 2) - 66 + 4)
		spc = makeSpanClass(0, noscan)
	} else {
		npages = uintptr(class_to_allocnpages[sVal>>2])
		spc = spanClass(sVal >> 1)
	}
	return createSpanCore(spc, baseAddr, npages, large, needzero)
}

// createSpanCore creates a span corresponding to memory region beginning at
// address 'base' and containing 'npages' number of pages. It sets the necessary
// metadata for the span, adds the span in the appropriate memory allocator list
// and also adds it in the sweepSpans datastructure so that this span would be
// swept in the next complete GC cycle.
func createSpanCore(spc spanClass, base, npages uintptr, large, needzero bool) *mspan {
	h := &mheap_

	// TODO jerrin XXX does spanalloc need a lock?
	// what about instead allocing from the per-p span caches.. ?
	s := (*mspan)(h.spanalloc.alloc())
	s.init(base, npages)
	// Initialize other metadata of s
	s.state.set(mSpanInUse)
	s.memtype = isPersistent
	s.spanclass = spc

	// copying span initialization code from alloc_m() in mheap.go
	if sizeclass := spc.sizeclass(); sizeclass == 0 { // indicates a large span
		s.elemsize = s.npages << pageShift
		// TODO missing setting nelems
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

	// Mark in-use span in arena page bitmap.
	arena, pageIdx, pageMask := pageIndexOf(s.base())
	arena.pageInUse[pageIdx] |= pageMask

	s.needzero = uint8(bool2int(needzero))

	if large == false {
		size := uintptr(class_to_size[spc.sizeclass()])
		n := (npages << pageShift) / size
		s.limit = s.base() + size*n
	} else {
		s.limit = s.base() + (npages << pageShift)
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

	h.setSpans(s.base(), s.npages, s)

	// Set the sweep generation(SG) of the reconstructed span as the global SG.
	// Global SG will not change during the reconstruction process as GC is
	// disabled.
	sg := h.sweepgen
	s.sweepgen = sg

	// Put span s in the appropriate memory allocator list
	// TODO jerrin is the lock required
	lock(&h.lock)
	if large {
		// TODO jerrin
		//h.free[isPersistent].insert(s)
		throw("large span not supported")
	} else {
		// In-use and empty (no free objects) small spans are stored in the empty
		// list in mcentral. Since the span is empty, it will not be cached in
		// mcache.
		c := &mheap_.central[isPersistent][spc].mcentral
		//lock(&c.lock)
		//c.empty.insertBack(s)
		c.fullSwept(sg).push(s)
		//unlock(&c.lock)
	}

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
	//h.sweepSpans[(h.sweepgen / 2 % 2)].push(s)
	unlock(&h.lock)

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
	// TODO XXX jerrin
	//t.pArena = parena
	ai := arenaIndex(base)
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	arena.pArena = parena

	// TODO XXX jerrin only first and last need to be set
	h.setSpans(t.base(), t.npages, t)
	t.needzero = needzero
	t.state.set(mSpanInUse)
	h.freeSpanLocked(t, true, true)
}

// InPmem checks whether 'addr' is an address in the persistent memory range
func InPmem(addr uintptr) bool {
	s := spanOfHeap(addr)
	if s == nil {
		return false
	}
	return s.memtype == isPersistent
}

// GetRoot returns the application root pointer. After a restart, the swizzling
// code will take care of setting the correct 'swizzled' pointer as root.
// GetRoot() returns nil if it is called before persistent memory initialization
// is completed.
func GetRoot() unsafe.Pointer {
	return pmemInfo.root
}

// SetRoot stores the application root pointer in the persistent memory header
// region.
func SetRoot(addr unsafe.Pointer) (err error) {
	s := spanOfHeap(uintptr(addr))
	if s == nil || s.memtype != isPersistent {
		return error(errorString("Invalid address passed to SetRoot"))
	}

	ai := arenaIndex(uintptr(addr))
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	pa := (*pArena)(unsafe.Pointer(arena.pArena))
	fo := pa.fileOffset
	po := uintptr(addr) - arena.pArena

	lock(&pmemInfo.rootLock)
	pmemInfo.root = addr
	pmemHeader.rootOffset = po + fo
	PersistRange((unsafe.Pointer)(&pmemHeader.rootOffset), intSize)
	unlock(&pmemInfo.rootLock)
	return
}

// enableGC runs a full GC cycle in a new goroutine.
// The argumnet gcp specifies garbage collection percentage and controls how
// often GC is run (see https://golang.org/pkg/runtime/debug/#SetGCPercent).
// Default value of gcp is 100.
func enableGC(gcp int) {
	setGCPercent(int32(gcp)) // restore GC percentage
	// Run GC so that unused memory in reconstructed spans are reclaimed
	go GC()
}

// Restores the heap type bit information for the reconstructed span 's'.
// The heap type bits is needed for the GC to identify what regions in the
// reconstructed span have pointers in them.
// This function copies the heap type bits that was logged in the persistent
// memory arena header to the volatile memory arena datastructures that holds
// the runtime type bits for this span. The volatile type bits for this span
// may be contained in one or more volatile arenas. Therefore, this function
// copies the heap type bits in a per volatile-memory arena manner.
func (ar *arenaInfo) restoreSpanHeapBits(s *mspan) {
	// Golang runtime uses 1 byte to record heap type bitmap of 32 bytes of heap
	// total heap type bytes to be copied
	totalBytes := (s.npages << pageShift) / bytesPerBitmapByte

	spanAddr := s.base()
	ai := arenaIndex(uintptr(spanAddr))
	arena := mheap_.arenas[ai.l1()][ai.l2()]
	parena := (*pArena)(unsafe.Pointer(arena.pArena))
	spanEnd := spanAddr + (s.npages << pageShift)

	for copied := uintptr(0); copied < totalBytes; {
		// each iteration copies heap type bits corresponding to the heap region
		// between 'spanAddr' and 'endAddr'
		ai = arenaIndex(spanAddr)
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

func swizzleArenas(arenas []*arenaInfo) (err error) {
	// todo
	return
}
