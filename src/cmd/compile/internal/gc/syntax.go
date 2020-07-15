// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// “Abstract” syntax representation.

package gc

import (
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/syntax"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
	"cmd/internal/src"
)

// A Node is a single node in the syntax tree.
// Actually the syntax tree is a syntax DAG, because there is only one
// node with Op=ONAME for a given instance of a variable x.
// The same is true for Op=OTYPE and Op=OLITERAL. See Node.mayBeShared.
type Node struct {
	// Tree structure.
	// Generic recursive walks should follow these fields.
	Left  *Node
	Right *Node
	Ninit Nodes
	Nbody Nodes
	List  Nodes
	Rlist Nodes

	// most nodes
	Type *types.Type
	Orig *Node // original form, for printing, and tracking copies of ONAMEs

	// func
	Func *Func

	// ONAME, OTYPE, OPACK, OLABEL, some OLITERAL
	Name *Name

	Sym *types.Sym  // various
	E   interface{} // Opt or Val, see methods below

	// Various. Usually an offset into a struct. For example:
	// - ONAME nodes that refer to local variables use it to identify their stack frame position.
	// - ODOT, ODOTPTR, and OINDREGSP use it to indicate offset relative to their base address.
	// - OSTRUCTKEY uses it to store the named field's offset.
	// - Named OLITERALs use it to store their ambient iota value.
	// Possibly still more uses. If you find any, document them.
	Xoffset int64

	Pos src.XPos

	flags bitset32

	Esc uint16 // EscXXX

	Op  Op
	aux uint8
}

func (n *Node) ResetAux() {
	n.aux = 0
}

func (n *Node) SubOp() Op {
	switch n.Op {
	case OASOP, ONAME:
	default:
		Fatalf("unexpected op: %v", n.Op)
	}
	return Op(n.aux)
}

func (n *Node) SetSubOp(op Op) {
	switch n.Op {
	case OASOP, ONAME:
	default:
		Fatalf("unexpected op: %v", n.Op)
	}
	n.aux = uint8(op)
}

func (n *Node) IndexMapLValue() bool {
	if n.Op != OINDEXMAP {
		Fatalf("unexpected op: %v", n.Op)
	}
	return n.aux != 0
}

func (n *Node) SetIndexMapLValue(b bool) {
	if n.Op != OINDEXMAP {
		Fatalf("unexpected op: %v", n.Op)
	}
	if b {
		n.aux = 1
	} else {
		n.aux = 0
	}
}

func (n *Node) TChanDir() types.ChanDir {
	if n.Op != OTCHAN {
		Fatalf("unexpected op: %v", n.Op)
	}
	return types.ChanDir(n.aux)
}

func (n *Node) SetTChanDir(dir types.ChanDir) {
	if n.Op != OTCHAN {
		Fatalf("unexpected op: %v", n.Op)
	}
	n.aux = uint8(dir)
}

func (n *Node) IsSynthetic() bool {
	name := n.Sym.Name
	return name[0] == '.' || name[0] == '~'
}

// IsAutoTmp indicates if n was created by the compiler as a temporary,
// based on the setting of the .AutoTemp flag in n's Name.
func (n *Node) IsAutoTmp() bool {
	if n == nil || n.Op != ONAME {
		return false
	}
	return n.Name.AutoTemp()
}

const (
	nodeClass, _     = iota, 1 << iota // PPARAM, PAUTO, PEXTERN, etc; three bits; first in the list because frequently accessed
	_, _                               // second nodeClass bit
	_, _                               // third nodeClass bit
	nodeWalkdef, _                     // tracks state during typecheckdef; 2 == loop detected; two bits
	_, _                               // second nodeWalkdef bit
	nodeTypecheck, _                   // tracks state during typechecking; 2 == loop detected; two bits
	_, _                               // second nodeTypecheck bit
	nodeInitorder, _                   // tracks state during init1; two bits
	_, _                               // second nodeInitorder bit
	_, nodeHasBreak
	_, nodeIsClosureVar
	_, nodeIsOutputParamHeapAddr
	_, nodeNoInline  // used internally by inliner to indicate that a function call should not be inlined; set for OCALLFUNC and OCALLMETH only
	_, nodeAssigned  // is the variable ever assigned to
	_, nodeAddrtaken // address taken, even if not moved to heap
	_, nodeImplicit
	_, nodeIsddd     // is the argument variadic
	_, nodeDiag      // already printed error about this
	_, nodeColas     // OAS resulting from :=
	_, nodeNonNil    // guaranteed to be non-nil
	_, nodeNoescape  // func arguments do not escape; TODO(rsc): move Noescape to Func struct (see CL 7360)
	_, nodeBounded   // bounds check unnecessary
	_, nodeAddable   // addressable
	_, nodeHasCall   // expression contains a function call
	_, nodeLikely    // if statement condition likely
	_, nodeHasVal    // node.E contains a Val
	_, nodeHasOpt    // node.E contains an Opt
	_, nodeEmbedded  // ODCLFIELD embedded type
	_, nodeInlFormal // OPAUTO created by inliner, derived from callee formal
	_, nodeInlLocal  // OPAUTO created by inliner, derived from callee local
	nodeTxClass, _   // tracks type of node when txn flag is used for compiling.
	_, _             // 0 -> nothing,
	// Before buildSSA, 1 -> node has been injected by syntax/parser.go.
	// During buildSSA, 1-> node is lhs of an assignment op within txn() block
	// 2 -> node reads a value within txn() block. Useful if tracking reads inside txn() block
	// 3 -> node known to be in pmem at compile time (created using pnew & escape analysis didn't put on stack)
)

