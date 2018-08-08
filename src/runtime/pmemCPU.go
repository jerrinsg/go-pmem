package runtime

import (
	"runtime/internal/cpuid"
)

func isCPUGenuineIntel() bool {
	return cpuid.VendorIdentificatorString == "GenuineIntel"
}

func isCPUClwbPresent() bool {
	if !isCPUGenuineIntel() {
		return false
	}

	return cpuid.HasExtendedFeature(cpuid.CLWB)
}

func isCPUClfushoptPresent() bool {
	return cpuid.HasExtendedFeature(cpuid.CLFLUSHOPT)
}
