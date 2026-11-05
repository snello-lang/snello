# 11. Traits

Status: generic traits and default methods with type parameters of their own are checked and compiled (stage 2, task 2.7); trait declarations with required methods, the `impl` list and its check, refinement, and traits as bounds on type parameters are checked and compiled (stage 2, task 2.2), and so are default methods with the resolution order that picks one, trait fields and `super` (stage 2, task 2.3); `examples/run/traits.snello` runs them. A trait used as a type is checked and compiled (stage 2, task 2.4); printing a trait value is not supported yet. The standard traits are predeclared, and `Drop` asks for its method; the others get theirs with task 2.5. The samples are cut from `examples/run/traits.snello` and `examples/07-traits.snello`.

A trait is a set of methods a type promises to have. A type declares which traits it implements, and the compiler checks the promise. A trait is also what a generic function asks of its type parameters: `T: Shape` means any `T` that implements `Shape`, and inside the function `T` has `Shape`'s methods.

## Declaring a trait

A trait lists its methods as method declarations without bodies, with `Self` as the receiver's type. `Self` stands for whatever type implements the trait:

```snello
trait Shape {
    func (s ref Self) Area() f64
    func (s ref Self) Name() string
}
```

The receiver is written as any receiver is, `Self`, `ref Self` or `mut Self`, and says how the method borrows or takes the value it is called on, exactly as it would on a struct. A trait method with another receiver type is refused: the receiver of a trait method is `Self`.

## Implementing a trait

A type says what it implements on its declaration, after the word `impl`, and its methods are ordinary methods:

```snello
struct Circle impl Shape {
    r f64
}

func (c ref Circle) Area() f64    { return 3.0 * c.r * c.r }
func (c ref Circle) Name() string { return "circle" }
```

There is nothing else to write. The compiler reads `Self` as `Circle` and checks that every method the trait requires is declared on `Circle` with the signature the trait gives it: the same receiver mode, the same parameters, the same results. An enum implements a trait the same way, `enum Kind impl Named { ... }`.

The list is the implementation. A type with an `Area` and a `Name` but no `impl Shape` is not a `Shape`, however its methods look, and a type implements exactly the traits it lists: each one is a separate promise, and none brings another with it. The standard traits are no exception, so a type that wants `+`, `==` and to be copied rather than moved lists `Add`, `Eq` and `Copy`, each on its own.

A missing method is reported at the trait's name in the `impl` list, with the method as the trait wrote it:

```snello
struct Tri impl Shape {}                                  // ERROR "Tri does not implement Shape: missing method Name() string"
func (t ref Tri) Area() f64 { return 0.0 }
```

A method that is there but does not match is reported at the method, with both signatures:

```snello
struct Sq impl Named {}
func (s Sq) Label() int { return 0 }                      // ERROR "method Sq.Label has the wrong signature for Named: have (Sq) Label() int, want (ref Sq) Label() string"
```

A type implements only the traits it lists. A type with an `Area` and a `Name` method that does not say `impl Shape` is not a `Shape`, and cannot be passed where one is asked for. The declaration is the promise; the methods alone are not.

A trait's name is looked up as a trait. A package may declare a type with the same name as a trait, as `io` declares its `Error` type, and still list that trait on the type: in an `impl` list or a bound, `Error` means the trait behind the type of that name, since a type cannot stand there.

## Default methods

A trait method may have a body. It is then a default: a type implementing the trait gets the method without declaring it, and may declare its own instead.

```snello
trait Shape {
    func (s ref Self) Area() f64
    func (s ref Self) Name() string
    func (s ref Self) Describe() string {
        name := s.Name()
        return fmt.Sprint(name, " with area ", s.Area())
    }
}
```

Inside the body, `s` is a value of `Self`, and `Self` has exactly the trait's methods: `s.Name()` and `s.Area()` are fine, and whatever type implements `Shape` supplies them. Nothing else about `Self` is known, so a value of `Self` moves when passed or assigned, as a value of a type parameter does, unless the trait refines `Copy`. The body is checked once, when the trait is declared, not once per implementing type.

A type that declares the method overrides the default:

```snello
func (r ref Rect) Describe() string { return "a rectangle" }
```

