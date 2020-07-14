package ssa

import (
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/src"
)

// This pass is run before writebarrier phase. This will look for stores within
// txn() block, and add a Log() call before this store to ensure crash consistency

var (
	TxLogFnOffset  int64
	TxLogFnStkSize int64
)

func needLog(v *Value) bool {
	if v.LogThisStore {
		v.LogThisStore = false // reset to avoid infinite loop
		if !IsStackAddr(v.Args[0]) {
			return true
		}
	}
	return false
}

// implementation inspired from ssa.writebarrier method
func logStore(f *Func) {
	if !Flag_txn || !f.HasTxn {
		return
	}
	var sb, sp *Value
	var after []*Value
	for _, v := range f.Entry.Values {
		if v.Op == OpSB {
			sb = v
		}
		if v.Op == OpSP {
			sp = v
		}
		if sb != nil && sp != nil {
			break
		}
	}
	if sb == nil {
		sb = f.Entry.NewValue0(f.Entry.Pos, OpSB, f.Config.Types.Uintptr)
	}
	if sp == nil {
		sp = f.Entry.NewValue0(f.Entry.Pos, OpSP, f.Config.Types.Uintptr)
	}

	blockDone := make(map[ID]bool)

loop:
	// allocate auxiliary date structures for computing store order
	// This needs to be re-done when f.NumValues() changes because we
	// inject new ssa nodes in the loop below when needLog(v) is true.
	sset := f.newSparseSet(f.NumValues())
	defer f.retSparseSet(sset)

	for _, b := range f.Blocks {
		if _, ok := blockDone[b.ID]; ok && blockDone[b.ID] {
			continue
		}
		blockDone[b.ID] = true
		storeNumber := make([]int32, f.NumValues())
		// First, order values in the current block w.r.t. stores.
		b.Values = storeOrder(b.Values, sset, storeNumber)
		for i, v := range b.Values {
			switch v.Op {
			case OpStore, OpZero, OpMove:
				if needLog(v) {
					txHandle := v.TxHandle
					v.TxHandle = nil // reset to nil as other ssa passes don't know about this
					after = append(after[:0], b.Values[i:]...)
					b.Values = b.Values[:i]
					ptr := v.Args[0] // memory location this Op changes

					// The memory before the store
					mem := v.MemoryArg()
					pos := v.Pos

					bThen := f.NewBlock(BlockPlain)
					bEnd := f.NewBlock(b.Kind)
					bThen.Pos = pos // TODO: (mohitv) verify pos settings
					bEnd.Pos = b.Pos
					b.Pos = pos

					// set up control flow for end block
					bEnd.SetControl(b.Control)
					bEnd.Likely = b.Likely
					for _, e := range b.Succs {
						bEnd.Succs = append(bEnd.Succs, e)
						e.b.Preds[e.i].b = bEnd
					}

					// set up control flow for inpmem test
					fn := f.fe.Syslook("inpmem")
					memIf, flag := rtcall(pos, fn, b, mem, sp, ptr, f.Config.Types.Bool) // inpmem call modifies memory
					b.Kind = BlockIf
					b.SetControl(flag)
					b.Likely = BranchUnlikely
					b.Succs = b.Succs[:0]
					b.AddEdgeTo(bThen)
					b.AddEdgeTo(bEnd)
					bThen.AddEdgeTo(bEnd)

					// prepare the then block.
					// emit tx.Log() call here
					memThen := txLogCall(pos, bThen, memIf, sb, sp, ptr, txHandle)

					// NO ELSE block

					// create a new phi node to merge memory
					m := bEnd.NewValue0(pos, OpPhi, types.TypeMem) // TODO: (mohitv) Not sure about pos
					m.Block = bEnd
					m.AddArg(memIf)
					m.AddArg(memThen)
					v.RemoveArg(2) // TODO: (mohitv) 2 is because OpStore has 3 args(0/1/2) , 2nd arg is memory arg
					v.AddArg(m)
					bEnd.Values = append(bEnd.Values, after...)
					for _, w := range after {
						w.Block = bEnd
					}
					goto loop
				}
			}
		}
	}
}

