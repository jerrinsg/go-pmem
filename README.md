## Introduction
`go-pmem` is a project that adds native persistent memory support to Go. This is
achieved through a combination of language extensions, enhancements to the Go
memory allocator and garbage collector, a growable heap design and runtime
support for pointer swizzling.

## Design
The design details of `go-pmem` can be found in the `design/` folder.

## How to Build
The persistent memory changes introduced in `go-pmem` is currently supported
only on Linux amd64. To compile the codebase:
```
$ cd src
$ ./all.bash
```
The compiled Go binary will be placed in the `bin/` directory. An example
application (`example.go`) written to use persistent memory features provided by
go-pmem can be found in the `design/` folder. To run this application, compile
it using the go-pmem binary as shown below:
```
$ cd design
$ ../bin/go build example.go
$ ./example
```
The official Go documentation on building the Go compiler can be found
[here](https://golang.org/doc/install/source).

## Related Projects
### go-pmem-transaction
This is a project that aims to make persistent memory more accessible to Go
programmers. It consists of two packages - `pmem` and `transaction`. `pmem`
package provides access to persistent memory data through named objects. The
`transaction` package provides an implementation of undo and redo logging for
crash-consistent updates to persistent memory data.
Project home page - https://github.com/vmware/go-pmem-transaction
### Go Redis
Go Redis is a Go implementation of Redis designed to run on persistent memory.
It is multi-threaded and implements a subset of the Redis commands. The
advantages of Go Redis are: much faster database load time on a server restart
due to its data being readily available in persistent memory and much higher
data throughput thanks to the multithreaded Go implementation.
Project home page - https://github.com/vmware-samples/go-redis-pmem
