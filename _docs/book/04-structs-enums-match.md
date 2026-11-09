# 4. Structs, enums and match

`examples/run/shapes.snello` exercises this chapter.

Snello has two ways to build a type out of others. A struct holds all of its fields at once. An enum holds exactly one of its variants at a time. `match` takes an enum apart, and the compiler checks that no variant is forgotten. Both exist only by declaration: a struct has a name, and there is no struct type written inline. A value that carries nothing is `()`, the unit of chapter 3.

## Structs

```go
struct Node {
    value    string
    parent   NodeID?               // None for the root
    children []NodeID
    live     bool                  // false after Remove; the slot is kept
}

struct Count { word string; n int }
```

A struct declaration is `struct Name { fields }`, one field per line or separated by `;`, each a name and a type. The traits a struct implements are listed on the same line, `struct TempDir impl Drop { path string }` (chapter 11). `type Name T` gives a new name to an existing type, as in `type NodeID int`.

A struct is built with a keyed literal and used with dot notation:

```go
n := Node{value: value, parent: None, children: []NodeID{}, live: true}
v := Vec3{1, 2, 3}                    // every field in order, when the names add nothing
t := Tree{nodes: []Node{{value: value, parent: None, children: []NodeID{}, live: true}}}
if !t.nodes[id].live { return }
```

Every field has a zero value, so `Stack{}` is an empty stack. A named type is converted with a call of its name, `NodeID(len(t.nodes))`.

### Methods

```go
func (t mut Tree) AddChild(parent NodeID, value string) NodeID { ... }
func (t ref Tree) Path(id NodeID) []string { ... }
func (f File) Close() Result<(), error> { ... }
```

A method is a function with a receiver in parentheses before its name, called with dot notation: `t.Path(b)`. The receiver says what the method does to its value, the way a parameter does. `t ref Tree` reads the tree; `t mut Tree` changes it; `f File` takes it, so after `f.Close()` the file is gone and cannot be used again. Chapter 6 gives the rules. A call borrows its receiver by itself, so `t.Path(b)` is written without `ref`.

## Enums

```go
enum State {
    Closed
    Connecting(attempt int)
    Open(session Session)
    Draining(session Session, ticks int)
    Failed(reason string)
}

enum Token {
    Num(f64)
    Plus
    Minus
    EOF
}
```

An enum lists its variants, one per line or separated by `;`. A variant is a bare name, or a name with fields in parentheses. Fields may be named, as in `Connecting(attempt int)`, or just typed, as in `Num(f64)`. A value is built by naming the variant: `Closed`, `Connecting(1)`, `Open(sess)`.

An enum value holds exactly the fields of the variant it is, and `match` is the only way to reach them. A state that is `Closed` has no session to read, and the compiler makes sure no code tries.

An enum whose payloads are all plain values, like `Token` above, is a `Copy` type: assigning it copies, as a number does. One that holds a string or a pointer is moved like any owned value. Chapter 5 has the rule.

Enums can be recursive through an owned pointer, which is how a tree-shaped value is written:

```go
enum Expr {
    Lit(f64)
    Neg(*Expr)                                  // *T: an owned heap pointer
    Bin(op Token, lhs *Expr, rhs *Expr)
}
```

`*Expr` is a pointer to a heap value with one owner, created with `&expr`; chapter 7 covers it. Two enums are built into the language: `T?`, an optional value with variants `Some(x)` and `None`, and `Result<T, E>` with `Ok(x)` and `Err(e)`. Chapter 8 covers them; under `match` they are ordinary enums.

### Methods and traits on an enum

An enum takes methods as a struct does, with a receiver lent `ref` or `mut` or taken by value, and the body reaches the fields of the variant through `match`, as `Describe` below does. What an enum implements is listed after its name, as for a struct:

```go
enum Token impl Copy, Eq {
    Num(f64)
    Plus
    Minus
    EOF
}

enum ConfigError impl Error {
    Missing(key string)
    BadValue(key string, value string)
}

func (e ref ConfigError) Error() string {
    return match e {
        Missing(k)     => "missing " + k,
        BadValue(k, v) => "bad value for " + k + ": " + v,
    }
}
```

The compiler checks the list: a trait named there is implemented, or the declaration is an error. For the standard traits a method need not be written. `Eq`, `Ord`, `Clone` and `Hash` listed without their method are derived variant by variant, as they are derived field by field for a struct, whenever every field has the trait. A derived `Cmp` orders by the variant's place in the declaration first, an earlier variant less than a later one, and two values of one variant by their fields in order, so with `enum Grade impl Ord { Fail; Pass(score int); Honors(score int, note string) }` every `Fail` is less than every `Pass`, and `Pass(1)` is less than `Pass(2)`.

Two of the standard traits an enum has without listing them. `==` compares any two values of an enum whose fields compare, the variants first and then the fields, and such an enum hashes the same way, so it keys a map as it is, `map[State]int{}`; a struct compares and hashes only by listing `Eq` and `Hash`. `Copy` is as the section above says, by structure: listing it makes the intent visible and checked, and the error names the field in the way, `E does not implement Copy: variant B has a field of type string, which is not Copy`. A type with a `Drop` method is never `Copy`, and an enum may have one:

