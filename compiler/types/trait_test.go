// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

// traitPrelude declares the traits and types the cases use: a trait
// with two required methods, one refining it, a trait of its own, a
// trait whose method takes a Self, two implementors, one of the
// refining trait, a type implementing nothing, a generic implementor,
// a type bounded by Copy, and a generic function bounded by the trait.
const traitPrelude = `
trait Shape {
	func (s ref Self) Area() f64
	func (s ref Self) Name() string
}
trait Named { func (n ref Self) Label() string }
trait Logged: Shape {}
trait Comparable { func (s ref Self) Same(o ref Self) bool }
struct Circle impl Shape { r f64 }
func (c ref Circle) Area() f64 { return 3.0 * c.r * c.r }
func (c ref Circle) Name() string { return "circle" }
struct Square impl Shape, Named { s f64 }
func (q ref Square) Area() f64 { return q.s * q.s }
func (q ref Square) Name() string { return "square" }
func (q ref Square) Label() string { return "sq" }
struct Tagged impl Logged { r f64 }
func (t ref Tagged) Area() f64 { return t.r }
func (t ref Tagged) Name() string { return "tagged" }
struct Blob { n int }
struct Boxed<T> impl Shape { v T }
func (b ref Boxed<T>) Area() f64 { return 1.0 }
func (b ref Boxed<T>) Name() string { return "box" }
struct Copied<T: Copy> { v T }
struct Bounded<T: Shape> { v T }
trait Greeter { func (g ref Self) Greet() string { return "hello" } }
trait Shouter: Greeter { func (g ref Self) Greet() string { return "HELLO" } }
trait Waver { func (w ref Self) Greet() string { return "wave" } }
trait Sub: Greeter {}
trait NeedsGreet { func (s ref Self) Greet() string }
trait NeedsInt { func (s ref Self) Greet() int }
trait Labeled { var label string; func (l ref Self) Label() string { return l.label.Clone() } }
trait Other { var label int }
trait Loud: Labeled {}
trait Loop { var again Self }
trait Kids { var kids []Self }
trait Shout: Labeled { func (s ref Self) S() string { return s.label.Clone() } }
func TotalArea<T: Shape>(xs ref []T) f64 {
	sum := 0.0
	for _, x := range xs {
		sum += x.Area()
	}

	return sum
}
`

