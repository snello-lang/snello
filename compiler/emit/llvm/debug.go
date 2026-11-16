// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/snello-lang/snello/compiler/emit/lower"
	"github.com/snello-lang/snello/compiler/token"
	"github.com/snello-lang/snello/compiler/types"
)

// The debugging information of a debug build, beside the line tables
// every build has: each variable and parameter of the program declared
// to the debugger at its slot, as clang declares one at -O0, in the
// lexical block of the Snello block that declares it; a closure's
// captured variables through the slot that holds its box; each
// function's type; and each type described by the layout this backend
// gives it, under the names the C backend writes it by, so that a
// debugger prints a variable the same through either backend and one
// set of formatters serves both. A release build writes none of it.

const (
	// declareName is the intrinsic that tells the debugger a variable
	// lives at an address, and declareText its declaration.
	declareName = "llvm.dbg.declare"
	declareText = "declare void @llvm.dbg.declare(metadata, metadata, metadata)"

	// byteBits converts the sizes the backend lays values out by, in
	// bytes, to the bits the debugging information counts in;
	// pointerBytes is the size of a pointer, and of a word of a payload.
	byteBits     = 8
	pointerBytes = 8

	// The members of the records the runtime and the C backend declare,
	// named as snello.h and the C backend name them.
	ptrMember   = "ptr"
	lenMember   = "len"
	capMember   = "cap"
	hasMember   = "has"
	valMember   = "val"
	okMember    = "ok"
	errMember   = "err"
	fnMember    = "fn"
	boxMember   = "box"
	atMember    = "at"
	tabMember   = "tab"
	dataMember  = "data"
	tagMember   = "tag"
	liveMember  = "sn_live"
	tupleMember = "e"

	// The C names of the records the runtime declares, and of the words
	// a function value's box is made of.
	stringName = "sn_string"
	unitName   = "sn_unit"
	chanName   = "sn_chan"
	mapName    = "sn_map"
	wordName   = "uint64_t"
	byteName   = "uint8_t"
	countName  = "int64_t"
	voidName   = "void"

	// ownAttributes are the attributes of a function of the program's
	// own in a debug build: left unoptimized and never written into
	// another, as clang's -O0 marks every function, while the standard
	// library's are optimized around it.
	ownAttributes = " optnone noinline"

	// symbolPrefix starts the C backend's name of a type it makes, which
	// the debugging information names it by; pointerIR is the IR's type
	// of an address, and envParam the parameter a closure is given its
	// box's address in (closure.go).
	symbolPrefix = "sn_"
	pointerIR    = "ptr"
	envParam     = "%env"

	// The tags of the records described: a struct and a union.
	structureTag = "DW_TAG_structure_type"
	unionTag     = "DW_TAG_union_type"

	// An enum's tag is an enumeration of tagBytes over tagBase, as a C
	// compiler lays out an enumeration of no negative value.
	tagBase  = "unsigned int"
	tagBytes = 4

	// stringWords are the words of a string or a slice: its pointer, its
	// length and its capacity.
	stringWords = 3

	// darwin is the system whose C library spells a 64-bit integer
	// wideDarwin, where Linux's spells it wide; unsigned is the word
	// before the unsigned one.
	darwin     = "darwin"
	wide       = "long"
	wideDarwin = "long long"
	unsigned   = "unsigned "

	// The operations of a capture's location: the closure's box is read
	// from its slot, and a capture lent to the closure is read through
	// the address the box holds.
	derefOp = "DW_OP_deref"
	plusOp  = "DW_OP_plus_uconst"
)

// mapMembers are the members of the runtime's sn_map, in order, each
// with what it points to, or a count where it points to nothing: the
// keys, the values, the hashes and the index, then four counts.
var mapMembers = []struct{ name, points string }{
	{"keys", voidName}, {"vals", voidName}, {"hashes", wordName}, {"ctrl", byteName},
	{"len", ""}, {"count", ""}, {"cap", ""}, {"slots", ""},
}

// cMacros are the C names that are macros, which the compilers
// describe as what they stand for.
var cMacros = map[string]string{"bool": "_Bool"}

// A cBase is a C compiler's own type, which a fixed-width name of the
// C library names: its size in bits and its encoding in the debugging
// information.
type cBase struct {
	bits     int64
	encoding string
}

