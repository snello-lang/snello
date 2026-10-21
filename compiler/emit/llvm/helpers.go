// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llvm

import (
	"strconv"
	"strings"

	"github.com/teak-lang/teak/compiler/emit/lower"
	"github.com/teak-lang/teak/compiler/token"
	"github.com/teak-lang/teak/compiler/types"
)

// Some operations on a composite type need a function of their own:
// turning a value into text, comparing two values, hashing one. They
// are generated on first use, one per type, under the runtime's
// prefix, and take their values by address: a ref receiver is a
// pointer in this backend, so a generated function and a method a
// type wrote for the same purpose are called the same way.

// A text is a string value and whether the expression built it, in
// which case whoever uses it drops it: a string read out of a value is
// only lent.
type text struct {
	v     value
	owned bool
}

// A piece is one part of a string concat builds: a literal, whose
// length is known here, or a text.
type piece struct {
	lit  string
	text text
}

// lit returns the text of a string constant, which nothing owns.
func (e *Emitter) lit(s string) text {
	return text{e.constString(s), false}
}

// join writes the concatenation of two texts, freeing the ones built
// here once joined; the result is built here. A zero text is nothing.
func (e *Emitter) join(a, b text) text {
	if a.v.typ == "" {
		return b
	}

	flags := 0
	if a.owned {
		flags |= 1
	}

	if b.owned {
		flags |= 2
	}

	return text{e.rt("tk_join", "%tk_string", a.v, b.v, value{"i32", strconv.Itoa(flags)}), true}
}

// joined writes the concatenation of open, the parts separated by sep,
// and close, freeing the parts built on the way.
func (e *Emitter) joined(open, close, sep string, parts []text) text {
	out := e.lit(open)
	for i, p := range parts {
		if i > 0 && sep != "" {
			out = e.join(out, e.lit(sep))
		}

		out = e.join(out, p)
	}

	return e.join(out, e.lit(close))
}

// concat writes the concatenation of the pieces as one string the
// caller owns. Two pieces are one tk_join, which allocates once; more
// are built in one allocation too: the whole is sized from the texts'
// lengths and the literals' before a byte is copied, then every piece
// is appended and the texts built here are dropped. fmt's printing
// goes through here, so a line of many operands is copied once, not
// once per operand.
func (e *Emitter) concat(pieces []piece, pos token.Pos) value {
	switch len(pieces) {
	case 0:
		return e.constString("")
	case 1:
		return e.built(e.pieceText(pieces[0]))
	case 2:
		return e.built(e.join(e.pieceText(pieces[0]), e.pieceText(pieces[1])))
	}

	known := 0
	var size value
	for _, p := range pieces {
		if p.text.v.typ == "" {
			known += len(p.lit)
			continue
		}

		n := e.extract(p.text.v, 1, "i64")
		if size.typ == "" {
			size = n
		} else {
			size = e.op("add i64 "+size.s+", "+n.s, "i64")
		}
	}

	if size.typ == "" {
		all := make([]string, len(pieces))
		for i, p := range pieces {
			all[i] = p.lit
		}

		return e.built(e.lit(strings.Join(all, "")))
	}

	if known > 0 {
		size = e.op("add i64 "+size.s+", "+strconv.Itoa(known), "i64")
	}

	buf := e.slot("%tk_slice")
	e.store(value{"%tk_slice", "zeroinitializer"}, buf)
	done := ""
	if known == 0 { // an empty result owns nothing, so no block is made for it
		some := e.op("icmp sgt i64 "+size.s+", 0", "i1")
		alloc := e.block("make")
		done = e.block("made")
		e.branch(some.s, alloc, done)
		e.label(alloc)
	}

	e.rt("tk_slice_make", "void", value{"ptr", buf}, value{"i64", "0"}, size, value{"i64", "1"})
	if done != "" {
		e.jump(done)
		e.label(done)
	}

	for _, p := range pieces {
		e.rt("tk_strings_appendString", "void", value{"ptr", buf}, e.pieceText(p).v)
	}

	for _, p := range pieces {
		if p.text.owned {
			e.dropOf(e.spill(p.text.v), types.Typ[types.String], pos)
		}
	}

	return e.load("%tk_string", buf)
}

// pieceText returns the piece as a text: a literal is lent.
func (e *Emitter) pieceText(p piece) text {
	if p.text.v.typ == "" {
		return e.lit(p.lit)
	}

	return p.text
}

