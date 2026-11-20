# 36. Running programs and catching signals

The design is section 6 and decision 19 of `../design/05-standard-library.md`. The packages are `std/os/exec/` and `std/os/signal/`; the runtime's side is `runtime/c/process.c`, with Windows' child in `process_windows.c`, and the poller's part in `net.c`.

A program runs another with `os/exec`, and is asked to stop, by the terminal's Ctrl-C or a `kill`, through `os/signal`. Both are built on what every other wait of a Snello program is built on: a child's end and a signal come through the runtime's poller and its channels, so a task waits on a child as it waits on a socket, no thread blocks for it, and no signal handler runs any code of the program's.

## Running a program

`exec.Command` names a program and its arguments; `Output` runs it, collects what it printed on both streams, and waits for it to end:

```go
out := exec.Command("git", "rev-parse", "HEAD").Output()?
fmt.Print(string(out.Stdout))
if !out.Status.Success() {
    fmt.Println("git:", out.Status, string(out.Stderr))
}
```

The program is looked up on `PATH` when its name holds no path separator, as a shell looks it up, and taken as written otherwise. `Run` runs it without collecting anything, the child sharing this program's terminal, and gives how it ended. A child that ends with another code than 0 is not a failure: `Status` says what happened, and the program decides. The failure is a program that cannot be started, an `io.Error` of Kind `io.NotExist` when nothing runs by that name: `exec nosuch: executable file not found in $PATH`.

A `Cmd` is a struct of settings with defaults, set before `Start`:

```go
cmd := exec.Command("make", "test")
cmd.Dir = "/src/app".Clone()                  // where the child starts; this program's directory by default
cmd.Env = Some([]string{"CI=1".Clone()})      // the whole environment; None, the default, is this program's
cmd.Stdout = exec.Piped                       // where each stream goes: Inherit, Piped or Null
```

Each standard stream of the child goes where its field says: `Inherit`, the default, shares this program's, so the child writes to the same terminal or file; `Null` connects it to the null device; `Piped` connects it to this program, as a `Pipe` the `Child` holds.

## Talking to a child

`Start` runs the command and returns at once with a `Child`, the handle on the running program. A stream the `Cmd` piped is a `Pipe` in the `Child`'s `Stdin`, `Stdout` or `Stderr`, which the program takes out to use: an `io.Writer` for the child's input, an `io.Reader` for its output, so `bufio` and `io.Copy` work over them as over a file:

```go
cmd := exec.Command("sort")
cmd.Stdin = exec.Piped
cmd.Stdout = exec.Piped
child := cmd.Start()?
input := take(mut child.Stdin)?             // the Pipe, moved out of the Child
input.WriteString("pear\napple\nfig\n")?
_ = input                                    // dropped: the end of the child's input
output := take(mut child.Stdout)?
lines := bufio.NewScanner(mut output)
for lines.Scan() {
    fmt.Println(lines.Text())                // apple, fig, pear
}

status := child.Wait()?
```

A read with nothing to read parks the task until the child writes, and a write the child is slow to take parks it until there is room: the rest of the program runs meanwhile. Dropping the input `Pipe` closes it, which is how the child learns its input has ended. A `Pipe` moved into a task reads or writes there, which is how a child that writes much to both streams is read: one task for each, so that a full pipe on one never holds the other. `Output` does exactly that.

`Wait` parks the task until the child has ended and gives its `Status`; asked again, it gives the same. It closes the child's input first when the `Child` still holds that `Pipe`, so that a child reading its input cannot wait for a program that waits for it. `Wait` reads nothing: a stream the command piped is the program's to read, and a child blocked on a full pipe no one reads is waited for for ever.

A `Status` is how the child ended: `Success()`, `Code()`, the exit code or `None` for a child a signal ended, `Signal()`, the signal's number or `None` for one that exited, and `String()`, `exit status 3` or `signal: killed`. A write to a child that has closed its input, or ended, fails with an `io.Error` of Kind `io.BrokenPipe`, whose `Written()` says how much the child took.

## Stopping a child

`Terminate` asks the child to end, with `SIGTERM`, which a program may catch and answer; `Kill` ends it at once, with `SIGKILL`, which none can. Both are nothing once the child has ended, and `Wait` then collects its end:

```go
child := exec.Command("server").Start()?
...
child.Terminate()?
fmt.Println(child.Wait()?)                   // signal: terminated
```

On Windows, which has no signal to send, `Terminate` is `Kill`: the child ends with the exit code 1.

