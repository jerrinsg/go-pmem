package runtime

import (
	"runtime/internal/sys"
	"unsafe"
)

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
// for an arena of size 'size'. The metadata occupies pArenaHeaderSize bytes to
// store the arena header and a variable number of bytes to store the heap type
// bitmap and the span bitmap. See pArena struct.
func metadataSize(size uintptr) uintptr {
	if size%pageSize != 0 {
		throw("size has to a multiple of page size")
	}
	// Size required for the heap type bitmap
	heapBitmapSize := size / bytesPerBitmapByte

	// Size required for the span bitmap
	spanBitmapSize := (size / pageSize) * spanBytesPerPage

	return uintptr(pArenaHeaderSize + heapBitmapSize + spanBitmapSize)
}

// Given a persistent memory arena of total 'size' bytes, this function computes
// how the arena should be divided into two regions - the metadata region (X bytes)
// and the actual allocator usable region (Y bytes). The allocator usable region
// has to be page aligned.
// size = X + Y
// Arena 'offset' indicates if any space in the beginning of the arena has to be
// reserved and hence unavailable to be used as metadata or data region.
// The following methodology is used for this computation.
// S' = size-offset
// The maximal possible allocator space is computed as:
// Y + metadataSize(Y) = S'
// The metadata size computed from this equation is then rounded up to a multiple
// of page size to find the required sizes.
//
// TODO: this calculations in this function needs to be further improved
func (p *pArena) layout() (uintptr, uintptr) {
	// ps := pageSize / spanBytesPerPage
	// Y + metadataSize(Y) = S'
	// Y + (pArenaHeaderSize + Y/bytesPerBitmapByte + Y/ps) = S'
	// Y = (ps * (S' - pArenaHeaderSize)) / ((ps/bytesPerBitmapByte) + 1 + ps)
	var off uintptr
	if p.fileOffset == 0 {
		off = pmemHeaderSize
	}
	ps := uintptr(pageSize / spanBytesPerPage)
	availSize := p.size - off
	Y := (ps * (availSize - pArenaHeaderSize)) / ((ps / bytesPerBitmapByte) + 1 + ps)
	rem := p.size - Y
	remRound := round(rem, pageSize)
	usable := p.size - remRound
	return remRound, usable
}

// This function goes through the persistent memory file, and ensure that its
// metadata is consistent. This involves ensuring the file was not externally
// truncated. Also, it ensures that the header magic in each of the arena
// metadata section is correct.
func verifyMetadata() error {
	// If the file size is less than the mapped size, then it was externally truncated
	mappedSize := pmemHeader.mappedSize
	fsize := getFileSize(pmemInfo.fname)
	if fsize < 0 {
		return error(errorString("Get file size failed"))
	}
	if fsize < int(mappedSize) {
		return error(errorString("File was externally truncated"))
	}

	if mappedSize == pmemHeaderSize {
		// The persistent memory file only contains the header region, and does
		// not contain any arenas.
		return nil
	}

	// Map the first page of each arena and check that the arena header magic
	// is correct. Also ensure that the global mapped size equals the sum of the
	// size of each arena.
	totalArenaSize := uintptr(0)
	for totalArenaSize < mappedSize {
		arenaOff := uintptr(0)
		mapAddr, isPmem, err := mapFile(pmemInfo.fname, pageSize, fileCreate,
			_DEFAULT_FMODE, totalArenaSize, nil)
		if err != 0 {
			return error(errorString("Arena map failed"))
		}
		if totalArenaSize == 0 {
			// Add the global header size to get to the first arena's metadata
			arenaOff = pmemHeaderSize
		}

		parena := (*pArena)(unsafe.Pointer(uintptr(mapAddr) + arenaOff))
		if parena.magic != hdrMagic || isPmem != pmemInfo.isPmem {
			munmap(mapAddr, pageSize)
			return error(errorString("Arena metadata mismatch"))
		}
		totalArenaSize += parena.size
		munmap(mapAddr, pageSize)
	}

	if totalArenaSize != mappedSize {
		return error(errorString("Arena size mismatch"))
	}

	return nil
}
