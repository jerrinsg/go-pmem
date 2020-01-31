// +build pmemTest

// This tests the garbage collector with respect to persistent memory. It is run
// only if a flag 'pmemTest' is specified:
// E.g.: ~/go-pmem/bin/go test -tags="pmemTest" -run TestPmem

package runtime_test

import (
	"log"
	"os"
	"runtime"
	"testing"
)

const (
	// TODO: pmemFile is currently placed in tmpfs
	pmemFile = "/dev/shm/___testfile"
)

func init() {
	os.Remove(pmemFile)
	_, err := runtime.PmemInit(pmemFile)
	if err != nil {
		log.Fatal("Pmem initialization failed")
	}
}

func TestPmemGcDeepNesting(t *testing.T) {
	type T [2][2][2][2][2][2][2][2][2][2]*int
	a := pnew(T)
	// Prevent the compiler from applying escape analysis.
	// This makes sure new(T) is allocated on heap, not on the stack.
	t.Logf("%p", a)

	a[0][0][0][0][0][0][0][0][0][0] = pnew(int)
	*a[0][0][0][0][0][0][0][0][0][0] = 13
	runtime.GC()
	if *a[0][0][0][0][0][0][0][0][0][0] != 13 {
		t.Fail()
	}
}

func TestPmemGcArraySlice(t *testing.T) {
	type X struct {
		buf     [1]byte
		nextbuf []byte
		next    *X
	}
	var head *X
	for i := 0; i < 10; i++ {
		p := pnew(X)
		p.buf[0] = 42
		p.next = head
		if head != nil {
			p.nextbuf = head.buf[:]
		}
		head = p
		runtime.GC()
	}
	for p := head; p != nil; p = p.next {
		if p.buf[0] != 42 {
			t.Fatal("corrupted heap")
		}
	}
}

func TestPmemGcRescan(t *testing.T) {
	type X struct {
		c     chan error
		nextx *X
	}
	type Y struct {
		X
		nexty *Y
		p     *int
	}
	var head *Y
	for i := 0; i < 10; i++ {
		p := pnew(Y)
		p.c = make(chan error)
		if head != nil {
			p.nextx = &head.X
		}
		p.nexty = head
		p.p = pnew(int)
		*p.p = 42
		head = p
		runtime.GC()
	}
	for p := head; p != nil; p = p.nexty {
		if *p.p != 42 {
			t.Fatal("corrupted heap")
		}
	}
}
