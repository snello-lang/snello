# 25. Attributes

An attribute is a typed constant written on a field, a variant or a type, for a reflecting function (chapter 24) to read while the program is compiled. A program declares its own, and a library declares its own, side by side on one field:

```go
type Usage string impl Attribute                     // a line of help
struct Range impl Attribute { min int; max int }     // the bounds of a number
struct Secret impl Attribute {}                      // a value never printed

struct Options {
    Port    int = 8080   @Usage("the port to listen on") @Range{min: 1, max: 65535} @json.Name("port")
    Verbose bool         @Usage("print each request")
    Token   string       @Secret @json.Skip
}
```

The program's `usage<T>` reads `Usage` and `Secret` to print its help, its `check<T>` reads `Range` to refuse a value out of bounds, and `encoding/json` reads `json.Name` and `json.Skip`; none sees the others' attributes. Each is written once, as a generic function over any struct, and the compiler writes the help text and the bounds into the code it compiles for `Options` as constants. Nothing of an attribute is kept when the program runs:

```
  -Port  the port to listen on
  -Verbose  print each request
  -Token  (secret)

Some("Port")
Ok("{\"port\":70000,\"Verbose\":true}")
```

`compiler/emit/internal/backendtest/testdata/attrs-program.snello` is that program, and `attrs.snello` the one the rest of this chapter is cut from, both built through both backends in release and debug builds.

## Why it is there

A function that works on any type, field by field, needs to be told more than the type says: the name a field takes in a file, that one field is secret, that a number has bounds. Without attributes, that is said in a second place, a table the function reads, which drifts from the type, or in code written by hand for each type, which reflection was meant to replace.

- **Go writes it in a struct tag**, `json:"port,omitempty"`, a string the compiler does not check: a misspelt option, `omitemtpy`, is passed over in silence, and every library parses the string again when the program runs.
- **Rust writes it in an attribute**, `#[serde(rename = "port")]`, which the derive macro checks, outside the type checker, with names that are only strings to the language.
- **Java and C# write it in an annotation**, typed as here, and read by reflection when the program runs.

Snello's attributes are values of types the program or a library declares, so a misspelt one is an unknown name, a wrong argument a type error, and the attributes a library takes are documented where it declares them. They are read when the program is compiled, as the rest of reflection is.

## Declaring an attribute

A type is an attribute when it lists the predeclared marker trait `Attribute`, as a type opens itself to reflection by listing `Reflect`: `Usage`, `Range` and `Secret` above, or `encoding/json`'s `Name`, `OmitEmpty` and `Skip`. An attribute is any constant of such a type (chapter 21): a conversion of a string, `Usage("the port")`, a struct literal whose fields are constants, `Range{min: 1, max: 65535}`, or a constant's name, `@Percent` for `const Percent = Range{min: 0, max: 100}`. A struct with no fields is written by its name alone, `@Secret`.

## Writing one

An attribute is written after what it is about:

- **a field**, after its type and its default: `Port int = 8080 @Usage("the port")`; fields declared together, `w, h int @Range{min: 0, max: 9}`, take it each;
- **a variant**, after its name and fields, and a variant's field after its type: `Circle(r f64 @json.Name("radius")) @json.Name("circle")`;
- **a struct or an enum**, after its name, its type parameters and what it lists, before its body: `struct Options impl Reflect @Usage("the server's options") { ... }`.

Several are written one after another, and two of one type on one declaration are refused. A composite literal before a body is written in parentheses, `struct S @(Range{min: 1, max: 9}) { ... }`, as Go writes one in an `if` condition, since the `{` there opens the body. One field carries the attributes of the program and of each library it uses, since each is its own type: `Port` above carries three.

## Where one is written

A library knows where it reads each of its attributes, and says so where it declares the type, by listing `FieldAttribute`, `VariantAttribute` or `TypeAttribute` in place of `Attribute`: predeclared traits that refine it, one for each place. `encoding/json` declares `struct Skip impl FieldAttribute {}` and `type Name string impl FieldAttribute, VariantAttribute`, so `@json.Skip` written on a struct, where json would never look for it, is an error, not an attribute passed over in silence:

```go
struct Server @json.Skip { Host string }   // ERROR "json.Skip is not written on a type: it lists FieldAttribute"
```

A variant's field is a field. A type that lists only `Attribute`, as `Usage` above does, is written on any of the three. Java's `@Target` and C#'s `AttributeUsage` say the same with an annotation on the annotation; here it is a trait the type lists, as `Reflect` and `Attribute` are.

## Reading one

A reflecting function reads an attribute with the predeclared `attr(x, A)`, which gives the attribute of type `A` written on `x`, an `A?`, or `None` when none is written. `x` is a part a loop over `fields(T)` or `variants(T)` gives, or a type parameter an arm matched as a struct or an enum:

```go
func usage<T>() string {
    var b strings.Builder
    match T {
        struct => {
            for f := range fields(T) {
                match attr(f, Usage) {
                    Some(u) => b.WriteString("  -" + f.name + "  " + string(u) + "\n"),
                    None => b.WriteString("  -" + f.name + if attr(f, Secret).IsSome() { "  (secret)\n" } else { "\n" }),
                }
            }
        }
        _ => {},
    }

    return b.String()
}
```

