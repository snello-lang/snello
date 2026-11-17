# 24. Reflection at compile time

A generic function can look at the type it was given: which kind of type it is, a struct's fields by name, an enum's variants. One function then serves every type:

```go
func Show<T>(v ref T) string {
    return match T {
        int => strconv.Itoa(v),
        string => v.Clone(),
        []E => "a list of " + strconv.Itoa(len(v)),
        struct => {
            s := "{"
            for f := range fields(T) {          // written out once per field
                s += f.name + ":" + Show(ref v.[f]) + " "
            }
            s + "}"
        }
        _ => "?",
    }
}
```

`Show(ref Point{x: 1, y: 2})` gives `{x:1 y:2 }`, and no line of `Show` names `Point`. All of it happens while the program is compiled: for `Point`, the compiler keeps the `struct` arm and writes the loop out twice, once for `x` and once for `y`, as if `Show<Point>` had been written by hand for `Point`. Nothing describes a type when the program runs.

`compiler/emit/internal/backendtest/testdata/reflect.snello` is the program this chapter is cut from, built through both backends in release and debug builds, and `std/encoding/json` is the library written this way.

## Why it is there

A program has code that does the same thing to every type, field by field: write it as JSON, read it back, print it for debugging, compare two values in a failed test and say which field differs, fill a struct of settings from the command line or a configuration file. Without reflection, that code is written again for every type, by hand or by a generator, and drifts from the type when a field is added. Reflection writes it once.

Go does it when the program runs: `reflect` reads a description of the type, kept in the binary, and `encoding/json` walks it for every value it encodes. That costs on every call, and its mistakes come when the program runs:

- **A type the code cannot handle is found at run time.** Go's `json.Marshal` of a struct with a channel field returns an error when it runs. In Snello, `json.Marshal` of such a type is refused when the program is compiled: `json.Marshal<Server>, json.writeValue<Server>, json.writeValue<chan int>: no arm of the match on T takes chan int`.
- **A field a package keeps to itself is read anyway, or skipped in silence.** Go's `encoding/json` skips unexported fields, so a struct of lowercase fields encodes as `{}`, and `reflect` with `unsafe` reads them all. Snello refuses at compile time, naming the field, unless the type lists `Reflect`: nothing is skipped in silence, and nothing a package hides is read without its say.
- **A value is forged from its fields.** Code that builds any type from parts can build one its package never meant to exist: a reference count of its choosing, a file over any descriptor. Snello builds a value from its fields only where a literal of it could be written, so an `rc.Rc`, an `os.File` or a lock cannot be made this way.
- **No cost on every call.** What runs is the code written per type: each field's name a constant, each call direct and open to inlining, with no description walked and nothing looked up.

## Which type: `match T`

A `match` on a type parameter chooses an arm by the type, the first that fits:

| Arm | Takes | In the arm |
|---|---|---|
| `int`, `string`, `f64` | that type alone | `T` is read as the arm's type |
| `i8 \| i16 \| i32` | any of them | the arm is checked once for each |
| `Point`, `Box<int>` | that declared type | `T` is read as it, its methods and fields too |
| `E?`, `[]E`, `*E`, `Box<E>` | a type of that form | `E` names what is inside |
| `map[K]V` | a map | `K` and `V`, `K` with `Eq` and `Hash` |
| `struct`, `enum` | any struct, any enum | `fields(T)` or `variants(T)` |
| `_` | any other type | |

Inside an arm, `T` is what the arm matched: in the `int` arm, `v` of type `ref T` is a `ref int`, and in a `[]E` arm a `ref []E` whose elements are `E`. A name a shape binds is a type parameter of the arm, and a match on it, `match E { int => ..., _ => ... }`, is a match on a type too. An arm takes no guard, since a type is matched by what it is.

An arm may name a trait, `json.Marshaler => v.MarshalJSON()`, and takes any type that implements it; in the arm `T` is bounded by the trait, so its methods are called on a value and its functions on the type, `T.UnmarshalJSON(text)` (chapter 26). Put first, it lets a type with its own encoding be written its own way and every other by its fields. A match inside that arm is on `T` so bounded, and its `struct` or `enum` arm reaches the type's parts as any does, with the trait's methods and functions still at hand: `Tag => match T { struct => { for f := range fields(T) { ... } }, _ => T.Tag() }`.

