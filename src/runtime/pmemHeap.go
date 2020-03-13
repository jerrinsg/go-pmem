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

	// Size of a int/uintptr variable
	intSize = unsafe.Sizeof(0)

	// maxMemTypes represents the memory types supported - persistent memory
	// and volatile memory.
	maxMemTypes = 2

	// The number of types that we want to cache separately in the mcache
	// (including slices which is always cached at index 1). If a type is not
	// specially cached, it occupies slot 0.
	maxCacheTypes = 51

	// The maximum span class of a small span
	maxSmallSpanclass = 133

	// The maximum value that will logged in span bitmap corresponding to a
	// small span. This is when the spanclass of the span is 133 and its
	// needzero and optTypeLog bit is 1.
	maxSmallSpanLogVal = (maxSmallSpanclass << 2) + (1 << 1) + 1

	// The effective permission of the created persistent memory file is
	// (mode & ~umask) where umask is the system wide umask.
	_DEFAULT_FMODE = 0666
)

var (
	memTypes   = []int{isPersistent, isNotPersistent}
	pmemHeader *pHeader
	// AppCallBack is used by applications to register a callback function that
	// is called before pointers are swizzled in the heap recovery path. The
	// transaction package uses the callback to abort undo log entries before
	// runtime swizzles pointers.
	AppCallBack func(unsafe.Pointer)
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

	// Up to maxCacheTypes types are cached separately in the persistent memory
	// mcache to minimize the amount of metadata that we log during each
	// allocation. typeMap is used to persistently store the relationship
	// between a cached type and its index within the cache.
	// typeMap[i] stores the pointer to the type (in RODATA section) cached at
	// index i. In mcache, index 1 is always used to allocate slices, and index
	// 0 is used for types which are not cached, so we need to persist the
	// mapping only for maxCacheTypes - 2 number of entries.
	typeMap [maxCacheTypes - 2]uintptr
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
		return nil, errorString("Unsupported architecture")
	}

	// Change persistent memory initialization state from not-done to ongoing
	if !atomic.Cas(&pmemInfo.initState, initNotDone, initOngoing) {
		return nil, errorString(`Persistent memory is already initialized
				or initialization is ongoing`)
	}

	// platformInit() checks if the platform supports eADR. If not, the cache
	// flush instruction is set according to the CPU capabilities.
	platformInit()

	// Set the persistent memory file name. This will be used to map the file
	// into memory in growPmemRegion().
	pmemInfo.fname = fname

	// Map the header section of the file to identify if this is a first-time
	// initialization.
	mapAddr, isPmem, err := mapFile(fname, int(pmemHeaderSize), fileCreate,
		_DEFAULT_FMODE, 0, nil)
	if err != 0 {
		return nil, errorString("Mapping persistent memory file failed")
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

		// Restore the type information
		for i := 0; i < maxCacheTypes-2; i++ {
			if pmemHeader.typeMap[i] == 0 {
				break
			}
			typOffset := pmemHeader.typeMap[i]
			typAssigns[typOffset] = i + 2
			numAssigned++
		}

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

	// Set persistent memory as initialized
	atomic.Store(&pmemInfo.initState, initDone)
	go typeProfileThread()

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

	// Temporary array to copy heap type bitmap into
	bitsArray []byte

	// We use heapBitsSetType to set heap type bitmap for spans where we employ
	// type caching. typ is used to store the type infomation of the cached type
	typ _type
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
			return errorString("Arena mapping failed")
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
				return errorString("Arena mapping failed")
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
		// arenaInfo struct and the pointers within it are garbage-collected
		// once this function returns
		ar := &arenaInfo{pa: parena, mapAddr: uintptr(mapAddr), bitsArray: make([]byte, 1024)}

		// Reconstruct the spans in this arena
		// Calling arena reconstruct in parallel using goroutines did not give
		// any significant performance difference.
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
			s := pa.createSpan(sval, addr)
			// The heap type bits need to be restored only if the span is known
			// to have pointers in it.
			if !s.spanclass.noscan() {
				ar.restoreSpanHeapBits(s)
			}
			i += s.npages
		}
	}
}

