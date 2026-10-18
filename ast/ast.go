// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ast declares the syntax tree of Teak, modeled on go/ast.
//
// There are five families of node: Decl, Stmt, Expr, Type and Pattern.
// Each family is a sealed interface: an interface with an unexported
// marker method, so only this package can add members and a type switch
// over the family plays the role a pattern match plays in a language
// with sum types. Every node records the position where it starts.
package ast

import "github.com/teak-lang/teak/token"

// Node is implemented by every node in the tree.
type Node interface {
	Pos() token.Pos
}

// Decl is a top-level declaration.
type Decl interface {
	Node
	declNode()
}

// Stmt is a statement.
type Stmt interface {
	Node
	stmtNode()
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// Type is a type expression.
type Type interface {
	Node
	typeNode()
}

// Pattern is the shape a value is tested against and taken apart by, on
// the left of => in a match arm or of := in an if-binding. Some(n) is a
// pattern: it matches an option that holds a value and binds that value
// to n.
type Pattern interface {
	Node
	patternNode()
}

// A File is one parsed source file.
type File struct {
	Package token.Pos
	Name    *Ident
	Imports []*ImportSpec
	Decls   []Decl
}

// An ImportSpec is one import path with an optional local name.
type ImportSpec struct {
	Name *Ident    // local name, or nil
	Path *BasicLit // the string literal
}

// An Ident is an identifier.
type Ident struct {
	NamePos token.Pos
	Name    string
}

// A BasicLit is a literal of kind INT, FLOAT, CHAR or STRING.
type BasicLit struct {
	ValuePos token.Pos
	Kind     token.Kind
	Value    string // source text, including quotes
}

// A Field is one or more names sharing a type, in a struct or a trait.
// Names is empty for an embedded field, as in Go.
type Field struct {
	Names []*Ident
	Type  Type
}

// A Param is a function parameter. Type is a *RefType for the ref and
// mut modes; any other type is passed by value.
type Param struct {
	Name *Ident // nil when unnamed, as in a function type
	Type Type
}

// A TypeParam is one type parameter with its bounds: K: Hash + Eq.
type TypeParam struct {
	Name   *Ident
	Bounds []Type
}

// A FuncDecl is a function or method. Recv is nil for a function. Body
// is nil for a required method inside a trait.
type FuncDecl struct {
	Func       token.Pos
	Recv       *Param // (t mut Tree): Name t, Type *RefType{Mut, Tree}
	Name       *Ident
	TypeParams []*TypeParam
	Params     []*Param
	Results    []Type
	Body       *BlockStmt
}

// A TypeDecl declares a named type. Def is a *StructType, an *EnumType,
// or any other type for a definition like `type NodeID int`.
type TypeDecl struct {
	TypePos    token.Pos
	Name       *Ident
	TypeParams []*TypeParam
	Impls      []Type // the traits after `impl`
	Def        Type
}

// A TraitDecl declares a trait: required and default methods, fields,
// and the traits it refines.
type TraitDecl struct {
	Trait      token.Pos
	Name       *Ident
	TypeParams []*TypeParam
	Refines    []Type // trait Logged: Shape
	Fields     []*Field
	Methods    []*FuncDecl
}

// A VarDecl is a var or const declaration, at top level or in a block.
type VarDecl struct {
	TokPos token.Pos
	Const  bool
	Names  []*Ident
	Type   Type   // nil when inferred
	Values []Expr // empty for a zero value
}

// A NamedType names a declared type, with an optional package qualifier
// and type arguments: Node, http.Request, List<T>, arena.Index<Node>.
type NamedType struct {
	Pkg  *Ident // nil when unqualified
	Name *Ident
	Args []Type
}

// SelfType is the keyword Self, the implementing type inside a trait.
type SelfType struct {
	Self token.Pos
}

// A PointerType is an owned heap pointer: *T.
type PointerType struct {
	Star token.Pos
	Elem Type
}

// An OptionType is T?.
type OptionType struct {
	Elem     Type
	Question token.Pos
}

// A RefType is a borrow: ref T, or mut T when Mut is set.
type RefType struct {
	Ref  token.Pos
	Mut  bool
	Elem Type
}

// A SliceType is []T.
type SliceType struct {
	Lbrack token.Pos
	Elem   Type
}

// An ArrayType is [N]T.
type ArrayType struct {
	Lbrack token.Pos
	Len    Expr
	Elem   Type
}

// A MapType is map[K]V.
type MapType struct {
	Map   token.Pos
	Key   Type
	Value Type
}

// ChanDir is the direction of a channel type.
type ChanDir int

const (
	SendRecv ChanDir = iota // chan T
	SendOnly                // chan<- T
	RecvOnly                // <-chan T
)

// A ChanType is a channel type.
type ChanType struct {
	Begin token.Pos
	Dir   ChanDir
	Elem  Type
}

// A FuncType is a function type: func(T) U.
type FuncType struct {
	Func    token.Pos
	Params  []*Param
	Results []Type
}

// A TupleType is (A, B).
type TupleType struct {
	Lparen token.Pos
	Elems  []Type
}

// A StructType is the body of a struct declaration, or an anonymous
// struct type used directly, as in chan struct{}.
type StructType struct {
	Struct token.Pos
	Fields []*Field
}

// An EnumType is the body of an enum declaration.
type EnumType struct {
	Enum     token.Pos
	Variants []*Variant
}

// A Variant is one case of an enum. Its fields are written like a
// parameter list, positional as in Num(float64) or named as in
// Bin(op Token, lhs *Expr, rhs *Expr), so they are parameters.
type Variant struct {
	Name   *Ident
	Fields []*Param
}

// A BlockStmt is a brace-delimited list of statements.
type BlockStmt struct {
	Lbrace token.Pos
	List   []Stmt
	Rbrace token.Pos
}

// A DeclStmt is a var or const declaration inside a block.
type DeclStmt struct {
	Decl *VarDecl
}

// An EmptyStmt is a lone semicolon.
type EmptyStmt struct {
	Semicolon token.Pos
}

// An ExprStmt is an expression used as a statement.
type ExprStmt struct {
	X Expr
}

// A SendStmt is ch <- v.
type SendStmt struct {
	Chan  Expr
	Arrow token.Pos
	Value Expr
}

// An IncDecStmt is x++ or x--.
type IncDecStmt struct {
	X      Expr
	TokPos token.Pos
	Tok    token.Kind
}

// An AssignStmt is an assignment or short declaration. Tok is ASSIGN,
// DEFINE, or an assignment operator such as ADD_ASSIGN.
type AssignStmt struct {
	Lhs    []Expr
	TokPos token.Pos
	Tok    token.Kind
	Rhs    []Expr
}

// A GoStmt starts a task.
type GoStmt struct {
	Go   token.Pos
	Call Expr
}

// A DeferStmt defers a call.
type DeferStmt struct {
	Defer token.Pos
	Call  Expr
}

// A ReturnStmt returns zero or more values.
type ReturnStmt struct {
	Return  token.Pos
	Results []Expr
}

// A BranchStmt is break or continue.
type BranchStmt struct {
	TokPos token.Pos
	Tok    token.Kind
}

// An IfStmt is Go's if, with an optional init statement.
type IfStmt struct {
	If   token.Pos
	Init Stmt // nil when absent
	Cond Expr
	Body *BlockStmt
	Else Stmt // *IfStmt, *IfBindStmt, *BlockStmt, or nil
}

// An IfBindStmt is `if x := opt { ... }`: the body runs with the
// pattern bound when the value is Some.
type IfBindStmt struct {
	If    token.Pos
	Pat   Pattern
	Value Expr
	Body  *BlockStmt
	Else  Stmt
}

// A ForStmt is Go's three-clause or condition-only for loop.
type ForStmt struct {
	For  token.Pos
	Init Stmt // nil when absent
	Cond Expr // nil for an infinite loop
	Post Stmt // nil when absent
	Body *BlockStmt
}

// A RangeStmt is `for k, v := range x`. With one variable it is in Key,
// whatever it means for the ranged value (an index for a slice, the item
// for a channel or iterator); the checker decides. Key and Value are nil
// when absent; Tok is DEFINE, ASSIGN, or ILLEGAL with no variables.
type RangeStmt struct {
	For   token.Pos
	Key   Expr
	Value Expr
	Tok   token.Kind
	X     Expr
	Body  *BlockStmt
}

// A SwitchStmt is Go's switch. Tag is nil for a tagless switch.
type SwitchStmt struct {
	Switch token.Pos
	Init   Stmt
	Tag    Expr
	Body   []*CaseClause
}

// A CaseClause is one case of a switch. List is empty for default.
type CaseClause struct {
	Case token.Pos
	List []Expr
	Body []Stmt
}

// A SelectStmt is Go's select.
type SelectStmt struct {
	Select token.Pos
	Body   []*CommClause
}

// A CommClause is one case of a select. Comm is a *SendStmt, an
// *ExprStmt or *AssignStmt containing a receive, or nil for default.
type CommClause struct {
	Case token.Pos
	Comm Stmt
	Body []Stmt
}

// A BadExpr is a placeholder for an expression the parser could not
// parse, covering the tokens from From up to but not including To. It
// keeps the tree well formed after an error, as in go/ast.
type BadExpr struct {
	From, To token.Pos
}

// A ParenExpr is a parenthesized expression.
type ParenExpr struct {
	Lparen token.Pos
	X      Expr
}

// A TupleExpr is (a, b), with two or more elements.
type TupleExpr struct {
	Lparen token.Pos
	Elts   []Expr
}

// A SelectorExpr is x.Sel.
type SelectorExpr struct {
	X   Expr
	Sel *Ident
}

// An IndexExpr is x[i].
type IndexExpr struct {
	X      Expr
	Lbrack token.Pos
	Index  Expr
}

// A SliceExpr is x[lo:hi]. Low and High may be nil.
type SliceExpr struct {
	X      Expr
	Lbrack token.Pos
	Low    Expr
	High   Expr
}

// A CallExpr is a call, with optional explicit type arguments:
// f(x), arena.New<Node>(), Some(x).
type CallExpr struct {
	Fun      Expr
	TypeArgs []Type
	Lparen   token.Pos
	Args     []Expr
}

// A UnaryExpr is a prefix operator applied to an operand. Op is one of
// SUB, NOT, XOR, ARROW (receive), AND (heap allocation &x), MUL
// (dereference *p), REF (borrow ref x) or MUT (borrow mut x).
type UnaryExpr struct {
	OpPos token.Pos
	Op    token.Kind
	X     Expr
}

// A BinaryExpr is x op y for every binary operator except ??.
type BinaryExpr struct {
	X     Expr
	OpPos token.Pos
	Op    token.Kind
	Y     Expr
}

// A CoalesceExpr is x ?? else. Else is an Expr, or a diverging
// *ReturnStmt, *BranchStmt or *BlockStmt.
type CoalesceExpr struct {
	X     Expr
	OpPos token.Pos
	Else  Node
}

// A TypeExpr is a type in expression position: the argument of make
// in make([]byte, n), or the function of a conversion in []byte(s).
// A composite literal's type is held by the literal itself.
type TypeExpr struct {
	Type Type
}

// A TryExpr is the postfix x?.
type TryExpr struct {
	X        Expr
	Question token.Pos
}

// A CompositeLit is T{...}. Type is nil when elided inside another
// literal. Elts holds Exprs or *KeyValueExprs.
type CompositeLit struct {
	Type   Type
	Lbrace token.Pos
	Elts   []Expr
}

// A KeyValueExpr is key: value inside a composite literal.
type KeyValueExpr struct {
	Key   Expr
	Value Expr
}

// A FuncLit is a function literal.
type FuncLit struct {
	Type *FuncType
	Body *BlockStmt
}

// An IfExpr is if used as an expression: every branch yields a value.
type IfExpr struct {
	If   token.Pos
	Cond Expr
	Then *BlockStmt
	Else Node // *BlockStmt or *IfExpr
}

// A MatchExpr is match x { arms }. In statement position it is wrapped
// in an ExprStmt.
type MatchExpr struct {
	Match token.Pos
	X     Expr
	Arms  []*MatchArm
}

// A MatchArm is pat [if guard] => body. Body is an Expr, a *BlockStmt,
// or a single simple statement such as a send, an assignment or a
// return, which the examples use freely: `Stop => return,`.
type MatchArm struct {
	Pat   Pattern
	Guard Expr
	Arrow token.Pos
	Body  Node
}

// A WildcardPat is _.
type WildcardPat struct {
	Underscore token.Pos
}

// An IdentPat binds a name, or names a constant variant such as None.
// Which one is decided by the checker, not the parser.
type IdentPat struct {
	Name *Ident
}

// A LitPat matches a literal.
type LitPat struct {
	Lit *BasicLit
}

// A RangePat is lo..=hi over literals.
type RangePat struct {
	Lo *BasicLit
	Hi *BasicLit
}

// A TuplePat is (p, q).
type TuplePat struct {
	Lparen token.Pos
	Elts   []Pattern
}

// A VariantPat is a variant with sub-patterns: Some(n), Bin(Plus, l, r).
// Path is an *Ident or a *SelectorExpr.
type VariantPat struct {
	Path Expr
	Args []Pattern
}

// An OrPat is p | q.
type OrPat struct {
	Alts []Pattern
}

func (x *File) Pos() token.Pos         { return x.Package }
func (x *ImportSpec) Pos() token.Pos   { return firstPos(x.Name, x.Path) }
func (x *Ident) Pos() token.Pos        { return x.NamePos }
func (x *BasicLit) Pos() token.Pos     { return x.ValuePos }
func (x *Field) Pos() token.Pos        { return firstPos(firstIdent(x.Names), x.Type) }
func (x *Param) Pos() token.Pos        { return firstPos(x.Name, x.Type) }
func (x *TypeParam) Pos() token.Pos    { return x.Name.Pos() }
func (x *FuncDecl) Pos() token.Pos     { return x.Func }
func (x *TypeDecl) Pos() token.Pos     { return x.TypePos }
func (x *TraitDecl) Pos() token.Pos    { return x.Trait }
func (x *VarDecl) Pos() token.Pos      { return x.TokPos }
func (x *NamedType) Pos() token.Pos    { return firstPos(x.Pkg, x.Name) }
func (x *SelfType) Pos() token.Pos     { return x.Self }
func (x *PointerType) Pos() token.Pos  { return x.Star }
func (x *OptionType) Pos() token.Pos   { return x.Elem.Pos() }
func (x *RefType) Pos() token.Pos      { return x.Ref }
func (x *SliceType) Pos() token.Pos    { return x.Lbrack }
func (x *ArrayType) Pos() token.Pos    { return x.Lbrack }
func (x *MapType) Pos() token.Pos      { return x.Map }
func (x *ChanType) Pos() token.Pos     { return x.Begin }
func (x *FuncType) Pos() token.Pos     { return x.Func }
func (x *TupleType) Pos() token.Pos    { return x.Lparen }
func (x *StructType) Pos() token.Pos   { return x.Struct }
func (x *EnumType) Pos() token.Pos     { return x.Enum }
func (x *Variant) Pos() token.Pos      { return x.Name.Pos() }
func (x *BlockStmt) Pos() token.Pos    { return x.Lbrace }
func (x *DeclStmt) Pos() token.Pos     { return x.Decl.Pos() }
func (x *EmptyStmt) Pos() token.Pos    { return x.Semicolon }
func (x *ExprStmt) Pos() token.Pos     { return x.X.Pos() }
func (x *SendStmt) Pos() token.Pos     { return x.Chan.Pos() }
func (x *IncDecStmt) Pos() token.Pos   { return x.X.Pos() }
func (x *AssignStmt) Pos() token.Pos   { return x.Lhs[0].Pos() }
func (x *GoStmt) Pos() token.Pos       { return x.Go }
func (x *DeferStmt) Pos() token.Pos    { return x.Defer }
func (x *ReturnStmt) Pos() token.Pos   { return x.Return }
func (x *BranchStmt) Pos() token.Pos   { return x.TokPos }
func (x *IfStmt) Pos() token.Pos       { return x.If }
func (x *IfBindStmt) Pos() token.Pos   { return x.If }
func (x *ForStmt) Pos() token.Pos      { return x.For }
func (x *RangeStmt) Pos() token.Pos    { return x.For }
func (x *SwitchStmt) Pos() token.Pos   { return x.Switch }
func (x *CaseClause) Pos() token.Pos   { return x.Case }
func (x *SelectStmt) Pos() token.Pos   { return x.Select }
func (x *CommClause) Pos() token.Pos   { return x.Case }
func (x *BadExpr) Pos() token.Pos      { return x.From }
func (x *ParenExpr) Pos() token.Pos    { return x.Lparen }
func (x *TupleExpr) Pos() token.Pos    { return x.Lparen }
func (x *SelectorExpr) Pos() token.Pos { return x.X.Pos() }
func (x *IndexExpr) Pos() token.Pos    { return x.X.Pos() }
func (x *SliceExpr) Pos() token.Pos    { return x.X.Pos() }
func (x *CallExpr) Pos() token.Pos     { return x.Fun.Pos() }
func (x *UnaryExpr) Pos() token.Pos    { return x.OpPos }
func (x *BinaryExpr) Pos() token.Pos   { return x.X.Pos() }
func (x *CoalesceExpr) Pos() token.Pos { return x.X.Pos() }
func (x *TypeExpr) Pos() token.Pos     { return x.Type.Pos() }
func (x *TryExpr) Pos() token.Pos      { return x.X.Pos() }
func (x *CompositeLit) Pos() token.Pos { return compositePos(x) }
func (x *KeyValueExpr) Pos() token.Pos { return x.Key.Pos() }
func (x *FuncLit) Pos() token.Pos      { return x.Type.Pos() }
func (x *IfExpr) Pos() token.Pos       { return x.If }
func (x *MatchExpr) Pos() token.Pos    { return x.Match }
func (x *MatchArm) Pos() token.Pos     { return x.Pat.Pos() }
func (x *WildcardPat) Pos() token.Pos  { return x.Underscore }
func (x *IdentPat) Pos() token.Pos     { return x.Name.Pos() }
func (x *LitPat) Pos() token.Pos       { return x.Lit.Pos() }
func (x *RangePat) Pos() token.Pos     { return x.Lo.Pos() }
func (x *TuplePat) Pos() token.Pos     { return x.Lparen }
func (x *VariantPat) Pos() token.Pos   { return x.Path.Pos() }
func (x *OrPat) Pos() token.Pos        { return x.Alts[0].Pos() }

// The marker methods are what seal each family. A type switch over Decl,
// Stmt, Expr, Type or Pattern can therefore enumerate every case.

func (*FuncDecl) declNode()  {}
func (*TypeDecl) declNode()  {}
func (*TraitDecl) declNode() {}
func (*VarDecl) declNode()   {}

func (*BlockStmt) stmtNode()  {}
func (*DeclStmt) stmtNode()   {}
func (*EmptyStmt) stmtNode()  {}
func (*ExprStmt) stmtNode()   {}
func (*SendStmt) stmtNode()   {}
func (*IncDecStmt) stmtNode() {}
func (*AssignStmt) stmtNode() {}
func (*GoStmt) stmtNode()     {}
func (*DeferStmt) stmtNode()  {}
func (*ReturnStmt) stmtNode() {}
func (*BranchStmt) stmtNode() {}
func (*IfStmt) stmtNode()     {}
func (*IfBindStmt) stmtNode() {}
func (*ForStmt) stmtNode()    {}
func (*RangeStmt) stmtNode()  {}
func (*SwitchStmt) stmtNode() {}
func (*SelectStmt) stmtNode() {}

func (*Ident) exprNode()        {}
func (*BasicLit) exprNode()     {}
func (*BadExpr) exprNode()      {}
func (*ParenExpr) exprNode()    {}
func (*TupleExpr) exprNode()    {}
func (*SelectorExpr) exprNode() {}
func (*IndexExpr) exprNode()    {}
func (*SliceExpr) exprNode()    {}
func (*CallExpr) exprNode()     {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*CoalesceExpr) exprNode() {}
func (*TypeExpr) exprNode()     {}
func (*TryExpr) exprNode()      {}
func (*CompositeLit) exprNode() {}
func (*KeyValueExpr) exprNode() {}
func (*FuncLit) exprNode()      {}
func (*IfExpr) exprNode()       {}
func (*MatchExpr) exprNode()    {}

func (*NamedType) typeNode()   {}
func (*SelfType) typeNode()    {}
func (*PointerType) typeNode() {}
func (*OptionType) typeNode()  {}
func (*RefType) typeNode()     {}
func (*SliceType) typeNode()   {}
func (*ArrayType) typeNode()   {}
func (*MapType) typeNode()     {}
func (*ChanType) typeNode()    {}
func (*FuncType) typeNode()    {}
func (*TupleType) typeNode()   {}
func (*StructType) typeNode()  {}
func (*EnumType) typeNode()    {}

func (*WildcardPat) patternNode() {}
func (*IdentPat) patternNode()    {}
func (*LitPat) patternNode()      {}
func (*RangePat) patternNode()    {}
func (*TuplePat) patternNode()    {}
func (*VariantPat) patternNode()  {}
func (*OrPat) patternNode()       {}

func compositePos(x *CompositeLit) token.Pos {
	if x.Type != nil {
		return x.Type.Pos()
	}

	return x.Lbrace
}

// firstPos returns the position of the optional leading node when it is
// present, else that of the node that follows it.
func firstPos(optional *Ident, next Node) token.Pos {
	if optional != nil {
		return optional.Pos()
	}

	return next.Pos()
}

func firstIdent(names []*Ident) *Ident {
	if len(names) == 0 {
		return nil
	}

	return names[0]
}