// cBases are the C compilers' types the basic types come down to.
var cBases = map[string]cBase{
	"_Bool":              {8, "DW_ATE_boolean"},
	"signed char":        {8, "DW_ATE_signed_char"},
	"short":              {16, "DW_ATE_signed"},
	"int":                {32, "DW_ATE_signed"},
	"long":               {64, "DW_ATE_signed"},
	"long long":          {64, "DW_ATE_signed"},
	"unsigned char":      {8, "DW_ATE_unsigned_char"},
	"unsigned short":     {16, "DW_ATE_unsigned"},
	"unsigned int":       {32, "DW_ATE_unsigned"},
	"unsigned long":      {64, "DW_ATE_unsigned"},
	"unsigned long long": {64, "DW_ATE_unsigned"},
	"float":              {32, "DW_ATE_float"},
	"double":             {64, "DW_ATE_float"},
}

// cNames are the C names that name another, as the C library's headers
// and snello.h declare them. The 64-bit ones are the system's
// (cNameOf).
var cNames = map[string]string{
	"sn_int":   "int64_t",
	"sn_uint":  "uint64_t",
	"int8_t":   "signed char",
	"int16_t":  "short",
	"int32_t":  "int",
	"uint8_t":  "unsigned char",
	"uint16_t": "unsigned short",
	"uint32_t": "unsigned int",
}

// A member is a part of a record described to the debugger: its name,
// empty for a union of no name, the node of its type, and its size and
// offset in bytes.
type member struct {
	name         string
	typ          int
	size, offset int64
}

// reserve takes the number of a metadata node written later, so that a
// type's members can name the type itself; writeNode writes it.
func (e *Emitter) reserve() int {
	e.nmeta++
	return e.nmeta
}

func (e *Emitter) writeNode(n int, text string) {
	put(&e.meta, "!", strconv.Itoa(n), " = ", text, "\n")
}

// debugging reports whether the function being written describes its
// variables: a debug build's function of the program, with a
// subprogram.
func (e *Emitter) debugging() bool {
	return e.cfg.Debug && e.fn != nil && e.fn.subprogram != 0 && !e.fn.artificial
}

// ownAttrs are the attributes of the function being written beside the
// module's own: ownAttributes for a function of the program's own in a
// debug build.
func (e *Emitter) ownAttrs() string {
	if e.cfg.Debug && e.ownFiles[e.file] {
		return ownAttributes
	}

	return ""
}

// scopeNow is the scope of what is written now: the lexical block of
// the innermost frame that has one, else the function's own scope.
func (e *Emitter) scopeNow() int {
	for i := len(e.fn.frames) - 1; i >= 0; i-- {
		if b := e.fn.frames[i].block; b != 0 {
			return b
		}
	}

	if e.fn.scope != 0 {
		return e.fn.scope
	}

	return e.fn.subprogram
}

// lexicalBlock is the node of a block opened inside the function being
// written, at the line being written, or none: the function's own
// first frame is its subprogram's, where its parameters are.
func (e *Emitter) lexicalBlock() int {
	if !e.debugging() || len(e.fn.frames) == 0 {
		return 0
	}

	return e.metaNode("distinct !DILexicalBlock(scope: !" + strconv.Itoa(e.scopeNow()) + ", file: !" + strconv.Itoa(e.fn.file) + ", line: " + strconv.Itoa(e.fn.line) + ", column: 0)")
}

// declareVar declares v, whose slot is slot, to the debugger: the
// arg-th parameter of its function, or a local for zero.
func (e *Emitter) declareVar(v *types.Var, slot string, arg int) {
	if !e.debugging() || v.Name() == "" || v.Name() == "_" {
		return
	}

	e.declareAt(v.Name(), e.debugType(v.Type()), e.lineOf(v.Pos()), slot, arg, "")
}