func (n *Node) Class() Class     { return Class(n.flags.get3(nodeClass)) }
func (n *Node) Walkdef() uint8   { return n.flags.get2(nodeWalkdef) }
func (n *Node) Typecheck() uint8 { return n.flags.get2(nodeTypecheck) }
func (n *Node) Initorder() uint8 { return n.flags.get2(nodeInitorder) }

func (n *Node) HasBreak() bool              { return n.flags&nodeHasBreak != 0 }
func (n *Node) IsClosureVar() bool          { return n.flags&nodeIsClosureVar != 0 }
func (n *Node) NoInline() bool              { return n.flags&nodeNoInline != 0 }
func (n *Node) IsOutputParamHeapAddr() bool { return n.flags&nodeIsOutputParamHeapAddr != 0 }
func (n *Node) Assigned() bool              { return n.flags&nodeAssigned != 0 }
func (n *Node) Addrtaken() bool             { return n.flags&nodeAddrtaken != 0 }
func (n *Node) Implicit() bool              { return n.flags&nodeImplicit != 0 }
func (n *Node) Isddd() bool                 { return n.flags&nodeIsddd != 0 }
func (n *Node) Diag() bool                  { return n.flags&nodeDiag != 0 }
func (n *Node) Colas() bool                 { return n.flags&nodeColas != 0 }
func (n *Node) NonNil() bool                { return n.flags&nodeNonNil != 0 }
func (n *Node) Noescape() bool              { return n.flags&nodeNoescape != 0 }
func (n *Node) Bounded() bool               { return n.flags&nodeBounded != 0 }
func (n *Node) Addable() bool               { return n.flags&nodeAddable != 0 }
func (n *Node) HasCall() bool               { return n.flags&nodeHasCall != 0 }
func (n *Node) Likely() bool                { return n.flags&nodeLikely != 0 }
func (n *Node) HasVal() bool                { return n.flags&nodeHasVal != 0 }
func (n *Node) HasOpt() bool                { return n.flags&nodeHasOpt != 0 }
func (n *Node) Embedded() bool              { return n.flags&nodeEmbedded != 0 }
func (n *Node) InlFormal() bool             { return n.flags&nodeInlFormal != 0 }
func (n *Node) InlLocal() bool              { return n.flags&nodeInlLocal != 0 }
func (n *Node) TxClass() uint8              { return n.flags.get2(nodeTxClass) }

func (n *Node) SetClass(b Class)     { n.flags.set3(nodeClass, uint8(b)) }
func (n *Node) SetWalkdef(b uint8)   { n.flags.set2(nodeWalkdef, b) }
func (n *Node) SetTypecheck(b uint8) { n.flags.set2(nodeTypecheck, b) }
func (n *Node) SetInitorder(b uint8) { n.flags.set2(nodeInitorder, b) }

func (n *Node) SetHasBreak(b bool)              { n.flags.set(nodeHasBreak, b) }
func (n *Node) SetIsClosureVar(b bool)          { n.flags.set(nodeIsClosureVar, b) }
func (n *Node) SetNoInline(b bool)              { n.flags.set(nodeNoInline, b) }
func (n *Node) SetIsOutputParamHeapAddr(b bool) { n.flags.set(nodeIsOutputParamHeapAddr, b) }
func (n *Node) SetAssigned(b bool)              { n.flags.set(nodeAssigned, b) }
func (n *Node) SetAddrtaken(b bool)             { n.flags.set(nodeAddrtaken, b) }
func (n *Node) SetImplicit(b bool)              { n.flags.set(nodeImplicit, b) }
func (n *Node) SetIsddd(b bool)                 { n.flags.set(nodeIsddd, b) }
func (n *Node) SetDiag(b bool)                  { n.flags.set(nodeDiag, b) }
func (n *Node) SetColas(b bool)                 { n.flags.set(nodeColas, b) }
func (n *Node) SetNonNil(b bool)                { n.flags.set(nodeNonNil, b) }
func (n *Node) SetNoescape(b bool)              { n.flags.set(nodeNoescape, b) }
func (n *Node) SetBounded(b bool)               { n.flags.set(nodeBounded, b) }
func (n *Node) SetAddable(b bool)               { n.flags.set(nodeAddable, b) }
func (n *Node) SetHasCall(b bool)               { n.flags.set(nodeHasCall, b) }
func (n *Node) SetLikely(b bool)                { n.flags.set(nodeLikely, b) }
func (n *Node) SetHasVal(b bool)                { n.flags.set(nodeHasVal, b) }
func (n *Node) SetHasOpt(b bool)                { n.flags.set(nodeHasOpt, b) }
func (n *Node) SetEmbedded(b bool)              { n.flags.set(nodeEmbedded, b) }
func (n *Node) SetInlFormal(b bool)             { n.flags.set(nodeInlFormal, b) }
func (n *Node) SetInlLocal(b bool)              { n.flags.set(nodeInlLocal, b) }
func (n *Node) SetTxClass(b uint8)              { n.flags.set2(nodeTxClass, b) }