A `Circle impl Shape` with no `Describe` of its own gets the trait's, so `circles[0].Describe()` prints `circle with area 3`, and `rects[0].Describe()` prints `a rectangle`. An override must have the signature the trait gives the method, as a required method must. A default may also satisfy another trait's requirement: a type implementing both a trait that requires `Name` and one that provides a default `Name` has it.

## Which default a type gets

A type may implement several traits, and more than one of them may provide a default for the same method. The compiler settles it when the program is compiled, in an order the declaration fixes:

1. The type's own methods come first. A declared method overrides every default.
2. Then the traits of the `impl` list, from right to left, each trait followed by the traits it refines. A trait reached more than once is consulted where it was reached last, so a trait always comes before the traits it refines, wherever the list names them.

A refining trait may override a default of the trait it refines:

```snello
trait Loud: Shape {
    func (s ref Self) Describe() string {
        name := s.Name()
        return fmt.Sprint("A ", name, "!")
    }
}

struct Square impl Shape, Loud { s f64 }
```

`Square` gets `Loud`'s `Describe`, since `Loud` refines `Shape` and so comes before it; `impl Loud, Shape` would give the same answer. The call `sq.Describe()` prints `A square!`.

Two traits that both provide a default for a method, where neither refines the other, leave the choice open, and the compiler refuses to make it:

```snello
struct G impl Greeter, Waver {}                           // ERROR "G inherits Greet from both Waver and Greeter: declare G.Greet to choose"
```

Declaring `G.Greet` settles it. Every definition a type sees for one method, required or default, must have the same signature once `Self` is read as the type; a disagreement is reported with both.

A default is compiled once per type that takes it, with `Self` read as that type, exactly as a generic function is compiled once per type argument. Each call of `Describe` on a `Circle` is a direct call of `Circle`'s copy, and a `Scaled<Circle>` that inherits the default gets a copy of its own.

## `super`

An override often wants to add to the method it replaces rather than start over. Inside a method, `super.M(args)` calls the next definition of `M` in the resolution order, with the method's own receiver:

```snello
trait Loud: Shape {
    func (s ref Self) Describe() string {
        d := super.Describe()
        return "[loud] " + d
    }
}

func (r ref Rect) Describe() string {
    d := super.Describe()
    return "a rectangle, " + d
}
```

"Next" is read in the resolution order of the type the method runs for. Inside a type's own method, the next definition is the first default for `M` among the type's traits, so `Rect`'s `Describe` wraps the one it overrides, and `rects[0].Describe()` prints `a rectangle, rect with area 6`. Inside a trait's default, it is the first default for `M` after that trait in the type's order. Usually that is a trait the current one refines, so `Loud`'s `Describe` wraps `Shape`'s and `sq.Describe()` prints `[loud] square with area 9`. But a type's `impl` list may put another trait's definition between the two, and then `super` reaches that one:

```snello
trait Plain { func (p ref Self) Tag() string { return "plain" } }
trait Fancy: Plain { func (p ref Self) Tag() string { d := super.Tag(); return "fancy " + d } }
trait Noted: Plain { func (p ref Self) Note() string { d := super.Tag(); return "note: " + d } }

struct Gift impl Fancy, Noted { n int }
```

`Gift`'s order is `Noted`, `Fancy`, `Plain`. `Noted` refines only `Plain`, but `Fancy` stands between them in `Gift`'s order, so `Noted`'s `super.Tag()` is `Fancy`'s `Tag`, whose `super` is `Plain`'s: `g.Note()` prints `note: fancy plain`. Had `Noted` defined `Tag` itself, `Gift` would have had two unrelated definitions of it, which is the ambiguity above. For the call to have one type wherever it lands, every definition of `M` a type can see must agree in signature, which the compiler checks when a refining trait redeclares a method and when a type takes its traits.

`super` stands for the receiver, so the rules of chapter 6 apply to it as to `s` or `r`: a `mut` method cannot be reached through a `ref` receiver, and the message says so, `cannot borrow super as mut: super is a ref receiver`; a value receiver passed to `super.M()` moves, as it would to `s.M()`. A `mut` receiver may call a `ref` default through `super`, as `Square`'s `Grow` does. `super` may be written inside a closure in the method, which then captures the receiver.

