// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lower

import (
	"github.com/snello-lang/snello/compiler/ast"
	"github.com/snello-lang/snello/compiler/types"
)

// TypeArm returns the arm of the match on a type x that the type t
// takes, the type the match's parameter stands for in the instance
// being written, and the substitution sub with what the arm's shape
// binds added, E of []E; nil when no arm takes t, which the checker has
// refused before a backend runs. sub itself is not changed.
func TypeArm(info *types.Info, x *ast.MatchExpr, t types.Type, sub map[*types.TypeParam]types.Type) (*ast.MatchArm, map[*types.TypeParam]types.Type) {
	arms := make([]types.TypeArm, len(x.Arms))
	for i, arm := range x.Arms {
		arms[i] = info.Arms[arm]
		if arms[i].Type != nil {
			arms[i].Type = types.Subst(arms[i].Type, sub) // an arm may name the function's other type parameters, []U
		}
	}

	i, binds := types.ChooseArm(arms, t)
	if i < 0 {
		return nil, sub
	}

	if len(binds) == 0 {
		return x.Arms[i], sub
	}

	out := make(map[*types.TypeParam]types.Type, len(sub)+len(binds))
	for p, a := range sub {
		out[p] = a
	}

	for p, a := range binds {
		out[p] = a
	}

	return x.Arms[i], out
}

// MatchedParam returns the type parameter a match on a type is on: the
// one its name stands for, or a field's type, f.type, the type
// parameter of the loop that gives f.
func MatchedParam(info *types.Info, x *ast.MatchExpr) *types.TypeParam {
	switch m := x.X.(type) {
	case *ast.Ident:
		if tn, ok := info.Uses[m].(*types.TypeName); ok {
			p, _ := tn.Type().(*types.TypeParam)
			return p
		}
	case *ast.SelectorExpr:
		if p := PartUsed(info, m.X); p != nil {
			tp, _ := p.Type().(*types.TypeParam)
			return tp
		}
	}

	return nil
}

// A PartCopy is one copy of a loop over a type's parts that a backend
// writes: the field or variant it is for, its name, and the
// substitution with the field's type in place of the loop's type
// parameter, f.type. A field of a variant has the variant beside it,
// and its index among the variant's fields.
type PartCopy struct {
	Field   *types.Var
	Variant *types.Variant
	Index   int
	Name    string
	Default ast.Expr // the field's default, folded, or nil: f.default
	Sub     map[*types.TypeParam]types.Type
}

// PartsOf returns the part a loop over a type's parts names and the
// copies to write of it for the instance whose substitution is sub, in
// the order of the fields or variants; bound holds the copies being
// written of the loops around it, whose variant fields(c) goes through.
// nil when s is no such loop.
func PartsOf(info *types.Info, s *ast.RangeStmt, sub map[*types.TypeParam]types.Type, bound map[*types.Part]PartCopy) (*types.Part, []PartCopy) {
	key, _ := s.Key.(*ast.Ident)
	return partsOf(info, key, s.X, sub, bound)
}

// EltParts is PartsOf for the element of a value built from a type's
// fields, T{ for f := range fields(T) { [f]: value } } or a variant's,
// c(for f := range fields(c) { value }).
func EltParts(info *types.Info, elt *ast.PartsElt, sub map[*types.TypeParam]types.Type, bound map[*types.Part]PartCopy) (*types.Part, []PartCopy) {
	return partsOf(info, elt.Var, elt.X, sub, bound)
}

// ArmParts is PartsOf for the arm of a match written once for each
// variant, for c := range variants(T) => ...
func ArmParts(info *types.Info, p *ast.PartsPat, sub map[*types.TypeParam]types.Type) (*types.Part, []PartCopy) {
	return partsOf(info, p.Var, p.X, sub, nil)
}

// partsOf returns the part key names, of the loop over x, and its
// copies for the instance whose substitution is sub.
func partsOf(info *types.Info, key *ast.Ident, x ast.Expr, sub map[*types.TypeParam]types.Type, bound map[*types.Part]PartCopy) (*types.Part, []PartCopy) {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		return nil, nil
	}

	fun, ok := call.Fun.(*ast.Ident)
	if !ok {
		return nil, nil
	}

	b, ok := info.Uses[fun].(*types.Builtin)
	if !ok || b.ID() != types.BuiltinFields && b.ID() != types.BuiltinVariants {
		return nil, nil
	}

	part, _ := info.Defs[key].(*types.Part)
	if part == nil {
		return nil, nil
	}

	of := types.Subst(part.Of(), sub).Underlying()
	switch {
	case part.IsVariant():
		if en, ok := of.(*types.Enum); ok {
			return part, variantCopies(en, sub)
		}
	case part.In() != nil: // fields(c), c a variant an enclosing loop gives
		if v := bound[part.In()].Variant; v != nil {
			return part, variantFieldCopies(v, part, sub)
		}
	default:
		if st, ok := of.(*types.Struct); ok {
			return part, fieldCopies(info, st, part, sub)
		}
	}

	return part, nil
}

// variantCopies returns a copy for each variant of en.
func variantCopies(en *types.Enum, sub map[*types.TypeParam]types.Type) []PartCopy {
	copies := make([]PartCopy, en.NumVariants())
	for i := range copies {
		v := en.Variant(i)
		copies[i] = PartCopy{Variant: v, Name: v.Name(), Sub: sub}
	}

	return copies
}

// variantFieldCopies returns a copy for each field of the variant v,
// with the field's type in place of the part's.
func variantFieldCopies(v *types.Variant, part *types.Part, sub map[*types.TypeParam]types.Type) []PartCopy {
	copies := make([]PartCopy, len(v.Fields()))
	for i, f := range v.Fields() {
		copies[i] = PartCopy{Field: f, Variant: v, Index: i, Name: f.Name(), Sub: withParam(sub, part, f.Type())}
	}

	return copies
}

// fieldCopies returns a copy for each field of the struct st, with the
// field's type in place of the part's and its default folded.
func fieldCopies(info *types.Info, st *types.Struct, part *types.Part, sub map[*types.TypeParam]types.Type) []PartCopy {
	copies := make([]PartCopy, st.NumFields())
	for i := range copies {
		f := st.Field(i)
		copies[i] = PartCopy{Field: f, Index: i, Name: f.Name(), Sub: withParam(sub, part, f.Type())}
		if d := st.Default(i); d != nil {
			copies[i].Default = Folded(info, d)
		}
	}

	return copies
}

// withParam returns sub with t in place of the part's type, f.type.
func withParam(sub map[*types.TypeParam]types.Type, part *types.Part, t types.Type) map[*types.TypeParam]types.Type {
	param, _ := part.Type().(*types.TypeParam)
	s := make(map[*types.TypeParam]types.Type, len(sub)+1)
	for p, a := range sub {
		s[p] = a
	}

	s[param] = t
	return s
}

// NameParts makes every selector that stands for the part p, a v.[f]
// the checker rewrote, name the field of the copy c, so that the
// backend writes it as the field it is in that copy.
func NameParts(info *types.Info, p *types.Part, c PartCopy) {
	for _, sel := range info.Parts[p] {
		sel.Sel.Name = c.Name
		info.Uses[sel.Sel] = c.Field
	}
}

// PartUsed returns the part a name stands for, f in f.name, or nil.
func PartUsed(info *types.Info, x ast.Expr) *types.Part {
	id, ok := x.(*ast.Ident)
	if !ok {
		return nil
	}

	p, _ := info.Uses[id].(*types.Part)
	return p
}
