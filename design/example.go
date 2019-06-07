package main

import (
	"log"
	"runtime"
	"unsafe"
)

type data struct {
	val1 int
	val2 int
}

var ptr *data

func main() {
	root, err := runtime.PmemInit("file.data")
	if err != nil {
		log.Fatalf("Persistent memory initialization failed with error ", err)
	}

	if root == nil {
		// first time run of the application
		ptr = pnew(data)
		ptr.val1 = 25
		ptr.val2 = 35
		runtime.PersistRange(unsafe.Pointer(ptr), unsafe.Sizeof(data{}))
		runtime.SetRoot(unsafe.Pointer(ptr))
		println("Data set as ", ptr.val1, " and ", ptr.val2)
	} else {
		// not a first time run of the application
		ptr = (*data)(root)
		println("Read back data ", ptr.val1, " and ", ptr.val2)
	}
}