A name in an arm that names no type, trait or shape's part is refused, `Shw names no type or trait: an arm for any type is _`, since a misspelled `Show` would otherwise bind every type and take every value the arms after it are written for.

An arm naming a type takes that type and no other. `type Celsius f64` is a type of its own, as it is when assigned, so it takes an arm that names `Celsius`, its kind's arm, `struct` for a type declared over a struct, or `_`, never the `f64` arm: written as an `f64`, a temperature would lose what its name says. A `Celsius` that should be written as a number says so in its own arm, `Celsius => writeValue(ref f64(*v), mut out)`.

The arms need not cover every type. A type that no arm takes is refused where the function is used with it, with the chain of calls that led there: `json.Marshal<Server>, json.writeValue<Server>, json.writeValue<chan int>: no arm of the match on T takes chan int`, the field `Events` of `Server` being a channel.

## A struct's fields

In a `struct` arm, `fields(T)` gives the struct's fields to a loop, which the compiler writes out once for each:

- `f.name` is the field's name, a constant string;
- `f.type` is its type, written where a type is, `readValue<f.type>(mut r)`, or matched, `match f.type { E? => ..., _ => ... }`;
- `f.default` is its default (chapter 23), an `f.type?`, `None` for a field without one;
- `v.[f]` is the field `f` of `v`, as `v.x` is the field `x`.

`v.[f]` is lent, read through a `ref` and changed through a `mut`, never moved out of `v`: the function is compiled for types it does not know, and one of them may implement `Drop`, whose `Drop` reads every field (chapter 5). `consume(v.[f])` is refused, `a field a loop over a type's parts reaches is lent`.

Since each copy of the loop is a block of its own, no `break` or `continue` leaves the loop, and it holds no `defer` or `select`; test the field with an `if`.

## Building a value from its fields

A literal with one loop in it builds a value of the struct's type, each field given what its line computes:

```go
func readValue<T>(r mut Reader) Result<T, Error> {
    return match T {
        int => r.readInt(math.MinInt, math.MaxInt),
        struct => Ok(T{ for f := range fields(T) { [f]: readValue<f.type>(mut r)? } }),
        ...
    }
}
```

The fields are made in order, and a `?` that leaves drops the ones already made, as in any literal. The value is built by a literal naming every field, so no zero value is made of a type without one, and no value is ever half made. A field the input leaves out can take its default, or `None` for an option:

```go
[f]: match r.findNamedValue(ref keys, f.name) {
    Some(at) => {
        r.at = at
        readValue<f.type>(mut r)?
    }
    None => match f.type {
        E? => None,
        _ => f.default ?? return Err(missingFieldError(start, f.name)),
    },
}
```

as `encoding/json` does: the object's keys are noted with where each value lies, and each field is found by its key.

## An enum's variants

In an `enum` arm, a match on the value takes one arm written out for each variant, so it covers them all:

```go
enum => match v {
    for c := range variants(T) => {     // v holds the variant c in this copy
        out.WriteString(c.name)
        for f := range fields(c) {
            writeValue(ref v.[f], mut out)    // the field of the variant v holds
        }
    }
}
```

The arm holds `v`: its fields are read as the variant `c`'s, so while it runs `v` is not assigned, moved or lent `mut`, `cannot assign to v: the arm written for each variant holds it`, as an arm binding a variant's fields holds the value it matched.

`c.name` is the variant's name and `fields(c)` its fields, as a struct's; a variant's field without a name has `""` for `f.name`. A variant is built by its constructor with one loop for its arguments, `c(for f := range fields(c) { readValue<f.type>(mut r)? })`, and the variant to build from an input is found by its name, in a loop over the variants written out once each:

```go
for c := range variants(T) {
    if name == c.name {
        return Ok(c(for f := range fields(c) { readValue<f.type>(mut r)? }))
    }
}
```

## Refusing a type: `refuse`

A reflecting function that cannot handle a type says so where the program is compiled, with `refuse`. It takes a message the compiler knows, string constants and parts' names joined with `+`, and is reported, with the chain of calls that led there, for each type whose instance reaches it. Here a program declares an attribute of its own, `Mark`, and a function that takes an enum with `Mark` only when every field of it is named:

