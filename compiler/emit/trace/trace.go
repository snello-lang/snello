// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package trace writes the table a Snello program carries for the trace
// a panic prints: from the line tables the compiler behind a backend
// wrote into the native object the program is linked from, the rows of
// each function, each the chain of frames at an offset into it, as the
// runtime reads them (runtime/c/trace.c over snello.h's sn_trace); and,
// once the program is linked, the address of each function, written
// into the binary, where the function is whole wherever the linker put
// it.
package trace

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	// cloneMark starts what a C compiler adds to the name of a copy it
	// makes of a function, foo.isra.0, whose frame is the function's.
	cloneMark = "."

	// tableSymbol names the table in the program, snello.h's
	// sn_trace_table, headerBytes long, whose first word, the reference,
	// takes its own linked address, from which the runtime learns how far
	// the loader moved the program, and whose next words are each part's
	// offset after partsSymbol and its count, anchorsField the offset of
	// the anchors. anchorBytes, rowBytes, frameBytes and offsetBytes are
	// the sizes of an anchor, a row, a frame and a name's offset, as
	// snello.h lays them out; an anchor's address is its first word,
	// written once the binary is linked. unwritten is the reference of a
	// table not yet written, as the runtime knows it.
	tableSymbol = "sn_trace"
	partsSymbol = "sn_trace_parts"
	anchorBytes = 24
	rowBytes    = 8
	frameBytes  = 16
	offsetBytes = 4
	unwritten   = 1

	// A Mach-O section's type is the low byte of its flags; zeroFill is
	// the type of a section of zeros, which the file holds no bytes of.
	// weakDefinition marks, in a Mach-O symbol's description, a weak
	// definition, which a strong one replaces at the link.
	sectionType    = 0xff
	zeroFill       = 0x1
	weakDefinition = 0x0080

	// snelloSuffix ends the name of a file of Snello source: a frame at
	// a line of any other file, the runtime's C, is the runtime's own.
	snelloSuffix = ".snello"

	// textSection names the one text section of a Mach-O object; an ELF
	// object's is any section of instructions.
	textSection = "__text"

	// noFrame is a row's frame, or a frame's caller, when there is none,
	// as the runtime reads it; unscoped marks, while a section's rows are
	// read, a row whose frame is its function's, from the symbol: a
	// compiler writing line tables alone writes no scope for a function
	// nothing is written into.
	noFrame  = -1
	unscoped = -2
)

// A header is the table as snello.h's sn_trace_table lays it out: the
// reference, and each part's offset after the parts' start and its
// count.
type header struct {
	Reference         uint64
	Anchors, NAnchors int64
	Rows, NRows       int64
	Frames, NFrames   int64
	Symbols, NSymbols int64
	Files, NFiles     int64
	Strings           int64
}

// headerBytes is the header's size, and anchorsField the offset of the
// anchors' offset in it, the word after the reference.
var (
	headerBytes  = uint64(binary.Size(header{}))
	anchorsField = uint64(binary.Size(header{}.Reference))
)

// A Layout is what Patch writes into the binary: the symbol of each
// anchor of the table, in the table's order, whose address in the
// binary is the anchor's.
type Layout struct {
	Anchors []string
}

// An object is what the reader needs of a native object: its text
// sections, each with its symbols by offset, and its DWARF.
type object struct {
	sections []section
	dwarf    *dwarf.Data
}

type section struct {
	name    string
	address uint64 // where its first byte is in the object's address space, which the DWARF's addresses are in
	size    uint64
	symbols []symbol
}

type symbol struct {
	name   string
	offset uint64
}

// Table reads the native objects at paths, the one the link-time
// optimizer wrote or the objects of a debug build, and returns the
// program's table, the header and the parts after it as the runtime
// reads them, and the layout to write into the binary. An object with
// no line tables, the runtime's in a debug build, adds its functions
// with no rows, which the walk passes over; a program with no table at
// all links with the runtime's own empty one.
func Table(paths ...string) ([]byte, *Layout, error) {
	var b builder
	b.frames = map[frameKey]int32{}
	b.files = map[string]int32{}
	b.symbols = map[string]int32{}
	b.ownFiles = map[string]string{}
	layout := &Layout{}
	var anchors []anchorRows
	for _, path := range paths {
		obj, err := read(path)
		if err != nil {
			return nil, nil, err
		}

		if err := b.object(obj, layout, &anchors); err != nil {
			return nil, nil, fmt.Errorf("%s: %v", path, err)
		}
	}

	return b.table(anchors), layout, nil
}