// built writes a text as a string the caller owns: a clone when it was
// only lent, and an empty string for nothing.
func (e *Emitter) built(t text) value {
	switch {
	case t.v.typ == "":
		return e.constString("")
	case !t.owned:
		return e.rt("tk_string_clone", "%tk_string", t.v)
	}

	return t.v
}

// textOf writes the conversion of v, a value of the Teak type t, to a
// string: the value is read through its borrows and pointers and
// turned into text at its address.
func (e *Emitter) textOf(v value, t types.Type, pos token.Pos, quoted bool) text {
	return e.textAt(e.addressOf(v, t), lower.ValueType(t), pos, quoted)
}

// addressOf returns the address of the value under the borrows and
// pointers in front of v, a value of type t: a value held in a
// register is put in a slot for it.
func (e *Emitter) addressOf(v value, t types.Type) string {
	if depth(t) == 0 {
		return e.spill(v)
	}

	for depth(t) > 1 {
		switch u := t.(type) {
		case *types.Ref:
			t = u.Elem()
		case *types.Pointer:
			v = e.deref(v)
			t = u.Elem()
		}

		v = e.load("ptr", v.s)
	}

	return v.s
}

// textAt writes the conversion of the value of type t at the address p
// to a string, and whether the string is built here; quoted says
// whether a string is written in quotes. A type implementing Error
// prints as its message, as the predeclared error does.
func (e *Emitter) textAt(p string, t types.Type, pos token.Pos, quoted bool) text {
	for depth(t) > 0 { // a pointer or a borrow prints as what it points at
		v := e.load("ptr", p)
		switch u := t.(type) {
		case *types.Ref:
			t = u.Elem()
		case *types.Pointer:
			v = e.deref(v)
			t = u.Elem()
		}

		p = v.s
	}

	if r, ok := t.(*types.Ref); ok && view(r) { // a view prints as what it lends
		t = r.Elem()
	}

	if n, ok := t.(*types.Named); ok && (n.Implements(types.ErrorTrait) || types.IsStringer(n)) {
		method := "String"
		if n.Implements(types.ErrorTrait) {
			method = "Error"
		}

		name, _ := e.methodNameOf(nil, n, method, pos)
		msg := e.op("call %tk_string @"+name+"(ptr "+p+")", "%tk_string")
		if quoted {
			return text{e.rt("tk_quote_owned", "%tk_string", msg), true}
		}

		return text{msg, true}
	}

	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return text{e.op("call %tk_string @"+e.stringer(t, pos)+"(ptr "+p+")", "%tk_string"), true}
	}

	ir := e.irType(t, pos)
	v := e.load(ir, p)
	switch {
	case isString(b) && quoted:
		return text{e.rt("tk_quote", "%tk_string", v), true}
	case isString(b):
		return text{v, false}
	case b.Kind() == types.Bool:
		return text{e.rt("tk_bool_to_string", "%tk_string", v), false}
	case b.Info()&types.IsFloat != 0 && ir == "float": // the fewest digits that read back as the same f32
		return text{e.rt("tk_f32_to_string", "%tk_string", v), true}
	case b.Info()&types.IsFloat != 0:
		return text{e.rt("tk_f64_to_string", "%tk_string", v), true}
	case b.Info()&types.IsUnsigned != 0:
		return text{e.rt("tk_uint_to_string", "%tk_string", e.widen(v, false)), true}
	case b.Info()&types.IsInteger != 0:
		return text{e.rt("tk_int_to_string", "%tk_string", e.widen(v, true)), true}
	}

	e.unsupported(pos, "printing values of type "+t.String())
	return e.lit("?")
}

// widen extends an integer value to a word, by its sign or with
// zeros.
func (e *Emitter) widen(v value, signed bool) value {
	if v.typ == "i64" {
		return v
	}

	if signed {
		return e.op("sext "+v.typed()+" to i64", "i64")
	}

	return e.op("zext "+v.typed()+" to i64", "i64")
}