**A dropped child is killed.** A `Child` owns the program it started. Dropping one that has not been waited for kills the child and collects its end, so no child is ever forgotten and no zombie is ever left; a child meant to run on after its handle goes is let go on purpose:

```go
child := exec.Command("daemon").Start()?
child.Detach()                               // the child runs on; the runtime collects its end when it comes
```

This is also what a deadline on a child means. `task.Within` ends a `Wait` as it ends any wait, with an `io.Error` of Kind `io.TimedOut`, the child still running; what to do with it is then written in the open:

```go
if task.Within(5 * time.Second, func() { child.Wait() }).IsNone() {
    child.Kill()?                            // or Terminate, or Detach
    child.Wait()?
}
```

Dropping the `Child` instead kills it the same. Go's `CommandContext` kills a child when its context ends; here the kill is the drop, and nothing more is written. A `Child` dropped inside the cancelled scope, where no wait can be made, is killed and left to the runtime to collect, as a detached child is.

A child's end belongs to the `Child`. A bound C library that waits for any child of the process, `wait` or `waitpid(-1, ...)`, can take it first: the `Wait` then fails with an `io.Error` of Kind `io.Other`, since how the child ended is lost, and the child is no zombie either way.

## Catching a signal

`os/signal` delivers the signals that ask a program to stop as values on a channel: `Interrupt`, the terminal's Ctrl-C, and `Terminate`, what `kill`, a service manager and a container's stop send:

```go
stop := signal.Notify(signal.Interrupt, signal.Terminate)
for {
    select {
    case s := <-stop:
        fmt.Println("stopping:", s)          // stopping: interrupt
        return
    case r := <-requests:
        serve(r)
    }
}
```

The channel holds one signal: one that arrives while the last is still unread is dropped, so a program slow to read sees that a signal came and not how many. Dropping the channel stops the delivery; the next of those signals is one the program did not ask for, and does what the next section says. A program that asked for a signal is not cancelled by it: the signal is its to answer.

## An interrupt is a cancel

A program that asks for no signal is stopped by `Interrupt` or `Terminate` the way `task.Within` stops a function's work: every wait of every task returns as chapter 33's table says, a receive with `None`, a read with an `io.Error` of Kind `io.Cancelled`, a sleep at once; `task.Cancelled()` answers `true`; `main` returns, and its drops run, with those of every task that ended before it; and the program then ends by the signal itself, so that a shell sees what it sees of any program the signal ended, the status `130` after Ctrl-C and `143` after a `kill`:

```go
func main() {
    log := os.Create("run.log")?             // closed by its drop as main returns, interrupted or not
    for job := range jobs {                  // None once interrupted: the loop ends
        work(ref log, job)
    }

    if task.Cancelled() {
        fmt.Println("interrupted")
    }
}
```

Go exits at once on either signal with nothing deferred run, and a C program dies where it stands; here a program that was written to stop on its deadline stops on Ctrl-C the same way, with nothing added.

A cancel reaches a wait and nothing else, as chapter 33 says: a task computing, a read of a file or the terminal, a lock, and `Join` or a group's `Wait` on a task that computes are not ended by it, and a `main` in one of them does not return. The second interrupt ends the program at once, which is what a user who presses Ctrl-C twice means; a program that must stop a computing task on an interrupt asks `task.Cancelled()` between rounds, as it does under a deadline.

On Windows, `Interrupt` is Ctrl-C or Ctrl-Break, and `Terminate` the console's close, the logoff or the shutdown, after which the system ends the program within the seconds it allows; a program cancelled by an interrupt ends with the exit code Windows gives a program Ctrl-C ended.

## Broken pipes

A write to a pipe or a connection whose reader is gone fails with an `io.Error` of Kind `io.BrokenPipe`; the program is not ended by it, as `SIGPIPE` ends a C program. The one exception is Unix's own rule: a write to standard output or standard error whose reader is gone ends the program by `SIGPIPE`, so that `prog | head` ends `prog` as every program in a pipeline is ended.

## What a child inherits

A child starts with its three standard streams and nothing else of this program's: every file, socket and pipe the runtime opens is closed at the exec. It starts with the default signal dispositions and an empty signal mask, whatever this program blocks or ignores for its own catching, so a child never finds Ctrl-C ignored because its parent catches it, the mistake a parent that ignores `SIGINT` makes in every language whose runtime forgets it.

## Errors