```go
struct Mark impl TypeAttribute {}

enum Shape @Mark {
    Dot
    Circle(r f64)
    Pair(int, int)
}

struct Point {
    X int
    Y int
}

func Need<T>() int {
    return match T {
        enum => match attr(T, Mark) {
            Some(_) => {
                for c := range variants(T) {
                    for f := range fields(c) {
                        match f.name {
                            "" => refuse("Need wants named fields: " + c.name + " has an unnamed one"),
                            _ => {},
                        }
                    }
                }

                1
            }
            None => 0,
        },
        _ => refuse("Need takes an enum"),
    }
}
```

`Need<Shape>()` is refused with `Need<Shape>: Need wants named fields: Pair has an unnamed one`, and `Need<Point>()` with `Need<Point>: Need takes an enum`. A function of another package is named with its package before it, as json's are in `json.Marshal<Shape>, json.writeValue<Shape>`. The compiler decides, for each type and each copy of a loop, the matches whose answer it knows: a match on a type, as before; a match on `attr(x, A)`, whose arm `Some` is taken when `A` is written on `x` and `None` when it is not; and a match on a part's name, `c.name` or `f.name`, against string literals. So a `refuse` in an arm one type takes and another does not refuses the one alone, and an enum without `Mark` above passes. A match with a guard, or one on anything else, is left to the program to decide when it runs, and every arm of it counts as reached.

Outside a generic function, `refuse` is reached by every program that holds it and is reported where it is written, `refuse("not on this platform")`. It is no value, as `panic` is, so it stands where any value is wanted, and it never runs: a program the compiler lets pass holds it only on paths no instance takes.

## What reflection reaches

A reflecting function reaches what code written by hand where it stands could:

- **every field of a type of its own package, and the exported fields of another's.** A struct of another package with a field the function cannot name is refused where the function is used with it: `json.Marshal<Config>, json.writeValue<Config>: field port of Config is not exported; export it, or list Reflect on Config`.
- **every field and variant of a type that lists `Reflect`.** `struct Config impl Reflect { port int }` is its package saying that any reflecting function may read and build it. A type that does not list it keeps what it keeps. A package opens only what it declares: `type Mine strings.Builder impl Reflect` opens none of `strings.Builder`'s fields, `count<Mine>: field buf of Mine is not exported by strings, which declares it; a type declared over its struct opens none of its fields`.
- **a variant's fields where the variant is,** by their place, as a pattern does.
- **no field that holds a borrow,** which is refused with the field named, as any type argument that holds a borrow is.

Types that grow without end are refused too: a function that, through its arms, calls itself with a larger type each time, `f<int>`, `f<[]int>`, `f<[][]int>`. A recursive type is not one: `write<Value>` asks for `write<[]Value>`, which asks for `write<Value>` again, already written. A chain of more than 256 calls of generic functions, each with types not asked about before, is refused at the call it began from, naming its first and last calls, since the checker follows no deeper and every instance compiled must have been asked about.

## `encoding/json`

```go
struct Server {
	Host string = "localhost"
	Port int = 8080
	Tags []string
	At   Point?
}

text := json.Marshal(ref server)?             // {"Host":"h","Port":1,"Tags":["a"],"At":null}
back := json.Unmarshal<Server>(ref text)?
```

`Marshal` writes one line with no space between the parts, for a program to read. `json.MarshalIndent(ref server, "", "  ")` writes the same JSON laid out for a person: each element and member on a line of its own, begun with the prefix, `""` here, and the indent once for each level, a space after each colon, an empty array or object kept as `[]` or `{}`. `json.Indent(ref text, "", "\t")` lays out JSON text already written the same way, and refuses text that is not one value, with its offset.

```go
pretty := json.MarshalIndent(ref server, "", "  ")?
// {
//   "Host": "h",
//   "Port": 1,
//   "Tags": [
//     "a"
//   ],
//   "At": null
// }
```