// object adds one object's functions to the table: an anchor and the
// rows of each.
func (b *builder) object(obj *object, layout *Layout, anchors *[]anchorRows) error {
	for _, s := range obj.sections {
		if len(s.symbols) == 0 {
			continue
		}

		rows, err := b.rows(obj.dwarf, s)
		if err != nil {
			return err
		}

		sort.SliceStable(s.symbols, func(a, b int) bool { return s.symbols[a].offset < s.symbols[b].offset })
		for i, sym := range s.symbols { // each function's rows: those from its offset to the next function's
			end := s.size
			if i+1 < len(s.symbols) {
				end = s.symbols[i+1].offset
			}

			if end <= sym.offset {
				continue
			}

			layout.Anchors = append(layout.Anchors, sym.name) // every function, rows or none: the walk passes over a frame of the runtime's and ends at one of the C library's
			*anchors = append(*anchors, anchorRows{end - sym.offset, functionRows(rows, sym.offset, end, b.own(sym.name))})
		}
	}

	return nil
}

// functionRows is the rows of the function at [start, end) of its
// section, each with its offset from start, the row the function
// begins in first, an unscoped row's frame resolved by own; none when
// the function has no frame of a Snello function, which still has an
// anchor, so that the walk knows its frames for the runtime's.
func functionRows(rows []row, start, end uint64, own func(row) int32) []row {
	var out []row
	for _, r := range rows {
		if r.offset >= end {
			break
		}

		if r.frame == unscoped {
			r.frame = noFrame
			if r.offset >= start { // a row of another function reaching into this one is no frame of either
				r.frame = own(r)
			}
		}

		if r.offset < start {
			if len(out) == 0 {
				out = append(out, row{offset: 0, frame: r.frame}) // the row the function begins in
			} else {
				out[0] = row{offset: 0, frame: r.frame}
			}

			continue
		}

		if len(out) > 0 && out[len(out)-1].frame == r.frame {
			continue
		}

		if len(out) > 0 && out[len(out)-1].offset == r.offset-start { // the row the function begins in, now its own
			out[len(out)-1].frame = r.frame
			continue
		}

		out = append(out, row{offset: r.offset - start, frame: r.frame})
	}

	for _, r := range out {
		if r.frame >= 0 {
			return out
		}
	}

	return nil
}

// own resolves an unscoped row of the function whose symbol is name to
// the function's own frame at the row's file and line, or to none when
// the line is no file of the program's.
func (b *builder) own(name string) func(row) int32 {
	return func(r row) int32 {
		if !strings.HasSuffix(r.file, snelloSuffix) {
			return noFrame
		}

		return b.frame(frameKey{b.symbol(name), b.file(baseName(r.file)), r.line, noFrame})
	}
}

// read opens the object as Mach-O or ELF and reads what the table
// needs, an ELF object laid out first, as layOut says.
func read(path string) (*object, error) {
	if m, err := macho.Open(path); err == nil {
		defer m.Close()
		return readMacho(m)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if _, err := elf.NewFile(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("%s: neither Mach-O nor ELF", path)
	}

	if data, err = layOut(data); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}

	e, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	return readELF(e)
}

func readMacho(m *macho.File) (*object, error) {
	obj := &object{}
	for i, s := range m.Sections {
		if s.Name != textSection || s.Seg != "__TEXT" {
			continue
		}

		sec := section{name: s.Name, address: s.Addr, size: s.Size}
		if m.Symtab != nil {
			for _, sym := range m.Symtab.Syms {
				if int(sym.Sect) == i+1 && sym.Type&0xe0 == 0 && sym.Value >= s.Addr && sym.Value < s.Addr+s.Size && !temporary(sym.Name) { // in the section, no debugging symbol
					sec.symbols = append(sec.symbols, symbol{strings.TrimPrefix(sym.Name, "_"), sym.Value - s.Addr})
				}
			}
		}

		obj.sections = append(obj.sections, sec)
	}

	if d, err := m.DWARF(); err == nil {
		obj.dwarf = d
	}

	return obj, nil
}

