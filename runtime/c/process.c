// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Signals, and the programs a program runs. A signal is caught by no
// handler: a thread of the runtime's own waits for the interrupt and
// the terminate from the program's start, whatever the scheduler is
// doing, on Linux with sigwait, the two blocked in every thread by the
// mask set before the runtime starts one, and on macOS on a kqueue
// whose signal filter records each, the two ignored, since there a
// signal sent to the process is never given to sigwait; on Windows the
// handler the console calls on a thread of the system's is that thread.
// Either offers the signal to the channels the program asked for it on,
// as a timer offers its time, and a signal no channel takes cancels
// the program's scope, so that every wait returns and main does, its
// drops run, and the program then ends by the signal; a second such
// signal ends the program at once. SIGPIPE is ignored, so that a write
// to a pipe whose reader is gone fails as an error, but for a write to
// the standard output or error, which ends the program by the signal
// as the system would have, in snello.c's write. These are the runtime
// calls the os/signal and os/exec packages declare without bodies,
// under the names the compiler gives them.

#define _GNU_SOURCE
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#include <windows.h>
#else
#include <fcntl.h>
#include <pthread.h>
#include <signal.h>
#include <spawn.h>
#include <sys/wait.h>
#include <unistd.h>
#endif

#if defined(__APPLE__)
#include <sys/event.h>
#elif defined(__linux__)
#include <sys/syscall.h>
#endif

// The values of signal.Signal, the tags its enum gives Interrupt and
// Terminate, and the bit of each in the mask a Notify asks with;
// signal_exit_base and the signal's number is the exit code of a
// program a signal ended where the signal cannot end it itself.
enum { signal_interrupt = 0, signal_terminate = 1, signal_exit_base = 128 };

// Where a child's standard streams go, exec.Stdio's values: this
// program's own, a pipe to or from this program, or the null device.
enum { stdio_inherit = 0, stdio_piped = 1, stdio_null = 2 };

#if defined(_WIN32)
// STATUS_CONTROL_C_EXIT is the code of a program Ctrl-C ended, the
// system's own handler's, which a program ends by once an interrupt
// cancelled it.
enum { control_c_exit = (int)0xC000013A };
#endif

// A notifier is a channel the program asked for signals on: the
// runtime's sending end of it, and the mask of the signals asked for.
typedef struct {
    sn_chan *ch;
    int64_t mask;
} notifier;

static sn_mutex notify_mu = SN_MUTEX_INIT;
static notifier *notifiers; // under notify_mu
static int notified, notify_room;

// interrupted is the signal that cancelled the program, 0 while none
// has: the first signal no channel takes cancels, the second ends.
static _Atomic int interrupted;

// A child is a program this one started: its process number; on Linux
// its pidfd, readable once it has ended, and on macOS the record of its
// exit event in the poller, NULL once the event can no longer come; how
// it ended once it has, found by the reap that collected it, or lost
// when something else in this program collected it first; and its
// place in the list of the detached children, which the signal thread
// reaps when they end.
typedef struct child {
    int64_t pid;
    int fd;
    void *pd;
    bool ended;
    bool lost;
    bool exited;
    int code;
    int sig;
    struct child *next;
} child;

#if !defined(_WIN32)
static sn_mutex detached_mu = SN_MUTEX_INIT;
static child *detached; // under detached_mu
static const char *null_device = "/dev/null";
extern char **environ;
static void reap_detached(void *which);
static void child_free(child *c);
#endif

#if defined(__APPLE__)
static int signal_queue = -1; // the kqueue the signal thread waits on, the signals ignored
#elif !defined(_WIN32)
static sigset_t caught; // the signals the runtime catches, blocked in every thread and waited for by one
#endif

// value is a signal's value of signal.Signal, written as the channel's
// element: the enum's tag in the first four bytes of a cell of eight,
// which is the element's size on either backend, zero past the tag.
typedef union {
    int32_t tag;
    uint64_t cell;
} signal_value;

// deliver offers the signal, by its value, to every channel that asked
// for it, and reports whether one did; a channel whose receiving end
// the program dropped is let go of here, at the first signal after.
static bool deliver(int64_t v) {
    signal_value value = { .cell = 0 };
    value.tag = (int32_t)v;
    bool taken = false;
    sn_mutex_lock(&notify_mu);
    for (int i = 0; i < notified;) {
        notifier *n = &notifiers[i];
        if (!sn_chan_open(n->ch)) {
            sn_chan_drop(&n->ch, SN_CHAN_SEND);
            sn_outside(-1);
            notifiers[i] = notifiers[--notified];
            continue;
        }

        if ((n->mask & ((int64_t)1 << v)) != 0) {
            sn_chan_offer(n->ch, &value); // dropped when the slot still holds the last: a delivery never waits
            taken = true;
        }

        i++;
    }

    sn_mutex_unlock(&notify_mu);
    return taken;
}

