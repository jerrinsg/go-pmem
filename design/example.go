package main

import (
	"flag"
	"github.com/vmware/go-pmem-transaction/pmem"
	"github.com/vmware/go-pmem-transaction/transaction"
)

type data struct {
	val1 int
	val2 int
	magic int
}

const (
	// A magic number used to identify if the root object initialization
	// completed successfully.
	magic   = 0x1B2E8BFF7BFBD154
)

var (
	pmemFile = flag.String("file", "testfile", "pmem file name")
)

func initialize(ptr *data) {
	txn("undo") {
		ptr.val1 = 25
		ptr.val2 = 35
		ptr.magic = magic
	}
	println("Data set as ", ptr.val1, " and ", ptr.val2)
}

func main() {
	var ptr *data
	flag.Parse()
	firstInit := pmem.Init(*pmemFile)
	if firstInit {
		// first time run of the application
		ptr = (*data)(pmem.New("root", ptr))
		initialize(ptr)
	} else {
		// not a first time initialization
		ptr = (*data)(pmem.Get("root", ptr))

		// even though this is not a first time initialization, we should still
		// check if the named object exists and data initialization completed
		// succesfully. The magic element within the named object helps check
		// for successful data initialization.

		if ptr == nil {
			ptr = (*data)(pmem.New("root", ptr))
		}

		if ptr.magic == magic {
			println("Read back data ", ptr.val1, " and ", ptr.val2)
		} else {
			initialize(ptr)
		}
	}
}