```go
enum Note impl Drop {
    Quiet
    Loud(s string)
}

func (n mut Note) Drop() { fmt.Println("the note is gone") }
```

`Drop` runs when the value goes out of scope whatever its variant, and the fields of the variant are dropped after it. Chapter 5 covers `Drop`.

An enum prints as it is written in a program: `fmt.Println(Connecting(2), Closed, Open("s1", 3))` prints `Connecting(2) Closed Open("s1", 3)`, a string field quoted, and `%v` prints the same. An enum that lists `fmt.Stringer` and writes `String` prints what `String` returns, as any type that lists it does.

### Generic enums

An enum may take type parameters, as a struct and a function may (chapter 12):

```go
enum Tree<T> {
    Leaf(v T)
    Branch(left *Tree<T>, right *Tree<T>)
}

func sum(t ref Tree<int>) int {
    return match t {
        Leaf(v)       => v,
        Branch(l, r)  => sum(l) + sum(r),
    }
}
```

The type argument comes from the fields a value is built with: `Leaf(3)` is a `Tree<int>`. A variant with no field of the parameter's type cannot say what the argument is, so it takes it from where it is used, a declared type, a parameter or a return: `var head *Node? = None` is typed, and `None` on its own is `cannot tell the type of None here; the context gives it no type`. `T?` and `Result<T, E>` are generic enums the library declares, `enum Option<T> { Some(v T); None }` with `T?` as its spelling and `enum Result<T, E> { Ok(v T); Err(e E) }`, which is why they behave as the enums here do.

### Variants as names

A variant is a value when it has no fields, `Closed`, and a constructor when it has, `Connecting(1)`: the name followed by the fields in the order of the declaration. The field names in a declaration, `Connecting(attempt int)`, are for the reader and for the error messages; a value is built by position, `Connecting(attempt: 1)` is not a form, and a pattern binds by position too, `Connecting(n)`. A variant with fields is not a function value: `xs.Map(Connecting)` is refused with `Connecting has fields and must be written Connecting(...)`, and the way to pass one is a closure, `func(n int) State { return Connecting(n) }`.

The variants of an enum from another package are named through the package when used as values, `io.NotExist`, and may be bare in a pattern on a value of that enum, as the next section says. One enum is predeclared, `Ordering`, with the variants `Less`, `Equal` and `Greater`: `Cmp` returns it, and a comparison reads `a.Cmp(ref b) == Greater`.

### What an enum is not

An enum is not a number. There is no `iota` and no conversion: `int(Red)` on `enum Color { Red; Green }` is `cannot convert Color to int`. An enumeration that is a number with names, a port, a flag word, a code from the system, is a type declared over the number with constants, as Go writes it, `type Color int` and `const Red Color = 0`; chapter 3 covers declared types. The fields of a value are reached through `match` or an `if` binding and nowhere else: there is no `s.attempt`, since the value may not be a `Connecting`, and the compiler does not guess. A `match` that leaves a variant out is refused with the variant named, `match is not exhaustive: missing Connecting(_)`, and that is the point of writing an enum: every place that takes the value apart is checked against every shape the value can have.

## Match

```go
func (s ref State) Describe() string {
    return match s {
        Closed            => "closed",
        Connecting(n)     => fmt.Sprintf("connecting (attempt %d)", n),
        Open(sess)        => "open " + sess.id,
        Draining(sess, t) => fmt.Sprintf("draining %s for %d ticks", sess.id, t),
        Failed(r)         => "failed: " + r,
    }
}
```

`match value { arms }` compares the value against each arm's pattern in order and runs the first that fits. An arm is `pattern => body`, ended by a comma or by the end of the line. A variant pattern binds the variant's fields to new names: in the `Open(sess)` arm, `sess` is the session inside the state.

A `match` is exhaustive. If `State` gained a variant, every `match` on it without a `_` arm would stop compiling until the new case is handled; the error names the missing variant. A `match` is an expression when every arm yields a value, so `return match s { ... }` is the whole function body.

### Patterns

| Pattern | Matches | Example |
|---|---|---|
| Literal | that value | `'+'`, `0`, `-1`, `"-i"` |
| Range | a value in the range, both ends included | `'0'..='9'` |
| Variant | that variant, binding its fields | `Connecting(n)`, `Bin(Plus, l, r)` |
| Name | anything, bound to the name | `t`, `sess` |
| Wildcard | anything, bound to nothing | `_` |
| Tuple | each element against its pattern | `(Open(sess), Shutdown)` |
| Alternatives | any of the patterns | `Plus \| Minus` |
| Guard | the pattern, and then a condition | `Connecting(n) if n < 3` |

A name in a pattern is a variant when a variant of that name is in sight, and a binding otherwise. The variants of an enum from another package are named through the package, `io.NotExist`, or bare when the value matched is of that enum: `match e.Kind() { NotExist => ..., Permission => ... }` reads as it would inside `io`, since the scrutinee's type says which enum the names belong to. A name that is no variant of it binds, as any name would.