// Val returns the Val for the node.
func (n *Node) Val() Val {
	if !n.HasVal() {
		return Val{}
	}
	return Val{n.E}
}

// SetVal sets the Val for the node, which must not have been used with SetOpt.
func (n *Node) SetVal(v Val) {
	if n.HasOpt() {
		Debug['h'] = 1
		Dump("have Opt", n)
		Fatalf("have Opt")
	}
	n.SetHasVal(true)
	n.E = v.U
}

// Opt returns the optimizer data for the node.
func (n *Node) Opt() interface{} {
	if !n.HasOpt() {
		return nil
	}
	return n.E
}

// SetOpt sets the optimizer data for the node, which must not have been used with SetVal.
// SetOpt(nil) is ignored for Vals to simplify call sites that are clearing Opts.
func (n *Node) SetOpt(x interface{}) {
	if x == nil && n.HasVal() {
		return
	}
	if n.HasVal() {
		Debug['h'] = 1
		Dump("have Val", n)
		Fatalf("have Val")
	}
	n.SetHasOpt(true)
	n.E = x
}

func (n *Node) Iota() int64 {
	return n.Xoffset
}

func (n *Node) SetIota(x int64) {
	n.Xoffset = x
}

// mayBeShared reports whether n may occur in multiple places in the AST.
// Extra care must be taken when mutating such a node.
func (n *Node) mayBeShared() bool {
	switch n.Op {
	case ONAME, OLITERAL, OTYPE:
		return true
	}
	return false
}

// isMethodExpression reports whether n represents a method expression T.M.
func (n *Node) isMethodExpression() bool {
	return n.Op == ONAME && n.Left != nil && n.Left.Op == OTYPE && n.Right != nil && n.Right.Op == ONAME
}

// funcname returns the name of the function n.
func (n *Node) funcname() string {
	if n == nil || n.Func == nil || n.Func.Nname == nil {
		return "<nil>"
	}
	return n.Func.Nname.Sym.Name
}

// Name holds Node fields used only by named nodes (ONAME, OTYPE, OPACK, OLABEL, some OLITERAL).
type Name struct {
	Pack      *Node      // real package for import . names
	Pkg       *types.Pkg // pkg for OPACK nodes
	Defn      *Node      // initializing assignment
	Curfn     *Node      // function for local variables
	Param     *Param     // additional fields for ONAME, OTYPE
	Decldepth int32      // declaration loop depth, increased for every loop or label
	Vargen    int32      // unique name for ONAME within a function.  Function outputs are numbered starting at one.
	flags     bitset8
}

const (
	nameCaptured = 1 << iota // is the variable captured by a closure
	nameReadonly
	nameByval     // is the variable captured by value or by reference
	nameNeedzero  // if it contains pointers, needs to be zeroed on function entry
	nameKeepalive // mark value live across unknown assembly call
	nameAutoTemp  // is the variable a temporary (implies no dwarf info. reset if escapes to heap)
	nameUsed      // for variable declared and not used error
)

func (n *Name) Captured() bool  { return n.flags&nameCaptured != 0 }
func (n *Name) Readonly() bool  { return n.flags&nameReadonly != 0 }
func (n *Name) Byval() bool     { return n.flags&nameByval != 0 }
func (n *Name) Needzero() bool  { return n.flags&nameNeedzero != 0 }
func (n *Name) Keepalive() bool { return n.flags&nameKeepalive != 0 }
func (n *Name) AutoTemp() bool  { return n.flags&nameAutoTemp != 0 }
func (n *Name) Used() bool      { return n.flags&nameUsed != 0 }

func (n *Name) SetCaptured(b bool)  { n.flags.set(nameCaptured, b) }
func (n *Name) SetReadonly(b bool)  { n.flags.set(nameReadonly, b) }
func (n *Name) SetByval(b bool)     { n.flags.set(nameByval, b) }
func (n *Name) SetNeedzero(b bool)  { n.flags.set(nameNeedzero, b) }
func (n *Name) SetKeepalive(b bool) { n.flags.set(nameKeepalive, b) }
func (n *Name) SetAutoTemp(b bool)  { n.flags.set(nameAutoTemp, b) }
func (n *Name) SetUsed(b bool)      { n.flags.set(nameUsed, b) }