func readELF(e *elf.File) (*object, error) {
	obj := &object{}
	syms, err := e.Symbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return nil, err
	}

	for i, s := range e.Sections {
		if s.Type != elf.SHT_PROGBITS || s.Flags&elf.SHF_EXECINSTR == 0 || s.Size == 0 {
			continue
		}

		sec := section{name: s.Name, address: s.Addr, size: s.Size}
		for _, sym := range syms {
			if int(sym.Section) == i && elf.ST_TYPE(sym.Info) == elf.STT_FUNC && sym.Name != "" { // the runtime's assembly marks its functions; a symbol of no type marks code or data
				sec.symbols = append(sec.symbols, symbol{sym.Name, sym.Value - s.Addr})
			}
		}

		obj.sections = append(obj.sections, sec)
	}

	if d, err := e.DWARF(); err == nil {
		obj.dwarf = d
	}

	return obj, nil
}

// temporary reports whether a Mach-O symbol is the assembler's own, a
// label the linker leaves out of the binary, which the table can hold
// nothing by.
func temporary(name string) bool {
	return strings.HasPrefix(name, "ltmp") || strings.HasPrefix(name, "L") || strings.HasPrefix(name, "l_")
}

// A frameKey identifies a frame of a chain: the function's symbol, the
// file, the line and the caller's frame, each by index.
type frameKey struct {
	function, file, line, caller int32
}

// A builder gathers the table's parts, each entry once.
type builder struct {
	ownFiles   map[string]string // each function's own file of the program, by name, for its code at no line
	frames     map[frameKey]int32
	frameList  []frameKey
	files      map[string]int32
	fileList   []string
	symbols    map[string]int32
	symbolList []string
}

// A row is a change of frame at an offset of its section: the frame,
// or unscoped with the row's file and line, for a row no scope of the
// debugging information covers, whose frame is its function's own
// once the function's symbol is known.
type row struct {
	offset uint64
	frame  int32
	file   string
	line   int32
}

type anchorRows struct {
	size uint64 // of the function
	rows []row  // with their offsets from its start
}

func (b *builder) file(name string) int32 {
	if i, ok := b.files[name]; ok {
		return i
	}

	i := int32(len(b.fileList))
	b.files[name] = i
	b.fileList = append(b.fileList, name)
	return i
}

func (b *builder) symbol(name string) int32 {
	if i, ok := b.symbols[name]; ok {
		return i
	}

	i := int32(len(b.symbolList))
	b.symbols[name] = i
	b.symbolList = append(b.symbolList, name)
	return i
}

func (b *builder) frame(k frameKey) int32 {
	if i, ok := b.frames[k]; ok {
		return i
	}

	i := int32(len(b.frameList))
	b.frames[k] = i
	b.frameList = append(b.frameList, k)
	return i
}

// A scope is a function or a call the optimizer wrote into it, with the
// addresses it covers: for a function, the program's file its own
// lines are from, when they are; for an inlined call, the file and
// line of the call in its caller.
type scope struct {
	name     string
	ranges   [][2]uint64
	ownFile  string
	callFile string
	callLine int32
	children []*scope
	depth    int // the depth its children are at, while they are read
}

// lineRow is one row of a line table: the address it starts at and the
// file and line of the code there.
type lineRow struct {
	address uint64
	file    string
	line    int32
	end     bool
}