func txLogCall(pos src.XPos, b *Block, mem, sb, sp, ptr, txHandle *Value) *Value {

	numArgs := 1 // TODO: (mohitv) Hardcoded, we'll only call Log() with 1 arg
	f := b.Func

	widthPtr := int64(types.Widthptr)
	intfPtrType := types.Types[types.TINTER].PtrTo()
	slLength := f.ConstInt64(f.Config.Types.UInt64, int64(numArgs))
	byteptr := f.Config.Types.BytePtr
	arrType := types.NewArray(types.Types[types.TINTER], int64(numArgs))
	arrTypeAddr := f.Entry.NewValue1A(f.Entry.Pos, OpAddr, byteptr, arrType.Symbol(), sb)

	// allocate memory for the arg to Log() method. This arg is a slice of empty interfaces
	mem, retV := rtcall(pos, f.fe.Syslook("newobject"), b, mem, sp, arrTypeAddr, arrType.PtrTo())

	// store {ptrType, ptr} in the memory returned by runtime.newobject()
	// Below code assumes that empty interfaces are stored in memory in the following
	// format (see runtime/runtime2.go):
	// type eface struct {
	//	_type *_type
	//	data  unsafe.Pointer
	// }
	// If runtime's representation of empty interfaces (eface) change, this code also
	// needs to change
	ptrTypeAddr := f.Entry.NewValue1A(f.Entry.Pos, OpAddr, byteptr, ptr.Type.Symbol(), sb)
	p := b.NewValue1I(pos, OpOffPtr, intfPtrType, 0, retV)
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, f.Config.Types.Uintptr, p, ptrTypeAddr, mem)
	p = b.NewValue1I(pos, OpOffPtr, f.Config.Types.BytePtrPtr, widthPtr, retV)
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, byteptr, p, ptr, mem)

	// TxHandle set by gc/ssa.go should be of type unsafe.Pointer and infact points
	// to an interface stored in memory in the following format (see runtime/runtim2.go):
	// type iface struct {
	// tab  *itab
	// data unsafe.Pointer
	// }
	// If runtime's representation of interface (iface) changes, below code also
	// needs to change
	p = b.NewValue1I(pos, OpOffPtr, f.Config.Types.UintptrPtr, widthPtr, txHandle)
	txHandleData := b.NewValue2(pos, OpLoad, byteptr, p, mem)
	txHandle = b.NewValue2(pos, OpLoad, f.Config.Types.Uintptr, txHandle, mem)

	// Below calculation based on representation of itab in runtime (see runtime/runtime2.go) which is:
	// type itab struct {
	// inter *interfacetype
	// _type *_type
	// hash  uint32 // copy of _type.hash. Used for type switches.
	// _     [4]byte
	// fun   [1]uintptr // variable sized. fun[0]==0 means _type does not implement inter.
	// }
	// If runtime's representation of itab changes, below calculation needs to change
	fnOffset := TxLogFnOffset + 2*int64(widthPtr) + 8
	fn := b.NewValue1I(pos, OpOffPtr, f.Config.Types.UintptrPtr, fnOffset, txHandle)
	codeptr := b.NewValue2(pos, OpLoad, f.Config.Types.Uintptr, fn, mem)

	// Start storing args before making method call
	off := f.Config.ctxt.FixedFrameSize()
	// Store receiver
	addr := b.NewValue1I(pos, OpOffPtr, f.Config.Types.UintptrPtr, off, sp)
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, f.Config.Types.Uintptr, addr, txHandleData, mem)

	// Store args to the method. For Log() this arg is a slice
	// Slice is represented as
	// type sliceHeader struct {
	// data unsafe.Pointer
	// len int
	// cap int
	// }
	// Details of a slice have been abstracted out by prior phases. So we need to
	// store each content of the above structure separately. Note: If runtime
	// representation of slice changes, below code needs to change.

	// Store data
	off += f.Config.Types.Uintptr.Size()
	addr = b.NewValue1I(pos, OpOffPtr, intfPtrType, off, sp)
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, byteptr, addr, retV, mem)
	// Store slice length
	off += byteptr.Size()
	addr = b.NewValue1I(pos, OpOffPtr, f.Config.Types.IntPtr, off, sp)
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, f.Config.Types.Int, addr, slLength, mem)
	// Store slice capacity
	off += f.Config.Types.Int.Size()
	addr = b.NewValue1I(pos, OpOffPtr, f.Config.Types.IntPtr, off, sp)
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, f.Config.Types.Int, addr, slLength, mem)

	// Issue call to Log() method
	mem = b.NewValue2(pos, OpInterCall, types.TypeMem, codeptr, mem)

	// This depends on the signature of Log() method in the transaction interface
	// This is set by gc for simplicity
	mem.AuxInt = TxLogFnStkSize
	return mem
}

// Sets up call to a runtime function specified by fn that takes one arg & returns
// one value. Currently used to call runtime.inpmem(uintptr) & runtime.newobject(*type)
func rtcall(pos src.XPos, fn *obj.LSym, b *Block, mem, sp, fnArg *Value,
	resultT *types.Type) (*Value, *Value) {
	// TODO: (mohitv) See what they mean by IsVolatile when OpMove in writebarrier.go
	config := b.Func.Config
	// Write args to the stack
	off := config.ctxt.FixedFrameSize()

	off = round(off, fnArg.Type.Alignment())
	arg := b.NewValue1I(pos, OpOffPtr, fnArg.Type.PtrTo(), off, sp) // TODO: (mohitv) See the type here...inpmem(uintptr) is the sign of the fn
	mem = b.NewValue3A(pos, OpStore, types.TypeMem, fnArg.Type, arg, fnArg, mem)
	off += fnArg.Type.Size()

	// Issue call
	mem = b.NewValue1A(pos, OpStaticCall, types.TypeMem, fn, mem)

	// Load result
	off = round(off, resultT.Alignment())
	p := b.NewValue1I(pos, OpOffPtr, resultT.PtrTo(), off, sp)
	retV := b.NewValue2(pos, OpLoad, resultT, p, mem)
	off += resultT.Size()
	off = round(off, config.PtrSize)
	mem.AuxInt = off - config.ctxt.FixedFrameSize()
	return mem, retV
}