// declareCaptures declares a closure's captured variables, through the
// slot holding env, the address of the closure's box: a capture copied
// into the box is at its offset in it, and when any capture is lent,
// the box's first word holds the address of the environment, which
// holds the copies and the addresses of the variables lent.
func (e *Emitter) declareCaptures(captured []types.Capture, inline bool, pos token.Pos) {
	if !e.debugging() || len(captured) == 0 {
		return
	}

	slot := e.slot(pointerIR)
	e.store(value{pointerIR, envParam}, slot)
	sizes := make([]int64, len(captured))
	aligns := make([]int64, len(captured))
	for i, cap := range captured {
		sizes[i], aligns[i] = pointerBytes, pointerBytes
		if cap.Copied {
			t := e.sub(cap.Var.Type())
			sizes[i], aligns[i] = e.size(t), e.align(t)
		}
	}

	line := e.lineOf(pos)
	for i, off := range layout(sizes, aligns) {
		ops := []string{derefOp}
		if !inline {
			ops = append(ops, derefOp)
		}

		ops = append(ops, plusOp, strconv.FormatInt(off, 10))
		if !captured[i].Copied {
			ops = append(ops, derefOp)
		}

		v := captured[i].Var
		e.declareAt(v.Name(), e.debugType(v.Type()), line, slot, 0, strings.Join(ops, ", "))
	}
}

// declareAt declares the variable name, of the type node typ, declared
// at line, at the address slot holds after the expression ops.
func (e *Emitter) declareAt(name string, typ, line int, slot string, arg int, ops string) {
	position := ""
	if arg > 0 {
		position = "arg: " + strconv.Itoa(arg) + ", "
	}

	node := e.metaNode("!DILocalVariable(name: " + strconv.Quote(name) + ", " + position + "scope: !" + strconv.Itoa(e.scopeNow()) + ", file: !" + strconv.Itoa(e.fn.file) + ", line: " + strconv.Itoa(line) + ", type: !" + strconv.Itoa(typ) + ")")
	e.declare(declareName, declareText)
	put(&e.fn.allocas, "  call void @", declareName, "(metadata ptr ", slot, ", metadata !", strconv.Itoa(node), ", metadata !DIExpression(", ops, ")), !dbg ", e.locationAt(line), "\n")
}

// routineType is the type of a function of the result and parameters
// given, as clang writes one, so that a debugger can call it: null for
// no result, and each parameter's type.
func (e *Emitter) routineType(result types.Type, params []*types.Var) string {
	parts := []string{"null"}
	if result != types.Unit && result != nil {
		parts[0] = "!" + strconv.Itoa(e.debugType(result))
	}

	for _, p := range params {
		parts = append(parts, "!"+strconv.Itoa(e.debugType(p.Type())))
	}

	return "!DISubroutineType(types: !{" + strings.Join(parts, ", ") + "})"
}

// debugType is the node describing the type t, written once.
func (e *Emitter) debugType(t types.Type) int {
	t = e.sub(t)
	key := lower.TypeKey(t) // one a type
	if n, ok := e.dtypes[key]; ok {
		return n
	}

	n := e.describe(key, t)
	e.dtypes[key] = n
	return n
}