Which definition is next depends on the type, so the compiler settles it per implementing type when it compiles the default, and the call is direct. `super` must be followed by a method call, is defined only inside a method, and needs a definition with a body to reach: `super.M()` in a trait whose refined traits only require `M` is refused, as it is in a type none of whose traits provides a default for `M`.

## Trait fields

A trait may declare fields with `var`. Every type implementing the trait stores them, inline, after its own fields, and sets them at construction like any other field:

```snello
trait Named {
    var name string
    func (n ref Self) Name() string { return n.name.Clone() }
}

struct Circle impl Shape, Named {
    r f64
}

circles := []Circle{{r: 1, name: "circle"}, {r: 2, name: "big circle"}}
rects[1].name = "unit square"
```

Inside the trait's default methods, a value of `Self` has the trait's fields as well as its methods, so `n.name` reads the field of whatever type implements `Named`; so does `x.name` on an `x` of type `T` bounded by `Named`. A field is a field: reading a `string` out of a borrowed `Self` moves it, which chapter 6 refuses, so the default above clones it, as a method of any struct would have to. The cheaper getter returns a borrow of the field, tied to the receiver as chapter 6 describes, and is what `examples/07-traits.snello` does:

```snello
func (n ref Self) Name() ref string { return ref n.name }
```

A trait field has no initial value; `var name string = "x"` is a syntax error.

Each field name means one place. A type declaring a field a trait of its also declares is refused at the type's field, and two traits giving one type a field of the same name are refused at the type, since neither could be meant. A trait with fields can be implemented only by a struct; an enum has nowhere to store them. A field whose type puts `Self` inside owned data, `var kids []Self`, is checked again for each implementing type, so a type holding a borrow cannot be stored through it, as it could not be stored in a slice of its own (chapter 6).

## Refinement

A trait may build on another, written with a colon after its name:

```snello
trait Solid: Shape {
    func (s ref Self) Volume(h f64) f64
}
```

A type implementing `Solid` must declare `Shape`'s methods as well as `Volume`, and implements `Shape` by doing so: a `Rect impl Solid` can be passed wherever a `Shape` is wanted. A trait may refine several traits, separated by commas, and a trait may not refine itself, directly or through others.

## Traits as bounds

A type parameter may be bounded by a trait. Inside the function, a value of the parameter's type has the trait's methods, and nothing else:

```snello
func TotalArea<T: Shape>(xs ref []T) f64 {
    sum := 0.0
    for _, x := range xs {
        sum += x.Area()
    }

    return sum
}
```

A call checks each type argument against the bounds of its parameter, found by inference as chapter 12 describes or written at the call. A type that does not implement the trait is refused with the parameter and the trait named:

```snello
TotalArea(ref circles)                                    // fine: Circle implements Shape
TotalArea(ref blobs)                                      // ERROR "cannot use Blob as the type parameter T of TotalArea: Blob does not implement Shape"
```

Without a bound, a type parameter has no methods, since the function must work for every type:

```snello
func Bad<T>(x ref T) f64 { return x.Area() }              // ERROR "Area is not a method of ref T: no bound of T declares it"
```

Several bounds are joined with `+`, `T: Shape + Named`, and the parameter has the methods of all of them. A bound by a refining trait brings the refined trait's methods: with `T: Solid`, both `x.Volume(h)` and `x.Area()` are there. A generic function may pass its own parameter on to another: inside `TotalArea<T: Shape>`, a call of `Describe<U: Shape>` with a `T` is fine, since `T` implements `Shape`; a parameter without the bound is refused the same way a type would be.

A generic type takes bounds the same way, and may itself implement a trait by using them:

```snello
struct Scaled<T: Shape> impl Shape {
    inner T
    k     f64
}

func (s ref Scaled<T>) Area() f64    { return s.inner.Area() * s.k * s.k }
func (s ref Scaled<T>) Name() string { return "scaled " + s.inner.Name() }
```

`Scaled<Circle>` is a `Shape`, and so is `Scaled<Scaled<Circle>>`; `Scaled<Blob>` is refused at the type, since `Blob` does not implement `Shape`.

## How a bound compiles