| The message | What it means, and what to do |
|---|---|
| `exec NAME: executable file not found in $PATH` | Nothing runs by that name; `%PATH%` on Windows |
| `exec NAME: cannot run executable found relative to current directory` | `PATH` holds `.` or an empty entry, and the program was found through it; run it as `./NAME` |
| `exec NAME: permission denied`, `exec NAME: no such file or directory` | The system refused to start it: not executable, or a path that names nothing |
| `exec NAME.bat: a batch file runs through cmd.exe: run it as cmd /c` | Windows runs a `.bat` or `.cmd` file through `cmd.exe`, which reads the arguments its own way; name `cmd` and quote them for it |
| `wait: no child processes` | Something else in this program, a bound library, collected the child's end; how it ended is lost |
| `wait: operation cancelled`, `read stdout: operation cancelled` | The task's scope was cancelled while it waited; the child runs on |
| `wait: i/o timeout` | The task's deadline passed while it waited; the child runs on |
| `write stdin: broken pipe` | The child closed its input, or ended; `Written()` has what it took |

## For maintainers

**The child.** `Start` resolves the program with `LookPath`, written in Snello in `exec_unix.snello` and `exec_windows.snello`, and calls the runtime's `start`, `sn_os__exec_start` in `process.c`. On Linux and macOS the child is made by `posix_spawn`: file actions `dup2` the three streams onto 0, 1 and 2, which clears their close-on-exec flags in the child while every other descriptor of this program's, all opened close-on-exec, closes; `addchdir_np` sets `Dir`; the spawn attributes give the child an empty signal mask and the default dispositions of `SIGINT`, `SIGTERM`, `SIGPIPE` and `SIGCHLD`; on macOS `POSIX_SPAWN_CLOEXEC_DEFAULT` closes what a bound C library may have opened without the flag, with `addinherit_np` naming an inherited stream. The child is then watched through the poller: on Linux its pidfd, `pidfd_open`, registered as a socket is and readable once the child has ended, its end collected by `waitid` with `P_PIDFD` and a signal sent through `pidfd_send_signal`, so that a number since given to another process is never touched; on macOS a record of its own in the poller, with no descriptor, which the runtime's signal thread readies at each `SIGCHLD`, and `waitpid`. The kqueue's process filter is not used for this: XNU's `proc_exit` posts its `NOTE_EXIT` before it marks the child a zombie and before it signals the parent, so a reap on that one-shot note can find nothing to collect, and nothing wakes the waiter again; `SIGCHLD` comes after the zombie is marked. Every live child is readied at a `SIGCHLD`, and a wait whose child still runs waits again. `Wait` is a loop of a reap without waiting and a park in `sn_net_wait_fd` or `sn_net_wait_proc`, the wait a socket's read makes, which a cancel ends with the same code; a reap that gets `ECHILD` marks the child ended with its end lost, which `Wait` reports as that error, so that a bound library that collected it breaks one `Wait` and never a drop or a `Detach`. A child started and then not watchable, a `pidfd_open` or a registration that failed, is killed and collected before `Start` fails, so a failure leaves nothing behind. A detached child goes on a list the signal thread reaps: on Linux at each `SIGCHLD`, which the thread waits for beside the interrupt, and on macOS at each `SIGCHLD` its kqueue records, so one list of the live children serves both systems, each marked detached or not. The exit records of macOS and Windows, which no descriptor table holds, are never returned to the allocator: a record let go is kept for the next child, since on Windows its completion may be in a worker's batch, read from the port before the record was let go, or posted again by the keeper, and a completion of the child before readies the next child's record, whose wait reaps nothing and waits again. A task in no scope that waited is listed under the program's own scope, and `task_entry` in `sched.c` unlists it at its end as it unlists one in a scope, so that an interrupt's cancel walks a list of live tasks.

