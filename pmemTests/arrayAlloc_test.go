// +build pmemTest

// This test is designed to test the optimizations added to speed up arrays
// allocated in persistent memory. It is run only if a flag 'pmemTest' is
// specified. This test need to be run two times to test the crash consistency
// aspects. E.g:
// ~/go-pmem/bin/go test -tags="pmemTest" -run TestPmem # run 1
// ~/go-pmem/bin/go test -tags="pmemTest" -run TestPmem # run 2

package main

import (
	"log"
	"runtime"
	"testing"
	"unsafe"
)

const (
	intSize   = 8       // Size of an int variable
	padSize   = 1048576 // 1MB
	maxLevel  = 4
	numChilds = 8
	dataFile  = "/dev/shm/datafile"
)

type ptrObj struct {
	ptr    *dataObj
	unused int
}

type dataObj struct {
	level    int
	dataPtrs []ptrObj
	padding  [padSize]byte
}

// Recursively populate the data objects
func (dPtr *dataObj) populateDataObjects(level int) {
	dPtr.level = level
	if level <= maxLevel {
		dPtr.dataPtrs = pmake([]ptrObj, numChilds)
		for i := 0; i < numChilds; i++ {
			newObj := pnew(dataObj)
			newObj.populateDataObjects(level + 1)
			dPtr.dataPtrs[i].ptr = newObj
		}
	}
}

var numChecked int

func (dPtr *dataObj) recursiveCheck(level int) bool {
	numChecked++
	if dPtr.level != level {
		return false
	}
	if level <= maxLevel {
		for i := 0; i < numChilds; i++ {
			child := dPtr.dataPtrs[i].ptr
			if child.recursiveCheck(level+1) == false {
				return false
			}
		}
	}
	return true
}

func TestPmemArrayAlloc(t *testing.T) {
	rootPtr, err := runtime.PmemInit(dataFile)
	if err != nil {
		log.Fatal("Pmem initialization failed with error ", err)
	}
	if rootPtr == nil {
		dPtr := pnew(dataObj)
		dPtr.populateDataObjects(1)
		runtime.SetRoot(unsafe.Pointer(dPtr))
	} else {
		// Run a full GC cycle
		runtime.GC()
		dPtr := (*dataObj)(rootPtr)
		if dPtr.recursiveCheck(1) == false {
			t.Fatal("Recursive check failed")
		}
	}
}