// end_now ends the program by the signal, at once: on Linux and macOS
// the signal's default action, restored and the signal raised on this
// thread with the signal unblocked, so that a shell sees the death it
// sees of any program; on Windows the exit code Ctrl-C gives.
static _Noreturn void end_now(int64_t v) {
    fflush(stdout);
#if defined(_WIN32)
    (void)v;
    ExitProcess((UINT)control_c_exit);
#else
    int sig = v == signal_interrupt ? SIGINT : SIGTERM;
    sigset_t one;
    sigemptyset(&one);
    sigaddset(&one, sig);
    signal(sig, SIG_DFL);
    pthread_sigmask(SIG_UNBLOCK, &one, NULL); // blocked on Linux; ignored, and so not blocked, on macOS
    raise(sig);
    _exit(signal_exit_base + sig);
#endif
}

// arrived is what the thread that caught the signal does with it: the
// channels that asked for it take it; a signal none takes cancels the
// program's scope the first time, and ends the program the second.
static void arrived(int64_t v) {
    if (deliver(v)) {
        return;
    }

    int none = 0;
    if (atomic_compare_exchange_strong(&interrupted, &none, (int)v + 1)) {
        sn_scope_cancel_root();
        return;
    }

    end_now(v);
}

#if defined(_WIN32)
// console_handler answers the console's control events on the thread
// the system calls it on: Ctrl-C and Ctrl-Break are the interrupt, and
// the console's close, the logoff and the shutdown the terminate, after
// which the handler never returns, since the system ends the process
// when it does, within the seconds it allows.
static BOOL WINAPI console_handler(DWORD type) {
    switch (type) {
    case CTRL_C_EVENT:
    case CTRL_BREAK_EVENT:
        arrived(signal_interrupt);
        return TRUE;
    case CTRL_CLOSE_EVENT:
    case CTRL_LOGOFF_EVENT:
    case CTRL_SHUTDOWN_EVENT:
        arrived(signal_terminate);
        Sleep(INFINITE);
        return TRUE;
    default:
        return FALSE;
    }
}

void sn_signals_start(void) { (void)SetConsoleCtrlHandler(console_handler, TRUE); }
#elif defined(__APPLE__)
// signal_main is the thread that waits for the caught signals, for the
// program's life: the kqueue's signal filter records a signal sent to
// the process whatever its disposition, ignored here.
static void *signal_main(void *arg) {
    (void)arg;
    for (;;) {
        struct kevent got;
        if (kevent(signal_queue, NULL, 0, &got, 1, NULL) != 1) {
            continue;
        }

        if (got.filter == EVFILT_PROC) {
            reap_detached(got.udata); // a detached child ended
        } else if (got.filter == EVFILT_SIGNAL) {
            arrived(got.ident == SIGINT ? signal_interrupt : signal_terminate);
        }
    }

    return NULL; // never reached: a thread's function has a result to give
}

void sn_signals_start(void) {
    signal(SIGINT, SIG_IGN);
    signal(SIGTERM, SIG_IGN);
    signal(SIGPIPE, SIG_IGN);
    signal_queue = kqueue();
    struct kevent ev[2];
    EV_SET(&ev[0], SIGINT, EVFILT_SIGNAL, EV_ADD, 0, 0, NULL);
    EV_SET(&ev[1], SIGTERM, EVFILT_SIGNAL, EV_ADD, 0, 0, NULL);
    if (signal_queue < 0 || kevent(signal_queue, ev, 2, NULL, 0, NULL) < 0 || !sn_thread_start(signal_main, NULL)) {
        sn_panic(SN_STR("cannot start the signal thread", 30));
    }
}
#else
// signal_main is the thread that waits for the caught signals, for the
// program's life, which are blocked in every thread and so pending for
// it alone.
static void *signal_main(void *arg) {
    (void)arg;
    for (;;) {
        int sig = 0;
        if (sigwait(&caught, &sig) != 0) {
            continue;
        }

        if (sig == SIGCHLD) {
            reap_detached(NULL); // a child ended: the detached ones are looked at
        } else {
            arrived(sig == SIGINT ? signal_interrupt : signal_terminate);
        }
    }

    return NULL; // never reached: a thread's function has a result to give
}