A struct is an object of its fields, under the names they are declared with; an option `null` or its value; a slice an array; a map with string or integer keys an object with its keys sorted, an integer key as its decimal in quotes, `{"2":true,"10":false}`, in the order of the values; an enum's variant its name, `"Dot"`, or an object of one key, `{"Circle":{"r":1.5}}`, `{"Bool":true}` for one unnamed field and `{"Pair":[1,2]}` for several. Reading finds each field by its key, wherever the text puts it, passes over a key the type has no field for, takes a missing field's default or `None`, and refuses any other missing field, a number its field cannot hold, a key given twice, and an integer key that is not written as JSON writes an integer, `"01"` or `"+1"`, or that its type cannot hold, each with its offset: `json: missing field Tags at offset 0`. `Marshal` refuses only a float JSON has no text for, a NaN or an infinity. An owned pointer is the value it points at, so `struct Node { V int; Up *Node? }` round-trips.

Reading holds the text to JSON's grammar: `01`, `+1`, `.5`, a member without its comma, a raw control character in a string and text after the value are errors with their offset. Arrays and objects nest at most 128 deep, in a value read or one passed over, since a task's stack does not grow: ten thousand `[` are an error, `exceeded max depth`, not a crash. A float is written by its shortest text, in plain decimals from `1e-6` up to `1e21` and with an exponent outside, an `f32` by its own, `0.1`; a string with its quotes, backslashes and control characters escaped and invalid UTF-8 as `\ufffd`, and nothing escaped for HTML.

A field is written under its declared name unless an attribute says otherwise: `@json.Name("host")` renames a field, a variant or a variant's field, `@json.OmitEmpty` leaves a field out when it is `None`, `""`, `[]` or `{}` and reads it back as empty, and `@json.Skip` keeps a field out of the text both ways (chapter 25). A type that lists `json.Marshaler` writes its own JSON, `func (c ref Celsius) MarshalJSON() Result<string, json.Error>`, which `Marshal` checks is one JSON value, and one that lists `json.Unmarshaler` reads its own, `func Celsius.UnmarshalJSON(text ref string) Result<Celsius, json.Error>`, given the raw text of its value; an offset in its error, made with `json.Error.At`, counts from where that value began.

An enum is written by default as its variant's name, alone or as an object's one key. Three attributes on the enum choose another layout, as Rust's `serde` names them. `@json.Tag("type")` writes the name under the key `type`, beside the variant's fields, `{"type":"Circle","r":1.5}`, `{"type":"Dot"}`, as most web APIs write a choice of shapes. `@json.Tag("t") @json.Content("c")` puts the variant's value under a key of its own, `{"t":"Pair","c":[1,2]}`, `{"t":"Dot"}`. `@json.Untagged` writes the value alone, `{"r":1.5}`, `[1,2]`, `null` for a variant without fields, and reads the first variant, in the order declared, whose value the text is. Reading finds the tag wherever the object puts it. What json cannot write is refused where the program is compiled: `json.Content` without `json.Tag`, `json.Untagged` with either, a layout written on a struct, and `json.Tag` alone on an enum with a variant whose field has no name, which has nothing to put beside the name, `json.Marshal<Shape>, json.writeValue<Shape>, json.checkLayout<Shape>: json.Tag writes a variant's fields beside its name, and Pair has an unnamed one: name it, or add json.Content`.

JSON without a type has two readers. `json.Parse(ref text)` checks the text once and indexes it into a `json.Doc`, which reads in place: `doc.Get(n, "servers")`, `doc.At(n, "/servers/0/port")`, `doc.Int(n)`, `doc.StringInPlace(n)` with nothing copied, and `doc.Decode<Server>(n)` for one part as a type. `json.Unmarshal<json.Value>(text)` reads an owned tree instead, which outlives the text: a number keeps its text, `json.Number("9007199254740993")`, an object its members in order, each key once, and `Marshal` writes it back compacted, its numbers and its members' order kept. A field of type `json.Text` passes its part of the input through unread.

A stream of values, a file or a connection, is read and written a value at a time. `json.NewEncoder(mut w)` writes to any `io.Writer`: `enc.Encode(ref v)` writes the value and a line break after it, in one write, so the stream has one value to a line, and `enc.SetIndent("", "  ")` lays each out. `json.NewDecoder(mut r)` reads from any `io.Reader`: `dec.Decode<T>()` returns the next value, or `None` at the end of the input, whatever the space between the values and however the reads cut their text.

```go
enc := json.NewEncoder(mut conn)
enc.Encode(ref event)?