// stringer returns the name of the function that turns a value of a
// composite type into text, writing it on first use: a struct as
// {field field}, or its name when it has no fields; an enum as
// Name(field, field); an option as Some(x) or None; a Result as Ok(x)
// or Err(e); a tuple as (a, b); a slice as [a b]; a map as map[k:v].
// A string inside a constructor is quoted, so the text reads as the
// value would be written; inside a struct, tuple or slice it is not.
func (e *Emitter) stringer(t types.Type, pos token.Pos) string {
	name := "tk_str_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "%tk_string", []string{"ptr %v"}, func() {
		ir := e.irType(t, pos)
		switch u := t.Underlying().(type) {
		case *types.Struct:
			if u.NumFields() == 0 {
				e.ret(e.lit(t.String()))
				return
			}

			var parts []text
			for i := range u.NumFields() {
				if lower.IsLocal(u.Field(i).Type()) {
					continue // the marker that keeps a value in its task holds nothing to print
				}

				parts = append(parts, e.textAt(e.gep(ir, "%v", i), u.Field(i).Type(), pos, false))
			}

			e.ret(e.joined("{", "}", " ", parts))
		case *types.Enum:
			tag := e.load("i32", e.gep(ir, "%v", 0))
			payload := e.gep(ir, "%v", 1)
			e.payloads(u, tag, pos, func(i int, vt string) {
				variant := u.Variant(i)
				parts := make([]text, len(variant.Fields()))
				for j, f := range variant.Fields() {
					parts[j] = e.textAt(e.gep(vt, payload, j), f.Type(), pos, true)
				}

				e.ret(e.joined(variant.Name()+"(", ")", ", ", parts))
			})
			e.names(u, tag)
		case *types.Option:
			has, val := e.optionParts(ir, "%v", u)
			some, none := e.block("some"), e.block("none")
			e.branch(has.s, some, none)
			e.label(none)
			e.ret(e.lit("None"))
			e.label(some)
			e.ret(e.joined("Some(", ")", "", []text{e.textAt(val, u.Elem(), pos, true)}))
		case *types.Result:
			ok := e.load("i1", e.gep(ir, "%v", 0))
			payload := e.gep(ir, "%v", 1)
			yes, no := e.block("ok"), e.block("err")
			e.branch(ok.s, yes, no)
			e.label(no)
			e.ret(e.joined("Err(", ")", "", []text{e.textAt(payload, u.Err(), pos, true)}))
			e.label(yes)
			e.ret(e.joined("Ok(", ")", "", []text{e.textAt(payload, u.Val(), pos, true)}))
		case *types.Tuple:
			parts := make([]text, u.Len())
			for i := range parts {
				parts[i] = e.textAt(e.gep(ir, "%v", i), u.At(i), pos, false)
			}

			e.ret(e.joined("(", ")", ", ", parts))
		case *types.Map:
			out := e.spill(e.constString("map["))
			e.eachEntry("%v", u, pos, func(k, v string) {
				n := e.load("i64", e.gep("%tk_string", out, 1))
				started := e.op("icmp sgt i64 "+n.s+", 4", "i1")
				sep, next := e.block("sep"), e.block("entry")
				e.branch(started.s, sep, next)
				e.label(sep)
				e.store(e.join(text{e.load("%tk_string", out), true}, e.lit(" ")).v, out)
				e.jump(next)
				e.label(next)
				entry := e.join(e.join(text{e.load("%tk_string", out), true}, e.textAt(k, u.Key(), pos, false)), e.lit(":"))
				e.store(e.join(entry, e.textAt(v, u.Value(), pos, false)).v, out)
			})
			e.ret(e.join(text{e.load("%tk_string", out), true}, e.lit("]")))
		case *types.Slice:
			e.elements(func(body func(p, i string)) { e.eachElementIndexed("%v", u.Elem(), pos, body) }, u.Elem(), pos)
		case *types.Array:
			e.elements(func(body func(p, i string)) {
				for i := int64(0); i < u.Len(); i++ {
					body(e.gep(ir, "%v", int(i)), strconv.FormatInt(i, 10))
				}
			}, u.Elem(), pos)
		case *types.Trait:
			e.unsupported(pos, "printing values of type "+t.String()) // the held value's type is not known here
			e.ret(e.lit("?"))
		default:
			e.ret(e.textAt("%v", t, pos, false))
		}
	})
	return name
}