// createSpan figures out the properties of the span to be reconstructed such as
// spanclass, number of pages, the needzero value, etc. and calls the core
// reconstruction function createSpanCore.
func (pa *pArena) createSpan(sVal uint32, baseAddr uintptr) *mspan {
	var npages uintptr
	var spc spanClass
	large := false
	needzero := (sVal & 1) == 1
	if sVal > maxSmallSpanLogVal { // large allocation
		large = true
		noscan := (sVal >> 2 & 1) == 1
		npages = uintptr((sVal >> 3) - 66 + 4)
		spc = makeSpanClass(0, noscan)
	} else {
		npages = uintptr(class_to_allocnpages[sVal>>3])
		spc = spanClass(sVal >> 2)
	}
	typIndex := 0
	if sVal>>1&1 != 0 {
		// Span uses optimized heap type bit logging. Find out the type index
		typAddr := pmemHeapBitsAddr(baseAddr, pa)
		typIndex = *(*int)(typAddr)
	}

	s := createSpanCore(spc, baseAddr, npages, large, needzero, typIndex)
	s.pArena = (uintptr)(unsafe.Pointer(pa))

	return s
}

// createSpanCore creates a span corresponding to memory region beginning at
// address 'base' and containing 'npages' number of pages. It sets the necessary
// metadata for the span, adds the span in the appropriate memory allocator list
// and also adds it in the sweepSpans datastructure so that this span would be
// swept in the next complete GC cycle.
func createSpanCore(spc spanClass, base, npages uintptr, large, needzero bool, typIndex int) *mspan {
	h := &mheap_

	lock(&h.lock)
	s := (*mspan)(h.spanalloc.alloc())
	unlock(&h.lock)

	s.init(base, npages)
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
	s.typIndex = typIndex

	h.setSpans(s.base(), s.npages, s)

	// Put span s in the appropriate memory allocator list
	lock(&h.lock)
	if large {
		h.busy[isPersistent].insertBack(s)
	} else {
		// In-use and empty (no free objects) small spans are stored in the empty
		// list in mcentral. Since the span is empty, it will not be cached in
		// mcache.
		c := &mheap_.central[isPersistent][spc][typIndex].mcentral
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

// inpmem checks whether 'addr' is an address in the persistent memory range
func inpmem(addr uintptr) bool {
	s := spanOfHeap(addr)
	if s == nil {
		return false
	}
	return s.memtype == isPersistent
}

// InPmem is the public API for inPmem()
func InPmem(addr uintptr) bool {
	return inpmem(addr)
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
		return errorString("Invalid address passed to SetRoot")
	}

	pa := (*pArena)(unsafe.Pointer(s.pArena))
	fo := pa.fileOffset
	po := uintptr(addr) - s.pArena

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
// If the span uses an optimized representation for storing the heap type bits
// (because the span is specially cached for allocations of a particular
// datatype) then heapBitsSetType() is called to copy the heap type bits
func (ar *arenaInfo) restoreSpanHeapBits(s *mspan) {
	spanAddr := s.base()
	spanEnd := spanAddr + (s.npages << pageShift)
	parena := (*pArena)(unsafe.Pointer(s.pArena))

	if s.typIndex == 0 {
		// Golang runtime uses 1 byte to record heap type bitmap of 32 bytes of
		// heap total heap type bytes to be copied
		totalBytes := (s.npages << pageShift) / bytesPerBitmapByte
		for copied := uintptr(0); copied < totalBytes; {
			// each iteration copies heap type bits corresponding to the heap
			// region between 'spanAddr' and 'endAddr'
			ai := arenaIndex(spanAddr)
			arenaEnd := arenaBase(ai) + heapArenaBytes
			endAddr := arenaEnd
			// Since a span can span across two arenas, the end adress to be
			// used to copy the heap type bits is the minimium of the span end
			// address and the arena end address.
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
		return
	}

	// We create a temporary byte array per arenaInfo structure to temporarily
	// copy the heap type bitmap in heapBitsSetType(). If the temp buffer is not
	// large enough to hold the heap bits, create a larger buffer here.
	// 2 bits per 8 bytes
	numBytesReqd := ((2 * s.elemsize / 8) + 7) / 8
	if len(ar.bitsArray) < int(numBytesReqd) {
		ar.bitsArray = make([]byte, numBytesReqd)
	}

	addr := uintptr(pmemHeapBitsAddr(spanAddr, parena))
	kindAddr := (*uint8)(unsafe.Pointer(addr + intSize))
	ar.typ.kind = *kindAddr

	sizeAddr := (*uintptr)(unsafe.Pointer(addr + 16))
	ar.typ.size = *sizeAddr

	ptrAddr := (*uintptr)(unsafe.Pointer(addr + 24))
	ar.typ.ptrdata = *ptrAddr

	gcDataAddr := unsafe.Pointer(addr + 32)
	ar.typ.gcdata = (*byte)(gcDataAddr)

	// If nelems is greater than 1, it implies this span contains array elements
	nelems := s.elemsize / ar.typ.size

	startAddr := s.base()
	for k := uintptr(0); k < s.nelems; k++ {
		tmpHeapBitsAddr := uintptr(unsafe.Pointer(&ar.bitsArray[0]))
		heapBitsSetType(startAddr, s.elemsize, nelems*ar.typ.size, &ar.typ, tmpHeapBitsAddr)
		startAddr += s.elemsize
	}
}

type tuple struct {
	s, e uintptr
}

var (
	// TODO move this to a swizzling specific data structure
	// The offset table stores, for each arena, the delta value by which pointers
	// that point into this arena should be offset by.
	offsetTable []int

	// rangeTable stores the address range at which each arena is mapped at.
	rangeTable []tuple
)

func swizzleArenas(arenas []*arenaInfo) (err error) {
	// Channel used to synchronize between goroutines that are used to swizzle
	// arenas.
	dc := make(chan bool, len(arenas))

	offsetTable = make([]int, len(arenas))
	rangeTable = make([]tuple, len(arenas))

	if pmemHeader.swizzleState == swizzleSetup {
		// There was a swizzle setup operating happening which did not complete.
		// Revert the log entries (if any) found in each arena. This would revert
		// any partial updates to mapAddr and delta value of each arena
		for _, ar := range arenas {
			pa := ar.pa
			pa.revertLog()
		}

		// Reset swizzle state to swizzleDone
		pmemHeader.setSwizzleState(swizzleDone)
	}

	if pmemHeader.swizzleState == swizzleOngoing {
		// There was a swizzle operation ongoing previously that has to be
		// completed first

		// Revert log entries (if any) found in each arena
		for i, ar := range arenas {
			pa := ar.pa
			pa.revertLog()
			// Compute the offset and range value for this arena
			offsetTable[i] = pa.delta
			rangeTable[i].s = uintptr(int(pa.mapAddr) - pa.delta)
			rangeTable[i].e = uintptr(int(pa.mapAddr) - pa.delta + int(pa.size))
		}

		// Complete any partially completed swizzling operation
		for _, ar := range arenas {
			go ar.swizzle(dc)
		}
		// Wait until all goroutines complete swizzling.
		for range arenas {
			<-dc
		}

		// Change the delta value in all arenas to 0. This has to be done before
		// state is set as swizzleDone.
		for _, ar := range arenas {
			pa := ar.pa
			pa.logEntry(unsafe.Pointer(&pa.delta))
			pa.delta = 0
			PersistRange(unsafe.Pointer(&pa.delta), intSize)
			// The data logged here will be discarded when resetLog() is called
			// before pointers are swizzled.
		}

		// Set swizzle state as swizzleDone
		pmemHeader.setSwizzleState(swizzleDone)
	}

	// At this point any partially completed swizzle operation from previous runs
	// is completed. If swizzling is required for this run, do that now.
	for i, ar := range arenas {
		// Set bytesSwizzled as 0. This can be done without logging as
		// swizzling is considered to have started only when swizzleState is set
		// as swizzleOngoing.
		pa := ar.pa
		pa.bytesSwizzled = 0
		PersistRange(unsafe.Pointer(&pa.bytesSwizzled), intSize)
		pa.resetLog()

		// Compute the offset and range value for this arena using the new
		// mapped address
		offsetTable[i] = int(ar.mapAddr) - (int(pa.mapAddr) - pa.delta)
		rangeTable[i].s = uintptr(int(pa.mapAddr) - pa.delta)
		rangeTable[i].e = uintptr(int(pa.mapAddr) - pa.delta + int(pa.size))
	}

	// Set swizzle state as swizzleSetup
	pmemHeader.setSwizzleState(swizzleSetup)

	for i, ar := range arenas {
		pa := ar.pa
		// Write the new map address and delta value to arena header
		pa.logEntry(unsafe.Pointer(&pa.mapAddr))
		pa.logEntry(unsafe.Pointer(&pa.delta))
		pa.mapAddr = ar.mapAddr
		pa.delta = offsetTable[i]
		// Commit persists the changes and then resets the log
		pa.commitLog()
	}

	// Set swizzle state as swizzleOngoing
	pmemHeader.setSwizzleState(swizzleOngoing)

	// Call the application callback function, if one is registered
	if AppCallBack != nil {
		newRoot := computeRootAddr(pmemHeader.rootOffset, arenas)
		AppCallBack(newRoot)
	}

	// Swizzle pointers in each arena
	for _, ar := range arenas {
		go ar.swizzle(dc)
	}
	// Wait until all goroutines complete swizzling.
	for range arenas {
		<-dc
	}

	for _, ar := range arenas {
		pa := ar.pa
		pa.logEntry(unsafe.Pointer(&pa.delta))
		pa.delta = 0
		PersistRange(unsafe.Pointer(&pa.delta), intSize)
	}

	// Set swizzle state as swizzleDone
	pmemHeader.setSwizzleState(swizzleDone)

	for _, ar := range arenas {
		pa := ar.pa
		pa.resetLog()
	}

	// The address of the application root pointer may have changed. So compute
	// the address of the root pointer, and set it as the root pointer.
	if pmemHeader.rootOffset != 0 {
		newRoot := computeRootAddr(pmemHeader.rootOffset, arenas)
		err = SetRoot(newRoot)
	}

	return
}

// Given the offset to the application root pointer in the persistent memory
// file, this function computes the actual virtual memory address corresponding
// to the root offset.
func computeRootAddr(offset uintptr, arenas []*arenaInfo) unsafe.Pointer {
	tot := uintptr(0)
	for _, ar := range arenas {
		pa := ar.pa
		if offset < tot+pa.size {
			aOff := offset - tot
			pu := uintptr(unsafe.Pointer(pa))
			return unsafe.Pointer(pu + aOff)
		}
		tot += pa.size
	}
	return nil
}

func (ph *pHeader) setSwizzleState(state int) {
	ph.swizzleState = state
	PersistRange(unsafe.Pointer(&ph.swizzleState), intSize)
}

// Find which arena index would have contained pointer x
func findArenaIndex(x uintptr, rangeTable []tuple) int {
	for i, t := range rangeTable {
		if x >= t.s && x < t.e {
			return i
		}
	}
	return -1
}

// Swizzle arena pa. skip is the number of bytes in the beginning of the arena
// that has to be skipped for swizzling.
func (ar *arenaInfo) swizzle(dc chan bool) {
	pa := ar.pa
	mdata, allocSize := pa.layout()

	// The start address of the allocator managed space in this arena
	start := ar.mapAddr + mdata

	for done := pa.bytesSwizzled; done < allocSize; {
		addr := start + done

		s := spanOfUnchecked(addr)
		end := s.base() + (s.npages << pageShift)

		// If the span is not in-use or if the span is known to not contain any
		// pointers, then swizzling can be skipped on this span.
		if s.state != mSpanInUse || s.spanclass.noscan() {
			rem := (end - addr)
			done += rem
			continue
		}

		// The swizzling function may have been called to complete a previously
		// partially executed swizzling. Compute the element index and the object
		// offset from which swizzling should be started.
		n := s.elemsize
		ei := (addr - s.base()) / s.elemsize // element index
		ob := s.base() + (ei * s.elemsize)   // object base address
		oo := (addr - ob)                    // offset within the object

		i := oo
		// j loop iterates over each element in the span and i loop iterates over
		// each 8-byte region in the object.
		for j := ei; j < s.nelems; j++ {
			addr = s.base() + (j * n) + (i)
			hbits := heapBitsForAddr(addr)
			is := i

			for ; i < n; i += 8 {
				if i != is {
					// skip advancing hbits and addr in the first iteration
					hbits = hbits.next()
					addr += 8
				}
				bits := hbits.bits()
				if i != 1*8 && bits&bitScan == 0 {
					break // no more pointers in this object
				}
				if bits&bitPointer == 0 {
					continue // not a pointer
				}

				au := (*uintptr)(unsafe.Pointer(addr))
				if *au == 0 {
					continue
				}
				newAddr := SwizzlePointer(*au)
				if newAddr == *au {
					// The swizzled address is the same as the current address.
					// Skip writing this.
					continue
				}

				pa.logEntry(unsafe.Pointer(&pa.bytesSwizzled))
				pa.logEntry(unsafe.Pointer(addr))

				pa.bytesSwizzled = (addr - start + 8)
				*au = newAddr

				// Commit persists the changes and then resets the log
				pa.commitLog()
			}
			i = 0
		}
		done = (end - start)
	}

	pa.bytesSwizzled = allocSize
	PersistRange(unsafe.Pointer(&pa.bytesSwizzled), intSize)
	dc <- true
}

// SwizzlePointer computes the swizzled pointer address corresponding to 'ptr'.
func SwizzlePointer(ptr uintptr) uintptr {
	// If findArenaIndex() returns -1, it indicates that 'ptr' is not a
	// valid persistent memory address. Hence return 0.
	ind := findArenaIndex(ptr, rangeTable)
	if ind == -1 {
		return 0
	}

	off := offsetTable[ind]
	return uintptr(int(ptr) + off)
}

// The following variables and functions are used for type profiling and
// type promotion to promote a type to be specially cached in mcache.

// TODO - to store profiling information about a type, we use arrays sized at
// 50000 entries. The index of a type in this array is computed as the
// difference between the type pointer and typeBase, divided by 32. This is a
// quick hack, and can be improved.

var (
	// A fixed base used to compute offset of the _type structure in the binary
	// RODATA section.
	typeBase uintptr

	// typeIndex() uses numTypToCheck to communicate with typeProfileThread()
	// about how many types should it profile.
	numTypToCheck uint64

	// numAssigned is used to track how many types have been specially cached
	// so far
	numAssigned int

	// Mapping between index assigned by typeIndex() and the type pointer
	typMap [100]*_type

	// Array used to capture number of persistent memory allocation of each
	// data type
	typProf [50000]uint64

	// prevAllocs is used for computing the allocation frequency of a type
	prevAllocs [50000]uint64

	// We support specially caching up to maxCacheTypes number of types. If a
	// type has been specially promoted, typAssigns maps that type to its index
	// within the mcache.
	typAssigns [50000]int

	// One heuristic used for type promotion is the total number of objects
	// allocated of that type. sizeclassMap helps figure out the target number
	// of allocations before a type will be considered to be
	sizeclassMap [50000]uint8
)

const (
	sleepInterval = 100000000 // 100 milliseconds
)

func init() {
	numAssigned = 1
	sections, _ := reflect_typelinks()
	typeBase = uintptr(unsafe.Pointer(sections[0]))
}

// This thread goes through the type profiling information at a fixed interval
// and decides when to promote a type to be specially cached. The heurisitic
// used for type promotion is the following - the number of allocations of such
// an object has exceeded the number of slots available in a span corresponding
// to this object sizeclass and its allocation frequency is greater than 100
// objects per second.
func typeProfileThread() {
	if numAssigned == maxCacheTypes-1 {
		// If we are coming from a restart path, all possible space may already
		// be used for caching types
		return
	}

	// TODO
	// (1) The heuristic used for promoting a type to be cached can be more
	// detailed such as computing moving average of allocation frequency, giving
	// more weight to recent allocations and less weight to past allocations.
	// (2) The allocation frequency used as a target for type promotion is 100
	// allocations per second. This is a simple metric and requires more fine
	// tuning.
	// (3) The threshold number of allocations required for a type to be
	// considered for special promotion is the number of slots in an mspan
	// corresponding to the sizeclass of that type. This again is a simple
	// metric and can be fine-tuned further.

	for {
		timeSleep(sleepInterval)
		for i := uint64(0); i < numTypToCheck; i++ {
			typ := typMap[i]
			if typ == nil {
				continue
			}
			off := (uintptr(unsafe.Pointer(typ)) - typeBase) / 32
			if typAssigns[off] == 0 {
				sz := sizeclassMap[off]
				threshAllocs := uint64(class_to_objects[sz])
				currAllocs := typProf[off]
				if currAllocs > threshAllocs {
					freq := (typProf[off] - prevAllocs[off])
					if freq > 100 {
						numAssigned++
						typAssigns[off] = numAssigned
						// store the mapping persistently
						pmemHeader.typeMap[numAssigned-2] = off
						PersistRange(unsafe.Pointer(&pmemHeader.typeMap[numAssigned-2]), intSize)
						typMap[i] = nil
						if numAssigned == maxCacheTypes-1 {
							// No more space to cache more type entries
							return
						}
					}
				}
				prevAllocs[off] = currAllocs
			}
		}
	}
}

// Given a type, typeIndex returns the index where that type is specially cached
// in the mcache. If the type is not yet specially cached, then this function
// increments the allocation count of this type.
func typeIndex(typ *_type, sizeclass uint8) int {
	// Slices are always cached at index 1
	if typ.kind&kindSlice == kindSlice {
		return 1
	}

	tu := uintptr(unsafe.Pointer(typ))
	offset := (tu - typeBase) / 32
	if offset >= 50000 || tu%32 != 0 {
		throw("Index overflow or type address not a multiple of 32")
	}

	if typAssigns[offset] != 0 {
		// This type has already been promoted to be specially cached, so just
		// return the index associated with the type.
		return typAssigns[offset]
	}

	// We would exhaust the heap before the counter wraps around
	count := atomic.Xadd64(&typProf[offset], 1)
	if count == 1 {
		sizeclassMap[offset] = sizeclass
		num := atomic.Xadd64(&numTypToCheck, 1)
		// The data race here is okay. typAllocThread() will check that the
		// type is non-nil before acting on it.
		typMap[num-1] = typ
	}
	return 0
}