A call through a bound is a direct call. `TotalArea` is compiled once for `Circle` and once for `Rect`, as chapter 12 describes for any generic function, and in the copy for `Circle` the call `x.Area()` is a call of `Circle`'s own `Area`. Nothing is looked up when the program runs, and a bounded parameter costs nothing a hand-written function would not.

## The standard traits

Some traits come with the language and can be listed after `impl` or used as bounds without being declared: `Copy`, `Clone`, `Eq`, `Ord`, `Hash`, `Drop`, `Error`, and the operator traits `Add`, `Sub`, `Mul`, `Div`, `Rem` and `Neg`. Each of the first seven asks for one method, or none:

| Trait | Method | What it gives |
|---|---|---|
| `Copy` | none | the value copies instead of moving (chapter 5) |
| `Clone` | `func (s ref Self) Clone() Self` | an owned copy, asked for by name |
| `Eq` | `func (s ref Self) Eq(o ref Self) bool` | `==` and `!=` |
| `Ord`, refining `Eq` | `func (s ref Self) Cmp(o ref Self) Ordering` | `Less`, `Equal` or `Greater` |
| `Hash`, refining `Eq` | `func (s ref Self) Hash() u64` | use as a map key |
| `Drop` | `func (s mut Self) Drop()` | code that runs when the value dies (chapter 5) |
| `Error` | `func (s ref Self) Error() string` | the message of an error value |

`Ordering` is a predeclared enum with the three cases named above, and every number and string has `Cmp`: `a.Cmp(b)` on two `int` values, `s.Cmp(t)` on two `ref string`.

The predeclared types implement these traits by their structure. Every number is `Copy`, `Clone`, `Eq`, `Ord` and `Hash`; a string is all of those but `Copy`; the `error` type is `Error`; a tuple, an array, an option, a slice or a map is what its parts are, so `(int, string)` is `Eq` and `Hash`, and `[]int` is `Clone` but not `Eq`. An enum whose payloads are comparable is `Eq` and `Hash` without saying so, since `==` on an enum has always been allowed. A struct is not: it implements a standard trait by listing it, and then in one of two ways. It can write the method. Or it can list the trait and write nothing, and the compiler derives the method from the fields: `Eq` compares them one by one, `Cmp` orders by the first field that differs, `Hash` combines their hashes, `Clone` clones each. Derivation needs every field to implement the trait, and the first field that does not is reported with its type:

```snello
struct Pt impl Copy, Eq, Ord { x, y int }           // Eq and Cmp derived; Copy checked
struct Bag impl Eq { items []int }                    // ERROR "Bag does not implement Eq: field items has type []int, which is not Eq"
```

`Copy` is the same check with no method to derive: a type listing it must have only `Copy` fields and no `Drop` method, and is refused at the field or the method otherwise. A struct or enum of `Copy` fields is `Copy` whether or not it says so, as chapter 5 describes, so the listing is a promise the compiler keeps for you. `Ord` and `Hash` refine `Eq`, so listing either derives `Eq` as well, or checks the `Eq` you wrote beside your `Cmp` or `Hash`. A type that writes one of `Eq` and `Hash` must write the other, since a map needs equal values to hash alike, and the compiler can promise that only for the pair it derives. A bound `T: Copy` admits exactly the types whose values copy, so `Copied<string>` is refused with `string does not implement Copy`, and the same holds for the others: `T: Ord` admits numbers, strings and the types listing `Ord`.

A map key must be `Eq` and `Hash`. Numbers, strings, booleans, enums and tuples of those are; a struct is when it lists `Hash`; a slice, a map or a trait value is not, and `map[Shape]int` is refused with `Shape cannot be a map key: it does not implement Hash`.

A type declared over a predeclared one, `type ID int`, is what that type is, `Ord` and `Clone` included, and it lists any other trait it implements after the type: `type Duration i64 impl fmt.Stringer` writes a `String` method and prints through it. `==` on a struct that wrote `Eq` calls it, and a map with such a key hashes and compares through the `Hash` and `Eq` it wrote; a derived method is written from the fields, so a derived `Clone` of a struct clones each field, calling the `Clone` a field's type wrote, and a derived `Cmp` of an enum orders the variants as declared and then their payloads. Numbers, strings and `bool` have `Eq`, `Hash` and `Clone` by name as well as `Cmp`, so a generic body bounded by one of those traits runs over them: `Largest<T: Ord + Clone>` in `examples/run/standard.snello` finds the largest `int`, `Pt` or `Grade`.

