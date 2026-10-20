// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "github.com/teak-lang/teak/ast"

// A value crosses a task boundary when a spawn captures it, a send puts
// it on a channel, or a receive takes it off one. It may cross when its
// type is Send: no other task can reach the memory it owns, so the task
// that receives it owns it alone and frees it alone
// (_docs/design/03-ownership-across-tasks.md). Send is computed from
// structure and never declared: numbers, bool, strings and channel
// handles are Send; a struct, enum, tuple, option or result is Send
// when every part is; a slice, array, map or owned pointer when its
// element is; a borrow never, as it points into a frame of the task
// that made it; a trait used as a type only when the trait refines
// Send, since the type behind it is not known; a type parameter only
// when bounded by Send. The standard library's local, a field that
// holds nothing, is the one predeclared type that is not Send: a type
// with such a field stays in its task, which is how rc.Rc, whose count
// is not atomic, says so.
//
// Nothing in Teak changes a value through a shared ref except under a
// lock, so safe to share between tasks and safe to send are one
// property, and there is no second one beside Send.

// staysInTask is the reason a local field gives; the exported declared
// type holding the field, directly or through unexported ones, takes
// it over under its own name, since that is the type the program sees:
// rc.Rc, not the cell inside it.
const staysInTask = "it stays in its task"

// IsSend reports whether a value of type t may cross a task boundary.
func IsSend(t Type) bool {
	return NotSend(t) == ""
}

// NotSend says why a value of type t cannot cross a task boundary, for
// an error message, or "" when it can: the part of t that is a borrow,
// the trait that does not refine Send, the type parameter not bounded
// by it, or the declared type that stays in its task. A type holding
// one of those is answered for by the part, so that the message names
// what the program must change: rc.Rc<Node> stays in its task, not the
// struct holding it.
func NotSend(t Type) string {
	why := notSend(t)
	if n, ok := t.(*Named); ok && why == staysInTask {
		return n.String() + " stays in its task" // unexported, asked for directly
	}

	return why
}

// notSend walks t for the first part that is not Send.
func notSend(t Type) string {
	switch t := t.(type) {
	case *Basic:
		if t.kind == Local {
			return staysInTask
		}
	case *Ref:
		return t.String() + " is a borrow"
	case *Pointer:
		return notSend(t.elem)
	case *Slice:
		return notSend(t.elem)
	case *Array:
		return notSend(t.elem)
	case *Option:
		return notSend(t.elem)
	case *Result:
		return firstNotSend(t.val, t.err)
	case *Map:
		return firstNotSend(t.key, t.value)
	case *Tuple:
		return firstNotSend(t.elems...)
	case *Struct:
		return firstNotSendField(t.fields)
	case *Enum:
		for _, v := range t.variants {
			if why := firstNotSendField(v.fields); why != "" {
				return why
			}
		}
	case *Named:
		return t.notSend()
	case *TypeParam:
		if !t.HasBound(SendTrait) {
			return t.String() + " is not bounded by Send"
		}
	case *Trait:
		if !t.Refines(SendTrait) {
			return t.String() + " does not refine Send"
		}
	}

	return ""
}

// notSend caches the answer on the named type, with its reason. A
// declaration that reaches itself through a pointer is asked about
// itself while its answer is being computed; it counts as Send there,
// so the recursion ends and the other fields decide. A type not yet
// resolved answers Send and caches nothing: the question is asked once
// the declarations are resolved, as the bound checks ask theirs, and a
// check at a type's formation waits as storable does.
func (n *Named) notSend() string {
	if n.sendState == copyUnknown && n.Underlying() != nil {
		n.sendState = copyYes // provisional, so that a cycle back to n ends here
		if why := notSend(n.underlying); why != "" {
			n.sendState = copyNo
			if why == staysInTask && ast.IsExported(n.obj.name) {
				why = n.String() + " stays in its task"
			}

			n.sendWhy = why
		}
	}

	return n.sendWhy
}

func firstNotSend(ts ...Type) string {
	for _, t := range ts {
		if why := notSend(t); why != "" {
			return why
		}
	}

	return ""
}

func firstNotSendField(vs []*Var) string {
	for _, v := range vs {
		if why := notSend(v.typ); why != "" {
			return why
		}
	}

	return ""
}