void sn_signals_start(void) {
    sigemptyset(&caught);
    sigaddset(&caught, SIGINT);
    sigaddset(&caught, SIGTERM);
    sigaddset(&caught, SIGCHLD);               // for the detached children, which the signal thread reaps
    pthread_sigmask(SIG_BLOCK, &caught, NULL); // every thread started after inherits the mask
    signal(SIGPIPE, SIG_IGN);
    if (!sn_thread_start(signal_main, NULL)) {
        sn_panic(SN_STR("cannot start the signal thread", 30));
    }
}
#endif

// sn_os__signal_notify is signal's notify: the runtime holds ch's sending
// end from now on and offers it each signal in mask, a bit a value of
// Signal; a program waiting on the channel is not deadlocked, since a
// signal is a wake from outside.
void sn_os__signal_notify(sn_chan *ch, int64_t mask) {
    sn_mutex_lock(&notify_mu);
    if (notified == notify_room) {
        notify_room = notify_room == 0 ? 4 : notify_room * 2;
        notifiers = realloc(notifiers, (size_t)notify_room * sizeof *notifiers);
        if (notifiers == NULL) {
            sn_panic(SN_STR("out of memory", 13));
        }
    }

    notifiers[notified++] = (notifier){ ch, mask };
    sn_outside(1);
    sn_mutex_unlock(&notify_mu);
}

void sn_signals_stop(void) {
    sn_mutex_lock(&notify_mu);
    for (int i = 0; i < notified; i++) {
        sn_chan_drop(&notifiers[i].ch, SN_CHAN_SEND);
    }

    free(notifiers);
    notifiers = NULL;
    notified = notify_room = 0;
    sn_mutex_unlock(&notify_mu);
#if !defined(_WIN32)
    sn_mutex_lock(&detached_mu); // a detached child that outlives the program is the runtime's, not a leak
    while (detached != NULL) {
        child *c = detached;
        detached = c->next;
        child_free(c);
    }

    sn_mutex_unlock(&detached_mu);
#endif
}

// sn_signals_finish is what the program's end does once main has
// returned: nothing for a program no signal cancelled, and else the
// end by the signal.
void sn_signals_finish(void) {
    int sig = atomic_load(&interrupted);
    if (sig != 0) {
        end_now(sig - 1);
    }
}

#if !defined(_WIN32)
// reap collects the child's end when it has ended, without waiting,
// and reports whether it had: on Linux through its pidfd, so that no
// other process is ever asked about, on macOS by its number, which is
// its own until it is reaped. A child the system no longer knows as
// this program's, which a bound library that waits for any child has
// collected, has ended with its end lost.
static bool reap(child *c) {
    if (c->ended) {
        return true;
    }

#if defined(__linux__)
    siginfo_t info;
    memset(&info, 0, sizeof info);
    if (waitid(P_PIDFD, (id_t)c->fd, &info, WEXITED | WNOHANG) != 0) {
        c->ended = c->lost = errno == ECHILD;
        return c->ended;
    }

    if (info.si_pid == 0) {
        return false;
    }

    c->exited = info.si_code == CLD_EXITED;
    c->code = c->exited ? info.si_status : 0;
    c->sig = c->exited ? 0 : info.si_status;
#else
    int status = 0;
    pid_t got = waitpid((pid_t)c->pid, &status, WNOHANG);
    if (got != (pid_t)c->pid) {
        c->ended = c->lost = got < 0 && errno == ECHILD;
        return c->ended;
    }

    c->exited = WIFEXITED(status);
    c->code = c->exited ? WEXITSTATUS(status) : 0;
    c->sig = WIFSIGNALED(status) ? WTERMSIG(status) : 0;
#endif
    c->ended = true;
    return true;
}

// child_free lets a child's record go, its pidfd or its exit event
// with it.
static void child_free(child *c) {
#if defined(__linux__)
    sn_net_unwatch(c->fd);
    close(c->fd);
#else
    if (c->pd != NULL) {
        sn_net_unwatch_proc(c->pd);
    }
#endif
    sn_free(c);
}