// names writes the blocks an enum's variants without fields reach,
// each returning the variant's name, and a block for a tag the enum
// does not have, which returns a question mark. It ends the function.
func (e *Emitter) names(u *types.Enum, tag value) {
	var cases []string
	blocks := make([]string, u.NumVariants())
	for i := 0; i < u.NumVariants(); i++ {
		if len(u.Variant(i).Fields()) > 0 {
			continue
		}

		blocks[i] = e.block("name")
		cases = append(cases, "i32 "+strconv.Itoa(i)+", label %"+blocks[i])
	}

	other := e.block("other")
	e.inst("switch i32 " + tag.s + ", label %" + other + " [ " + strings.Join(cases, " ") + " ]")
	e.fn.terminated = true
	for i, b := range blocks {
		if b == "" {
			continue
		}

		e.label(b)
		e.ret(e.lit(u.Variant(i).Name()))
	}

	e.label(other)
	e.ret(e.lit("?"))
}

// elements writes the body of a printer over elements reached by each,
// [a b c], as Go prints a slice or an array, and ends the function.
func (e *Emitter) elements(each func(body func(p, i string)), elem types.Type, pos token.Pos) {
	out := e.spill(e.constString("["))
	each(func(p, i string) {
		first := e.op("icmp eq i64 "+i+", 0", "i1")
		sep, next := e.block("sep"), e.block("elem")
		e.branch(first.s, next, sep)
		e.label(sep)
		e.store(e.join(text{e.load("%tk_string", out), true}, e.lit(" ")).v, out)
		e.jump(next)
		e.label(next)
		e.store(e.join(text{e.load("%tk_string", out), true}, e.textAt(p, elem, pos, false)).v, out)
	})
	e.ret(e.join(text{e.load("%tk_string", out), true}, e.lit("]")))
}

// ret ends the function being written returning the text as a string
// the caller owns.
func (e *Emitter) ret(t text) {
	e.inst("ret " + e.built(t).typed())
	e.fn.terminated = true
}

// payloads writes a switch on tag over the variants of u that carry
// fields, calling f for each in a block of its own with the IR type of
// its fields, and a block after the switch for the variants that carry
// none; a function written part by part handles the payload of
// whichever variant the value holds. A block f leaves open jumps to
// the block after.
func (e *Emitter) payloads(u *types.Enum, tag value, pos token.Pos, f func(i int, vt string)) {
	done := e.block("payloads.done")
	var cases []string
	blocks := make([]string, u.NumVariants())
	for i := 0; i < u.NumVariants(); i++ {
		if len(u.Variant(i).Fields()) == 0 {
			continue
		}

		blocks[i] = e.block("payload")
		cases = append(cases, "i32 "+strconv.Itoa(i)+", label %"+blocks[i])
	}

	e.inst("switch i32 " + tag.s + ", label %" + done + " [ " + strings.Join(cases, " ") + " ]")
	e.fn.terminated = true
	for i, b := range blocks {
		if b == "" {
			continue
		}

		e.label(b)
		f(i, e.variantType(u.Variant(i).Fields(), pos))
		if !e.fn.terminated {
			e.jump(done)
		}
	}

	e.label(done)
}

// equal writes the test that the values of type t at the addresses a
// and b are equal: the Eq method of a type that wrote one, the
// operator for numbers and booleans, the runtime for strings, and a
// generated function for anything composite.
func (e *Emitter) equal(a, b string, t types.Type, pos token.Pos) value {
	t = lower.ValueType(t)
	if n, ok := t.(*types.Named); ok && written(n, "Eq") { // the type says how; a derived Eq is the comparison of the parts below
		name, _ := e.methodNameOf(nil, n, "Eq", pos)
		return e.op("call i1 @"+name+"(ptr "+a+", ptr "+b+")", "i1")
	}

	if basic, ok := t.Underlying().(*types.Basic); ok {
		ir := e.irType(t, pos)
		l, r := e.load(ir, a), e.load(ir, b)
		if isString(basic) {
			return e.rt("tk_string_eq", "i1", l, r)
		}

		if isFloat(basic) {
			return e.op("fcmp oeq "+l.typed()+", "+r.s, "i1")
		}

		return e.op("icmp eq "+l.typed()+", "+r.s, "i1")
	}

	if _, ok := t.Underlying().(*types.Chan); ok { // handles on one channel are equal, as Go's are
		l, r := e.load("ptr", a), e.load("ptr", b)
		return e.op("icmp eq "+l.typed()+", "+r.s, "i1")
	}

	return e.op("call i1 @"+e.comparer(t, pos)+"(ptr "+a+", ptr "+b+")", "i1")
}