**The pipes.** On Linux and macOS a pipe's end here is non-blocking, a property of that end alone, and registered with the poller for its one direction, `sn_net_watch`; a `Pipe`'s read or write is `sn_os__exec_read` or `sn_os__exec_write`, the loop a socket's makes, parking in `sn_net_wait_fd` while nothing can move. On Windows a pipe cannot be polled for readiness, so this program's end is a named pipe, named by the process number and a random number and made as the first instance of its name so that a name in use fails and another is tried, opened with `FILE_FLAG_OVERLAPPED` and bound to the completion port under its own key, and each read or write is one overlapped operation, a `pipe_op` record in `net.c` whose completion the port posts and whose record the task waits on as it waits on a socket; a cancel calls `CancelIoEx` and then waits for the completion whatever the scope, `sn_net_wait_op_done`, since the system holds the buffer the task lent until it comes. The Windows child is `process_windows.c`: `CreateProcessW` with the three handles inheritable through the attribute list and no other, the command line quoted as the C runtime's parser reads one, a UTF-16 environment, the child on this program's console, and its process handle associated with the port by a wait completion packet, as the keeper's timer is, `sn_wait_packet_make` in `sched.c`, with no callback thread; a child the packet cannot be made for is ended with `TerminateProcess` before `Start` fails. A batch file is refused before the call, `startable` in `exec_windows.snello`, since the system runs one through `cmd.exe`, whose parser reads the quoted line its own way and runs what a crafted argument tells it to: a program that means to run one names `cmd` and quotes the arguments for it, as Node refuses them and Rust escapes them after the same finding.

**The signals.** `sn_signals_start`, called by `sn_init` before any thread exists, starts the signal thread: on Linux the interrupt, the terminate and `SIGCHLD` are blocked in the main thread, which every thread started after inherits, and the thread waits for them with `sigwait`; on macOS, where a signal sent to the process is never given to `sigwait`, the two are ignored and the thread waits on a kqueue whose signal filter records each, which it does of an ignored signal, and which records `SIGCHLD` too, under its default disposition, for the children; on Windows the handler `SetConsoleCtrlHandler` registers is that thread, on one of the system's, and for the console's close, the logoff and the shutdown never returns, since the system ends the process when it does. `SIGPIPE` is set to be ignored at the same time, and `sn_write_fd` in `snello.c` ends the program by it for a write to descriptor 1 or 2 that fails with `EPIPE`, the disposition restored and the signal raised. A signal is offered to each channel a `Notify` registered, `sn_os__signal_notify`, through `sn_chan_offer`, as a timer offers its time, dropped when the slot is full, and a channel whose receiving end the program dropped is let go at that delivery; a signal no channel takes cancels the program's root scope, `sn_scope_cancel_root`, the first time, and ends the program at once the second, by the signal's default action. `sn_signals_finish`, at `sn_finish`, ends a program the first signal cancelled by that signal once `main` has returned, standard output flushed; the debug build's `sn_finish` runs its count of what was released first, `sn_signals_stop` having let go of the runtime's channel ends as `sn_timers_stop` lets go of a timer's.

**The root scope.** A task in no scope, the main task and every task spawned in none, keeps no scope in its record, so a spawn in none pays nothing and a spawn for a handle keeps its lazy word; but it is the root's: `sn_cancel_state` and `sn_cancel_wait_state` read the root's state for it, and its first wait a cancel may end puts it in the root's list, `root_enlist` in `scope.c`, which a cancel of the root walks and wakes. Every scope made in no scope links under the root, so a cancel of the root walks into each. A group that takes a running task out of no scope takes it out of the root's list under the root's lock, `root_unlink`, the one lock taken under a group's, and the task's own enlisting reads the handle's word under that lock, so that the two cannot both put the task in a list. The root is never freed.

**Tests.** `std/os/exec/exec_test.snello` holds what runs on every system, a `Cmd`'s defaults and the error of a program that does not exist; `exec_unix_test.snello` and `exec_windows_test.snello` run real programs, each system's own: `Output` with both streams and the exit code, the pipes through `sort`, `Dir` and `Env`, `Null`, `Terminate` and its status, a child killed by the drop, a detached child reaped, a `Wait` under a deadline, a child dropped inside the scope whose deadline passed, a write to an ended child, `LookPath`'s refusals and the error of a file that may not run, and on Windows a batch file refused; `std/os/signal/signal_unix_test.snello` sends the program its own interrupt and terminate through `kill` and reads them from `Notify`, holds the slot to one signal, and a channel to the signals it asked for. The programs of `compiler/emit/internal/backendtest` run through both backends with interrupts sent by the runner: a waiting `main` whose drop runs and which exits by the signal, one whose tasks in no scope waited and ended before the interrupt, a computing `main`, one blocked reading its input and one joining computing tasks, each ended by the second interrupt and not the first, and a notified program cancelled by nothing. `examples/subprocess` runs itself as a worker. The stack analysis has a figure for every C-library call the child's path makes, in `compiler/emit/stack/testdata/libc.c` and `libc_windows.c`; the one call it cannot count is the wait packet's, made through a pointer, so a task that starts a child on Windows runs on a guarded stack.
