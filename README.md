[![Actions Status](https://github.com/jerrinsg/go-pmem/workflows/CI/badge.svg)](https://github.com/jerrinsg/go-pmem/actions)

## Introduction
`go-pmem` is a project that adds native persistent memory support to Go. This is
achieved through a combination of language extensions, compiler instrumentation,
and runtime changes. Detailed design and implementation details of go-pmem
can be found in the [ATC 2020 paper](https://www.usenix.org/conference/atc20/presentation/george)
on go-pmem. We have also created a [website](https://vmware.github.io/persistent-memory-projects/)
which contains additional documentation and performance reports. `go-pmem` is
based on the 1.15 release version of Go.

## How to Build
The persistent memory changes introduced in `go-pmem` is currently supported
only on Linux amd64. To compile the codebase:
```
$ cd src
$ ./make.bash
```
The compiled Go binary will be placed in the `bin/` directory. An example
application (`example.go`) written to use persistent memory features provided by
go-pmem can be found in the `design/` folder. This application depends on the
[go-pmem-transaction](https://github.com/vmware/go-pmem-transaction) package and
can be compiled as follows:
```
$ GO111MODULE=off ../bin/go get -u github.com/vmware/go-pmem-transaction/...
$ cd design
$ GO111MODULE=off ../bin/go build -txn example.go
$ ./example
```
The official Go documentation on building the Go compiler can be found
[here](https://golang.org/doc/install/source).

## Related Projects
### go-pmem-transaction
`go-pmem-transaction` project provides two packages that go-pmem depends on.
The `pmem` package provides APIs to initialize persistent memory and access
persistent memory data through named objects. The `transaction` package provides
the transactional functionalities that go-pmem uses for enabling crash-consistent
persistent memory data updates.
Project home page - https://github.com/vmware/go-pmem-transaction
### Go Redis
Go Redis is a Go implementation of Redis designed to run on persistent memory.
It is multi-threaded and implements a subset of the Redis commands. The
advantages of Go Redis are: much faster database load time on a server restart
due to its data being readily available in persistent memory and much higher
data throughput thanks to the multithreaded Go implementation.
Project home page - https://github.com/vmware-samples/go-redis-pmem