// comparer returns the name of the function comparing two values of a
// composite type part by part, writing it on first use.
func (e *Emitter) comparer(t types.Type, pos token.Pos) string {
	name := "tk_eq_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "i1", []string{"ptr %a", "ptr %b"}, func() {
		ir := e.irType(t, pos)
		differ := e.block("differ")
		same := func(a, b string, pt types.Type) {
			next := e.block("same")
			e.branch(e.equal(a, b, pt, pos).s, next, differ)
			e.label(next)
		}

		switch u := t.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				same(e.gep(ir, "%a", i), e.gep(ir, "%b", i), u.Field(i).Type())
			}
		case *types.Enum:
			ta, tb := e.load("i32", e.gep(ir, "%a", 0)), e.load("i32", e.gep(ir, "%b", 0))
			tags := e.block("tags")
			e.branch(e.op("icmp eq "+ta.typed()+", "+tb.s, "i1").s, tags, differ)
			e.label(tags)
			pa, pb := e.gep(ir, "%a", 1), e.gep(ir, "%b", 1)
			e.payloads(u, ta, pos, func(i int, vt string) {
				for j, f := range u.Variant(i).Fields() {
					same(e.gep(vt, pa, j), e.gep(vt, pb, j), f.Type())
				}
			})
		case *types.Option:
			ha, va := e.optionParts(ir, "%a", u)
			hb, vb := e.optionParts(ir, "%b", u)
			flags, both := e.block("flags"), e.block("both")
			e.branch(e.op("icmp eq "+ha.typed()+", "+hb.s, "i1").s, flags, differ)
			e.label(flags)
			none := e.block("none")
			e.branch(ha.s, both, none)
			e.label(none)
			e.inst("ret i1 true")
			e.fn.terminated = true
			e.label(both)
			same(va, vb, u.Elem())
		case *types.Result:
			oa, ob := e.load("i1", e.gep(ir, "%a", 0)), e.load("i1", e.gep(ir, "%b", 0))
			flags := e.block("flags")
			e.branch(e.op("icmp eq "+oa.typed()+", "+ob.s, "i1").s, flags, differ)
			e.label(flags)
			pa, pb := e.gep(ir, "%a", 1), e.gep(ir, "%b", 1)
			yes, no := e.block("ok"), e.block("err")
			e.branch(oa.s, yes, no)
			e.label(no)
			same(pa, pb, u.Err())
			e.inst("ret i1 true")
			e.fn.terminated = true
			e.label(yes)
			same(pa, pb, u.Val())
		case *types.Tuple:
			for i := 0; i < u.Len(); i++ {
				same(e.gep(ir, "%a", i), e.gep(ir, "%b", i), u.At(i))
			}
		case *types.Pointer:
			same(e.deref(e.load("ptr", "%a")).s, e.deref(e.load("ptr", "%b")).s, u.Elem())
		case *types.Slice:
			na, nb := e.load("i64", e.gep(ir, "%a", 1)), e.load("i64", e.gep(ir, "%b", 1))
			lens := e.block("lens")
			e.branch(e.op("icmp eq "+na.typed()+", "+nb.s, "i1").s, lens, differ)
			e.label(lens)
			pb := e.load("ptr", e.gep(ir, "%b", 0))
			et := e.irType(u.Elem(), pos)
			e.eachElementIndexed("%a", u.Elem(), pos, func(p, i string) {
				same(p, e.elem(et, pb.s, i), u.Elem())
			})
		case *types.Array:
			for i := int64(0); i < u.Len(); i++ {
				same(e.gep(ir, "%a", int(i)), e.gep(ir, "%b", int(i)), u.Elem())
			}
		default:
			e.unsupported(pos, "comparing values of type "+t.String())
		}

		e.inst("ret i1 true")
		e.fn.terminated = true
		e.label(differ)
		e.inst("ret i1 false")
		e.fn.terminated = true
	})
	return name
}

// hash writes the hash of the value of key type t at the address p:
// the Hash method of a type that wrote one, the bytes of a string, the
// value of a number, and a mix of the parts for a tuple, struct,
// option or enum, which is also what a derived Hash means.
func (e *Emitter) hash(p string, t types.Type, pos token.Pos) value {
	t = lower.ValueType(t)
	if n, ok := t.(*types.Named); ok && written(n, "Hash") {
		name, _ := e.methodNameOf(nil, n, "Hash", pos)
		return e.op("call i64 @"+name+"(ptr "+p+")", "i64")
	}

	if _, ok := t.Underlying().(*types.Enum); ok {
		return e.op("call i64 @"+e.hasher(t, pos)+"(ptr "+p+")", "i64")
	}

	return e.hashParts(p, t, pos)
}

