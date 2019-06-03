// +build !amd64

TEXT runtime·fallocate(SB), $0
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