// rows reads the line tables and the scopes of every compile unit for
// the addresses of section s and returns the section's rows, in order
// of offset, consecutive rows of one chain merged: each row's chain is
// the frames at its address, the innermost first, the frames of no
// Snello function left out, and a row of no Snello frame has none.
func (b *builder) rows(d *dwarf.Data, s section) ([]row, error) {
	if d == nil {
		return nil, nil
	}

	var lines []lineRow
	var scopes []*scope
	r := d.Reader()
	for {
		cu, err := r.Next()
		if err != nil {
			return nil, err
		}

		if cu == nil {
			break
		}

		if cu.Tag != dwarf.TagCompileUnit {
			r.SkipChildren()
			continue
		}

		lr, err := d.LineReader(cu)
		if err != nil {
			return nil, err
		}

		files := []string{}
		if lr != nil {
			for _, f := range lr.Files() {
				if f == nil {
					files = append(files, "")
				} else {
					files = append(files, f.Name)
				}
			}

			var le dwarf.LineEntry
			for {
				if err := lr.Next(&le); err != nil {
					break
				}

				name := ""
				if le.File != nil {
					name = le.File.Name
				}

				lines = append(lines, lineRow{le.Address, name, int32(le.Line), le.EndSequence})
			}
		}

		cuScopes, err := readScopes(d, r, files)
		if err != nil {
			return nil, err
		}

		scopes = append(scopes, cuScopes...)
	}

	starts := make(map[uint64]string, len(s.symbols))
	named := make(map[string]bool, len(s.symbols))
	for _, sym := range s.symbols {
		starts[s.address+sym.offset] = sym.name
		named[sym.name] = true
	}

	for _, sc := range scopes { // a function the debugging information names as the program does, by no symbol of the object: its symbol is the object's at its start
		if named[sc.name] || len(sc.ranges) == 0 {
			continue
		}

		if name, ok := starts[sc.ranges[0][0]]; ok {
			sc.name, _, _ = strings.Cut(name, cloneMark) // a compiler's copy of a function, foo.isra.0, is the function's frame
		}
	}

	sort.SliceStable(lines, func(i, j int) bool { return lines[i].address < lines[j].address })
	for _, l := range lines { // a function's own file: that of its first line of its own, outside any call written into it
		if l.end || !strings.HasSuffix(l.file, snelloSuffix) {
			continue
		}

		for _, sc := range scopes {
			if sc.ownFile == "" && sc.covers(l.address) {
				var path []*scope
				descend(&path, sc, l.address)
				if len(path) == 0 {
					sc.ownFile = l.file
					if b.ownFiles[sc.name] == "" {
						b.ownFiles[sc.name] = l.file
					}
				}

				break
			}
		}
	}

	var out []row
	lastFrame := int32(-2)
	for i, l := range lines { // a row of line 0 is code the optimizer merged from two lines, or made itself: the frame keeps no line
		if l.address < s.address || l.address >= s.address+s.size {
			continue
		}

		frame := int32(noFrame)
		if !l.end && !(i+1 < len(lines) && lines[i+1].address == l.address && !lines[i+1].end) { // of rows at one address, the last
			frame = b.chain(l, scopes)
		}

		if frame == lastFrame && frame != unscoped {
			continue
		}

		out = append(out, row{l.address - s.address, frame, l.file, l.line})
		lastFrame = frame
	}

	return out, nil
}

// readScopes reads the subprograms of a compile unit, whose children
// the reader is at, each with the inlined calls within it, as a tree.
func readScopes(d *dwarf.Data, r *dwarf.Reader, files []string) ([]*scope, error) {
	var out []*scope
	var stack []*scope
	depth := 0
	for {
		e, err := r.Next()
		if err != nil {
			return nil, err
		}

		if e == nil {
			break
		}

		if e.Tag == 0 { // the end of the children of the entry above
			if depth == 0 {
				break
			}

			depth--
			if n := len(stack); n > 0 && stack[n-1].depth == depth {
				stack = stack[:n-1]
			}

			continue
		}

		switch e.Tag {
		case dwarf.TagSubprogram, dwarf.TagInlinedSubroutine:
			sc := &scope{name: nameOf(d, e)}
			if rs, err := d.Ranges(e); err == nil {
				sc.ranges = rs
			}

			if e.Tag == dwarf.TagInlinedSubroutine {
				if f, ok := e.Val(dwarf.AttrCallFile).(int64); ok && f >= 0 && int(f) < len(files) {
					sc.callFile = files[f]
				}

				if l, ok := e.Val(dwarf.AttrCallLine).(int64); ok {
					sc.callLine = int32(l)
				}
			}

			if len(sc.ranges) > 0 {
				if e.Tag == dwarf.TagSubprogram || len(stack) == 0 {
					out = append(out, sc)
				} else {
					parent := stack[len(stack)-1]
					parent.children = append(parent.children, sc)
				}
			}

			if e.Children {
				sc.depth = depth
				stack = append(stack, sc)
				depth++
			}
		default:
			if e.Children {
				depth++
			}
		}
	}

	return out, nil
}

// nameOf is a subprogram's or an inlined call's function symbol: its
// linkage name, which a function named as the program names it carries
// beside that name, or else its name, which is the symbol of a
// function named by it; through its abstract origin or its
// specification when it has one.
func nameOf(d *dwarf.Data, e *dwarf.Entry) string {
	for _, attr := range []dwarf.Attr{dwarf.AttrLinkageName, dwarf.AttrName} { // the symbol, which a function named as the program names it carries as its linkage name
		if name, ok := e.Val(attr).(string); ok {
			return name
		}
	}

	for _, attr := range []dwarf.Attr{dwarf.AttrAbstractOrigin, dwarf.AttrSpecification} {
		if off, ok := e.Val(attr).(dwarf.Offset); ok {
			r := d.Reader()
			r.Seek(off)
			if origin, err := r.Next(); err == nil && origin != nil {
				return nameOf(d, origin)
			}
		}
	}

	return ""
}