// TestTraits checks trait declarations, the impl check and bounds:
// that a type listing a trait must declare each required method with
// the trait's signature, that a bound lets a type parameter use the
// trait's methods and admits only the types that implement it, and the
// errors of a wrong declaration or use.
func TestTraits(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		// Implementing a trait: every required method, with the trait's signature.
		{"enum Kind impl Named { A; B }\nfunc (k ref Kind) Label() string { return \"k\" }", nil},
		{"struct Tri impl Shape {}\nfunc (t ref Tri) Area() f64 { return 0.0 }", []string{"Tri does not implement Shape: missing method Name() string"}},
		{"type ID int impl Named\nfunc (i ref ID) Label() string { return \"id\" }", nil}, // a type declared over a number lists its traits too
		{"type ID int impl Named", []string{"ID does not implement Named: missing method Label() string"}},
		{"struct Sq impl Named {}\nfunc (s Sq) Label() int { return 0 }", []string{"method Sq.Label has the wrong signature for Named: have (Sq) Label() int, want (ref Sq) Label() string"}},
		{"struct Sq impl Named {}\nfunc (s mut Sq) Label() string { return \"\" }", []string{"method Sq.Label has the wrong signature for Named: have (mut Sq) Label() string, want (ref Sq) Label() string"}},
		{"struct Sq impl Named {}\nfunc (s ref Sq) Label(n int) string { return \"\" }", []string{"have (ref Sq) Label(int) string, want (ref Sq) Label() string"}},
		{"struct Sq impl Named {}\nfunc (s ref Sq) Label() int { return 0 }", []string{"have (ref Sq) Label() int, want (ref Sq) Label() string"}},
		{"struct P impl Comparable {}\nfunc (p ref P) Same(o ref P) bool { return true }", nil}, // Self in a parameter is the type
		{"struct P impl Comparable {}\nfunc (p ref P) Same(o ref Circle) bool { return true }", []string{"have (ref P) Same(ref Circle) bool, want (ref P) Same(ref P) bool"}},
		{"struct L impl Logged {}", []string{"L does not implement Logged: missing method Area() f64, required by Shape", "L does not implement Logged: missing method Name() string, required by Shape"}},
		{"struct L impl Shape, Logged {}", []string{"L does not implement Shape: missing method Area() f64", "L does not implement Shape: missing method Name() string"}}, // each method once, at the first trait that asks
		{"struct W impl Shape {}\nfunc (w ref W) Area() Nope { return 0 }\nfunc (w ref W) Name() string { return \"\" }", []string{"undefined: Nope"}},                    // a signature that failed is not compared again

		// Default methods: inherited, overridden, resolved right to left with a refining trait first, or ambiguous.
		{"struct G impl Greeter {}\nfunc f(g ref G) string { return g.Greet() }", nil},
		{"struct G impl Greeter {}\nfunc (g ref G) Greet() string { return \"own\" }", nil},
		{"struct G impl Greeter {}\nfunc (g ref G) Greet() int { return 0 }", []string{"method G.Greet has the wrong signature for Greeter: have (ref G) Greet() int, want (ref G) Greet() string"}},
		{"struct G impl Shouter, Greeter {}", nil}, // Shouter refines Greeter, so its default wins wherever it is listed
		{"struct G impl Greeter, Shouter {}", nil},
		{"struct G impl Greeter, Waver {}", []string{"G inherits Greet from both Waver and Greeter: declare G.Greet to choose"}},
		{"struct G impl Shouter, Waver {}", []string{"G inherits Greet from both Waver and Shouter: declare G.Greet to choose"}}, // once, not again for Greeter
		{"struct G impl Greeter, Waver {}\nfunc (g ref G) Greet() string { return \"mine\" }", nil},
		{"struct G impl NeedsGreet, Greeter {}", nil}, // a default of one trait satisfies another's requirement
		{"struct G impl NeedsInt, Greeter {}", []string{"method G.Greet has the wrong signature for NeedsInt: have (ref G) Greet() string, want (ref G) Greet() int"}},
		{"struct G impl Sub {}\nfunc f(g ref G) string { return g.Greet() }", nil}, // inherited through a trait that adds nothing
		{"enum E impl Greeter { A; B }\nfunc f(e ref E) string { return e.Greet() }", nil},
		{"struct G impl Greeter, Waver {}\nfunc (g ref G) Greet() int { return 0 }", []string{"method G.Greet has the wrong signature for Waver: have (ref G) Greet() int, want (ref G) Greet() string"}}, // once, for the first provider
		{"trait B: Shape { func (s ref Self) Area() int { return 1 } }", []string{"method B.Area has the wrong signature for Shape: have (ref Self) Area() int, want (ref Self) Area() f64"}},
		{"trait B: Shape { func (s ref Self) Area() int }", []string{"method B.Area has the wrong signature for Shape: have (ref Self) Area() int, want (ref Self) Area() f64"}},
		{"trait B: Shape { func (s ref Self) Area() f64 { return 1.0 } }\nstruct G impl B {}\nfunc (g ref G) Name() string { return \"g\" }", nil},                                                   // a default for a refined requirement
		{"trait B: Sub { func (s ref Self) Greet() int { return 1 } }", []string{"method B.Greet has the wrong signature for Greeter: have (ref Self) Greet() int, want (ref Self) Greet() string"}}, // through a trait in between
		{"func Hi<T: Greeter>(x ref T) string { return x.Greet() }", nil},
		{"struct Wrap<T> impl Greeter { v T }\nfunc f(w ref Wrap<int>) string { return w.Greet() }", nil},
		{"trait Twice { func (s ref Self) Area() f64; func (s ref Self) Double() f64 { return s.Area() * 2.0 } }", nil},
		{"trait Bad { func (s ref Self) Oops() f64 { return s.Area() } }", []string{"Area is not a field or method of ref Self: no bound of Self declares it"}},
		{"trait Mv { func (s Self) Take() Self { return s }; func (s Self) Both() (Self, Self) { return (s.Take(), s.Take()) } }", []string{"use of moved value s"}},

		// super: the next definition in the order, called with the method's own receiver.
		{"trait Sup: Greeter { func (g ref Self) Greet() string { return \"[\" + super.Greet() + \"]\" } }", nil},
		{"trait Sup: Greeter { func (g ref Self) Loud() string { return super.Greet() } }", nil}, // another method's next definition
		{"struct G impl Greeter {}\nfunc (g ref G) Greet() string { return \"<\" + super.Greet() + \">\" }", nil},
		{"struct G impl Shouter, Waver {}\nfunc (g ref G) Greet() string { return super.Greet() }", nil}, // an own method settles the ambiguity, and super takes the first
		{"trait Sup: NeedsGreet { func (g ref Self) Greet() string { return super.Greet() } }", []string{"no trait refined by Sup defines Greet with a body"}},
		{"trait Sup { func (g ref Self) Greet() string { return super.Greet() } }", []string{"no trait refined by Sup defines Greet with a body"}},
		{"struct G impl NeedsGreet {}\nfunc (g ref G) Greet() string { return super.Greet() }", []string{"no trait of G defines Greet with a body"}},
		{"struct G {}\nfunc (g ref G) Greet() string { return super.Greet() }", []string{"no trait of G defines Greet with a body"}},
		{"func f() string { return super.Greet() }", []string{"super is only defined inside a method"}},
		{"trait Sup: Greeter { func (g ref Self) Greet() string { f := func() string { return super.Greet() }; return f() } }", nil}, // the closure captures the receiver
		{"struct G impl Greeter {}\nfunc (g ref G) Greet() string { f := func() string { return super.Greet() }; return f() }", nil},
		{"func f() { g := func() string { return super.Greet() }; print(g()) }", []string{"super is only defined inside a method"}},
		{"func f() { print(super) }", []string{"super must be followed by a method: super.Method()"}},
		{"struct G impl Greeter {}\nfunc (g ref G) Greet() string { f := super.Greet; return f() }", []string{"super.Greet must be called"}},
		{"trait Mu { func (s mut Self) Bump() {} }\ntrait Nu: Mu { func (s ref Self) Poke() { super.Bump() } }", []string{"cannot borrow super as mut: super is a ref receiver"}},
		{"trait Mu { func (s mut Self) Bump() {} }\ntrait Nu: Mu { func (s mut Self) Poke() { super.Bump() } }", nil},
		{"trait Mv { func (s Self) Take() Self { return s } }\ntrait Mv2: Mv { func (s Self) Twice() (Self, Self) { return (super.Take(), s.Take()) } }", []string{"use of moved value s"}}, // super moves a value receiver
		{"trait Mv { func (s Self) Take() Self { return s } }\nstruct G impl Mv { s string }\nfunc (g G) Both() (G, G) { return (super.Take(), g.Take()) }", []string{"use of moved value g"}},
		{"trait Arg { func (s ref Self) At(i int) int { return i } }\ntrait Arg2: Arg { func (s ref Self) At(i int) int { return super.At(i + 1) } }", nil},
		{"struct W<T> impl Greeter { v T }\nfunc (w ref W<T>) Greet() string { return super.Greet() }", nil}, // in a generic type's own method
		{"enum E impl Greeter { A; B }\nfunc (e ref E) Greet() string { return super.Greet() }", nil},
		{"trait Y1 { func (s ref Self) M() int { return 1 } }\ntrait Y2 { func (s ref Self) M() string { return \"\" } }\ntrait X: Y1, Y2 {}\ntrait W: X { func (s ref Self) M() int { return 1 } }", []string{"method W.M has the wrong signature for Y2: have (ref Self) M() int, want (ref Self) M() string"}}, // every definition behind it, not the first

		// Trait fields: stored in the implementing struct, set at construction, read through Self or a bound.
		{"struct Tag impl Labeled {}\nfunc f() string { t := Tag{label: \"x\"}; return t.label }", nil},
		{"struct Tag impl Labeled {}\nfunc f(t ref Tag) string { return t.Label() }", nil},    // the default reads the field through Self
		{"struct Tag impl Loud {}\nfunc f(t ref Tag) string { return t.label.Clone() }", nil}, // through a refining trait
		{"struct W<T> impl Labeled { v T }\nfunc f(w ref W<int>) string { return w.label.Clone() }", nil},
		{"func L<T: Labeled>(x ref T) string { return x.label.Clone() }", nil},
		{"func L<T: Labeled>(x mut T) { x.label = \"y\" }", nil},
		{"func L<T: Loud>(x ref T) string { return x.label.Clone() }", nil}, // a field of a refined trait, through the bound
		{"struct Tag impl Shout {}\nfunc f(t ref Tag) string { return t.S() }", nil},
		{"func L<T: Labeled>(x ref T) string { return x.label }", []string{"cannot move x.label out of a borrow"}}, // a field is a field
		{"trait Bad: Labeled { func (s ref Self) B() string { return s.label } }", []string{"cannot move s.label out of a borrow"}},
		{"struct W<T> impl Labeled { v T }\nfunc f() { a := W<int>{v: 1, label: \"x\"}; b := W{v: 2, label: \"y\"}; print(a.label, b.label) }", nil},
		{"struct Both impl Loud, Labeled {}\nfunc f() { b := Both{label: \"x\"}; print(b.label) }", nil}, // the field reached twice, kept once
		{"struct H impl Kids { r ref int }", []string{"a slice cannot hold H: it holds a borrow"}},
		{"struct F impl Kids { n int }\nfunc f() { k := F{n: 1, kids: []F{}}; print(k.n) }", nil},
		{"struct Tag impl Labeled { label int }", []string{"field label is also a field of Labeled"}},
		{"struct Tag impl Labeled, Other {}", []string{"Tag gets a field label from both Other and Labeled"}},
		{"enum E impl Labeled { A }", []string{"E is not a struct and cannot carry the field label of Labeled"}},
		{"struct L impl Loop {}", []string{"invalid recursive type L: it contains itself"}},
		{"func f(t ref Tag) int { return len(t.label) }\nstruct Tag impl Labeled {}", nil}, // the fields are there before any body
		{"struct Tag impl Labeled {}\nfunc f() { t := Tag{label: 1}; print(t) }", []string{"cannot use a value of type int as string in field label"}},

		// The standard traits: Drop asks for its method; Copy is a property of the type.
		{"struct R impl Drop { n int }", []string{"R does not implement Drop: missing method Drop()"}},
		{"struct R impl Drop { n int }\nfunc (r ref R) Drop() {}", []string{"method R.Drop has the wrong signature for Drop: have (ref R) Drop(), want (mut R) Drop()"}},
		{"func f() { c := Copied<int>{v: 1}; print(c.v) }", nil},
		{"func f() { c := Copied<string>{v: \"a\"}; print(c.v) }", []string{"cannot use string as the type parameter T of Copied: string does not implement Copy"}},
		{"struct Holder { c Copied<Node> }", []string{"cannot use Node as the type parameter T of Copied: Node does not implement Copy"}},                 // asked before Node was resolved
		{"struct Holder { c Copied<Pt> }\nstruct Pt { x int }", nil},                                                                                      // Pt is Copy, known only once it is resolved
		{"func Any<T: Eq>(x T) {}\nfunc f() { Any(Blob{n: 1}) }", []string{"cannot use Blob as the type parameter T of Any: Blob does not implement Eq"}}, // a struct is Eq by listing it
		{"func Any<T: Eq>(x T) {}\nstruct Pt impl Eq { x int }\nfunc f() { Any(Pt{x: 1}) }", nil},
		{"func Any<T: Eq>(x T) {}\nfunc f() { Any(3); Any(\"s\"); Any((1, 2)); Any(Some(1)) }", nil},

		// Derivation from the fields: a listed standard trait without the method, each field asked.
		{"struct R impl Copy { s string }", []string{"R does not implement Copy: field s has type string, which is not Copy"}},
		{"struct R impl Copy, Drop { n int }\nfunc (r mut R) Drop() {}", []string{"R does not implement Copy: it implements Drop"}},
		{"enum E impl Copy { A; B(s string) }", []string{"E does not implement Copy: variant B has a field of type string, which is not Copy"}},
		{"struct R impl Copy { x int; y f64; ok bool; t (int, int) }", nil},
		{"struct R impl Eq { xs []int }", []string{"R does not implement Eq: field xs has type []int, which is not Eq"}},
		{"struct R impl Eq { xs []int }\nfunc (a ref R) Eq(o ref R) bool { return true }", nil},
		{"struct R impl Eq { xs []int }\nfunc (a ref R) Eq(o R) bool { return true }", []string{"method R.Eq has the wrong signature for Eq: have (ref R) Eq(R) bool, want (ref R) Eq(ref R) bool"}},
		{"struct R impl Eq { n int }\nfunc f(a R, b R) bool { return a.Eq(ref b) && a == b }", nil}, // derived, usable by name and by ==
		{"struct R impl Ord { n int; s string }\nfunc f(a R, b R) bool { return a.Cmp(ref b) == Less }", nil},
		{"struct R impl Ord { xs []int }\nfunc (a ref R) Cmp(o ref R) Ordering { return Equal }", []string{"R does not implement Eq: field xs has type []int, which is not Eq, required by Ord"}},
		{"struct R impl Ord { p Blob }", []string{"R does not implement Ord: field p has type Blob, which is not Ord"}},
		{"struct R impl Hash { n int; s string }\nfunc f(r ref R) u64 { return r.Hash() }", nil},
		{"struct R impl Hash { n int }\nfunc (a ref R) Eq(o ref R) bool { return true }", []string{"R writes one of Eq and Hash and derives the other: write both or neither, since equal values must hash alike"}},
		{"struct R impl Hash { n int }\nfunc (a ref R) Eq(o ref R) bool { return true }\nfunc (a ref R) Hash() u64 { return 0 }", nil},
		{"struct R impl Clone { p *Blob }", []string{"R does not implement Clone: field p has type *Blob, which is not Clone"}},
		{"struct R impl Clone { s string; xs []int; o int? }\nfunc f(r ref R) R { return r.Clone() }", nil},
		{"struct R impl Error { n int }", []string{"R does not implement Error: missing method Error() string"}},
		{"struct R impl Error { n int }\nfunc (r ref R) Error() string { return \"r\" }\nfunc E<T: Error>(e ref T) string { return e.Error() }\nfunc f(r R) string { return E(ref r) }", nil},
		{"func M<T: Ord>(a ref T, b ref T) Ordering { return a.Cmp(b) }\nfunc f(a Blob, b Blob) Ordering { return M(ref a, ref b) }", []string{"cannot use Blob as the type parameter T of M: Blob does not implement Ord"}},
		{"func M<T: Ord>(a ref T, b ref T) Ordering { return a.Cmp(b) }\nfunc f(a int, b int, s string, t string) bool { return M(ref a, ref b) == M(ref s, ref t) }", nil},
		{"func M<T: Ord>(a ref T, b ref T) bool { return a == b }", nil}, // Ord refines Eq
		{"func f(a int, b int, s ref string, t ref string) bool { return a.Cmp(b) == Less && s.Cmp(t) == Greater }", nil},
		{"func C<T: Clone>(x ref T) T { return x.Clone() }\nfunc f(s ref string, xs ref []int) { C(s); C(xs) }", nil},
		{"func C<T: Clone>(x ref T) T { return x.Clone() }\nfunc f(c ref Circle) { C(c) }", []string{"cannot use Circle as the type parameter T of C: Circle does not implement Clone"}},

		// A map key must be Eq and Hash.
		{"func f(m map[Shape]int) {}", []string{"Shape cannot be a map key: a trait value cannot be one"}},
		{"func f(m map[Blob]int) {}", []string{"Blob cannot be a map key: it does not implement Hash"}},
		{"func f(m map[Pt]int) {}\nstruct Pt impl Hash { x int }", nil}, // asked before Pt was resolved
		{"func f(m map[(int, string)]int, n map[string][]int) {}", nil},
		{"struct Keyed<K: Hash> { m map[K]int }\nfunc f(k Keyed<Blob>) {}", []string{"cannot use Blob as the type parameter K of Keyed: Blob does not implement Hash"}}, // a struct is Hash by listing it
		{"struct Pt impl Eq { x int }\nfunc f(m map[Pt]int) {}", []string{"Pt cannot be a map key: it does not implement Hash"}},                                        // Eq alone is not Hash
		{"struct Pt impl Eq { x int }\nfunc H<T: Hash>(x ref T) u64 { return x.Hash() }\nfunc f(p Pt) u64 { return H(ref p) }", []string{"cannot use Pt as the type parameter T of H: Pt does not implement Hash"}},
		{"func f(m map[Hue]int) {}\nenum Hue { Red; Green }", nil},
		{"struct S { m map[Hue]int }\nenum Hue { Red; Green }", nil},
		{"struct S { m map[Hue]int }\nenum Hue { Red; Green(xs []int) }", []string{"Hue cannot be a map key: it does not implement Hash"}}, // asked in a field, before Hue's payloads are known: the question waits
		{"func f(m map[Hue]int) {}\nenum Hue { Red; Green(xs []int) }", []string{"Hue cannot be a map key: it does not implement Hash"}},
		{"trait Key: Hash {}\nfunc f(m map[Key]int) {}", []string{"Key cannot be a map key: a trait value cannot be one"}},
		{"trait Key: Hash {}\nstruct Q impl Key { xs []int }", []string{"Q does not implement Hash: field xs has type []int, which is not Hash, required by Key"}},
		{"trait Key: Hash { var m map[Self]int }\nstruct Q impl Key { n int }\nfunc (q ref Q) Eq(o ref Q) bool { return q.n == o.n }\nfunc (q ref Q) Hash() u64 { return 1 }", nil}, // Self is Hash through Key
		{"trait Keyed { var m map[Self]int }", []string{"Self cannot be a map key: it does not implement Hash"}},
		{"func g<T>(a T, b T) bool { return a == b }", []string{"values of type T cannot be compared with =="}},
		{"struct Pair<T: Eq> impl Eq { a T; b T }\nfunc f(p Pair<int>, q Pair<int>) bool { return p == q && p.Eq(ref q) }", nil},
		{"struct Loose<T> impl Eq { a T }", []string{"Loose does not implement Eq: field a has type T, which is not Eq"}},
		{"type ID int\nfunc O<T: Ord>(a ref T, b ref T) Ordering { return a.Cmp(b) }\nfunc f(a ID, b ID) Ordering { return O(ref a, ref b) }", nil}, // a type over a number is what the number is
		{"type Name string\nfunc C<T: Clone>(x ref T) T { return x.Clone() }\nfunc f(n ref Name) Name { return C(n) }", nil},
		{"struct R impl Eq, Ord { n int }\nfunc (a ref R) Eq(o ref R) bool { return true }", nil},
		{"struct Both impl Ord, Eq { n int }\nfunc (a ref Both) Cmp(o ref Both) Ordering { return Equal }", nil},
		{"struct Link impl Eq, Clone, Hash { v int; next *Link? }", nil}, // a type reaching itself through a pointer

		// The operator traits: a + b is a.Add(b), both operands by value, the right one's type an argument.
		{"struct V impl Add { x int }\nfunc (a V) Add(o V) V { return V{x: a.x + o.x} }\nfunc f(a V, b V) V { return a + b }", nil},
		{"struct V impl Add { x int }", []string{"V does not implement Add: missing method Add(V) V"}},
		{"struct V impl Add { x int }\nfunc (a ref V) Add(o V) V { return V{x: 1} }", []string{"method V.Add has the wrong signature for Add: have (ref V) Add(V) V, want (V) Add(V) V"}},
		{"struct V impl Mul<f64> { x f64 }\nfunc (a V) Mul(k f64) V { return V{x: a.x * k} }\nfunc f(a V) V { return a * 2.0 }", nil},
		{"struct V impl Mul<f64> { x f64 }\nfunc (a V) Mul(k f64) V { return V{x: a.x * k} }\nfunc f(a V, b V) V { return a * b }", []string{"cannot use b (V) as f64 in the right operand of *"}},
		{"struct V impl Mul { x f64 }\nfunc (a V) Mul(o V) V { return a }\nfunc f(a V, k f64) V { return a * k }", []string{"cannot use k (f64) as V in the right operand of *"}},
		{"struct V impl Mul, Mul<f64> { x f64 }\nfunc (a V) Mul(o V) V { return a }", []string{"Mul listed twice"}},
		{"struct V impl Mul<f64, int> { x f64 }", []string{"Mul takes one type argument, the type of the other operand"}},
		{"struct V impl Shape<int> { x f64 }", []string{"Shape takes no type arguments"}},
		{"struct V impl Neg { x int }\nfunc (a V) Neg() V { return V{x: -a.x} }\nfunc f(a V) V { return -a }", nil},
		{"struct V { x int }\nfunc f(a V) V { return -a }", []string{"operator - is not defined on V"}},
		{"struct V { x int }\nfunc f(a V, b V) V { return a + b }", []string{"operator + is not defined on V"}},
		{"struct M impl Add { s string }\nfunc (a M) Add(o M) M { return a }\nfunc f(a M, b M) { c := a + b; keep(c.s); keep(a.s) }", []string{"use of moved value a"}}, // a non-Copy operand moves
		{"struct M impl Add { s string }\nfunc (a M) Add(o M) M { return a }\nfunc f(a ref M, b M) M { return a + b }", []string{"cannot move a out of a borrow"}},
		{"struct M impl Add { s string }\nfunc (a M) Add(o M) M { return a }\nfunc f(a mut M, b M) M { return a.Add(b) }", []string{"cannot move a out of a borrow"}}, // a value receiver through a borrow: the same for a method
		{"struct M impl Add { s string }\nfunc (a M) Add(o M) M { return a }\nfunc f(a ref M, b ref M) bool { return a.s == b.s }", nil},                              // reading through it is fine
		{"func Sum<T: Add>(a T, b T) T { return a + b }\nfunc f(s string, t string) int { return Sum(1, 2) + len(Sum(s, t)) }", nil},                                  // numbers and strings have +
		{"func Sum<T: Add>(a T, b T) T { return a + b }\nfunc f(a Blob, b Blob) Blob { return Sum(a, b) }", []string{"cannot use Blob as the type parameter T of Sum: Blob does not implement Add"}},
		{"func Half<T: Div>(a T, b T) T { return a / b }\nfunc f(s string, t string) string { return Half(s, t) }", []string{"cannot use string as the type parameter T of Half: string does not implement Div"}},
		{"func Sc<T: Mul<f64>>(a T, k f64) T { return a * k }\nstruct V impl Mul<f64> { x f64 }\nfunc (a V) Mul(k f64) V { return a }\nfunc f(v V) f64 { return Sc(v, 2.0).x + Sc(1.5, 2.0) }", nil}, // f64 is Mul<f64>
		{"func Sc<T: Mul<f64>>(a T, k f64) T { return a * k }\nfunc f() int { return Sc(1, 2.0) }", []string{"cannot use int as the type parameter T of Sc: int does not implement Mul<f64>"}},
		{"func N<T: Neg>(a T) T { return -a }\nfunc f(s string) string { return N(s) }", []string{"cannot use string as the type parameter T of N: string does not implement Neg"}},
		{"struct M impl Add { s string }\nfunc (a M) Add(o M) M { return a }\nfunc f(a M) M { return a + a }", []string{"use of moved value a"}},                                                                                                           // the left operand is taken before the right is read
		{"struct V impl Mul<f64> { x f64 }\nfunc (a V) Mul(k f64) V { return a }\nfunc Sq<T: Mul>(a T, b T) T { return a * b }\nfunc f(v V, w V) V { return Sq(v, w) }", []string{"cannot use V as the type parameter T of Sq: V does not implement Mul"}}, // Mul<f64> is not Mul<V>
		{"struct V impl Mul { x f64 }\nfunc (a V) Mul(o V) V { return a }\nfunc Sq<T: Mul<V>>(a T, b V) T { return a * b }\nfunc f(v V, w V) V { return Sq(v, w) }", nil},                                                                                  // Mul listed bare is Mul<V>
		{"struct V impl Mul<V> { x f64 }\nfunc (a V) Mul(o V) V { return a }\nfunc Sq<T: Mul>(a T, b T) T { return a * b }\nfunc f(v V, w V) V { return Sq(v, w) }", nil},                                                                                  // and Mul<V> listed is Mul
		{"struct V impl Mul<f64> { x f64 }\nfunc (a V) Mul(k f64) V { return a }\nfunc f(v V) V { return v * 2 }", nil},                                                                                                                                    // the right operand takes its hint from the method
		{"struct V impl Add, Mul<f64> { x f64 }\nfunc (a V) Add(o V) V { return a }\nfunc (a V) Mul(k f64) V { return a }\nfunc f(v mut V, w V) { v += w; v *= 2 }", nil},                                                                                  // a += b is a = a + b
		{"struct V impl Mul<f64> { x f64 }\nfunc (a V) Mul(k f64) V { return a }\nfunc f(v mut V, w V) { v *= w }", []string{"cannot use w (V) as f64 in the right operand of *="}},
		{"struct M impl Add { s string }\nfunc (a M) Add(o M) M { return a }\nfunc f(xs mut []M, m M) { xs[0] += m }", nil}, // the element's value goes into the method and the sum comes back
		{"enum Sign impl Copy, Add, Div, Rem { Up; Down }\nfunc (a Sign) Add(o Sign) Sign { return a }\nfunc (a Sign) Div(o Sign) Sign { return a }\nfunc (a Sign) Rem(o Sign) Sign { return a }\nfunc f(a Sign, b Sign) Sign { return a + b / a % b }", nil},
		{"struct P<T: Copy + Mul> impl Mul<T> { v T }\nfunc (a P<T>) Mul(k T) P<T> { return P<T>{v: a.v * k} }\nfunc f(p P<f64>) P<f64> { return p * 2.0 }", nil}, // the declaration's own parameter as the argument
		{"func Own<T: Mul<T>>(a T, b T) T { return a * b }\nfunc f() int { return Own(2, 3) }", nil},                                                              // and in a bound
		{"struct V impl Mul<Nope> { x f64 }\nfunc (a V) Mul(k f64) V { return a }", []string{"undefined: Nope"}},                                                  // one message, no instance of an invalid type
		{"func D<T: Drop>(x T) {}\nfunc f() { D(Blob{n: 1}) }", []string{"cannot use Blob as the type parameter T of D: Blob does not implement Drop"}},

		// Bounds: a call admits the types that implement the trait, and the body uses its methods.
		{"func f(cs []Circle) f64 { return TotalArea(ref cs) }", nil},
		{"func f(bs []Boxed<int>) f64 { return TotalArea(ref bs) }", nil},
		{"func f(ts []Tagged) f64 { return TotalArea(ref ts) }", nil}, // implementing Logged implements Shape
		{"func Pass<U: Logged>(xs ref []U) f64 { return TotalArea(xs) }", nil},
		{"func Eq2<T: Comparable>(a ref T, b ref T) bool { return a.Same(b) }", nil},
		{"func f(bs []Blob) f64 { return TotalArea(ref bs) }", []string{"cannot use Blob as the type parameter T of TotalArea: Blob does not implement Shape"}},
		{"func f(ns []int) f64 { return TotalArea(ref ns) }", []string{"cannot use int as the type parameter T of TotalArea: int does not implement Shape"}},
		{"func f(b Bounded<Circle>) f64 { return b.v.Area() }", nil},
		{"func f(b Bounded<Blob>) {}", []string{"cannot use Blob as the type parameter T of Bounded: Blob does not implement Shape"}},
		{"func Describe<T: Shape>(x ref T) string { return x.Name() }", nil},
		{"func Both<T: Shape + Named>(x ref T) string { return x.Name() + x.Label() }", nil},
		{"func Through<T: Logged>(x ref T) f64 { return x.Area() }", nil}, // a refining bound brings the refined trait's methods
		{"func Pass<U: Shape>(xs ref []U) f64 { return TotalArea(xs) }", nil},
		{"func Pass<U>(xs ref []U) f64 { return TotalArea(xs) }", []string{"cannot use U as the type parameter T of TotalArea: U does not implement Shape"}},
		{"func Bad<T>(x ref T) f64 { return x.Area() }", []string{"Area is not a field or method of ref T: no bound of T declares it"}},
		{"func Bad<T: Named>(x ref T) f64 { return x.Area() }", []string{"Area is not a field or method of ref T: no bound of T declares it"}},
		{"func Own<T: Shape>(x T) f64 { return x.Area() }", nil}, // an owned value is lent to the ref receiver

		// Wrong lists and uses.
		{"struct X impl Blob {}", []string{"Blob is not a trait"}},
		{"struct X impl keep {}", []string{"keep is not a trait"}},
		{"struct X impl Nope {}", []string{"undefined: Nope"}},
		{"func f<T: Blob>() {}", []string{"Blob is not a trait"}},
		{"trait Twice: Blob {}", []string{"Blob is not a trait"}},
		{"struct X impl fmt.Formatter {}", []string{"undefined: fmt.Formatter"}},
		{"struct X impl fmt.Stringer {}", []string{"X does not implement Stringer: missing method String() string"}},                   // fmt's one trait
		{"struct Error impl Error { msg string }\nfunc (e ref Error) Error() string { return e.msg.Clone() }", nil},                    // the trait behind the type of the same name
		{"struct Error { msg string }\nstruct X impl Error {}", []string{"X does not implement Error: missing method Error() string"}}, // the trait, not the struct beside it
		{"func f<T: fmt.Formatter>() {}", []string{"undefined: fmt.Formatter"}},
		{"struct X impl Shape<int> {}", []string{"Shape takes no type arguments"}},
		{"struct X impl Named, Named {}\nfunc (x ref X) Label() string { return \"\" }", []string{"Named listed twice"}},
		{"trait Both: Shape, Shape {}", []string{"Shape listed twice"}},
		{"func f() { s := Shape{} ; print(s) }", []string{"cannot build a value of type Shape with a composite literal"}},

		// A trait as a type: an owned value made from a value of an implementing type, with the trait's methods and fields.
		{"func f(c Circle) Shape { return c }", nil},
		{"func f() { var s Shape = Circle{r: 1}; print(s.Area()) }", nil},
		{"func f(s Shape) f64 { return s.Area() }", nil},
		{"func f(s ref Shape) string { return s.Name() }", nil},
		{"func f() { xs := []Shape{Circle{r: 1}, Square{s: 2}}; print(len(xs)) }", nil},
		{"func f(xs ref []Shape) f64 { return TotalArea(xs) }", nil}, // a trait value satisfies its trait's bound
		{"func f(xs ref []Logged) f64 { return TotalArea(xs) }", nil},
		{"func Eq2<T: Comparable>(a ref T, b ref T) bool { return a.Same(b) }\nfunc f(a Comparable, b Comparable) bool { return Eq2(ref a, ref b) }", []string{"cannot use Comparable as the type parameter T of Eq2: the method Same of Comparable mentions Self"}},
		{"func K<T: Kids>(x ref T) {}\nfunc f(k Kids) { K(ref k) }", []string{"cannot use Kids as the type parameter T of K: the field kids of Kids mentions Self"}},
		{"func f(l Labeled) string { return l.Label() }", nil},     // a default, dispatched on the value's type
		{"func f(l ref Labeled) int { return len(l.label) }", nil}, // a trait field, through the value
		{"func f(l mut Labeled) { l.label = \"x\" }", nil},
		{"trait Mu { func (s mut Self) Bump() }\nfunc f(x mut Mu) { x.Bump() }", nil},
		{"trait Mu { func (s mut Self) Bump() }\nfunc f(x ref Mu) { x.Bump() }", []string{"cannot borrow x as mut: x is a ref borrow"}},
		{"func f(s Shape) { t := s; print(s.Area(), t.Area()) }", []string{"use of moved value s"}}, // a trait value moves
		{"func f(b Blob) Shape { return b }", []string{"cannot use b (Blob) as Shape in return: Blob does not implement Shape"}},
		{"func f(c ref Circle) Shape { return c }", []string{"cannot use c (ref Circle) as Shape in return: a trait value is made from an owned value, not a borrow"}},
		{"func f(l Logged) Shape { return l }", []string{"cannot use l (Logged) as Shape in return: a trait value cannot be used as another trait yet"}},
		{"func f(n int) Shape { return n }", []string{"cannot use n (int) as Shape in return: only a declared type implements a trait"}},
		{"func f(a Comparable, b Comparable) bool { return a.Same(b) }", []string{"cannot call Same through Comparable: its signature mentions Self"}},
		{"trait Mk { func (s ref Self) Dup() Self }\nfunc f(m ref Mk) { m.Dup() }", []string{"cannot call Dup through Mk: its signature mentions Self"}}, // Self in a result
		{"trait Lk { var next *Self? }\nfunc f(l ref Lk) { print(l.next) }", []string{"cannot reach next through Lk: its type mentions Self"}},
		{"func f(t Logged) f64 { return t.Area() }", nil}, // a refined trait's method, through the value
		{"func f(l ref Loud) string { return l.label.Clone() + l.Label() }", nil},
		{"func f(c Circle) f64 { var s Shape = c; return c.r + s.Area() }", nil}, // a Copy value is copied into the trait value
		{"func f(b Bounded<Logged>) {}", nil},
		{"func f(c Copied<Shape>) {}", []string{"Shape does not implement Copy"}},
		{"func f(l Labeled) { x := l.label; print(x, l.Label()) }", []string{"use of partially moved value l"}},
		{"func f<T: Shape>(x T) Shape { return x }", []string{"cannot use x (T) as Shape in return: a type parameter cannot become a trait value yet"}},
		{"func f(c *Circle) Shape { return c }", []string{"cannot use c (*Circle) as Shape in return: a pointer does not become a trait value; the value it holds does"}},
		{"struct R impl Named { r ref int }\nfunc (x ref R) Label() string { return \"r\" }\nfunc g() Named { n := 1; r := R{r: ref n}; return r }", []string{"cannot use r (R) as Named in return: R holds a borrow and cannot become a trait value"}},
		{"struct R impl Named { r ref int }\nfunc (x ref R) Label() string { return \"r\" }\nfunc g(n ref int) []Named { r := R{r: n}; return []Named{r} }", []string{"R holds a borrow and cannot become a trait value"}},
		{"func f(s Shape) { s.Nope() }", []string{"Nope is not a field or method of Shape"}},
		{"func f(s Self) {}", []string{"Self is only defined inside a trait"}},

		// Wrong declarations.
		{"trait T { func (s ref Self) F(); func (s ref Self) F() }", []string{"method T.F redeclared; first declared at"}},
		{"trait T { func (s ref Circle) F() }", []string{"a trait method's receiver is Self, ref Self or mut Self"}},
		{"trait T { func F() }", []string{"a trait method needs a receiver: (s ref Self)"}},
		{"trait T: T {}", []string{"trait T refines itself"}},
		{"trait A: B {}\ntrait B: A {}", []string{"trait A refines itself"}},
		{"trait T<U> { func (s ref Self) F() U }", nil},
		{"trait T { func (s ref Self) F<U>(u U) }", []string{"a required method cannot have type parameters: an implementor's method has none"}},
		// Generic traits: an instance per argument list, with the methods over the arguments.
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It<int> { n int }\nfunc (r mut R) Next() int? { return None }", nil},
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It<int> { n int }\nfunc (r mut R) Next() string? { return None }", []string{"method R.Next has the wrong signature for It<int>: have (mut R) Next() string?, want (mut R) Next() int?"}},
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It { n int }", []string{"It takes 1 type argument: It<...>"}},
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It<int, int> { n int }", []string{"It takes 1 type argument"}},
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It<int> { n int }\nfunc (r mut R) Next() int? { return None }\nfunc Sum<I: It<int>>(it mut I) int { n := 0; for { n += it.Next() ?? break }; return n }\nfunc f(r mut R) int { return Sum(mut r) }", nil},
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It<int> { n int }\nfunc (r mut R) Next() int? { return None }\nfunc Sum<I: It<string>>(it mut I) int { return 0 }\nfunc f(r mut R) int { return Sum(mut r) }", []string{"cannot use R as the type parameter I of Sum: R does not implement It<string>"}},
		{"trait It<U> { func (s mut Self) Next() U?\nfunc (s mut Self) Count() int { n := 0; for { s.Next() ?? break; n += 1 }; return n } }\nstruct R impl It<int> { n int }\nfunc (r mut R) Next() int? { return None }\nfunc f(r mut R) int { return r.Count() }", nil},                                       // a default over the argument
		{"trait It<U> { func (s mut Self) Next() U?\nfunc (s mut Self) Count() int { return 0 } }\ntrait Two<U>: It<U> { func (s mut Self) Twice() int { return 2 } }\nstruct R impl Two<int> { n int }\nfunc (r mut R) Next() int? { return None }\nfunc f(r mut R) int { return r.Count() + r.Twice() }", nil}, // the refined instance's defaults come along
		{"trait It<U> { func (s mut Self) Next() U? }\ntrait Two<U>: It<U> {}\nstruct R impl Two<int> { n int }\nfunc (r mut R) Next() int? { return None }\nfunc Sum<I: It<int>>(it mut I) int { return 0 }\nfunc f(r mut R) int { return Sum(mut r) }", nil},                                                   // and a bound on the refined trait is met
		{"trait It<U> { func (s mut Self) Next() U? }\nstruct R impl It<int>, It<string> { n int }\nfunc (r mut R) Next() int? { return None }", []string{"It<string> listed with another instance of It: a type implements a generic trait at one argument"}},
		{"trait It<U> { func (s mut Self) Next() U? }\nfunc f(it mut It<int>) {}", []string{"It is a generic trait and is not a type yet: bound a type parameter by it, I: It<...>"}},
		{"trait It<U> { func (s mut Self) Next() U?\nfunc (s Self) With<V>(v V) V { return v } }\nstruct R impl It<int> { n int }\nfunc (r mut R) Next() int? { return None }\nfunc mk() R { return R{n: 0} }\nfunc f() int { return mk().With(1) }", nil}, // a generic method on a call's result
	}
	for _, tt := range tests {
		got := checkErrors(t, prelude+traitPrelude+tt.src)
		if len(got) != len(tt.want) {
			t.Errorf("%s:\n  got %d errors %q\n  want %d %q", tt.src, len(got), got, len(tt.want), tt.want)
			continue
		}

		for i := range got {
			if !strings.Contains(got[i], tt.want[i]) {
				t.Errorf("%s:\n  got  %q\n  want %q", tt.src, got[i], tt.want[i])
			}
		}
	}
}
