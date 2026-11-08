# Negative programs

Programs the checker must reject. Each breaks exactly one rule and carries exactly one `// ERROR "text"` comment on the line the error is reported at; the checker's message for that line must contain the text, and no other line may produce an error. This is the form the Go compiler's errorcheck tests use and the form `compiler/parser/testdata/errors/` already uses for syntax errors.

Every file's header comment states the rule it breaks and the idiom that fixes it, so the file doubles as documentation of the message. The book chapters 5 and 6 (`../../../_docs/book/`) explain the same rules in prose.

## Rules and the programs that test them

Rules are numbered as in `../../../_docs/design/04-borrows-ref-and-mut.md`; the move rules come from `01-language-design.md` and the task-boundary rules from `03-ownership-across-tasks.md`.

| Rule | Program |
|---|---|
| A moved value is dead | use-after-move, move-in-branch, closure-moves, use-after-close |
| A move inside a loop happens every iteration | move-in-loop |
| Indexing lends; it does not move | move-out-of-slice |
| Nothing moves out through a borrow | move-out-of-borrow |
| A partial move ends the whole | partial-move |
| 4. Any number of `ref`, or exactly one `mut` | mut-while-ref, two-mut, two-slots |
| 4. The owner is frozen while borrowed | assign-while-borrowed, move-while-borrowed |
| 5. A borrow passes on narrower or equal | ref-as-mut (a type mismatch), assign-through-ref, mut-through-ref-receiver |
| 2. A borrow cannot be stored | borrow-in-slice, borrow-struct-in-slice, closure-stored |
| 2. A borrow cannot cross a task boundary | borrow-across-spawn, borrow-on-channel |
| A spawn moves what its closure mentions | use-after-spawn, spawn-twice |
| What crosses a task boundary is `Send` | spawn-not-send, channel-not-send |
| Lock-protected data is reached only through a guard, a borrow | guard-across-spawn, guard-of-temporary |
| The races a task can make: a value used after it was sent, an Arc's value changed, a borrowing closure spawned | use-after-send, mutate-through-arc, borrowing-closure-spawned |
| 3. A returned borrow has one source | return-two-borrows, return-local-borrow, closure-returned |
| 1. and 6. A borrow lives inside one function; the owner outlives it | return-local-borrow, closure-returned |
| No `nil` | nil-used |
| A package variable is read and lent as `ref` by every task, and never assigned, lent as `mut` or moved out of; its type is `Send` | global-assigned, global-mut, global-moved, global-not-send, global-declared-not-send, global-trait-not-send |
| `?` passes an error into the one variant of the function's error enum that holds its type, or into a trait value; a derived `Error()` needs printable fields; `%+v` prints an error's chain and nothing else | try-ambiguous, try-no-variant, error-unprintable, format-plus |
| `match` is exhaustive | missing-arm |
| `?` propagates to the same kind | wrong-question-kind |

The checker handles every program here: nil-used, missing-arm, wrong-question-kind, ref-as-mut, the eight move programs, the seven borrow programs of rules 4 and 5 and the eight escape programs of rules 2, 3 and 6; `handledErrors` in `compiler/types/check_test.go` lists them, and a program not on the list must produce no error. A message about a live borrow ends with both ends of the conflict: `by r at 16:5, used at 18:17`.

## Message conventions

The messages are the plan for the checker, chosen here so that they read the same way across rules:

- A dead value: `use of moved value X`, `use of partially moved value X`, `X moved in a previous iteration of the loop`.
- A move that is not allowed: `cannot move X out of ...`, `cannot move X: X is borrowed`.
- A borrow that is not allowed: `cannot borrow X as mut: <reason>`, where the reason is `X is borrowed as ref` or `X is already borrowed as mut`; `... through a ref receiver` for the receiver case. Passing a `ref` where a `mut` is wanted is a type mismatch: `cannot use X (ref T) as mut T`.
- A write that is not allowed: `cannot assign to X: X is borrowed`, `cannot assign to X through a ref borrow`.
- An escaping borrow: `cannot return ...`, `... cannot be returned`, `... cannot be stored`, `a spawned task cannot hold a borrow`, `a channel cannot carry a borrow`, `a slice cannot hold a borrow`.
- A value that is not `Send`: `cannot spawn with X: <why>`, `a channel cannot carry T: <why>`, where the reason names the part that stops the crossing: `rc.Rc<int> stays in its task`, `ref T is a borrow`, `Shape does not refine Send`, `T is not bounded by Send`.
- A signature that cannot be written (`return-two-borrows`) is reported on the `func` line; a statement that breaks a rule is reported on its own line. An error in a type does not cascade: the line after `[]ref string{}` is not reported again.
- Rule 4 is checked after the arguments of a call are evaluated, so `v.Push(v.Len())` is accepted (`04-borrows-ref-and-mut.md`, ordering rule); no negative program tests the call itself.

The programs assume the call-site rule from `05-standard-library.md` section 5: an owned value passed to a `ref` parameter is borrowed without a keyword, `mut` is written when the argument is an owned place, and a borrow variable is passed on as itself with no keyword, since its mode is part of its type (`bump(a, b)` in `two-mut`, `push(xs)` in `ref-as-mut`).

The checker reports both ends of a conflict, the use and the move, or the two borrows, as Go reports both uses of a redeclared name. The annotation names only the line the error is anchored at.