A value whose type implements `Error` prints as its message: `fmt.Println("error:", e)` writes what `e.Error()` returns, and an `Err(e)` prints the message in quotes as it would a string. `io.Error` is one such type (chapter 14).

### The operator traits

The operator traits give a type the arithmetic operators. Each has one method, taking both operands by value and returning `Self`: `Add` asks for `func (a Self) Add(o Self) Self` and gives `+`; `Sub`, `Mul`, `Div` and `Rem` do the same for `-`, `*`, `/` and `%`; `Neg` asks for `func (a Self) Neg() Self` and gives the unary `-`. The right operand has the type of the left unless the trait is listed with an argument: `Mul<f64>` asks for `func (a Self) Mul(k f64) Self`, so a vector scales by a number. `Mul` alone means `Mul<Self>`, and the two spellings are one trait: a `Vec2` listing `Mul` satisfies a bound `T: Mul<Vec2>`, and one listing `Mul<f64>` does not satisfy `T: Mul`, since that method takes a `Vec2`. The argument may be a type parameter of the declaration, `struct P<T: Mul> impl Mul<T>`, or of the bound, `Own<T: Mul<T>>`. A type has one `Mul`, so it lists `Mul` or `Mul<f64>`, not both.

```snello
struct Vec2 impl Copy, Eq, Add, Sub, Mul<f64>, Neg {
    x, y f64
}

func (a Vec2) Add(b Vec2) Vec2 { return Vec2{x: a.x + b.x, y: a.y + b.y} }
func (a Vec2) Mul(k f64) Vec2  { return Vec2{x: a.x * k, y: a.y * k} }
func (a Vec2) Neg() Vec2       { return Vec2{x: -a.x, y: -a.y} }

c := a + b
fmt.Println(c, c - a == b, a * 2.0, -a)              // {4 6} true {2 4} {-1 -2}
```

Each trait in the list stands alone: `Add` gives `+` and nothing more. `Vec2` lists `Copy` so that `a + b` leaves `a` and `b` usable, and `Eq` so that `==` is defined; without `Copy`, the operands move. `a + b` is `a.Add(b)`, and the compiler writes it as that direct call. The operands move into the method unless their type is `Copy`, as a value receiver and a by-value argument always do, so a `Money` that owns its currency string is consumed by `wallet + tip`, and a borrow of such a value cannot be an operand, as it could not be a value receiver: `cannot move a out of a borrow`. The right operand must have the type the method asks for, and a literal takes it, so `v * 2` scales by `2.0`; `v * w` on a `Vec2` that lists `Mul<f64>` is refused with `cannot use w (Vec2) as f64 in the right operand of *`. `a + a` on a value that moves is a use after a move, as `a.Add(a)` is, since the left operand is taken first. Compound assignment follows: `v += w` is `v = v + w` and `vs[0] *= 2` is `vs[0] = vs[0] * 2`, the old value going into the method and the result coming back to the place. Numbers have every operator with their own type on the right, and strings have `+`, so a bound admits them: `Sum<T: Add + Copy>` runs over a `[]int` and a `[]Vec2` alike, and `T: Mul<f64>` admits `f64` and `Vec2` but not `int`. `==` is not an operator trait, it is `Eq`; and `<` has no trait, since an ordering is asked for by name, `a.Cmp(b)`. `examples/run/standard.snello` runs all of this.

## A trait as a type

A trait's name is also a type. A value of type `Shape` holds a value of any type that implements `Shape`, and a `[]Shape` holds a mix of them:

```snello
mixed := []Shape{Circle{r: 1, name: "circle"}, Rect{w: 2, h: 3, name: "rect"}}

func PrintAll(xs ref []Shape) {
    for _, x := range xs { fmt.Println(x.Describe()) }
}
```

