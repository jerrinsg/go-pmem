package cpuid

func cpuid_low(arg1, arg2 uint32) (eax, ebx, ecx, edx uint32) // implemented in cpuidlow_amd64.s
func xgetbv_low(arg1 uint32) (eax, edx uint32)                // implemented in cpuidlow_amd64.s