// describe writes the node of the type t, whose key is key: a record's
// is taken under the key before its members are written, so that a
// type that holds itself through a pointer names its own node.
func (e *Emitter) describe(key string, t types.Type) int {
	switch u := t.(type) {
	case *types.Basic:
		if isString(u) {
			return e.stringType()
		}

		name := lower.BasicName(u)
		if base, ok := cMacros[name]; ok {
			name = base
		}

		return e.cType(name)
	case *types.Named:
		switch under := u.Underlying().(type) {
		case *types.Struct:
			return e.structType(key, u, under)
		case *types.Enum:
			return e.enumType(key, u, under)
		}

		return e.typedef(lower.NamedName(u), e.debugType(u.Underlying()))
	case *types.Ref:
		if view(u) {
			return e.debugType(u.Elem())
		}

		return e.pointerTo(e.debugType(u.Elem()))
	case *types.Pointer:
		return e.pointerTo(e.debugType(u.Elem()))
	case *types.Chan:
		return e.pointerTo(e.declared(chanName))
	case *types.Option:
		if nullable(u) { // the pointer itself, as a record of it alone, of its layout, which a debugger's formatter reads as None when null
			return e.record(key, structureTag, lower.Symbol(symbolPrefix, u), pointerBytes, []member{{valMember, e.debugType(u.Elem()), pointerBytes, 0}})
		}

		ts := []types.Type{types.Typ[types.Bool], u.Elem()}
		offs := e.offsets(ts)
		return e.record(key, structureTag, lower.Symbol(symbolPrefix, u), e.size(u), []member{
			{hasMember, e.debugType(ts[0]), e.size(ts[0]), offs[0]},
			{valMember, e.debugType(ts[1]), e.size(ts[1]), offs[1]},
		})
	case *types.Result:
		words := pointerBytes * e.payloadWords(u.Val(), u.Err())
		n := e.reserve()
		e.dtypes[key] = n
		payload := e.record("", unionTag, "", words, []member{
			{valMember, e.debugType(u.Val()), e.size(u.Val()), 0},
			{errMember, e.debugType(u.Err()), e.size(u.Err()), 0},
		})
		return e.recordAt(n, structureTag, lower.Symbol(symbolPrefix, u), e.size(u), []member{
			{okMember, e.debugType(types.Typ[types.Bool]), 1, 0},
			{"", payload, words, pointerBytes},
		})
	case *types.Slice:
		return e.record(key, structureTag, lower.Symbol(symbolPrefix, u), e.size(u), e.counted(e.pointerTo(e.debugType(u.Elem()))))
	case *types.Array:
		elem := e.debugType(u.Elem())
		size := e.size(u)
		return e.record(key, structureTag, lower.Symbol(symbolPrefix, u), size, []member{{atMember, e.array(elem, size, u.Len()), size, 0}})
	case *types.Map:
		return e.typedef(lower.Symbol(symbolPrefix, u), e.mapRecord())
	case *types.Tuple:
		ts := make([]types.Type, u.Len())
		for i := range ts {
			ts[i] = u.At(i)
		}

		if u == types.Unit {
			return e.record(key, structureTag, unitName, 0, nil)
		}

		offs := e.offsets(ts)
		ms := make([]member, len(ts))
		for i, part := range ts {
			ms[i] = member{tupleMember + strconv.Itoa(i), e.debugType(part), e.size(part), offs[i]}
		}

		return e.record(key, structureTag, lower.Symbol(symbolPrefix, u), e.size(u), ms)
	case *types.Signature:
		box := pointerBytes * int64(e.boxWords)
		fn := e.metaNode("!DIDerivedType(tag: DW_TAG_pointer_type, baseType: !DISubroutineType(types: !{null}), size: " + strconv.Itoa(pointerBytes*byteBits) + ")")
		return e.record(key, structureTag, lower.Symbol(symbolPrefix, u), e.size(u), []member{
			{fnMember, fn, pointerBytes, 0},
			{boxMember, e.array(e.cType(wordName), box, int64(e.boxWords)), box, pointerBytes},
		})
	case *types.Trait:
		void := e.pointerTo(0)
		return e.record(key, structureTag, lower.TraitName(u), e.size(u), []member{
			{tabMember, void, pointerBytes, 0},
			{dataMember, void, pointerBytes, pointerBytes},
		})
	}

	return e.cType(voidName)
}

// structType describes a declared struct: its fields in order, and the
// flag after them that a type with a Drop method has.
func (e *Emitter) structType(key string, t *types.Named, s *types.Struct) int {
	n := e.reserve()
	e.dtypes[key] = n
	ts := make([]types.Type, 0, s.NumFields()+1)
	names := make([]string, 0, s.NumFields()+1)
	for i := 0; i < s.NumFields(); i++ {
		ts = append(ts, s.Field(i).Type())
		names = append(names, lower.Ident(s.Field(i).Name()))
	}

	if hasDrop(t) {
		ts = append(ts, types.Typ[types.Bool])
		names = append(names, liveMember)
	}

	offs := e.offsets(ts)
	ms := make([]member, len(ts))
	for i, f := range ts {
		ms[i] = member{names[i], e.debugType(f), e.size(f), offs[i]}
	}

	return e.recordAt(n, structureTag, lower.NamedName(t), e.size(t), ms)
}