// covers reports whether a scope covers an address.
func (sc *scope) covers(address uint64) bool {
	for _, r := range sc.ranges {
		if address >= r[0] && address < r[1] {
			return true
		}
	}

	return false
}

// chain is the row's frame: the innermost scope at its address, at the
// row's file and line, and each enclosing scope at the file and line of
// the call written into it, the frames of no Snello function dropped
// from the inside. Code at no line, which the optimizer merged from two
// lines, is at its function's own file when the function is the
// program's; -1 when no frame is a Snello function's.
func (b *builder) chain(l lineRow, scopes []*scope) int32 {
	var path []*scope
	for _, sc := range scopes {
		if sc.covers(l.address) {
			path = append(path, sc)
			descend(&path, sc, l.address)
			break
		}
	}

	if len(path) == 0 {
		return unscoped
	}

	type entry struct {
		name, file string
		line       int32
	}

	var entries []entry // innermost first
	file, line := l.file, l.line
	for i := len(path) - 1; i >= 0; i-- {
		if line == 0 && b.ownFiles[path[i].name] != "" { // code the optimizer merged from two lines of a function of the program: the function's own file, at no line
			file = b.ownFiles[path[i].name]
		}

		entries = append(entries, entry{path[i].name, file, line})
		file, line = path[i].callFile, path[i].callLine
	}

	for len(entries) > 0 && (!strings.HasSuffix(entries[0].file, snelloSuffix) || entries[0].name == "") {
		entries = entries[1:]
	}

	if len(entries) == 0 {
		return noFrame
	}

	caller := int32(noFrame)
	for i := len(entries) - 1; i >= 0; i-- {
		en := entries[i]
		if !strings.HasSuffix(en.file, snelloSuffix) || en.name == "" {
			continue
		}

		caller = b.frame(frameKey{b.symbol(en.name), b.file(baseName(en.file)), en.line, caller})
	}

	return caller
}

// descend extends the path with the inlined calls that cover address,
// innermost last.
func descend(path *[]*scope, sc *scope, address uint64) {
	for _, child := range sc.children {
		if child.covers(address) {
			*path = append(*path, child)
			descend(path, child, address)
			return
		}
	}
}

// baseName is the file's name without its directories, as the trace
// prints it.
func baseName(name string) string {
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// table writes the table: the header, then the parts at the offsets
// the header gives, the anchors first and in the table's order, which
// Patch puts in order of address.
func (b *builder) table(anchors []anchorRows) []byte {
	var parts bytes.Buffer
	h := header{Reference: unwritten, Anchors: int64(parts.Len()), NAnchors: int64(len(anchors))}
	rows := 0
	for _, a := range anchors {
		var r [anchorBytes]byte
		binary.LittleEndian.PutUint32(r[anchorsField:], uint32(a.size))
		binary.LittleEndian.PutUint32(r[anchorsField+offsetBytes:], uint32(rows))
		binary.LittleEndian.PutUint32(r[anchorsField+2*offsetBytes:], uint32(len(a.rows)))
		parts.Write(r[:])
		rows += len(a.rows)
	}

	h.Rows, h.NRows = int64(parts.Len()), int64(rows)
	for _, a := range anchors {
		for _, r := range a.rows {
			var row [rowBytes]byte
			binary.LittleEndian.PutUint32(row[:], uint32(r.offset))
			binary.LittleEndian.PutUint32(row[offsetBytes:], uint32(r.frame))
			parts.Write(row[:])
		}
	}

	h.Frames, h.NFrames = int64(parts.Len()), int64(len(b.frameList))
	for _, f := range b.frameList {
		var frame [frameBytes]byte
		for i, v := range []int32{f.function, f.file, f.line, f.caller} {
			binary.LittleEndian.PutUint32(frame[i*offsetBytes:], uint32(v))
		}

		parts.Write(frame[:])
	}

	var strings bytes.Buffer
	offsets := func(list []string) {
		for _, name := range list {
			var at [offsetBytes]byte
			binary.LittleEndian.PutUint32(at[:], uint32(strings.Len()))
			parts.Write(at[:])
			strings.WriteString(name)
			strings.WriteByte(0)
		}
	}

	h.Symbols, h.NSymbols = int64(parts.Len()), int64(len(b.symbolList))
	offsets(b.symbolList)
	h.Files, h.NFiles = int64(parts.Len()), int64(len(b.fileList))
	offsets(b.fileList)
	h.Strings = int64(parts.Len())
	parts.Write(strings.Bytes())
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, h)
	out.Write(parts.Bytes())
	return out.Bytes()
}