dec := json.NewDecoder(mut conn)
for {
	ev := dec.Decode<Event>()? ?? break
	handle(ev)
}
```

One large array or object is read a piece at a time, so it is never held whole: `BeginArray` reads its `[`, `Decode` an element while `More` says one comes, and `EndArray` its `]`; in an object `Key` reads each member's key before `Decode` reads its value. `Peek` tells the kind of what comes next, so a program can walk JSON it knows nothing of.

```go
dec.BeginArray()?
for dec.More()? {
	rec := dec.Decode<Record>()? ?? break
	total += rec.Amount
}
dec.EndArray()?
```

A value's text may take at most 64 MiB, so a stream that never ends a string, or sends one endless array, cannot make the Decoder hold more than that and one block of 4 KiB: past the limit the value is refused, `value longer than 67108864 bytes, the Decoder's limit`, with its offset, and every later call returns the same error. `dec.SetLimit(n)` sets another bound, and zero or less removes it. A key read with `Key` is held to the limit, and so is each element of an array read a piece at a time, so a long array of small records streams under a small limit. Arrays and objects opened a piece at a time nest at most 128 deep, as `Unmarshal` reads them, so a stream of opening brackets is refused too.

```go
dec := json.NewDecoder(mut conn)
dec.SetLimit(1 << 20) // one MiB a message
```

A value is read by `Unmarshal`'s rules, and an error's offset counts from the start of the input. A value its type refuses is passed, so the next `Decode` reads the one after it; text cut off by the end of the input is `unexpected end of input`, again at each call. When the reader fails, every later call returns its failure, which `dec.IoError()` gives, so the program can see an `io.TimedOut` from an `io.Reset`; an Encoder whose writer failed does the same, since the writer may hold part of a value. `dec.Offset()` is where the next byte lies in the input, and `dec.Buffered()` lends the bytes read past the JSON, for an input that goes on in another form.

## Memory safe, no collector

Reflection adds no kind of value and nothing to the program when it runs. A reflecting function is a generic function: checked once against the rules every function is, then compiled per type with its loops written out, so what runs is ordinary code over ordinary values, each owned once and dropped once. A field is lent and never moved, so no `Drop` reads a field taken from it. A value is built by a literal naming every field, so none is half made, and a `?` drops the fields made before it. A type is built from its fields only where a literal of it could be written, or where its package listed `Reflect`, so nothing is forged. Field and variant names are constant strings in the code written per type, and nothing describes a type at run time. An Encoder and a Decoder hold a `mut` borrow of their writer or reader, so nothing else uses it while they live; a Decoder reads each value where it lies in its block, a borrow of the block that ends before the next read moves it.

## For maintainers