type Param struct {
	Ntype    *Node
	Heapaddr *Node // temp holding heap address of param

	// ONAME PAUTOHEAP
	Stackcopy *Node // the PPARAM/PPARAMOUT on-stack slot (moved func params only)

	// ONAME closure linkage
	// Consider:
	//
	//	func f() {
	//		x := 1 // x1
	//		func() {
	//			use(x) // x2
	//			func() {
	//				use(x) // x3
	//				--- parser is here ---
	//			}()
	//		}()
	//	}
	//
	// There is an original declaration of x and then a chain of mentions of x
	// leading into the current function. Each time x is mentioned in a new closure,
	// we create a variable representing x for use in that specific closure,
	// since the way you get to x is different in each closure.
	//
	// Let's number the specific variables as shown in the code:
	// x1 is the original x, x2 is when mentioned in the closure,
	// and x3 is when mentioned in the closure in the closure.
	//
	// We keep these linked (assume N > 1):
	//
	//   - x1.Defn = original declaration statement for x (like most variables)
	//   - x1.Innermost = current innermost closure x (in this case x3), or nil for none
	//   - x1.IsClosureVar() = false
	//
	//   - xN.Defn = x1, N > 1
	//   - xN.IsClosureVar() = true, N > 1
	//   - x2.Outer = nil
	//   - xN.Outer = x(N-1), N > 2
	//
	//
	// When we look up x in the symbol table, we always get x1.
	// Then we can use x1.Innermost (if not nil) to get the x
	// for the innermost known closure function,
	// but the first reference in a closure will find either no x1.Innermost
	// or an x1.Innermost with .Funcdepth < Funcdepth.
	// In that case, a new xN must be created, linked in with:
	//
	//     xN.Defn = x1
	//     xN.Outer = x1.Innermost
	//     x1.Innermost = xN
	//
	// When we finish the function, we'll process its closure variables
	// and find xN and pop it off the list using:
	//
	//     x1 := xN.Defn
	//     x1.Innermost = xN.Outer
	//
	// We leave xN.Innermost set so that we can still get to the original
	// variable quickly. Not shown here, but once we're
	// done parsing a function and no longer need xN.Outer for the
	// lexical x reference links as described above, closurebody
	// recomputes xN.Outer as the semantic x reference link tree,
	// even filling in x in intermediate closures that might not
	// have mentioned it along the way to inner closures that did.
	// See closurebody for details.
	//
	// During the eventual compilation, then, for closure variables we have:
	//
	//     xN.Defn = original variable
	//     xN.Outer = variable captured in next outward scope
	//                to make closure where xN appears
	//
	// Because of the sharding of pieces of the node, x.Defn means x.Name.Defn
	// and x.Innermost/Outer means x.Name.Param.Innermost/Outer.
	Innermost *Node
	Outer     *Node

	// OTYPE
	//
	// TODO: Should Func pragmas also be stored on the Name?
	Pragma syntax.Pragma
	Alias  bool // node is alias for Ntype (only used when type-checking ODCLTYPE)
}

// Functions
//
// A simple function declaration is represented as an ODCLFUNC node f
// and an ONAME node n. They're linked to one another through
// f.Func.Nname == n and n.Name.Defn == f. When functions are
// referenced by name in an expression, the function's ONAME node is
// used directly.
//
// Function names have n.Class() == PFUNC. This distinguishes them
// from variables of function type.
//
// Confusingly, n.Func and f.Func both exist, but commonly point to
// different Funcs. (Exception: an OCALLPART's Func does point to its
// ODCLFUNC's Func.)
//
// A method declaration is represented like functions, except n.Sym
// will be the qualified method name (e.g., "T.m") and
// f.Func.Shortname is the bare method name (e.g., "m").
//
// Method expressions are represented as ONAME/PFUNC nodes like
// function names, but their Left and Right fields still point to the
// type and method, respectively. They can be distinguished from
// normal functions with isMethodExpression. Also, unlike function
// name nodes, method expression nodes exist for each method
// expression. The declaration ONAME can be accessed with
// x.Type.Nname(), where x is the method expression ONAME node.
//
// Method values are represented by ODOTMETH/ODOTINTER when called
// immediately, and OCALLPART otherwise. They are like method
// expressions, except that for ODOTMETH/ODOTINTER the method name is
// stored in Sym instead of Right.
//
// Closures are represented by OCLOSURE node c. They link back and
// forth with the ODCLFUNC via Func.Closure; that is, c.Func.Closure
// == f and f.Func.Closure == c.
//
// Function bodies are stored in f.Nbody, and inline function bodies
// are stored in n.Func.Inl. Pragmas are stored in f.Func.Pragma.
//
// Imported functions skip the ODCLFUNC, so n.Name.Defn is nil. They
// also use Dcl instead of Inldcl.

