// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package natvis writes the visualizers Windows' debuggers show a Snello
// program's values by, as the program writes them: a string as its
// text, a slice as its length and its elements, an option as None or
// Some, a result as Ok or Err, an enum as its case and the case's
// values, and a map as its length. A visualizer names the type it is
// for, and a slice's, an option's, a result's and an enum's are as
// many as the program has, so the visualizers of a program are written
// from its own debugging information: every type of one of those kinds
// the program describes is given one. The file is a natvis file, which
// the linker embeds in the program's PDB, where WinDbg, cdb and Visual
// Studio find it.
package natvis

import (
	"bytes"
	"debug/dwarf"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The names the backends give the runtime's records and the compiler's
// types, as the debugger's script for lldb and gdb reads them: a
// string, a map, the start of a slice's, an option's and a result's
// type, and an enum's tag, an enumeration named with tagPrefix whose
// enumerators are casePrefix, the length of the enum's name, the name
// and the case's, joined by caseSeparator; a member named as C may not
// name one has clashSuffix after it.
const (
	stringType    = "sn_string"
	mapType       = "sn_map"
	slicePrefix   = "sn_slice_"
	optionPrefix  = "sn_opt_"
	resultPrefix  = "sn_res_"
	tagPrefix     = "sn_tag_"
	casePrefix    = "sn_case_"
	caseSeparator = "_"
	clashSuffix   = "_"
	tagMember     = "tag"

	// The members of those records.
	ptrMember   = "ptr"
	lenMember   = "len"
	hasMember   = "has"
	valMember   = "val"
	okMember    = "ok"
	errMember   = "err"
	countMember = "count"

	// unionKind is the kind of a union among a struct's types.
	unionKind = "union"

	// header opens the file and footer closes it.
	header = `<?xml version="1.0" encoding="utf-8"?>
<AutoVisualizer xmlns="http://schemas.microsoft.com/vstudio/debugger/natvis/2010">
`
	footer = "</AutoVisualizer>\n"
)

// xmlEntities are the characters XML gives a meaning, and their
// entities.
var xmlEntities = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// fixed are the visualizers of the runtime's own records, one type
// each, whatever the program: a string, its bytes as UTF-8 text, and a
// map, every map's type a name for the one record.
var fixed = []visualizer{
	{name: stringType, shown: []shown{{when: lenMember + " == 0", text: `""`}, {text: "{" + ptrMember + ",[" + lenMember + "]s8}"}}},
	{name: mapType, shown: []shown{{text: "len={" + countMember + "}"}}},
}

// A visualizer is one type's: the texts it is shown as, the first whose
// condition holds, and for a slice the elements it expands to.
type visualizer struct {
	name  string
	shown []shown
	slice bool
}

// A shown is a text a value is shown as, when the condition holds or
// always.
type shown struct {
	when string
	text string
}

// FromDWARF writes the natvis file of the program whose debugging
// information is d: the runtime's records' visualizers, and one for
// each slice, option, result and enum type d describes, once each,
// sorted by name.
func FromDWARF(d *dwarf.Data) ([]byte, error) {
	seen := map[string]bool{}
	var found []visualizer
	r := d.Reader()
	for {
		e, err := r.Next()
		if err != nil {
			return nil, err
		}

		if e == nil {
			break
		}

		if e.Tag != dwarf.TagStructType {
			continue
		}

		name, _ := e.Val(dwarf.AttrName).(string)
		if name == "" || seen[name] {
			continue
		}

		t, err := d.Type(e.Offset)
		if err != nil {
			continue // a type debug/dwarf cannot read, as a bound library's C may give, is shown by its members
		}

		s, ok := t.(*dwarf.StructType)
		if !ok || s.Incomplete {
			continue
		}

		if v, ok := visualizerOf(s); ok {
			seen[name] = true
			found = append(found, v)
		}
	}

	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })
	var out bytes.Buffer
	out.WriteString(header)
	for _, v := range append(append([]visualizer(nil), fixed...), found...) {
		v.write(&out)
	}

	out.WriteString(footer)
	return out.Bytes(), nil
}