// hashParts writes the hash of the value of type t at the address p
// from its parts, without asking for a method: what a derived Hash is.
func (e *Emitter) hashParts(p string, t types.Type, pos token.Pos) value {
	ir := e.irType(t, pos)
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case isString(u):
			s := e.load(ir, p)
			return e.rt("tk_hash_bytes", "i64", e.extract(s, 0, "ptr"), e.extract(s, 1, "i64"))
		case isFloat(u):
			v := e.load(ir, p)
			h := e.rt("tk_hash_bytes", "i64", value{"ptr", p}, value{"i64", strconv.FormatInt(e.size(t), 10)})
			isZero := e.op("fcmp oeq "+v.typed()+", 0.0", "i1")
			return e.op("select i1 "+isZero.s+", i64 0, "+h.typed(), "i64") // 0.0 and -0.0 are equal, so they hash alike
		}

		return e.mix(value{"i64", "0"}, e.widen(e.load(ir, p), false))
	case *types.Chan: // the channel it is a handle on
		return e.mix(value{"i64", "0"}, e.op("ptrtoint ptr "+e.load("ptr", p).s+" to i64", "i64"))
	case *types.Option:
		has, val := e.optionParts(ir, p, u)
		some, done := e.block("some"), e.block("hashed")
		from := e.block("none")
		e.jump(from)
		e.label(from)
		e.branch(has.s, some, done)
		e.label(some)
		h := e.mix(value{"i64", "1"}, e.hash(val, u.Elem(), pos))
		fromSome := e.block("from.some")
		e.jump(fromSome)
		e.label(fromSome)
		e.jump(done)
		e.label(done)
		return e.op("phi i64 [ 0, %"+from+" ], [ "+h.s+", %"+fromSome+" ]", "i64")
	case *types.Tuple:
		h := value{"i64", "0"}
		for i := 0; i < u.Len(); i++ {
			h = e.mix(h, e.hash(e.gep(ir, p, i), u.At(i), pos))
		}

		return h
	case *types.Struct:
		h := value{"i64", "0"}
		for i := 0; i < u.NumFields(); i++ {
			h = e.mix(h, e.hash(e.gep(ir, p, i), u.Field(i).Type(), pos))
		}

		return h
	}

	e.unsupported(pos, "map keys of type "+t.String())
	return value{"i64", "0"}
}

// mix folds v into the hash h through the runtime.
func (e *Emitter) mix(h, v value) value {
	return e.rt("tk_hash_mix", "i64", h, v)
}

// hasher returns the name of the function hashing a value of a
// declared type part by part, the derived Hash, writing it on first
// use; an enum mixes its tag with the payload of its variant.
func (e *Emitter) hasher(t types.Type, pos token.Pos) string {
	name := "tk_hash_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	e.helper(name, "i64", []string{"ptr %v"}, func() {
		u, ok := t.Underlying().(*types.Enum)
		if !ok {
			e.inst("ret " + e.hashParts("%v", t, pos).typed())
			e.fn.terminated = true
			return
		}

		ir := e.irType(t, pos)
		tag := e.load("i32", e.gep(ir, "%v", 0))
		h := e.spill(e.mix(value{"i64", "0"}, e.widen(tag, false)))
		payload := e.gep(ir, "%v", 1)
		e.payloads(u, tag, pos, func(i int, vt string) {
			for j, f := range u.Variant(i).Fields() {
				e.store(e.mix(e.load("i64", h), e.hash(e.gep(vt, payload, j), f.Type(), pos)), h)
			}
		})
		e.inst("ret " + e.load("i64", h).typed())
		e.fn.terminated = true
	})
	return name
}

// less writes whether the number l orders before r, both of type t.
// Floats take Go's order, the one sort and cmp.Compare use: a NaN
// before every other value and level with another NaN, so a sort
// with NaN in it is ordered and IsSorted agrees with it.
func (e *Emitter) less(l, r value, t types.Type) value {
	if !isFloat(t) {
		name, _ := compare(token.LSS, false, isUnsigned(t))
		return e.op("icmp "+name+" "+l.typed()+", "+r.s, "i1")
	}

	lt := e.op("fcmp olt "+l.typed()+", "+r.s, "i1")
	nan := e.op("fcmp uno "+l.typed()+", "+l.s, "i1")
	number := e.op("fcmp ord "+r.typed()+", "+r.s, "i1")
	first := e.op("and i1 "+nan.s+", "+number.s, "i1")
	return e.op("or i1 "+lt.s+", "+first.s, "i1")
}

