# 4. Structs, enums and match

Status: implemented. The syntax is parsed (stage 0), the types, bindings and exhaustiveness are checked (stage 1, task 1.4, `teak check`), and `teak build` compiles all of it to native code (stage 1, task 1.5). `examples/run/shapes.teak` exercises this chapter.

Teak has two ways to build a type out of others. A struct holds all of its fields at once. An enum holds exactly one of its variants at a time. `match` takes an enum apart, and the compiler checks that no variant is forgotten. Both exist only by declaration: a struct has a name, and there is no struct type written inline. A value that carries nothing is `()`, the unit of chapter 3.

## Structs

```teak
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

```teak
n := Node{value: value, parent: None, children: []NodeID{}, live: true}
v := Vec3{1, 2, 3}                    // every field in order, when the names add nothing
t := Tree{nodes: []Node{{value: value, parent: None, children: []NodeID{}, live: true}}}
if !t.nodes[id].live { return }
```

Every field has a zero value, so `Stack{}` is an empty stack. A named type is converted with a call of its name, `NodeID(len(t.nodes))`.

### Methods

```teak
func (t mut Tree) AddChild(parent NodeID, value string) NodeID { ... }
func (t ref Tree) Path(id NodeID) []string { ... }
func (f File) Close() Result<(), error> { ... }
```

A method is a function with a receiver in parentheses before its name, called with dot notation: `t.Path(b)`. The receiver says what the method does to its value, the way a parameter does. `t ref Tree` reads the tree; `t mut Tree` changes it; `f File` takes it, so after `f.Close()` the file is gone and cannot be used again. Chapter 6 gives the rules. A call borrows its receiver by itself, so `t.Path(b)` is written without `ref`.

## Enums

```teak
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

```teak
enum Expr {
    Lit(f64)
    Neg(*Expr)                                  // *T: an owned heap pointer
    Bin(op Token, lhs *Expr, rhs *Expr)
}
```

`*Expr` is a pointer to a heap value with one owner, created with `&expr`; chapter 7 covers it. Two enums are built into the language: `T?`, an optional value with variants `Some(x)` and `None`, and `Result<T, E>` with `Ok(x)` and `Err(e)`. Chapter 8 covers them; under `match` they are ordinary enums.

## Match

```teak
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

```teak
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

### Arm bodies

The body of an arm is an expression, a block, or a single statement:

```teak
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

### Binding without a match

When only one variant matters, an `if` binding is shorter than a two-arm `match`:

```teak
if parent := t.nodes[id].parent {
    t.nodes[parent].children.RemoveValue(id)
}
if (k, v) := line.SplitOnce("=") { ... }
```

The block runs with `parent` bound when the optional holds a value. The left side may be a name, `_`, a tuple of them, or a variant pattern such as `Some(n)`. Chapter 8 covers `T?` and the `??` operator, which unwraps or runs an else: `n := take(mut s.head) ?? return None`.

## What a value costs

A struct is its fields laid out in order, nothing more: no header, no hidden pointer. An enum is a small tag followed by room for the largest variant's payload, so `Shape` above is one tag and two `f64`, wherever it is stored. Building a variant writes the tag and the payload in place; it allocates nothing. `match` reads the tag and compares it to the arm's variant, in order, and binds names to the payload's fields where they lie; a `match` on a borrow binds borrows of the fields, and copies nothing either, while one on a place reached through a borrow copies the fields a `:=` would copy and borrows the rest (chapter 6).

An optional `T?` is a flag and room for a `T`, and `Result<T, E>` is a flag and room for whichever of `T` and `E` it holds. Only `&expr` allocates: it moves the value into a cell on the heap and gives back the owned pointer to it. A pointer is one machine word, so `*Node?` is a word and a flag, which is how a recursive type stays finite.

Those are also the rules the C backend follows, and `teak emit FILE` shows them applied to a program.