- **The design.** `_docs/design/11-reflection-at-compile-time.md`, and the bullet "Reflection is at compile time" in `00-syntax-conventions.md`.
- **The parser.** `parseMatchExpr` in `compiler/parser/expr.go` reads a match whose value names a type parameter in scope, or `f.type`, as one on a type, `MatchExpr.OnType`, with `parseTypeArm`'s arms of types, shapes and kinds, `ast.TypePat`; `Parser.fn` is the function being parsed, whose type parameters, its own, its receiver's and those of the type a function of a type belongs to, `isTypeParam` in `decl.go` reads when a match is met, and `Parser.shapes` the names a shape binds in its arm (`shapeNames`), made only inside a type arm. `x.[f]` is `ast.PartExpr`; a literal's or a call's loop, `ast.PartsElt`, is `parsePartsElt`; the arm written per variant, `ast.PartsPat`, is `parsePartsPat`. `f.type` and `f.default` are read by `parseNameOrType` in `types.go`.
- **The match on a type.** `typeMatch` in `compiler/types/reflect.go`: `splitAlternatives` makes an arm listing several types one per type, copying the body with `ast.Clone`; `matchArm` records `Info.Arms` and makes the arm known, `Checker.refined` for a type or shape, read by `refine` wherever a name's type, a written type and a function's results are read, `Checker.kinds` for a kind; `shapeNames` declares a shape's names, a map's key bound by `Hash`. `ChooseArm` decides which arm a type takes, for the checker's walk and the backends alike: a type's own arm by identity, a shape's by unifying it, a kind's by the type's underlying type, then `_`. An arm whose type holds the matched parameter, `E?` in a match on `E`, is refused, since no type is one, and so is an arm whose whole type is a name nothing declares, which `shapeNames` would bind to any type. A trait arm makes `T` known as a fresh parameter bounded by the trait, and a kind's arm nested in it records its kind on that one: `armParam` follows `refined` to it where `fields`, `variants` and `attr` ask what kind `T` is.
- **Parts.** `partsLoop` checks a loop over `fields` or `variants`, two predeclared built-ins, declaring the loop's `Part`, an object in `object.go` whose type is the field's type parameter; `partSelector` checks `f.name`, `f.default` and `c.name`; `partExpr` checks `v.[f]`, lent; `partsLit` a struct built from its fields; `variantMatch` the arm written per variant; `variantBuild` a variant built from its fields. After a body is checked, `selectParts` rewrites each `v.[f]` into a selector, with `ast.Rewrite`, listed in `Info.Parts`; `ast.Rewrite` replaces an expression in any slot that can hold the result, a match arm's value among them, whose slot is a `Node`. The loop is a region of the move pass marked `parts`, which `branch` and `inParts` read to refuse `break`, `continue`, `defer` and `select`.
- **The questions a type answers.** `reach` in `compiler/types/reach.go`, after the package's bodies, walks, when anything in the build matches on a type, from every call with its types known through each generic function's declaration, which its `Func` keeps in bytes its fields leave free, choosing each arm with `ChooseArm` and going through each loop's parts: `reachable` refuses a field the function cannot reach and one holding a borrow, `grows` types that grow twice along the chain, a chain past `maxChain` is reported once at its root, and every message carries the chain, `where` and `step`. The walk's roots are the bodies of the package's functions that are not generic, in the order written, so a mistake is reported at the first call that leads to it; a call binds a method's receiver type arguments, or `Self` for a trait's default, before the callee's own. `reachWalk` holds no `Checker`, so that a check that never walks keeps its `Checker` off the heap.
- **`refuse`.** `refuseCall` in `compiler/types/refuse.go` checks the message, text the compiler knows (`unknownMessage`), and reports one outside a generic function where it is written, `inGeneric` finding no type parameter in scope; inside one it sets `Checker.refuses`, so that the walk runs. The walk reports it where an instance reaches it (`reachWalk.refused`, its text from `message` with the copy's names), and `reachWalk.decided` decides a match on `attr(x, A)` (`attrOf`, as `lower.AttrOf` finds it for the backends) or on a part's name (`partName`), with each field a loop gives bound as each variant is (`reachPart`). Both backends write it as `panic`, on a path no instance takes.
- **The backends.** `compiler/emit/lower/reflect.go` holds what both ask: `TypeArm` and `MatchedParam` for a match on a type; `PartsOf`, `EltParts` and `ArmParts` the copies of a loop, each a `PartCopy` with its field or variant, its default and its substitution; `NameParts` names the field of each rewritten selector for a copy. The C backend writes them in `typeMatch` and `variantMatch` in `compiler/emit/c/match.go`, `partsLoop` and `touch` in `stmt.go`, `partsLit`, `partValues` and `variantBuild` in `expr.go`, and a variant's field in `field` in `trait.go`; the LLVM backend in the functions of the same names in `compiler/emit/llvm`, a variant's field in `fieldAddr`.
- **The library.** `std/encoding/json/json.snello`, and `Reflect` in `compiler/types/universe.go` with `fields` and `variants`, documented in `std/builtin/builtin.snello`.
- **The tests.** The parser's `TestReflection`; `compiler/types/reflect_test.go`, `reach_test.go` and `testdata/errors/reflect-lent.snello` and `reflect-loop.snello`; `compiler/emit/lower/reflect_test.go`; `compiler/ast/clone_test.go` for `Clone`, `Rewrite` and `Inspect`; `reflect.snello` through both backends; and `std/encoding/json/json_test.snello`, which round-trips the values of the JSON example, a recursive enum holding collections.