// Patch writes each anchor's address, its function's in the binary at
// path, into the table, puts the anchors in order of address, and
// writes the table's reference with its own address; it returns the
// binary's bytes so patched, for the caller to write. A function the
// binary has no symbol of its own for, or two of, is left at no
// address, and the walk ends at a frame of it.
func Patch(path string, layout *Layout) ([]byte, error) {
	if layout == nil || len(layout.Anchors) == 0 {
		return nil, nil
	}

	addresses, place, err := symbols(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	table, ok := place(tableSymbol)
	addr, found := addresses[tableSymbol]
	if !ok || !found || table+headerBytes > uint64(len(data)) {
		return nil, fmt.Errorf("%s: the table of the panic's trace is not in the binary", path)
	}

	parts, ok := place(partsSymbol)
	at := parts + binary.LittleEndian.Uint64(data[table+anchorsField:])
	if !ok || at+uint64(len(layout.Anchors))*anchorBytes > uint64(len(data)) {
		return nil, fmt.Errorf("%s: the anchors of the panic's trace are not in the binary", path)
	}

	records := make([][]byte, len(layout.Anchors))
	for i, name := range layout.Anchors {
		records[i] = data[at+uint64(i)*anchorBytes : at+uint64(i+1)*anchorBytes]
		if addr, ok := addresses[name]; ok {
			binary.LittleEndian.PutUint64(records[i], addr)
		}
	}

	sorted := make([]byte, 0, len(layout.Anchors)*anchorBytes) // in order of address, for the runtime's search
	sort.SliceStable(records, func(i, j int) bool {
		return binary.LittleEndian.Uint64(records[i]) < binary.LittleEndian.Uint64(records[j])
	})
	for _, r := range records {
		sorted = append(sorted, r...)
	}

	copy(data[at:], sorted)
	binary.LittleEndian.PutUint64(data[table:], addr)
	return data, nil
}

// symbols reads the binary's function and data symbols by name, those
// named once, and returns them with a function giving a data symbol's
// offset in the file.
func symbols(path string) (map[string]uint64, func(string) (uint64, bool), error) {
	addresses := map[string]uint64{}
	twice := map[string]bool{}
	weak := map[string]bool{}
	add := func(name string, value uint64, isWeak bool) { // a weak definition yields to a strong one: the runtime's
		if _, ok := addresses[name]; ok { // empty tables under the program's own
			if isWeak {
				return
			}

			if !weak[name] {
				twice[name] = true
			}
		}

		addresses[name] = value
		weak[name] = isWeak
	}

	if m, err := macho.Open(path); err == nil {
		defer m.Close()
		if m.Symtab != nil {
			for _, s := range m.Symtab.Syms {
				if s.Type&0xe0 == 0 && s.Sect != 0 {
					add(strings.TrimPrefix(s.Name, "_"), s.Value, s.Desc&weakDefinition != 0)
				}
			}
		}

		for name := range twice {
			delete(addresses, name)
		}

		place := func(name string) (uint64, bool) {
			addr, ok := addresses[name]
			if !ok {
				return 0, false
			}

			for _, s := range m.Sections {
				if addr >= s.Addr && addr < s.Addr+s.Size && s.Flags&sectionType != zeroFill { // a zero-filled section has no bytes in the file
					return uint64(s.Offset) + addr - s.Addr, true
				}
			}

			return 0, false
		}

		return addresses, place, nil
	}

	e, err := elf.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: neither Mach-O nor ELF", path)
	}

	defer e.Close()
	syms, err := e.Symbols()
	if err != nil {
		return nil, nil, err
	}

	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF && s.Section < elf.SHN_LORESERVE && s.Name != "" {
			add(s.Name, s.Value, elf.ST_BIND(s.Info) == elf.STB_WEAK)
		}
	}

	for name := range twice {
		delete(addresses, name)
	}

	place := func(name string) (uint64, bool) {
		addr, ok := addresses[name]
		if !ok {
			return 0, false
		}

		for _, s := range e.Sections {
			if s.Type != elf.SHT_NOBITS && addr >= s.Addr && addr < s.Addr+s.Size && s.Addr != 0 {
				return s.Offset + addr - s.Addr, true
			}
		}

		return 0, false
	}

	return addresses, place, nil
}