// Func holds Node fields used only with function-like nodes.
type Func struct {
	Shortname *types.Sym
	Enter     Nodes // for example, allocate and initialize memory for escaping parameters
	Exit      Nodes
	Cvars     Nodes   // closure params
	Dcl       []*Node // autodcl for this func/closure

	// Parents records the parent scope of each scope within a
	// function. The root scope (0) has no parent, so the i'th
	// scope's parent is stored at Parents[i-1].
	Parents []ScopeID

	// Marks records scope boundary changes.
	Marks []Mark

	// Closgen tracks how many closures have been generated within
	// this function. Used by closurename for creating unique
	// function names.
	Closgen int

	FieldTrack map[*types.Sym]struct{}
	DebugInfo  *ssa.FuncDebug
	Ntype      *Node // signature
	Top        int   // top context (Ecall, Eproc, etc)
	Closure    *Node // OCLOSURE <-> ODCLFUNC
	Nname      *Node
	lsym       *obj.LSym

	Inl *Inline

	Label int32 // largest auto-generated label in this function

	Endlineno src.XPos
	WBPos     src.XPos // position of first write barrier; see SetWBPos

	Pragma syntax.Pragma // go:xxx function annotations

	flags bitset16

	// nwbrCalls records the LSyms of functions called by this
	// function for go:nowritebarrierrec analysis. Only filled in
	// if nowritebarrierrecCheck != nil.
	nwbrCalls *[]nowritebarrierrecCallSym
}

// An Inline holds fields used for function bodies that can be inlined.
type Inline struct {
	Cost int32 // heuristic cost of inlining this function

	// Copies of Func.Dcl and Nbody for use during inlining.
	Dcl  []*Node
	Body []*Node
}

// A Mark represents a scope boundary.
type Mark struct {
	// Pos is the position of the token that marks the scope
	// change.
	Pos src.XPos

	// Scope identifies the innermost scope to the right of Pos.
	Scope ScopeID
}

// A ScopeID represents a lexical scope within a function.
type ScopeID int32

const (
	funcDupok         = 1 << iota // duplicate definitions ok
	funcWrapper                   // is method wrapper
	funcNeedctxt                  // function uses context register (has closure variables)
	funcReflectMethod             // function calls reflect.Type.Method or MethodByName
	funcIsHiddenClosure
	funcHasDefer            // contains a defer statement
	funcNilCheckDisabled    // disable nil checks when compiling this function
	funcInlinabilityChecked // inliner has already determined whether the function is inlinable
	funcExportInline        // include inline body in export data
	funcInstrumentBody      // add race/msan instrumentation during SSA construction
)

func (f *Func) Dupok() bool               { return f.flags&funcDupok != 0 }
func (f *Func) Wrapper() bool             { return f.flags&funcWrapper != 0 }
func (f *Func) Needctxt() bool            { return f.flags&funcNeedctxt != 0 }
func (f *Func) ReflectMethod() bool       { return f.flags&funcReflectMethod != 0 }
func (f *Func) IsHiddenClosure() bool     { return f.flags&funcIsHiddenClosure != 0 }
func (f *Func) HasDefer() bool            { return f.flags&funcHasDefer != 0 }
func (f *Func) NilCheckDisabled() bool    { return f.flags&funcNilCheckDisabled != 0 }
func (f *Func) InlinabilityChecked() bool { return f.flags&funcInlinabilityChecked != 0 }
func (f *Func) ExportInline() bool        { return f.flags&funcExportInline != 0 }
func (f *Func) InstrumentBody() bool      { return f.flags&funcInstrumentBody != 0 }

func (f *Func) SetDupok(b bool)               { f.flags.set(funcDupok, b) }
func (f *Func) SetWrapper(b bool)             { f.flags.set(funcWrapper, b) }
func (f *Func) SetNeedctxt(b bool)            { f.flags.set(funcNeedctxt, b) }
func (f *Func) SetReflectMethod(b bool)       { f.flags.set(funcReflectMethod, b) }
func (f *Func) SetIsHiddenClosure(b bool)     { f.flags.set(funcIsHiddenClosure, b) }
func (f *Func) SetHasDefer(b bool)            { f.flags.set(funcHasDefer, b) }
func (f *Func) SetNilCheckDisabled(b bool)    { f.flags.set(funcNilCheckDisabled, b) }
func (f *Func) SetInlinabilityChecked(b bool) { f.flags.set(funcInlinabilityChecked, b) }
func (f *Func) SetExportInline(b bool)        { f.flags.set(funcExportInline, b) }
func (f *Func) SetInstrumentBody(b bool)      { f.flags.set(funcInstrumentBody, b) }

func (f *Func) setWBPos(pos src.XPos) {
	if Debug_wb != 0 {
		Warnl(pos, "write barrier")
	}
	if !f.WBPos.IsKnown() {
		f.WBPos = pos
	}
}

//go:generate stringer -type=Op -trimprefix=O

type Op uint8