// enumType describes a declared enum: its tag, an enumeration of its
// cases, a union of one record a case with fields, named after the
// case, at the payload's offset, and the flag after the payload that a
// type with a Drop method has.
func (e *Emitter) enumType(key string, t *types.Named, en *types.Enum) int {
	n := e.reserve()
	e.dtypes[key] = n
	cases := make([]string, en.NumVariants())
	var payloads []member
	var widest int64 // the largest case's fields, in bytes
	for i := range cases {
		v := en.Variant(i)
		cases[i] = "!DIEnumerator(name: " + strconv.Quote(lower.CaseName(t, v)) + ", value: " + strconv.Itoa(i) + ", isUnsigned: true)"
		if len(v.Fields()) == 0 {
			continue
		}

		ts := make([]types.Type, len(v.Fields()))
		for j, f := range v.Fields() {
			ts[j] = f.Type()
		}

		offs := e.offsets(ts)
		ms := make([]member, len(ts))
		for j, f := range v.Fields() {
			ms[j] = member{lower.VariantField(f, j), e.debugType(f.Type()), e.size(f.Type()), offs[j]}
		}

		size := e.structSize(ts)
		widest = max(widest, size)
		payloads = append(payloads, member{lower.VariantMember(v), e.record("", structureTag, "", size, ms), size, 0})
	}

	tag := e.metaNode("!DICompositeType(tag: DW_TAG_enumeration_type, name: " + strconv.Quote(lower.TagName(t)) + ", baseType: !" + strconv.Itoa(e.cType(tagBase)) + ", size: " + debugBits(tagBytes) + ", elements: !{" + strings.Join(cases, ", ") + "})")
	ms := []member{{tagMember, tag, tagBytes, 0}}
	payload := (widest + pointerBytes - 1) / pointerBytes * pointerBytes
	if len(payloads) > 0 {
		ms = append(ms, member{"", e.record("", unionTag, "", payload, payloads), payload, pointerBytes})
	}

	if hasDrop(t) {
		ms = append(ms, member{liveMember, e.debugType(types.Typ[types.Bool]), 1, e.size(t) - pointerBytes})
	}

	return e.recordAt(n, structureTag, lower.NamedName(t), e.size(t), ms)
}

// stringType describes the runtime's string: its bytes, and its
// length and capacity, int64_t in snello.h.
func (e *Emitter) stringType() int {
	if n, ok := e.cnodes[stringName]; ok {
		return n
	}

	count := e.cType(countName)
	n := e.record("", structureTag, stringName, stringWords*pointerBytes, []member{
		{ptrMember, e.pointerTo(e.cType(byteName)), pointerBytes, 0},
		{lenMember, count, pointerBytes, pointerBytes},
		{capMember, count, pointerBytes, 2 * pointerBytes},
	})
	e.cnodes[stringName] = n
	return n
}

// counted are the members of a string or a slice: the pointer ptr, whose
// node is given, then its length and capacity.
func (e *Emitter) counted(ptr int) []member {
	count := e.cType(lower.BasicName(types.Typ[types.Int]))
	return []member{
		{ptrMember, ptr, pointerBytes, 0},
		{lenMember, count, pointerBytes, pointerBytes},
		{capMember, count, pointerBytes, 2 * pointerBytes},
	}
}

// mapRecord describes the runtime's sn_map, which every map type of the
// program is a name of.
func (e *Emitter) mapRecord() int {
	if n, ok := e.cnodes[mapName]; ok {
		return n
	}

	ms := make([]member, len(mapMembers))
	for i, m := range mapMembers {
		typ := e.cType(countName)
		switch {
		case m.points == voidName:
			typ = e.pointerTo(0)
		case m.points != "":
			typ = e.pointerTo(e.cType(m.points))
		}

		ms[i] = member{m.name, typ, pointerBytes, int64(i) * pointerBytes}
	}

	n := e.record("", structureTag, mapName, int64(len(mapMembers))*pointerBytes, ms)
	e.cnodes[mapName] = n
	return n
}

// record writes a record of the debugging information, a structure or
// a union, under key when one is given, and returns its node.
func (e *Emitter) record(key, tag, name string, size int64, ms []member) int {
	n := e.reserve()
	if key != "" {
		e.dtypes[key] = n
	}

	return e.recordAt(n, tag, name, size, ms)
}

// recordAt writes the record whose node n was reserved.
func (e *Emitter) recordAt(n int, tag, name string, size int64, ms []member) int {
	elems := make([]string, len(ms))
	for i, m := range ms {
		elems[i] = "!" + strconv.Itoa(e.metaNode("!DIDerivedType(tag: DW_TAG_member, "+named(m.name)+"scope: !"+strconv.Itoa(n)+", baseType: !"+strconv.Itoa(m.typ)+", size: "+debugBits(m.size)+", offset: "+debugBits(m.offset)+")"))
	}

	e.writeNode(n, "distinct !DICompositeType(tag: "+tag+", "+named(name)+"size: "+debugBits(size)+", elements: !{"+strings.Join(elems, ", ")+"})")
	return n
}