Which attribute `attr` gives is decided while the program is compiled: in each copy of the loop, `attr(f, Usage)` is written as a constant, `Some(Usage("the port to listen on"))` for `Port` and `None` for `Token`, and nothing about attributes is looked up when the program runs. The code around it runs as any code does: `usage<Options>()` writes its help with `WriteString` when it is called, and its `match` is on that constant, which an optimizing build of the C compiler or LLVM may fold, as it may any match on a constant. A field's value is read as the type a match on its type took, so `check` compares a field with its bounds only where the field is an `int`:

```go
match attr(f, Range) {
    Some(r) => match f.type {
        int => {
            if v.[f] < r.min || v.[f] > r.max {
                return Some(f.name)
            }
        }
        _ => {},
    },
    None => {},
}
```

An instance of a generic type reads its declaration's attributes: `Pair<int>` and `Pair<string>` have the attributes written on `Pair`.

## What is refused

| Written | Refused with |
|---|---|
| `Port int @5` | `a value of type int is not an attribute: an attribute's type lists Attribute` |
| `Port int @Plain("x")`, `Plain` not listing `Attribute` | `a value of type Plain is not an attribute` |
| `Port int @Range`, a struct with fields | `Range is a type: write a value of it, the attribute, as a constant` |
| `Port int @Usage(text)`, `text` a variable | `text is not constant` |
| `Port int @Usage("a") @Usage("b")` | `Usage is given twice` |
| `Base @Usage("x")` on an embedded field | `an embedded field takes no attribute` |
| `attr(f, Plain)` | `Plain is not an attribute's type` |
| `struct Server @json.Skip { ... }`, an attribute of fields on a type | `json.Skip is not written on a type: it lists FieldAttribute` |
| `struct G<T> impl Attribute { v T }`, `enum E impl Attribute { ... }` | `G cannot be an attribute: an attribute is a constant, a number, a string, a bool or a struct of them, and not generic` |
| `attr(T, Usage)` outside an arm `struct` or `enum` | `attr reads the attributes of a part a loop over fields or variants gives, attr(f, ...), or of a type parameter a struct or enum arm matched, attr(T, ...)` |

An attribute is not written on a function, a method, a parameter or a package. What reflection reads is the data a type is made of, and an attribute on a function would ask for code that runs on functions, which nothing in Snello does.

## Memory safe, no collector

An attribute is a constant of the types a constant may have: numbers, strings, bools and structs of them. The compiler holds it in a table by the declaration it is written on, so two packages' declarations never share one, and writes it into the code as any constant is written, so nothing of it is allocated, kept or freed when the program runs, and there is nothing a borrow could reach.

## For maintainers

- **The design.** Section 8 of `_docs/design/11-reflection-at-compile-time.md`, and the bullet "Attributes are typed constants read at compile time" in `00-syntax-conventions.md`.
- **The syntax.** `@` is `token.AT`. `parseAttrs` in `compiler/parser/parser.go` reads `{"@" PrimaryExpr}` after a field (`parseField`), a variant (`parseVariants`) and before a struct's or enum's body (`parseStructOrEnumDecl`), where a `{` opens the body, as in a control clause. The file keeps them beside its tree, `ast.File.Attrs`, by the node each is written on, as `go/ast` keeps comments, so a field written without any is no larger.
- **The check.** A type listing `Attribute`, or a trait refining it, must be one a constant may have, and not generic (`checkImpls` in `trait.go`). `FieldAttribute`, `VariantAttribute` and `TypeAttribute` refine `Attribute` (`universe.go`); `attrsOn` refuses an attribute on a place its type does not list, with `fits` and the table `attrPlaces` at the top of `attr.go`. `checkAttrs` in `compiler/types/attr.go` runs once the impl lists are resolved, declaration by declaration, so each mistake is reported once and in order. `attribute` takes a struct with no fields by its name (`attrNamedType`), a constant by its name (`attrConst`), or any constant expression (`constValue`), and requires the type to list `Attribute`. A conversion that keeps a string a string or a bool a bool is a constant expression for this (`isConstantType` and `foldConversion` in `constant.go`). Each declaration's attributes go in `Info.Attrs` by the declaration itself, a type's name, a field or a variant, so a `Var` grows by nothing and two packages' declarations never share; `Info.TypeAttr`, `FieldAttr`, `VariantAttr` and `VariantFieldAttr` look one up, an instance's through its generic type's declaration.
- **Reading.** `attrCall` checks `attr`; `lower.AttrOf` in `compiler/emit/lower/reflect.go` finds the attribute a copy and an instance read, from the copy's field or variant or the type the parameter stands for. The C backend writes it in `builtinCall` in `expr.go` and the LLVM backend in `builtinCall` in `builtins.go`: `Some` of the folded constant, `Some` of the type's zero for a struct with no fields, or `None`.
- **The tests.** The scanner's attribute line; the parser's `TestAttrs`; `compiler/types/attr_test.go`, with `TestConversionKinds` in `constant_test.go`; `TestAttrOf` in `compiler/emit/lower/reflect_test.go`; and `attrs.snello` through both backends.