// Node ops.
const (
	OXXX Op = iota

	// names
	ONAME    // var or func name
	ONONAME  // unnamed arg or return value: f(int, string) (int, error) { etc }
	OTYPE    // type name
	OPACK    // import
	OLITERAL // literal

	// expressions
	OADD             // Left + Right
	OSUB             // Left - Right
	OOR              // Left | Right
	OXOR             // Left ^ Right
	OADDSTR          // +{List} (string addition, list elements are strings)
	OADDR            // &Left
	OANDAND          // Left && Right
	OAPPEND          // append(List); after walk, Left may contain elem type descriptor
	OARRAYBYTESTR    // Type(Left) (Type is string, Left is a []byte)
	OARRAYBYTESTRTMP // Type(Left) (Type is string, Left is a []byte, ephemeral)
	OARRAYRUNESTR    // Type(Left) (Type is string, Left is a []rune)
	OSTRARRAYBYTE    // Type(Left) (Type is []byte, Left is a string)
	OSTRARRAYBYTETMP // Type(Left) (Type is []byte, Left is a string, ephemeral)
	OSTRARRAYRUNE    // Type(Left) (Type is []rune, Left is a string)
	OAS              // Left = Right or (if Colas=true) Left := Right
	OAS2             // List = Rlist (x, y, z = a, b, c)
	OAS2FUNC         // List = Rlist (x, y = f())
	OAS2RECV         // List = Rlist (x, ok = <-c)
	OAS2MAPR         // List = Rlist (x, ok = m["foo"])
	OAS2DOTTYPE      // List = Rlist (x, ok = I.(int))
	OASOP            // Left Etype= Right (x += y)
	OCALL            // Left(List) (function call, method call or type conversion)

	// OCALLFUNC, OCALLMETH, and OCALLINTER have the same structure.
	// Prior to walk, they are: Left(List), where List is all regular arguments.
	// If present, Right is an ODDDARG that holds the
	// generated slice used in a call to a variadic function.
	// After walk, List is a series of assignments to temporaries,
	// and Rlist is an updated set of arguments, including any ODDDARG slice.
	// TODO(josharian/khr): Use Ninit instead of List for the assignments to temporaries. See CL 114797.
	OCALLFUNC  // Left(List/Rlist) (function call f(args))
	OCALLMETH  // Left(List/Rlist) (direct method call x.Method(args))
	OCALLINTER // Left(List/Rlist) (interface method call x.Method(args))
	OCALLPART  // Left.Right (method expression x.Method, not called)
	OCAP       // cap(Left)
	OCLOSE     // close(Left)
	OCLOSURE   // func Type { Body } (func literal)
	OCOMPLIT   // Right{List} (composite literal, not yet lowered to specific form)
	OMAPLIT    // Type{List} (composite literal, Type is map)
	OSTRUCTLIT // Type{List} (composite literal, Type is struct)
	OARRAYLIT  // Type{List} (composite literal, Type is array)
	OSLICELIT  // Type{List} (composite literal, Type is slice)
	OPTRLIT    // &Left (left is composite literal)
	OCONV      // Type(Left) (type conversion)
	OCONVIFACE // Type(Left) (type conversion, to interface)
	OCONVNOP   // Type(Left) (type conversion, no effect)
	OCOPY      // copy(Left, Right)
	ODCL       // var Left (declares Left of type Left.Type)

	// Used during parsing but don't last.
	ODCLFUNC  // func f() or func (r) f()
	ODCLFIELD // struct field, interface field, or func/method argument/return value.
	ODCLCONST // const pi = 3.14
	ODCLTYPE  // type Int int or type Int = int

	ODELETE      // delete(Left, Right)
	ODOT         // Left.Sym (Left is of struct type)
	ODOTPTR      // Left.Sym (Left is of pointer to struct type)
	ODOTMETH     // Left.Sym (Left is non-interface, Right is method name)
	ODOTINTER    // Left.Sym (Left is interface, Right is method name)
	OXDOT        // Left.Sym (before rewrite to one of the preceding)
	ODOTTYPE     // Left.Right or Left.Type (.Right during parsing, .Type once resolved); after walk, .Right contains address of interface type descriptor and .Right.Right contains address of concrete type descriptor
	ODOTTYPE2    // Left.Right or Left.Type (.Right during parsing, .Type once resolved; on rhs of OAS2DOTTYPE); after walk, .Right contains address of interface type descriptor
	OEQ          // Left == Right
	ONE          // Left != Right
	OLT          // Left < Right
	OLE          // Left <= Right
	OGE          // Left >= Right
	OGT          // Left > Right
	OIND         // *Left
	OINDEX       // Left[Right] (index of array or slice)
	OINDEXMAP    // Left[Right] (index of map)
	OKEY         // Left:Right (key:value in struct/array/map literal)
	OSTRUCTKEY   // Sym:Left (key:value in struct literal, after type checking)
	OLEN         // len(Left)
	OMAKE        // make(List) (before type checking converts to one of the following)
	OPMAKE       // persistent memory version of make(List)
	OMAKECHAN    // make(Type, Left) (type is chan)
	OMAKEMAP     // make(Type, Left) (type is map)
	OMAKESLICE   // make(Type, Left, Right) (type is slice)
	OPMAKESLICE  // make(Type, Left, Right) (type is slice)
	OMUL         // Left * Right
	ODIV         // Left / Right
	OMOD         // Left % Right
	OLSH         // Left << Right
	ORSH         // Left >> Right
	OAND         // Left & Right
	OANDNOT      // Left &^ Right
	ONEW         // new(Left)
	OPNEW        // new(Left)
	ONOT         // !Left
	OCOM         // ^Left
	OPLUS        // +Left
	OMINUS       // -Left
	OOROR        // Left || Right
	OPANIC       // panic(Left)
	OPRINT       // print(List)
	OPRINTN      // println(List)
	OPAREN       // (Left)
	OSEND        // Left <- Right
	OSLICE       // Left[List[0] : List[1]] (Left is untypechecked or slice)
	OSLICEARR    // Left[List[0] : List[1]] (Left is array)
	OSLICESTR    // Left[List[0] : List[1]] (Left is string)
	OSLICE3      // Left[List[0] : List[1] : List[2]] (Left is untypedchecked or slice)
	OSLICE3ARR   // Left[List[0] : List[1] : List[2]] (Left is array)
	OSLICEHEADER // sliceheader{Left, List[0], List[1]} (Left is unsafe.Pointer, List[0] is length, List[1] is capacity)
	ORECOVER     // recover()
	ORECV        // <-Left
	ORUNESTR     // Type(Left) (Type is string, Left is rune)
	OSELRECV     // Left = <-Right.Left: (appears as .Left of OCASE; Right.Op == ORECV)
	OSELRECV2    // List = <-Right.Left: (apperas as .Left of OCASE; count(List) == 2, Right.Op == ORECV)
	OIOTA        // iota
	OREAL        // real(Left)
	OIMAG        // imag(Left)
	OCOMPLEX     // complex(Left, Right)
	OALIGNOF     // unsafe.Alignof(Left)
	OOFFSETOF    // unsafe.Offsetof(Left)
	OSIZEOF      // unsafe.Sizeof(Left)

	// statements
	OBLOCK    // { List } (block of code)
	OTXBLOCK  // txn() { List } (block of code enclosed within txn() keyword)
	OBREAK    // break [Sym]
	OCASE     // case Left or List[0]..List[1]: Nbody (select case after processing; Left==nil and List==nil means default)
	OXCASE    // case List: Nbody (select case before processing; List==nil means default)
	OCONTINUE // continue [Sym]
	ODEFER    // defer Left (Left must be call)
	OEMPTY    // no-op (empty statement)
	OFALL     // fallthrough
	OFOR      // for Ninit; Left; Right { Nbody }
	// OFORUNTIL is like OFOR, but the test (Left) is applied after the body:
	// 	Ninit
	// 	top: { Nbody }   // Execute the body at least once
	// 	cont: Right
	// 	if Left {        // And then test the loop condition
	// 		List     // Before looping to top, execute List
	// 		goto top
	// 	}
	// OFORUNTIL is created by walk. There's no way to write this in Go code.
	OFORUNTIL
	OGOTO   // goto Sym
	OIF     // if Ninit; Left { Nbody } else { Rlist }
	OLABEL  // Sym:
	OPROC   // go Left (Left must be call)
	ORANGE  // for List = range Right { Nbody }
	ORETURN // return List
	OSELECT // select { List } (List is list of OXCASE or OCASE)
	OSWITCH // switch Ninit; Left { List } (List is a list of OXCASE or OCASE)
	OTYPESW // Left = Right.(type) (appears as .Left of OSWITCH)

	// types
	OTCHAN   // chan int
	OTMAP    // map[string]int
	OTSTRUCT // struct{}
	OTINTER  // interface{}
	OTFUNC   // func()
	OTARRAY  // []int, [8]int, [N]int or [...]int

	// misc
	ODDD        // func f(args ...int) or f(l...) or var a = [...]int{0, 1, 2}.
	ODDDARG     // func f(args ...int), introduced by escape analysis.
	OINLCALL    // intermediary representation of an inlined call.
	OEFACE      // itable and data words of an empty-interface value.
	OITAB       // itable word of an interface value.
	OIDATA      // data word of an interface value in Left
	OSPTR       // base pointer of a slice or string.
	OCLOSUREVAR // variable reference at beginning of closure function
	OCFUNC      // reference to c function pointer (not go func value)
	OCHECKNIL   // emit code to ensure pointer/interface not nil
	OVARDEF     // variable is about to be fully initialized
	OVARKILL    // variable is dead
	OVARLIVE    // variable is alive
	OINDREGSP   // offset plus indirect of REGSP, such as 8(SP).

	// arch-specific opcodes
	ORETJMP // return to other function
	OGETG   // runtime.getg() (read g pointer)

	OEND
)

