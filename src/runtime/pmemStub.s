// +build !amd64

TEXT runtime·sfence(SB),$0
    // not implemented
	RET

TEXT runtime·clflush(SB), $0
    // not implemented
	RET

TEXT runtime·clwb(SB), $0
    // not implemented
	RET

TEXT runtime·clflushopt(SB), $0
    // not implemented
	RET

TEXT runtime·fallocate(SB), $0
    // not implemented
	RET

TEXT runtime·msync(SB),$0
    // not implemented
	RET

TEXT runtime·unlinkat(SB),$0
    // not implemented
	RET

TEXT runtime·ftruncate(SB),$0
    // not implemented
	RET

TEXT runtime·fstat(SB),$0
    // not implemented
	RET

TEXT runtime·readlink(SB),$0
	// not implemented
	RET
