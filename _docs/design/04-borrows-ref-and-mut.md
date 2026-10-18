# Borrows: `ref` and `mut`

Status: decided, 2026-10-17. This note explains the three ways a value can be handed to a function in Teak, what `ref` and `mut` mean, the rules the compiler enforces, and what they buy. It is the part of the memory model a Go developer meets first, on the first function signature they write.

## The problem

Go gives you two ways to pass a value, and one of them means four things at once.

```go
func Area(r Rect) f64       // by value: a copy
func Grow(r *Rect)              // by pointer
```

A `*Rect` parameter says "do not copy", but it also silently says "may be mutated", "may be nil", and "may be kept after the call returns". The caller cannot tell from the signature which of those `Grow` will do. Code reviews, documentation, and bugs all live in that gap.

Without a garbage collector the gap is fatal rather than annoying. If a callee may keep the pointer, the caller cannot free the value when it is done. Something has to say, in the signature, whether the callee is looking, changing, or taking.

## The three parameter modes

| Teak | Meaning | Go equivalent | After the call, the caller… |
|---|---|---|---|
| `func f(v T)` | **Take.** Ownership moves into `f`. | by value, when the callee keeps it | cannot use `v` (moved), unless `T` is `Copy` |
| `func f(v ref T)` | **Look.** A shared, read-only borrow. | `*T` or by value, when the callee only reads | still owns `v`, unchanged |
| `func f(v mut T)` | **Change.** An exclusive, mutable borrow. | `*T`, when the callee mutates | still owns `v`, possibly changed |

The same three modes apply to method receivers:

```teak
func (s ref Server) GetUser(...)      // reads the server
func (t mut Tree) AddChild(...)       // changes the tree
func (f File) Close() io.Error?       // consumes the file; it cannot be used after
```

And to local bindings and call sites, where the keyword marks what is being created:

```teak
n := ref t.nodes[id]                  // n is a borrow of one node, no copy
strings.Fields(ref job.text)          // lend the text for the call; `ref` is optional here
sort.Sort(mut list)                   // lend the list for mutation
```

At a call site `mut` is always written and `ref` may be left out: an owned value passed where a `ref T` parameter is expected is borrowed for the call without a keyword, exactly as a receiver is, so `fmt.Println("error:", e)` lends `e` and keeps it. Writing the `ref` makes the same borrow visible. An owned pointer is borrowed the same way, as what it holds: `total(head)` with `head *Node` lends the node to a `ref Node` parameter (clarified 2026-10-18). A visible mutation is worth a word, as Go's `&x` is; a read is not. Decided in `05-standard-library.md`, section 5.

`Copy` types (numbers, bools, small plain structs) are copied rather than moved when passed by value, so for them the first row behaves like Go and borrowing is rarely needed.

## The rules

1. **A borrow lives from its creation to its last use, inside one function.** A borrow passed to a callee ends when the callee returns. A borrow bound to a local, `n := ref t.nodes[id]`, ends at the last line that uses `n`. The compiler computes this; nothing is written.
2. **A borrow cannot be stored in owned data or sent.** It cannot be put in a field of an owned struct, captured by a closure that is stored, placed in a channel, or captured by `spawn`. A struct or closure that holds a borrow is itself a borrow (see below).
3. **A borrow may be returned only when its source is unambiguous.** The compiler ties a returned borrow to exactly one input:
   - the receiver, for a method with a `ref` or `mut` receiver: `func (s ref Stack<T>) Peek() ref T?`;
   - otherwise the only borrowed parameter: `func Trim(s ref string) ref string`. A function with two borrowed inputs and a borrowed output is rejected: `func Longest(a ref string, b ref string) ref string` cannot be written; return a clone or an index instead. At the call site, the input borrow is extended to the last use of the returned one. This is exactly the rule Rust applies when no lifetime is written; Teak has no syntax for the cases it does not cover.
4. **Any number of `ref`, or exactly one `mut`.** While a value is borrowed `mut`, no other borrow of it exists and the owner cannot touch it. While it is borrowed `ref`, any number of other `ref` borrows may exist, but no `mut` and no mutation by the owner.
5. **A borrow can be passed on, narrower or equal.** A `mut T` can be passed to a function wanting `ref T` or `mut T`. A `ref T` can only be passed on as `ref T`. A borrow of a field or element can be derived from a borrow of the whole: from `t mut Tree`, `mut t.nodes[i]`. A borrow variable may be reassigned to a borrow derived from itself.
6. **The owner outlives every borrow.** Guaranteed by rules 1 to 3: a borrow never leaves the function that can see its owner, except by rule 3, where it rides on an input borrow the caller already holds.

One ordering rule makes rule 4 practical. **In a call, the arguments are evaluated first, and their borrows end, before the receiver or a `mut` argument is borrowed.** So `v.Push(v.Len())` is legal: `v.Len()` borrows `v` as `ref`, returns, and the borrow is over before `Push` takes `v` as `mut`. What rule 4 rejects is a borrow that must still be live when the `mut` one starts, such as a `ref` bound to a local and used after the call. (Decided 2026-10-18.)

