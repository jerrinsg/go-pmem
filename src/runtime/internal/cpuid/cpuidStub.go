// +build !amd64

package cpuid

func cpuid_low(arg1, arg2 uint32) (eax, ebx, ecx, edx uint32) {
	// not implemented
	return
}

func xgetbv_low(arg1 uint32) (eax, edx uint32) {
	// not implemented
	return
}