// visualizerOf returns the visualizer of the struct s, and false for one
// of no kind the debugger shows otherwise than as its members.
func visualizerOf(s *dwarf.StructType) (visualizer, bool) {
	v := visualizer{name: s.StructName}
	switch {
	case strings.HasPrefix(s.StructName, slicePrefix):
		v.slice = true
		v.shown = []shown{{text: "len={" + lenMember + "}"}}
	case strings.HasPrefix(s.StructName, optionPrefix):
		absent := hasMember + " == 0"
		if fieldOf(s, hasMember) == nil { // an option of a pointer is the pointer, null for None
			absent = valMember + " == 0"
			if isReference(fieldOf(s, valMember)) {
				absent = "&" + absent // a reference's address, not the value it names
			}
		}

		v.shown = []shown{{when: absent, text: "None"}, {text: "Some({" + valMember + "})"}}
	case strings.HasPrefix(s.StructName, resultPrefix):
		v.shown = []shown{{when: okMember + " != 0", text: "Ok({" + valMember + "})"}, {text: "Err({" + errMember + "})"}}
	default:
		cases, ok := enumCases(s)
		if !ok {
			return visualizer{}, false
		}

		v.shown = cases
	}

	return v, true
}

// enumCases returns the texts of an enum's struct, one for each case:
// its name, with its values when it has any, when the tag names it; and
// false for a struct that is no enum's, whose first member is no tag.
func enumCases(s *dwarf.StructType) ([]shown, bool) {
	if len(s.Field) == 0 || s.Field[0].Name != tagMember {
		return nil, false
	}

	tag, ok := s.Field[0].Type.(*dwarf.EnumType)
	if !ok || !strings.HasPrefix(tag.EnumName, tagPrefix) {
		return nil, false
	}

	payloads := map[string]*dwarf.StructType{} // each case's fields, by the union's member that holds them
	for _, f := range s.Field[1:] {
		if u, ok := f.Type.(*dwarf.StructType); ok && u.Kind == unionKind {
			for _, m := range u.Field {
				if p, ok := m.Type.(*dwarf.StructType); ok {
					payloads[m.Name] = p
				}
			}
		}
	}

	var out []shown
	for _, v := range tag.Val {
		name := caseOf(v.Name)
		text := name
		for _, member := range []string{name, name + clashSuffix} {
			p := payloads[member]
			if p == nil || len(p.Field) == 0 {
				continue
			}

			var parts []string
			for _, f := range p.Field {
				parts = append(parts, "{"+member+"."+f.Name+"}")
			}

			text = name + "(" + strings.Join(parts, ", ") + ")"
			break
		}

		out = append(out, shown{when: tagMember + " == " + strconv.FormatInt(v.Val, 10), text: text})
	}

	return out, true
}

// caseOf returns the case an enumerator of an enum's tag names: what
// follows the enum's name, whose length in letters the enumerator
// gives, sn_case_5_Shape_Rect; the enumerator itself when it is not
// one.
func caseOf(enumerator string) string {
	rest, ok := strings.CutPrefix(enumerator, casePrefix)
	if !ok {
		return enumerator
	}

	length, named, ok := strings.Cut(rest, caseSeparator)
	n, err := strconv.Atoi(length)
	if !ok || err != nil || n < 0 {
		return enumerator
	}

	for i := range named { // i is the byte offset of each letter in turn
		if n == 0 {
			if c, ok := strings.CutPrefix(named[i:], caseSeparator); ok {
				return c
			}

			return enumerator
		}

		n--
	}

	return enumerator
}

// isReference reports whether the field is a reference, as the LLVM
// backend describes an option of a borrow's value, which debug/dwarf
// reads as a type it does not model.
func isReference(f *dwarf.StructField) bool {
	if f == nil {
		return false
	}

	u, ok := f.Type.(*dwarf.UnsupportedType)
	return ok && u.Tag == dwarf.TagReferenceType
}

// fieldOf returns the field of s named name, or nil.
func fieldOf(s *dwarf.StructType, name string) *dwarf.StructField {
	for _, f := range s.Field {
		if f.Name == name {
			return f
		}
	}

	return nil
}

// write writes the visualizer as the natvis file's Type element.
func (v visualizer) write(out *bytes.Buffer) {
	fmt.Fprintf(out, "  <Type Name=\"%s\">\n", escape(v.name))
	for _, s := range v.shown {
		out.WriteString("    <DisplayString")
		if s.when != "" {
			fmt.Fprintf(out, " Condition=\"%s\"", escape(s.when))
		}

		fmt.Fprintf(out, ">%s</DisplayString>\n", escape(s.text))
	}

	if v.slice {
		fmt.Fprintf(out, "    <Expand>\n      <ArrayItems>\n        <Size>%s</Size>\n        <ValuePointer>%s</ValuePointer>\n      </ArrayItems>\n    </Expand>\n", lenMember, ptrMember)
	}

	out.WriteString("  </Type>\n")
}

// escape writes the characters XML gives a meaning in a text or an
// attribute's value as the entities that stand for them.
func escape(text string) string {
	return xmlEntities.Replace(text)
}
