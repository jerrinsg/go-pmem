// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !plan9
// +build !solaris
// +build !windows
// +build !linux !amd64
// +build !linux !arm64
// +build !js
// +build !darwin
// +build !aix

package runtime

import "unsafe"

// mmap calls the mmap system call. It is implemented in assembly.
// The err result is an OS error code such as ENOMEM.
func mmap(addr unsafe.Pointer, n uintptr, prot, flags, fd int32, off uintptr) (p unsafe.Pointer, err int)

// munmap calls the munmap system call. It is implemented in assembly.
func munmap(addr unsafe.Pointer, n uintptr)