// Nodes is a pointer to a slice of *Node.
// For fields that are not used in most nodes, this is used instead of
// a slice to save space.
type Nodes struct{ slice *[]*Node }

// asNodes returns a slice of *Node as a Nodes value.
func asNodes(s []*Node) Nodes {
	return Nodes{&s}
}

// Slice returns the entries in Nodes as a slice.
// Changes to the slice entries (as in s[i] = n) will be reflected in
// the Nodes.
func (n Nodes) Slice() []*Node {
	if n.slice == nil {
		return nil
	}
	return *n.slice
}

// Len returns the number of entries in Nodes.
func (n Nodes) Len() int {
	if n.slice == nil {
		return 0
	}
	return len(*n.slice)
}

// Index returns the i'th element of Nodes.
// It panics if n does not have at least i+1 elements.
func (n Nodes) Index(i int) *Node {
	return (*n.slice)[i]
}

// First returns the first element of Nodes (same as n.Index(0)).
// It panics if n has no elements.
func (n Nodes) First() *Node {
	return (*n.slice)[0]
}

// Second returns the second element of Nodes (same as n.Index(1)).
// It panics if n has fewer than two elements.
func (n Nodes) Second() *Node {
	return (*n.slice)[1]
}

// Set sets n to a slice.
// This takes ownership of the slice.
func (n *Nodes) Set(s []*Node) {
	if len(s) == 0 {
		n.slice = nil
	} else {
		// Copy s and take address of t rather than s to avoid
		// allocation in the case where len(s) == 0 (which is
		// over 3x more common, dynamically, for make.bash).
		t := s
		n.slice = &t
	}
}