// cType is the node of a C type by its name: a name of another, as the
// C library and snello.h declare it, or a type of the C compilers'
// own, written once, so that the chain of names is the C compilers'.
func (e *Emitter) cType(name string) int {
	if n, ok := e.cnodes[name]; ok {
		return n
	}

	var n int
	switch base, ok := cBases[name]; {
	case ok:
		n = e.metaNode("!DIBasicType(name: " + strconv.Quote(name) + ", size: " + strconv.FormatInt(base.bits, 10) + ", encoding: " + base.encoding + ")")
	case name == voidName:
		n = e.metaNode("!DIBasicType(tag: DW_TAG_unspecified_type, name: " + strconv.Quote(voidName) + ")")
	default:
		n = e.typedef(name, e.cType(e.cNameOf(name)))
	}

	e.cnodes[name] = n
	return n
}

// cNameOf is the type the C name name is a name of, the 64-bit ones
// the system's.
func (e *Emitter) cNameOf(name string) string {
	if to, ok := cNames[name]; ok {
		return to
	}

	name64 := wide
	if system := e.cfg.OS; system == darwin || system == "" && runtime.GOOS == darwin {
		name64 = wideDarwin
	}

	if name == wordName {
		return unsigned + name64
	}

	return name64
}

// typedef writes a name of the type node to.
func (e *Emitter) typedef(name string, to int) int {
	return e.metaNode("!DIDerivedType(tag: DW_TAG_typedef, name: " + strconv.Quote(name) + ", baseType: !" + strconv.Itoa(to) + ")")
}

// pointerTo writes a pointer to the type node to, or to void for zero.
func (e *Emitter) pointerTo(to int) int {
	base := "null"
	if to != 0 {
		base = "!" + strconv.Itoa(to)
	}

	return e.metaNode("!DIDerivedType(tag: DW_TAG_pointer_type, baseType: " + base + ", size: " + strconv.Itoa(pointerBytes*byteBits) + ")")
}

// declared writes a record the runtime declares and the program never
// sees inside, by its name alone.
func (e *Emitter) declared(name string) int {
	if n, ok := e.cnodes[name]; ok {
		return n
	}

	n := e.metaNode("!DICompositeType(tag: DW_TAG_structure_type, name: " + strconv.Quote(name) + ", flags: DIFlagFwdDecl)")
	e.cnodes[name] = n
	return n
}

// array writes an array of count elements of the type node elem, size
// bytes in all.
func (e *Emitter) array(elem int, size, count int64) int {
	return e.metaNode("!DICompositeType(tag: DW_TAG_array_type, baseType: !" + strconv.Itoa(elem) + ", size: " + debugBits(size) + ", elements: !{!DISubrange(count: " + strconv.FormatInt(count, 10) + ")})")
}

// offsets are the offsets of fields of the types ts laid out as the IR
// lays out a struct of them.
func (e *Emitter) offsets(ts []types.Type) []int64 {
	sizes := make([]int64, len(ts))
	aligns := make([]int64, len(ts))
	for i, t := range ts {
		sizes[i], aligns[i] = e.size(t), e.align(t)
	}

	return layout(sizes, aligns)
}

// layout lays out parts of the sizes and alignments given, each at its
// alignment after the last, as structSize does, and returns their
// offsets.
func layout(sizes, aligns []int64) []int64 {
	offs := make([]int64, len(sizes))
	var n int64
	for i := range sizes {
		n = (n + aligns[i] - 1) / aligns[i] * aligns[i]
		offs[i] = n
		n += sizes[i]
	}

	return offs
}

// lineOf is the line of pos in the file being written, zero for none.
func (e *Emitter) lineOf(pos token.Pos) int {
	if !pos.IsValid() {
		return 0
	}

	return e.file.Position(pos).Line
}

// named writes a name field of a node, or nothing for no name.
func named(name string) string {
	if name == "" {
		return ""
	}

	return "name: " + strconv.Quote(name) + ", "
}

// debugBits writes a size in bytes as the bits the nodes count in.
func debugBits(bytes int64) string {
	return strconv.FormatInt(bytes*byteBits, 10)
}