// reap_detached reaps the detached children that have ended: on macOS
// the one whose exit event came, which; on Linux, where a SIGCHLD may
// stand for several, every one on the list that has.
static void reap_detached(void *which) {
    sn_mutex_lock(&detached_mu);
    for (child **p = &detached; *p != NULL;) {
        child *c = *p;
        if ((which == NULL || which == c) && reap(c)) {
            *p = c->next;
            child_free(c);
            continue;
        }

        p = &c->next;
    }

    sn_mutex_unlock(&detached_mu);
}

// cstring copies s with a zero after it, for the system's calls.
static char *cstring(sn_string s) {
    char *p = malloc((size_t)s.len + 1);
    if (p == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    memcpy(p, s.ptr, (size_t)s.len);
    p[s.len] = 0;
    return p;
}

// cstrings copies the strings of xs, with first before them when it
// is given, into an array ending in NULL, as argv and envp are passed.
static char **cstrings(const sn_string *first, sn_slice xs) {
    size_t n = (size_t)xs.len + (first != NULL ? 1 : 0);
    char **out = malloc((n + 1) * sizeof *out);
    if (out == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    size_t i = 0;
    if (first != NULL) {
        out[i++] = cstring(*first);
    }

    for (int64_t k = 0; k < xs.len; k++) {
        out[i++] = cstring(((const sn_string *)xs.ptr)[k]);
    }

    out[i] = NULL;
    return out;
}

static void free_cstrings(char **xs) {
    for (char **p = xs; *p != NULL; p++) {
        free(*p);
    }

    free(xs);
}

// a_pipe makes a pipe whose both ends are closed at an exec, and
// returns the system's error number.
static int a_pipe(int fds[2]) {
#if defined(__linux__)
    return pipe2(fds, O_CLOEXEC) == 0 ? 0 : errno;
#else
    if (pipe(fds) != 0) {
        return errno;
    }

    if (fcntl(fds[0], F_SETFD, FD_CLOEXEC) != 0 || fcntl(fds[1], F_SETFD, FD_CLOEXEC) != 0) {
        int code = errno;
        close(fds[0]);
        close(fds[1]);
        return code;
    }

    return 0;
#endif
}

// close_fds closes the descriptors of fds that are not -1.
static void close_fds(int *fds, int n) {
    for (int i = 0; i < n; i++) {
        if (fds[i] >= 0) {
            close(fds[i]);
        }
    }
}

// stream_action adds to actions what the child's stream i is given,
// as mode says, stdio_inherit, stdio_piped or stdio_null: for a piped
// one, a pipe whose end here, made non-blocking, is stored in ours and
// whose end for the child in theirs. It returns the system's error
// number.
static int stream_action(posix_spawn_file_actions_t *actions, int i, int64_t mode, int *ours, int *theirs) {
    switch (mode) {
    case stdio_piped: {
        int fds[2];
        int code = a_pipe(fds);
        if (code != 0) {
            return code;
        }

        *theirs = i == 0 ? fds[0] : fds[1];
        *ours = i == 0 ? fds[1] : fds[0];
        if (fcntl(*ours, F_SETFL, O_NONBLOCK) != 0) {
            return errno;
        }

        posix_spawn_file_actions_adddup2(actions, *theirs, i);
        return 0;
    }
    case stdio_null:
        posix_spawn_file_actions_addopen(actions, i, null_device, i == 0 ? O_RDONLY : O_WRONLY, 0);
        return 0;
    default:
#if defined(__APPLE__)
        posix_spawn_file_actions_addinherit_np(actions, i); // named, since every other descriptor closes
#endif
        return 0;
    }
}

// spawn_child starts the program at path with args after its name and
// the environment envp, this program's for NULL, in dir unless dir is
// empty, with the streams actions say: by posix_spawn, which copies no
// page tables, the child given the default mask and the default
// dispositions of the signals this program blocks or ignores for its
// own catching, so that it never inherits them. It stores the child's
// number and returns the system's error number.
static int spawn_child(sn_string path, sn_slice args, sn_string dir, char **envp, posix_spawn_file_actions_t *actions,
                       pid_t *made) {
    int code = 0;
    char *cdir = NULL;
    if (dir.len > 0) {
        cdir = cstring(dir);
#if defined(__APPLE__)
        // The call with the np suffix is the one macOS 14.4, the floor,
        // has; the SDK of a later macOS marks it deprecated for the one
        // without, which that floor lacks.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
#endif
        code = posix_spawn_file_actions_addchdir_np(actions, cdir);
#if defined(__APPLE__)
#pragma clang diagnostic pop
#endif
    }

    if (code == 0) {
        posix_spawnattr_t attr;
        posix_spawnattr_init(&attr);
        sigset_t none, defaults;
        sigemptyset(&none);
        sigemptyset(&defaults);
        sigaddset(&defaults, SIGINT);
        sigaddset(&defaults, SIGTERM);
        sigaddset(&defaults, SIGPIPE);
        sigaddset(&defaults, SIGCHLD);
        short flags = POSIX_SPAWN_SETSIGMASK | POSIX_SPAWN_SETSIGDEF;
#if defined(__APPLE__)
        flags |= POSIX_SPAWN_CLOEXEC_DEFAULT;
#endif
        posix_spawnattr_setflags(&attr, flags);
        posix_spawnattr_setsigmask(&attr, &none);
        posix_spawnattr_setsigdefault(&attr, &defaults);
        char *cpath = cstring(path);
        char **argv = cstrings(&path, args);
        code = posix_spawn(made, cpath, actions, &attr, argv, envp != NULL ? envp : environ);
        free(cpath);
        free_cstrings(argv);
        posix_spawnattr_destroy(&attr);
    }

    free(cdir);
    return code;
}

// watch_child makes the record of the child with the number pid,
// watched through the poller: on Linux by its pidfd, readable once the
// child has ended; on macOS by the exit event its record is registered
// for, NULL for a child that ended already, which the first reap
// collects. It returns the system's error number.
static int watch_child(pid_t pid, child **out) {
    child *c = sn_alloc(sizeof *c);
    memset(c, 0, sizeof *c);
    c->pid = pid;
    c->fd = -1;
#if defined(__linux__)
    c->fd = (int)syscall(SYS_pidfd_open, pid, 0);
    if (c->fd < 0) {
        int code = errno;
        sn_free(c);
        return code;
    }

    int code = sn_net_watch(c->fd, SN_WATCH_READ);
    if (code != 0) {
        close(c->fd);
        sn_free(c);
        return code;
    }
#else
    c->pd = sn_net_watch_proc(pid);
#endif
    *out = c;
    return 0;
}

// sn_os__exec_start is exec's start: the program at path, which is
// run as given, with args after its name, in dir unless dir is empty,
// with the environment env when hasEnv says so and else this
// program's, its three standard streams going where the modes say.
// It stores the child's number and record, and the descriptors of
// this program's ends of the pipes, -1 for a stream not piped, and
// returns the system's error number. The three streams are set by
// posix_spawn's file actions, which clear their close-on-exec flags
// in the child, while every other descriptor of this program's keeps
// its own and closes.
int64_t sn_os__exec_start(sn_string path, void *args_slice, sn_string dir, void *env_slice, bool has_env,
                          int64_t in_mode, int64_t out_mode, int64_t err_mode, int64_t *pid, int64_t *handle,
                          int64_t *in_fd, int64_t *out_fd, int64_t *err_fd) {
    sn_slice args = *(const sn_slice *)args_slice, env = *(const sn_slice *)env_slice;
    int64_t modes[3] = { in_mode, out_mode, err_mode };
    int64_t *parents[3] = { in_fd, out_fd, err_fd };
    int theirs[3] = { -1, -1, -1 }, ours[3] = { -1, -1, -1 };
    posix_spawn_file_actions_t actions;
    posix_spawn_file_actions_init(&actions);
    int code = 0;
    for (int i = 0; i < 3 && code == 0; i++) {
        code = stream_action(&actions, i, modes[i], &ours[i], &theirs[i]);
    }

    pid_t made = 0;
    if (code == 0) {
        char **envp = has_env ? cstrings(NULL, env) : NULL;
        code = spawn_child(path, args, dir, envp, &actions, &made);
        if (envp != NULL) {
            free_cstrings(envp);
        }
    }

    posix_spawn_file_actions_destroy(&actions);
    close_fds(theirs, 3);
    child *c = NULL;
    if (code == 0) {
        code = watch_child(made, &c);
    }

    for (int i = 0; i < 3 && code == 0; i++) {
        if (ours[i] >= 0) {
            code = sn_net_watch(ours[i], i == 0 ? SN_WATCH_WRITE : SN_WATCH_READ);
        }
    }

    if (code != 0) {
        close_fds(ours, 3);
        if (made != 0) { // started, and not to be watched: ended here and collected, so that nothing is left behind
            kill(made, SIGKILL);
            while (waitpid(made, NULL, 0) < 0 && errno == EINTR) {
            }
        }

        if (c != NULL) {
            child_free(c);
        }

        return code;
    }

    for (int i = 0; i < 3; i++) {
        *parents[i] = ours[i];
    }

    *pid = made;
    *handle = (int64_t)(intptr_t)c;
    return 0;
}

// sn_os__exec_wait is Child's wait: the running task parks until the
// child has ended, and its end is stored; a cancel of the task's scope
// returns its code with the child still running, and a child whose end
// something else in this program collected ECHILD.
int64_t sn_os__exec_wait(int64_t handle, bool *exited, int64_t *code, int64_t *sig) {
    child *c = (child *)(intptr_t)handle;
    for (;;) {
        if (reap(c)) {
            if (c->lost) {
                return ECHILD;
            }

            *exited = c->exited;
            *code = c->code;
            *sig = c->sig;
            return 0;
        }

#if defined(__linux__)
        int cut = sn_net_wait_fd(c->fd, false);
#else
        if (c->pd == NULL) {
            return ECHILD; // ended before its event was registered, and not this program's to collect
        }

        int cut = sn_net_wait_proc(c->pd);
#endif
        if (cut != 0) {
            return cut;
        }
    }
}

// sn_os__exec_signal sends the child the signal, nothing once it has
// ended: on Linux through its pidfd, so that a number since given to
// another process is never signalled.
int64_t sn_os__exec_signal(int64_t handle, int64_t sig) {
    child *c = (child *)(intptr_t)handle;
    if (c->ended) {
        return 0;
    }

#if defined(__linux__)
    if (syscall(SYS_pidfd_send_signal, c->fd, (int)sig, NULL, 0) != 0 && errno != ESRCH) {
        return errno;
    }
#else
    if (kill((pid_t)c->pid, (int)sig) != 0 && errno != ESRCH) {
        return errno;
    }
#endif
    return 0;
}

// sn_os__exec_detach lets the child go: its record goes to the signal
// thread, which reaps it when it ends, at once when it has.
void sn_os__exec_detach(int64_t handle) {
    child *c = (child *)(intptr_t)handle;
    if (reap(c)) {
        child_free(c);
        return;
    }

#if defined(__APPLE__)
    struct kevent ev;
    EV_SET(&ev, (uintptr_t)c->pid, EVFILT_PROC, EV_ADD | EV_ONESHOT, NOTE_EXIT, 0, c);
    if (kevent(signal_queue, &ev, 1, NULL, 0, NULL) != 0) { // ended since the reap above
        reap(c);
        child_free(c);
        return;
    }
#endif
    sn_mutex_lock(&detached_mu);
    c->next = detached;
    detached = c;
    sn_mutex_unlock(&detached_mu);
    reap_detached(c); // ended between the reap above and the listing: a SIGCHLD or an event may have come already
}

// sn_os__exec_closeChild frees the record of a child that has been
// waited for.
void sn_os__exec_closeChild(int64_t handle) { child_free((child *)(intptr_t)handle); }

// sn_os__exec_read reads what the pipe has into buf and stores how
// much, 0 once the child has closed its end, parking while it has
// nothing; sn_os__exec_write writes all of buf and stores how much it
// wrote, which is less on a failure, EPIPE once the child has closed
// its end; sn_os__exec_closePipe closes this program's end.
int64_t sn_os__exec_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    if (buf->len == 0) {
        *n = 0;
        return 0;
    }

    for (;;) {
        ssize_t got = read((int)fd, buf->ptr, (size_t)buf->len);
        if (got >= 0) {
            *n = got;
            return 0;
        }

        if (errno == EINTR) {
            continue;
        }

        if (errno != EAGAIN && errno != EWOULDBLOCK) {
            return errno;
        }

        int cut = sn_net_wait_fd((int)fd, false);
        if (cut != 0) {
            return cut;
        }
    }
}

int64_t sn_os__exec_write(int64_t fd, sn_slice_u8 buf, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    *n = 0;
    while (*n < buf.len) {
        ssize_t put = write((int)fd, buf.ptr + *n, (size_t)(buf.len - *n));
        if (put >= 0) {
            *n += put;
            continue;
        }

        if (errno == EINTR) {
            continue;
        }

        if (errno != EAGAIN && errno != EWOULDBLOCK) {
            return errno;
        }

        int cut = sn_net_wait_fd((int)fd, true);
        if (cut != 0) {
            return cut;
        }
    }

    return 0;
}

void sn_os__exec_closePipe(int64_t fd) {
    sn_net_unwatch((int)fd);
    close((int)fd);
}
#endif