// compare writes the Ordering of the values of type t at the addresses
// a and b: numbers and strings through the comparisons the backend
// has, a declared type through its Cmp, written or derived.
func (e *Emitter) compare(a, b string, t types.Type, pos token.Pos) value {
	t = lower.ValueType(t)
	ord := e.irType(types.Ordering, pos)
	if n, ok := t.(*types.Named); ok && n.Method("Cmp") != nil {
		name, _ := e.methodNameOf(nil, n, "Cmp", pos)
		return e.op("call "+ord+" @"+name+"(ptr "+a+", ptr "+b+")", ord)
	}

	if basic, ok := t.Underlying().(*types.Basic); ok {
		ir := e.irType(t, pos)
		l, r := e.load(ir, a), e.load(ir, b)
		if isString(basic) {
			return e.order(e.rt("tk_string_cmp", "i32", l, r))
		}

		less := e.op("zext i1 "+e.less(l, r, t).s+" to i32", "i32")
		more := e.op("zext i1 "+e.less(r, l, t).s+" to i32", "i32")
		return e.order(e.op("sub i32 "+more.s+", "+less.s, "i32"))
	}

	e.unsupported(pos, "ordering values of type "+t.String())
	return value{ord, "zeroinitializer"}
}

// order turns an i32 comparison result, negative, zero or positive,
// into an Ordering: Less, Equal or Greater, the tags 0, 1 and 2.
func (e *Emitter) order(c value) value {
	ord := e.irType(types.Ordering, token.NoPos)
	neg := e.op("icmp slt "+c.typed()+", 0", "i1")
	posv := e.op("icmp sgt "+c.typed()+", 0", "i1")
	tag := e.op("select i1 "+posv.s+", i32 2, i32 1", "i32")
	tag = e.op("select i1 "+neg.s+", i32 0, "+tag.typed(), "i32")
	return e.insert(value{ord, "zeroinitializer"}, tag, 0)
}

// orderer returns the name of the function comparing two values of a
// declared type part by part, the derived Cmp, writing it on first use:
// the first part that differs decides, and an enum's variants are in
// declaration order, with the payloads compared when the variants
// agree.
func (e *Emitter) orderer(t types.Type, pos token.Pos) string {
	name := "tk_cmp_" + lower.Mangle(t)
	if e.state[name] {
		return name
	}

	e.state[name] = true
	ord := e.irType(types.Ordering, pos)
	e.helper(name, ord, []string{"ptr %a", "ptr %b"}, func() {
		ir := e.irType(t, pos)
		decide := func(a, b string, pt types.Type) {
			r := e.compare(a, b, pt, pos)
			tag := e.extract(r, 0, "i32")
			equal := e.op("icmp eq "+tag.typed()+", 1", "i1")
			next, differ := e.block("equal"), e.block("decided")
			e.branch(equal.s, next, differ)
			e.label(differ)
			e.inst("ret " + r.typed())
			e.fn.terminated = true
			e.label(next)
		}

		switch u := t.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < u.NumFields(); i++ {
				decide(e.gep(ir, "%a", i), e.gep(ir, "%b", i), u.Field(i).Type())
			}
		case *types.Enum:
			ta, tb := e.load("i32", e.gep(ir, "%a", 0)), e.load("i32", e.gep(ir, "%b", 0))
			tags, differ := e.block("tags"), e.block("tags.differ")
			e.branch(e.op("icmp eq "+ta.typed()+", "+tb.s, "i1").s, tags, differ)
			e.label(differ)
			e.inst("ret " + e.order(e.op("sub i32 "+ta.s+", "+tb.s, "i32")).typed())
			e.fn.terminated = true
			e.label(tags)
			pa, pb := e.gep(ir, "%a", 1), e.gep(ir, "%b", 1)
			e.payloads(u, ta, pos, func(i int, vt string) {
				for j, f := range u.Variant(i).Fields() {
					decide(e.gep(vt, pa, j), e.gep(vt, pb, j), f.Type())
				}
			})
		default:
			e.unsupported(pos, "ordering values of type "+t.String())
		}

		e.inst("ret " + e.order(value{"i32", "0"}).typed())
		e.fn.terminated = true
	})
	return name
}
