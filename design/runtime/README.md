## Introduction
This folder contains few design documents explaining the runtime changes added
for persistent memory support in Go.

Go-pmem is a project that adds native persistent memory support to the Go
programming language. Key contributions of this work are:
* Language extensions for persistent memory allocation
* Garbage collection support for persistent memory
* A heap recovery mechanism in case of an application crash/restart
* Runtime pointer swizzling support
* A dynamically sized persistent memory heap that can grow based on usage

## Language Extensions
We have added two new APIs to Go to support persistent memory allocations:
1. ```func pmake(t Type, size ...IntType) Type```

   The `pmake` API is used to create a slice in pmem. The semantics of `pmake` is
exactly the same as the `make` API in Go. Creating maps in persistent
memory is not yet supported.
2. ```func pnew(Type) *Type```

   Just like `new`, `pnew` creates a zero-value object of the Type argument in
persistent memory and returns a pointer to this object.

## Runtime Changes
The changes to the Go runtime are threefold: 
1. the memory allocator has been enhanced to support persistent memory allocations
2. the garbage collector now collects objects from both volatile and persistent memory heap
3. introduction of a heap recovery mechanism

One of the goal of this project was to keep the changes to the Go runtime to a
minimum. Rather than change all runtime code related to memory management and
garbage collection to be crash consistent, we store a minimal amount of
additional metadata to design a heap recovery mechanism.

### Memory Allocator
An additional data member `memtype` has been added to the `mspan` datastructure
to distinguish between persistent and volatile spans. The memory allocator
datastructures have been augmented to hold persistent memory and volatile memory
spans separately. `mcache` now has two `alloc` arrays - `alloc[0]` is the cache
of spans for volatile memory and `alloc[1]` is the cache of spans for persistent
memory. `mcentral` and `mheap` have also been similarly modified.

### Garbage Collector
There is very minimal changes to the implementation of the garbage collector.
The only change is to put back the spans it frees up in to the appropriate
memory allocator datastructures.

## Metada Logging
As mentioned, all runtime state is stored in volatile memory. To support heap
recovery, we store a minimal amount of additional metadata in the persistent
memory file. Two kinds of metadata are stored - (i) heap type bits and (ii) span
information

All metadata updates potentially happens on a memory allocation request and when
spans are freed.

### Heap Type Bits
The heap type bits are used by the garbage collector to identify what regions in
memory have pointers in them. Heap type bits are set by the allocator as and
when it allocates heap objects. Whenever heap type bits are set for persistent
memory objects, they are copied to the heap type bitmap in the persistent memory
arena header. Since the garbage collector looks only at the heap type
bits of live objects (the objects that it can reach), any crashes that happen
during logging of the heap type bits are benign. On an application restart, the
heap type bits are restored for persistent memory objects.

### Span Table
To support a heap recovery mechanism, it is necessary to log some of the state
of the persistent memory spans in use. The span table reserves 4 bytes for each
page in persistent memory to capture the state information of the span that
may exist with the corresponding persistent memory page as its start address.

The value logged is different for a small and large span. The logged value is
* small span -  ```((s.spc) << 1 | s.needzero)```
* large span - ```((66+s.npages-4) << 2 | s.spc << 1 | s.needzero)```

## Heap Recovery
The heap recovery essentially consists of three steps - recreate persistent
memory spans using the span table, copy the heap type bits corresponding to
these spans, and run a garbage collection cycle.

For each persistent memory span, we do not log which objects in the span are in
use. But as it turns out, this is not required! During heap recovery, we run a
full garbage collection cycle using the application root pointer. The garbage
collector follows the valid pointers in persistent memory, and marks which
objects are actually in-use.

## Runtime APIs
The runtime exposes a few APIs to help manage persistent memory.
1. ```func PmemInit(fname string) (unsafe.Pointer, error)```

   `PmemInit` is the API used to initialize persistent memory. It takes the path to
a file in persistent memory as input. It returns a pointer and an error. The
error indicates if persistent memory initialization was successful. The pointer
returned back is the application root pointer, if any was set previously using
the `SetRoot()` API.

2. ```func InPmem(addr uintptr) bool```

   `InPmem` returns whether addr is a persistent memory pointer or not.

3. ```func SetRoot(addr unsafe.Pointer) (err error)```

   `SetRoot()` is used to set the application root pointer. The application root
pointer is the pointer through which the application accesses all data in
persistent memory.

4. ```func GetRoot() unsafe.Pointer```

   `GetRoot()` is used to get back the application root pointer.

## Cache Flush APIs
Writes to a persistent memory file can be guaranteed to be persistent only
when the data is flushed from the processor's cache. To ensure writes are
persisted, the runtime offers a few cache flush APIs.

1. ```func PersistRange(addr unsafe.Pointer, len uintptr)```

   `PersistRange` flushes the CPU caches in the range `addr` to `addr+len`.
If processor support is available, this call results in optimized cache flush
instructions such as `clwb` and `clflushopt` being called. Since `clwb` and
`clflushopt` can result in caches being flushed in parallel, a memory fence
instruction is necessary at the end to ensure that writes are persisted
before the function returns. PersistRange takes care of executing the memory
fence instruction if necessary.
If the optimized cache instructions are not available, `PersistRange` calls the
`clflush` instruction to flush the cache.
If the persistent memory medium does not support direct-access (DAX), then
`PersistRange` calls `msync()` instead of CPU cache flush instructions.

2. ```func FlushRange(addr unsafe.Pointer, len uintptr)```

   `FlushRange` is similar to `PersistRange`. But it calls only the data flush
instructions but does not do a memory fence.

3. ```func Fence()```

   `Fence` is used to invoke a memory fence operation.

## Example Code

The following is an example code that uses the runtime APIs to store and
retrieve data from persistent memory.

```go
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

```