Patterns nest: `Bin(Plus, l, r)` matches a `Bin` whose first field is `Plus` and binds the other two. Matching on a tuple of two enums writes a state machine's transition table in one place:

```go
func step(s State, ev Event) State {
    return match (s, ev) {
        (Closed, Dial)                       => Connecting(1),
        (Connecting(n), Timeout) if n < 3    => Connecting(n + 1),
        (Connecting(_), Timeout)             => Failed("gave up"),
        (Connecting(_), Connected(sess))     => Open(sess),
        (Open(sess), Shutdown)               => Draining(sess, 5),
        (Draining(_, _), Timeout)            => Closed,
        (Open(_), Fail(m))                   => Failed(m),
        (Failed(_), Dial)                    => Connecting(1),
        (s, _)                               => s,
    }
}
```

Arms are tried in order, so the guarded `Connecting(n) if n < 3` must come before the plain `Connecting(_)`, and the last arm, `(s, _)`, catches every pair not handled above it and hands the state back unchanged.

A guard runs whether or not its arm is taken: when it fails, the next arm is tried. So a guard reads, and never moves a value declared before it; a name it declares itself, inside a closure or a block it calls, is its own. `Some(v) if read(v) > 0` lends `v` and is fine; `Some(v) if eat(v)`, where `eat` takes its string, is refused with `cannot move v in a match guard: it runs whether or not its arm is taken; lend it with ref`, since the arms after it would find `v` gone.

### Arm bodies

The body of an arm is an expression, a block, or a single statement:

```go
match c {
    '+' => return Ok(Plus),
    '0'..='9' => return Ok(Num(l.number(c))),
    _ => return Err(BadChar(c, l.pos - 1)),
}

match p.cur {
    Num(v) => { p.advance()?; return Ok(Lit(v)) },
    RParen => p.advance()?,
    t      => return Err(Unexpected(t, p.lex.pos)),
}
```

Because a comma ends the arm, a `return` inside an arm carries one expression; to return several values, return a tuple, `return (n, right)`. A `match` whose arms all return or yield nothing is a statement.

### Match on conditions

A `match` with no value takes conditions instead of patterns: the arms are tried in order, and the first whose condition holds runs. It is what Go writes as a `switch` with no value, and what an `if` and `else if` chain does, laid out as a table:

```go
func grade(score int) string {
    return match {
        score >= 90 => "A",
        score >= 80 => "B",
        score >= 70 => "C",
        _           => "F",
    }
}
```

Each arm's condition is a `bool`, and its body is what any arm's is: an expression, a block, or one statement, `return`, `break` and `continue` among them:

```go
for _, n := range readings {
    match {
        n < 0  => continue,
        n == 0 => break,
        _      => total += n,
    }
}
```

The arm `_` is required, and comes last. The compiler cannot tell whether a list of conditions covers every case, so the arm for when none holds is written out, `_ => {}` when nothing is to happen, which an `if` chain that forgets its `else` leaves unsaid; a match without it is refused with `a match with no value needs a last arm _ => ..., for when no condition holds`, and an arm after it would never run and is refused too. An arm is its condition alone: two conditions are joined with `&&`, not with a guard. A condition is a guard on the arm `_`, so it moves nothing, for the reason above; the arms themselves are paths of their own, as the branches of an `if` are, so a value one arm moves cannot be used after the `match` unless that arm gave it a new value.

Snello has no `switch`: `match x { ... }` branches on what a value is, `match { ... }` on which condition holds, and between them they do what a `switch` does, as expressions, with every case accounted for.

### Binding without a match

When only one variant matters, an `if` binding is shorter than a two-arm `match`:

```go
if parent := t.nodes[id].parent {
    t.nodes[parent].children.RemoveValue(id)
}
if (k, v) := line.SplitOnce("=") { ... }
```

The block runs with `parent` bound when the optional holds a value. The left side may be a name, `_`, a tuple of them, or a variant pattern such as `Some(n)`. Chapter 8 covers `T?` and the `??` operator, which unwraps or runs an else: `n := take(mut s.head) ?? return None`.

## What a value costs

A struct is its fields laid out in order, nothing more: no header, no hidden pointer. An enum is a small tag followed by room for the largest variant's payload, so `Shape` above is one tag and two `f64`, wherever it is stored. Building a variant writes the tag and the payload in place; it allocates nothing. `match` reads the tag and compares it to the arm's variant, in order, and binds names to the payload's fields where they lie; a `match` on a borrow binds borrows of the fields, and copies nothing either, while one on a place reached through a borrow copies the fields a `:=` would copy and borrows the rest (chapter 6).

An optional `T?` is a flag and room for a `T`, and `Result<T, E>` is a flag and room for whichever of `T` and `E` it holds. Only `&expr` allocates: it moves the value into a cell on the heap and gives back the owned pointer to it. A pointer is one machine word, so `*Node?` is a word and a flag, which is how a recursive type stays finite.

Those are also the rules the C backend follows, and `snello emit FILE` shows them applied to a program.

