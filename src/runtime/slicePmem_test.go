// +build pmemTest

package runtime_test

import (
	"testing"
)

func TestPmemSideEffectOrder(t *testing.T) {
	x = pmake([]int, 0, 10)
	x = append(x, 1, f())
	if x[0] != 1 || x[1] != 2 {
		t.Error("append failed: ", x[0], x[1])
	}
}