// Set1 sets n to a slice containing a single node.
func (n *Nodes) Set1(n1 *Node) {
	n.slice = &[]*Node{n1}
}

// Set2 sets n to a slice containing two nodes.
func (n *Nodes) Set2(n1, n2 *Node) {
	n.slice = &[]*Node{n1, n2}
}

// Set3 sets n to a slice containing three nodes.
func (n *Nodes) Set3(n1, n2, n3 *Node) {
	n.slice = &[]*Node{n1, n2, n3}
}

// MoveNodes sets n to the contents of n2, then clears n2.
func (n *Nodes) MoveNodes(n2 *Nodes) {
	n.slice = n2.slice
	n2.slice = nil
}

// SetIndex sets the i'th element of Nodes to node.
// It panics if n does not have at least i+1 elements.
func (n Nodes) SetIndex(i int, node *Node) {
	(*n.slice)[i] = node
}

// SetFirst sets the first element of Nodes to node.
// It panics if n does not have at least one elements.
func (n Nodes) SetFirst(node *Node) {
	(*n.slice)[0] = node
}

// SetSecond sets the second element of Nodes to node.
// It panics if n does not have at least two elements.
func (n Nodes) SetSecond(node *Node) {
	(*n.slice)[1] = node
}

// Addr returns the address of the i'th element of Nodes.
// It panics if n does not have at least i+1 elements.
func (n Nodes) Addr(i int) **Node {
	return &(*n.slice)[i]
}

// Append appends entries to Nodes.
func (n *Nodes) Append(a ...*Node) {
	if len(a) == 0 {
		return
	}
	if n.slice == nil {
		s := make([]*Node, len(a))
		copy(s, a)
		n.slice = &s
		return
	}
	*n.slice = append(*n.slice, a...)
}

// Prepend prepends entries to Nodes.
// If a slice is passed in, this will take ownership of it.
func (n *Nodes) Prepend(a ...*Node) {
	if len(a) == 0 {
		return
	}
	if n.slice == nil {
		n.slice = &a
	} else {
		*n.slice = append(a, *n.slice...)
	}
}

// AppendNodes appends the contents of *n2 to n, then clears n2.
func (n *Nodes) AppendNodes(n2 *Nodes) {
	switch {
	case n2.slice == nil:
	case n.slice == nil:
		n.slice = n2.slice
	default:
		*n.slice = append(*n.slice, *n2.slice...)
	}
	n2.slice = nil
}

// inspect invokes f on each node in an AST in depth-first order.
// If f(n) returns false, inspect skips visiting n's children.
func inspect(n *Node, f func(*Node) bool) {
	if n == nil || !f(n) {
		return
	}
	inspectList(n.Ninit, f)
	inspect(n.Left, f)
	inspect(n.Right, f)
	inspectList(n.List, f)
	inspectList(n.Nbody, f)
	inspectList(n.Rlist, f)
}

func inspectList(l Nodes, f func(*Node) bool) {
	for _, n := range l.Slice() {
		inspect(n, f)
	}
}

// nodeQueue is a FIFO queue of *Node. The zero value of nodeQueue is
// a ready-to-use empty queue.
type nodeQueue struct {
	ring       []*Node
	head, tail int
}

// empty reports whether q contains no Nodes.
func (q *nodeQueue) empty() bool {
	return q.head == q.tail
}

// pushRight appends n to the right of the queue.
func (q *nodeQueue) pushRight(n *Node) {
	if len(q.ring) == 0 {
		q.ring = make([]*Node, 16)
	} else if q.head+len(q.ring) == q.tail {
		// Grow the ring.
		nring := make([]*Node, len(q.ring)*2)
		// Copy the old elements.
		part := q.ring[q.head%len(q.ring):]
		if q.tail-q.head <= len(part) {
			part = part[:q.tail-q.head]
			copy(nring, part)
		} else {
			pos := copy(nring, part)
			copy(nring[pos:], q.ring[:q.tail%len(q.ring)])
		}
		q.ring, q.head, q.tail = nring, 0, q.tail-q.head
	}

	q.ring[q.tail%len(q.ring)] = n
	q.tail++
}

// popLeft pops a node from the left of the queue. It panics if q is
// empty.
func (q *nodeQueue) popLeft() *Node {
	if q.empty() {
		panic("dequeue empty")
	}
	n := q.ring[q.head%len(q.ring)]
	q.head++
	return n
}