A value of an implementing type becomes a `Shape` wherever a `Shape` is wanted: assigned, passed, returned, or written as an element. The value moves into the trait value, or is copied into it when its type is `Copy`; the trait value owns it from then on, so a trait value is not `Copy` and moves as a `string` does, and it is dropped with what it holds. A borrow does not become a trait value, since there would be nothing to own, and neither does a type that holds a borrow, since the trait value would hide the borrow from the rules of chapter 6. A type that does not list the trait is refused with the reason:

```snello
func f(b Blob) Shape { return b }                         // ERROR "cannot use b (Blob) as Shape in return: Blob does not implement Shape"
```

A trait value has the trait's methods and fields, including the methods of the traits it refines and the defaults, and a call goes to the method of the type the value holds: `x.Describe()` on a `Shape` holding a `Rect` runs `Rect`'s `Describe`. The receiver rules are the usual ones, so a `mut` method needs a `mut Shape` or an owned one. A method whose parameters or results are written with `Self`, such as `Same(o ref Self) bool`, cannot be called through a trait value, since the value's own type is not known where the call is written; the compiler says so. A trait value also satisfies its own trait as a bound, so `TotalArea(ref mixed)` works on a `[]Shape`, with the same limit: the bound's traits must have no method or field written with `Self`, since the body may reach any of them through the parameter. `Eq<T: Comparable>` refuses two `Comparable` values with `the method Same of Comparable mentions Self`.

The cost is one allocation when the value is made and one indirect call per method call, and nothing else: the value is a pointer to the owned data and a pointer to a table of the type's methods, built once per type and trait when the program is compiled, with the method each name resolves to for that type, so an inherited default or `super` works through a trait value as it does on the type. A method taking `Self` by value consumes the trait value, as it would consume the type's value; a `mut` method needs a `mut` trait value. A trait value built in an expression and used at once, `Make().Describe()`, is dropped when its statement ends, as any such value is. A trait value cannot be built with a composite literal, `Shape{}`, since it has no fields of its own; it is made from a value. A value of a refining trait, `Logged`, is not yet usable where a `Shape` is wanted, and a trait value cannot be printed yet; `examples/run/traits.snello` runs the rest.

## Generic traits

A trait may take type parameters, written as a type's are, and its methods use them: `trait Iterator<T>` asks for `Next() T?`. A type implements an instance, `impl Iterator<int>`, and gets the methods with the argument in place; a generic type may implement the trait over one of its own parameters, `struct MapIter<I: Iterator<T>, T, U> impl Iterator<U>`, and a bound may name an instance, `I: Iterator<T>`, which a type argument satisfies by implementing exactly that instance. A default method may have type parameters of its own, `Map<U>(f func(T) U)`, inferred at the call from its function argument as a generic function's are; a required method may not, since the implementor's method has none to give it. Inside the trait, `Iterator<T>` names the trait itself, so a default may hand `Self` to a type bounded by it. The sample is cut from `examples/run/iterators.snello`, which `examples/18-iterators.snello` is built on:

```snello
trait Iterator<T> {
	func (it mut Self) Next() T?
	func (it Self) Map<U>(f func(T) U) MapIter<Self, T, U> { return MapIter{inner: it, f: f} }
	func (it mut Self) Collect() []T {
		out := []T{}
		for { out.Push(it.Next() ?? break) }
		return out
	}
}

struct Range impl Iterator<int> { cur int; end int }

evens := Range{0, 5}.Map(plusTen).Filter(func(x ref int) bool { return x % 2 == 0 }).Collect()   // [10 12 14]
```

Each instance of a generic trait is its own trait to the checker, made once per argument list, and a type lists one instance of it, as a type has one `Mul`. A default method of a generic trait compiles once per implementing type, with the trait's arguments and the method's own in place.

Two limits stand for now. A generic trait is not a type: `func drain(it mut Iterator<int>)` is refused, and a type parameter bounded by the instance, `drain<I: Iterator<int>>(it mut I)`, is the way to write it. And a bound's arguments are not inferred from the type that meets it: `CountAll<T, I: Iterator<T>>(it mut I)` is called as `CountAll<int, Range>(mut r)`, since `T` is not read off `Range`'s `impl Iterator<int>`.

## What is still to come

A generic trait as a type, `[]Iterator<int>`, and the inference of a bound's arguments from the implementing type, above; and a trait value of a generic instance waits for a program that needs one.