Two more decisions came with the checker (2026-10-18). **What a pattern binds through a borrow.** A `match`, an `if` binding or `??` on a borrow variable binds borrows of the same kind, `Copy` or not: `Full(p)` through `s mut Slot` makes `p` a `mut Point`. On a place reached through a borrow, `t.nodes[id].parent` in a method on `t mut Tree`, a binding copies what `:=` would copy and borrows the rest, so `parent` is a plain `NodeID` and does not keep `t.nodes` lent; changing a `Copy` payload in place is written `match mut xs[0]`, as a mutation at a call site is written `mut`. This is the rule Rust applies to a place scrutinee, with the borrow of a non-`Copy` part where Rust would refuse to move. **A `mut` binding through a map read is refused**, with a message naming `m.GetMut(k)`, since `m[k]` for a key the map does not hold reads the zero value and a write to it would be lost.

So Teak has lifetimes. It infers every one of them and rejects the programs where inference is ambiguous, instead of asking for an annotation. Rules 2 and 3 are what make inference always possible: a borrow never outlives the function it was created in, except by riding on one input borrow.

## What cannot be written

The price of never annotating. Each has an idiom the examples use.

| Not expressible | Idiom instead | Example |
|---|---|---|
| A function returning one of two borrowed inputs | Return a clone, or an index | |
| An owned value holding a borrow: `[]ref T`, a stored callback capturing a local | An iterator (a borrow-holding struct), indices, or own the data | 10, 18 |
| A self-referential struct | An arena with indices | 01, 15 |
| Lending a local to a spawned task | Move it in, or share through `Arc` | 02, 03 |

## What `ref` buys

- **No copy.** Passing a large struct, a slice, or a string by `ref` moves nothing and allocates nothing.
- **A promise of no change.** The callee cannot mutate through a `ref`, and the compiler enforces it. The caller knows its value is intact.
- **Many readers at once.** Several `ref` borrows of one value may exist together, so read-heavy code is never serialized by the type system.
- **No nil.** A `ref T` always refers to a live value. Absence is `ref T?`.

## What `mut` buys

- **Mutation without giving up ownership.** The callee changes the value in place; the caller still owns it afterward. This is the common Go `*T` case, made explicit.
- **No aliasing.** Exactly one `mut` exists at a time, so a function holding one knows nothing else can observe or change the value while it works. Whole classes of bugs, iterator invalidation, a map modified during a range, a slice reallocated under a pointer, cannot happen.
- **Locks become borrows.** `Mutex<T>.Lock()` returns a guard that is a `mut T`. Holding the lock and holding the borrow are the same thing, so touching the data without the lock, or forgetting to release it, is a compile error rather than a bug.
- **Intent is in the signature.** A reader sees `mut` and knows the function changes its argument. A reviewer sees `ref` and knows it does not.

## What taking by value buys

- **Transfer of responsibility.** `Close(f File)` consumes the file, so a closed file cannot be used again. `step(s State, ev Event) State` moves the old state in and builds the new one from its parts without cloning.
- **The callee may keep it.** Storing a value in a struct, sending it on a channel, or capturing it in a task all require owning it. By-value is how ownership gets there.

## What it costs

- **A choice at every signature.** Go developers write `T` or `*T` by habit; Teak asks which of three things the function does. The default is easy, `ref` for reading, `mut` for changing, by value for keeping, but it is a decision Go does not ask for.
- **Some Go patterns need restructuring.** Holding a `mut` borrow of one slice element while mutating another is rejected, so code copies the needed values out first (`../../examples/15-lru.teak`). Two mutable borrows of one struct's different fields are allowed; two of the same slice are not, because the compiler cannot prove the indices differ.
- **Borrow-holding structs and closures.** A struct with a `ref` or `mut` field, and a closure that captures by borrow, are themselves borrows and obey every rule above. A `Lexer` holding `src ref string` is legal for the duration of a parse (example 05); storing it in owned data or sending it is not. Returning one follows rule 3 like any borrow: tied to the receiver or to the only borrowed parameter. That is how `text.Fields()` returns an iterator that borrows its receiver, and why `NewLexer(src ref string) Lexer` is legal while `NewLexer(src ref string, name ref string) Lexer` is not. (Clarified 2026-10-18; an earlier draft said a borrow-holding struct could never be returned.)

## Choosing, in one line

Reading it: `ref`. Changing it: `mut`. Keeping it, sending it, or consuming it: by value.

## Precedent

Rust's `&T` and `&mut T` with the same shared-or-exclusive rule, minus lifetimes. Hylo's `let`, `inout`, and `sink` parameter conventions, which are the same three modes under different names. The second-class values paper by Osvald et al. for the escape restriction that makes lifetimes unnecessary. Swift's `borrowing` and `consuming` modifiers, added for the same reason.
