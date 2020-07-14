package ssa

// This pass is run before writebarrier phase. This will look for stores within
// txn() block, and add a Log() call before this store to ensure crash consistency
func logStore(f *Func) {
	if !Flag_txn || !f.HasTxn {
		return
	}
}
