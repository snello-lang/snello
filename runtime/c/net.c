// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Sockets and the poller: a program reads and writes a connection as
// blocking code and runs as event-driven code. Every socket is
// non-blocking and is
// registered with the system's poller, kqueue or epoll, once, when it
// is made; an operation that would block parks the task until the
// poller says the socket changed, and the worker that hears of the
// event, looking at the poller between tasks or waiting in it while
// idle (sched.c), runs the task, which retries. A write to a
// connection the other side closed fails with EPIPE rather than raising
// SIGPIPE, per socket, so that the rest of the program, its pipes
// included, keeps the system's behavior. These are the system calls the
// net package declares without a body, under the names the compiler
// gives them: each returns the system's error number, zero on success,
// and stores its result through its last parameter, as the os functions
// do.

#define _GNU_SOURCE // glibc's accept4 and the socket calls under -std=c11
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
// winsock2.h in a block of its own, as the others need it first.
#include <winsock2.h>

#include <mswsock.h>
#include <winternl.h>
#include <ws2tcpip.h>
#pragma comment(lib, "ws2_32")
#pragma comment(lib, "ntdll")
#else
#include <arpa/inet.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <unistd.h>
#endif

#if defined(__APPLE__)
#include <sys/event.h>
#include <sys/sysctl.h>
#elif !defined(_WIN32)
#include <sys/epoll.h>
#endif

#if defined(_WIN32)
// The AFD driver's poll, the ioctl that asks it when a socket is ready.
#define IOCTL_AFD_POLL 0x00012024

// The AFD driver's events: data or a connection to accept, the peer
// gone or reset, the socket closed here, room to send, a connect that
// failed.
enum {
    afd_poll_receive = 0x0001,
    afd_poll_send = 0x0004,
    afd_poll_disconnect = 0x0008,
    afd_poll_abort = 0x0010,
    afd_poll_local_close = 0x0020,
    afd_poll_accept = 0x0080,
    afd_poll_connect_fail = 0x0100,
    afd_ended = afd_poll_abort | afd_poll_local_close | afd_poll_connect_fail,
    afd_read = afd_poll_receive | afd_poll_accept | afd_poll_disconnect | afd_ended,
    afd_write = afd_poll_send | afd_ended,
};
#endif

// last_error reads errno through a call the compiler can neither
// inline nor merge: glibc declares the function behind errno const, so
// a loop that parks between two reads could otherwise read the errno of
// the thread the task left (sched.c says why a task may move).
#if defined(_WIN32)
// On Windows a socket call reports its failure through WSAGetLastError,
// as its own code, which is read as the error number the same failure
// has on the other systems, so that io.Kind and the messages read it
// alike.
SN_HELD_STATIC static int errno_of_wsa(int code) {
    switch (code) {
    case 0:
        return 0;
    case WSAEWOULDBLOCK:
        return EWOULDBLOCK;
    case WSAEINPROGRESS:
        return EINPROGRESS;
    case WSAEALREADY:
        return EALREADY;
    case WSAEISCONN:
        return EISCONN;
    case WSAENOTCONN:
        return ENOTCONN;
    case WSAECONNREFUSED:
        return ECONNREFUSED;
    case WSAECONNRESET:
        return ECONNRESET;
    case WSAECONNABORTED:
        return ECONNABORTED;
    case WSAESHUTDOWN:
        return EPIPE;
    case WSAETIMEDOUT:
        return ETIMEDOUT;
    case WSAEADDRINUSE:
        return EADDRINUSE;
    case WSAEADDRNOTAVAIL:
        return EADDRNOTAVAIL;
    case WSAEAFNOSUPPORT:
        return EAFNOSUPPORT;
    case WSAEPROTONOSUPPORT:
        return EPROTONOSUPPORT;
    case WSAENETUNREACH:
        return ENETUNREACH;
    case WSAEHOSTUNREACH:
        return EHOSTUNREACH;
    case WSAEINTR:
        return EINTR;
    case WSAEMFILE:
        return EMFILE;
    case WSAENOBUFS:
        return ENOBUFS;
    case WSAEACCES:
        return EACCES;
    case WSAEINVAL:
        return EINVAL;
    case WSAENOTSOCK:
        return ENOTSOCK;
    case WSAEMSGSIZE:
        return EMSGSIZE;
    case WSAENETRESET:
        return ENETRESET;
    case WSAENETDOWN:
        return ENETDOWN;
    case WSAEOPNOTSUPP:
        return EOPNOTSUPP;
    case WSAENOPROTOOPT:
        return ENOPROTOOPT;
    }

    return EIO;
}

SN_HELD_STATIC static __attribute__((noinline)) int last_error(void) {
    __asm__ volatile("" ::: "memory");
    return errno_of_wsa(WSAGetLastError());
}
#else
SN_HELD_STATIC static __attribute__((noinline)) int last_error(void) {
    __asm__ volatile("" ::: "memory");
    return errno;
}
#endif

// SN_SOCK_FLAGS makes a socket non-blocking and closed on exec as it is
// made, where the system can: Linux takes both as flags to socket and
// accept4, as Go asks for them, so a socket is never open across an exec
// for a moment and costs no further call. Darwin has neither flag, and
// prepare sets both with fcntl, as Go does there.
#if defined(_WIN32)
#define SN_SOCK_FLAGS 0 // made not inherited by NEW_SOCKET, and non-blocking by prepare
#elif defined(SOCK_NONBLOCK) && defined(SOCK_CLOEXEC)
#define SN_SOCK_FLAGS (SOCK_NONBLOCK | SOCK_CLOEXEC)
#else
#define SN_SOCK_FLAGS 0
#endif

// prepare makes a socket non-blocking and closed on exec, where it was
// not made so, and keeps a write to a connection the other side closed
// from raising SIGPIPE, which would end the program: the write fails
// with EPIPE instead, as an error the library returns. Linux says so per
// write, with MSG_NOSIGNAL; Darwin per socket. On a failure it returns
// the name of the call that failed, for the library's message, and NULL
// otherwise.
SN_HELD_STATIC static const char *prepare(int fd) {
#if defined(_WIN32)
    u_long yes = 1;
    return ioctlsocket((SOCKET)fd, FIONBIO, &yes) == 0 ? NULL : "ioctlsocket";
#endif
#if defined(__APPLE__)
    int yes = 1;
    if (setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof yes) != 0) {
        return "setsockopt";
    }
#endif
#if SN_SOCK_FLAGS == 0 && !defined(_WIN32)
    if (fcntl(fd, F_SETFD, FD_CLOEXEC) != 0) {
        return "fcntl";
    }

    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) != 0) {
        return "fcntl";
    }
#else
    (void)fd;
#endif

    return NULL;
}

// connected readies a connection, accepted or dialed: prepared, and
// with Nagle's delay off, as Go makes every TCP connection, so a small
// write goes out at once rather than waiting for the last one's ack.
// It names the call that failed, as prepare does.
SN_HELD_STATIC static const char *connected(int fd) {
    const char *failed = prepare(fd);
    if (failed != NULL) {
        return failed;
    }

    int yes = 1;
    return setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, (const void *)&yes, sizeof yes) == 0 ? NULL : "setsockopt";
}

// backlog is how many connections a listening socket keeps waiting for
// Accept: the system's largest, as Go asks for, read once.
SN_HELD_STATIC static int backlog(void) {
#if defined(_WIN32)
    return SOMAXCONN; // Windows' own word for its largest
#else
    static int n;
    if (n > 0) {
        return n;
    }

    int got = SOMAXCONN;
#if defined(__APPLE__)
    int v = 0;
    size_t len = sizeof v;
    if (sysctlbyname("kern.ipc.somaxconn", &v, &len, NULL, 0) == 0 && v > 0) {
        got = v;
    }
#else
    FILE *f = fopen("/proc/sys/net/core/somaxconn", "re"); // closed on exec, as every descriptor here
    if (f != NULL) {
        int v = 0;
        if (fscanf(f, "%d", &v) == 1 && v > 0) {
            got = v;
        }

        fclose(f);
    }
#endif
    n = got > 65535 ? 65535 : got; // the kernels keep it in sixteen bits
    return n;
#endif
}

#if defined(MSG_NOSIGNAL)
#define SN_NOSIGNAL MSG_NOSIGNAL
#else
#define SN_NOSIGNAL 0
#endif

// The socket calls that differ on Windows, where a socket is a handle a
// Winsock call takes, made once Winsock is started, not inherited by any
// program the process starts, and read with recv, a count that fits an
// int at a time.
#if defined(_WIN32)
#define NEW_SOCKET(family)                                                                                             \
    (sn_winsock_start() ? (int)WSASocketW(family, SOCK_STREAM, IPPROTO_TCP, NULL, 0,                                   \
                                          WSA_FLAG_OVERLAPPED | WSA_FLAG_NO_HANDLE_INHERIT)                            \
                        : -1)
#define CLOSE_SOCKET(s)       closesocket((SOCKET)(s))
#define READ_SOCKET(s, p, n)  recv((SOCKET)(s), (char *)(p), (int)((n) < INT32_MAX ? (n) : INT32_MAX), 0)
#define SEND_SOCKET(s, p, n)  send((SOCKET)(s), (const char *)(p), (int)((n) < INT32_MAX ? (n) : INT32_MAX), 0)
#define ACCEPT_SOCKET(s)      ((int)accept((SOCKET)(s), NULL, NULL))
#define SOCKET_ERROR_OF(code) errno_of_wsa(code)
#else
#define NEW_SOCKET(family)   socket(family, SOCK_STREAM | SN_SOCK_FLAGS, 0)
#define CLOSE_SOCKET(s)      close(s)
#define READ_SOCKET(s, p, n) read(s, p, n)
#define SEND_SOCKET(s, p, n) send(s, p, n, SN_NOSIGNAL)
#if SN_SOCK_FLAGS != 0
#define ACCEPT_SOCKET(s) accept4(s, NULL, NULL, SN_SOCK_FLAGS)
#else
#define ACCEPT_SOCKET(s) accept(s, NULL, NULL)
#endif
#define SOCKET_ERROR_OF(code) (code)
#endif

#if defined(_WIN32)
static sn_once winsock_once = SN_ONCE_INIT; // Winsock's start, before the first socket or lookup
static bool winsock_up;
static int winsock_code; // what Winsock's start returned, when it failed
static bool make_port(void);
#endif

static sn_mutex poller_mu = SN_MUTEX_INIT;
static _Atomic int poller =
    -1; // made once under poller_mu, read by any thread without it; on Windows 0 once the port is
#if defined(_WIN32)
static HANDLE port; // the completion port every poll, the keeper's wake and its deadline come through, once poller is 0
static HANDLE afd;  // the AFD driver's device the sockets are polled through, its completions on port
#endif

// waiting counts the tasks in a socket's word, which only a look at the
// poller wakes: while it is zero no worker looks, and a program without
// sockets makes no call into the poller.
static _Atomic int64_t waiting;

// promise counts n more tasks waiting on a socket, or n fewer once
// their wakes are kept, among the wakes the deadlock detector counts.
SN_HELD_STATIC static void promise(int64_t n) {
    atomic_fetch_add(&waiting, n);
    sn_outside(n);
}

// A socket's poll_desc is Go's pollDesc: what the poller knows of it,
// one word for reading and one for writing, each holding nothing,
// ready, an event that came while no task waited, or the task waiting.
// A socket is registered once, when it is made, for both directions and
// edge-triggered, EV_CLEAR on kqueue and EPOLLET on epoll: the poller
// hears of each change, and marks the word ready or wakes its task. A
// wait that finds the word ready returns at once, to try its call
// again, with no system call to register and no park; one that does not
// puts its task in the word and parks. The words change by
// compare-and-swap alone, so a wait takes no lock. A task in a word is
// a wake promised from outside the workers, which the deadlock detector
// counts, from just before the task goes in until it is taken out:
// by the poller, which wakes it and then keeps the promise, as a timer
// that fires does, or by the task itself, after a wake that came for
// something else. A waker that takes the task out holds no lock the
// task takes, so it wakes the task through the direction's handoff
// (internal.h), and a task that finds itself taken out waits for the
// handoff before it goes on: it may have been taken out while running,
// past a deadline or out of a park a kept wake ended, and could
// otherwise end and be freed before its waker reads it.
//
// Each direction also has its deadline, as Go's pollDesc has rd and wd:
// 0 for none, -1 once it has passed, and above zero while its timer is
// in a timing wheel, kept at rt or wt. When the timer fires, on a
// worker between two tasks, it marks the deadline passed and takes out
// the task waiting, if one is, which finds it passed. A task puts itself in its word and
// then looks at the deadline, and the timer marks the deadline and then
// looks at the word, both sequentially consistent, so one of the two
// sees the other and no task waits past its deadline.
enum { pd_none = 0, pd_ready = 1 };

typedef struct {
    _Atomic uintptr_t r;
    _Atomic uintptr_t w;
    sn_handoff rh; // the handoffs the wakers of r and w wake through
    sn_handoff wh;
    _Atomic int64_t rd;
    _Atomic int64_t wd;
    sn_deadline rt; // timer.c's, under its wheel's lock
    sn_deadline wt;
#if defined(_WIN32)
    HANDLE base;           // the socket the AFD driver polls: the base provider's, under any layered one
    _Atomic(void *) rpoll; // the poll a read's wait armed and the poller has not yet taken, or NULL
    _Atomic(void *) wpoll; // a write's
#endif
} poll_desc;

// The descriptors are found by number in a table that never moves, so a
// wait finds its own without a lock: chunks of 4096, for descriptor
// numbers below 2^24, each made when a socket first needs it and kept,
// so an event still on its way to a socket since closed names memory
// that is there.
enum { pd_chunk = 4096, pd_chunks = 4096 };

static _Atomic(poll_desc *) pds[pd_chunks];

static int wait_pd(poll_desc *pd, int fd, bool write);

SN_HELD_STATIC static poll_desc *pd_of(int fd) {
#if defined(_WIN32)
    fd >>= 2; // a socket's handle is a multiple of four
#endif
    if (fd < 0 || fd >= pd_chunk * pd_chunks) {
        sn_panic(SN_STR("net: descriptor number out of range", 35));
    }

    _Atomic(poll_desc *) *slot = &pds[fd / pd_chunk];
    poll_desc *c = atomic_load_explicit(slot, memory_order_acquire);
    if (c == NULL) {
        poll_desc *made = calloc(pd_chunk, sizeof *made);
        if (made == NULL) {
            sn_panic(SN_STR("out of memory", 13));
        }

        if (atomic_compare_exchange_strong_explicit(slot, &c, made, memory_order_acq_rel, memory_order_acquire)) {
            c = made;
        } else {
            free(made); // another thread made the chunk first
        }
    }

    return &c[fd % pd_chunk];
}

// ready marks a word ready, or takes the task waiting in it out and
// claims it, and returns the task it took out, or NULL, with whether
// the claim made it runnable, which the caller then queues or runs.
// The caller counts the wakes kept only once the tasks are on a queue
// or running, so that an idle worker looking at that moment finds work
// and not a program in which nothing can run. The woken task tries its
// call again, which finds what the event was for.
SN_HELD_STATIC static sn_task *ready(_Atomic uintptr_t *word, sn_handoff *woken, bool *claimed) {
    uintptr_t old = atomic_load_explicit(word, memory_order_acquire);
    for (;;) {
        if (old == pd_ready) {
            return NULL;
        }

        uintptr_t next = old == pd_none ? pd_ready : pd_none;
        if (atomic_compare_exchange_weak_explicit(word, &old, next, memory_order_acq_rel, memory_order_acquire)) {
            break;
        }
    }

    if (old == pd_none) {
        return NULL;
    }

    *claimed = sn_handoff_claim(woken, (sn_task *)old);
    return (sn_task *)old;
}

// take adds the task ready took out, if it did, to the tasks taken, and
// to those woke holds when the claim made it runnable.
SN_HELD_STATIC static void take(sn_task *t, bool claimed, sn_task **woke, int *n, int *taken) {
    if (t != NULL) {
        (*taken)++;
        if (claimed) {
            woke[(*n)++] = t;
        }
    }
}

SN_HELD int64_t sn_net_waiting(void) { return atomic_load(&waiting); }

SN_HELD void sn_net_kept(int64_t taken) {
    if (taken > 0) {
        promise(-taken);
    }
}

#if defined(_WIN32)
// On Windows a socket is not registered with the poller once, as on the
// other systems, since the AFD driver's poll is one-shot: a wait that is
// about to park arms a poll of its direction, unless one is armed, and
// the completion port says when it completes; each poll is a block of
// its own, so that a poll of a socket since closed, completing late,
// names memory still there and is told from the poll of the next socket
// with its number. This is the readiness the other systems' pollers
// give, through the port, as the AFD driver gives it to Winsock's own
// select.

// The poll's question and its answer, as the driver reads and writes
// them: a socket's handle, the events asked for and those that came, and
// a status, under a timeout and a count of the sockets asked about.
typedef struct {
    HANDLE handle;
    ULONG events;
    NTSTATUS status;
} afd_poll_handle_info;

typedef struct {
    LARGE_INTEGER timeout;
    ULONG count;
    ULONG exclusive;
    afd_poll_handle_info handles[1];
} afd_poll_info;

// An afd_poll is one poll armed: the status the driver writes, the
// question and its answer, and the socket's descriptor and direction.
// It is the poller's own, as a registration with epoll is the kernel's,
// so it comes from the C library and not the runtime's allocator, whose
// count a debug build checks at exit: a poll cancelled as its socket
// closed is freed when the port next gives it back, which may be after
// the program's last task.
typedef struct {
    IO_STATUS_BLOCK iosb;
    afd_poll_info info;
    poll_desc *pd;
    bool write;
} afd_poll;

// A pipe_op is one overlapped read or write of a pipe's end, for
// process_windows.c: the OVERLAPPED the system's call takes, first, so
// that the completion the port posts names the record, and the
// descriptor record whose read word the completion readies.
typedef struct {
    OVERLAPPED ov;
    poll_desc pd;
} pipe_op;

// arm_poll makes sure a poll of the direction is armed on fd, and returns 0,
// or the error number of a failure to arm one.
SN_HELD_STATIC static int arm_poll(int fd, bool write) {
    poll_desc *pd = pd_of(fd);
    _Atomic(void *) *slot = write ? &pd->wpoll : &pd->rpoll;
    if (atomic_load(slot) != NULL) {
        return 0;
    }

    afd_poll *p = calloc(1, sizeof *p);
    if (p == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    p->pd = pd;
    p->write = write;
    p->info.timeout.QuadPart = INT64_MAX;
    p->info.count = 1;
    p->info.handles[0].handle = pd->base;
    p->info.handles[0].events = write ? afd_write : afd_read;
    void *none = NULL;
    if (!atomic_compare_exchange_strong(slot, &none, p)) { // another wait armed one meanwhile
        free(p);
        return 0;
    }

    NTSTATUS st = NtDeviceIoControlFile(afd, NULL, NULL, p, &p->iosb, IOCTL_AFD_POLL, &p->info, sizeof p->info,
                                        &p->info, sizeof p->info);
    if (st != STATUS_PENDING && !NT_SUCCESS(st)) { // a poll that completed at once completes through the port too
        void *mine = p;
        atomic_compare_exchange_strong(slot, &mine, NULL);
        free(p);
        return sn_errno_of(RtlNtStatusToDosError(st));
    }

    return 0;
}

// polled takes a poll the port completed and frees it. A poll still the
// descriptor's armed one makes its direction ready, or takes its task
// out to try its call again, which finds what came; one that is not
// was cancelled as its socket closed, and wakes no one, since the
// descriptor may be the next socket's with the number.
SN_HELD_STATIC static sn_task *polled(afd_poll *p, bool *claimed) {
    poll_desc *pd = p->pd;
    _Atomic(void *) *slot = p->write ? &pd->wpoll : &pd->rpoll;
    void *mine = p;
    sn_task *t = NULL;
    if (atomic_compare_exchange_strong(slot, &mine, NULL)) {
        t = p->write ? ready(&pd->w, &pd->wh, claimed) : ready(&pd->r, &pd->rh, claimed);
    }

    free(p);
    return t;
}

// cancel_polls takes the polls armed on a socket about to close out of
// its descriptor and cancels them; each still completes through the
// port, where polled frees it. The cancel names a poll by its status
// block, so one the port completed meanwhile, and polled freed, may name
// a later poll that took its memory: that poll completes early, and its
// task tries its call again and finds nothing, as after any spurious
// wake.
SN_HELD_STATIC static void cancel_polls(poll_desc *pd) {
    _Atomic(void *) *slots[2] = { &pd->rpoll, &pd->wpoll };
    for (int i = 0; i < 2; i++) {
        afd_poll *p = atomic_exchange(slots[i], NULL);
        if (p != NULL) {
            (void)CancelIoEx(afd, (OVERLAPPED *)&p->iosb);
        }
    }
}

SN_HELD void *sn_net_port(void) { return port; }

SN_HELD int sn_net_poll(sn_task **woke, int *taken) {
    int n = 0;
    *taken = 0;
    OVERLAPPED_ENTRY events[SN_POLL_EVENTS];
    ULONG got = 0;
    if (!GetQueuedCompletionStatusEx(port, events, SN_POLL_EVENTS, &got, 0, FALSE)) {
        return 0;
    }

    for (ULONG i = 0; i < got; i++) {
        bool claimed = false;
        sn_task *t = NULL;
        poll_desc *pd = NULL;
        switch (events[i].lpCompletionKey) {
        case SN_KEY_POLL:
            t = polled((afd_poll *)events[i].lpOverlapped, &claimed);
            break;
        case SN_KEY_CHILD: // a child's exit: its record is the packet's context
            pd = events[i].lpOverlapped == NULL ? NULL : (poll_desc *)events[i].lpOverlapped;
            break;
        case SN_KEY_PIPE: // a pipe operation's completion: its record begins with the OVERLAPPED
            pd = &((pipe_op *)events[i].lpOverlapped)->pd;
            break;
        default: // the keeper's, which it gets back while it waits
            sn_keeper_packet(events[i].lpCompletionKey);
            continue;
        }

        if (pd != NULL) {
            t = ready(&pd->r, &pd->rh, &claimed);
        }

        take(t, claimed, woke, &n, taken);
    }

    return n;
}
#else
SN_HELD int sn_net_poll(sn_task **woke, int *taken) {
    int n = 0;
    *taken = 0;
#if defined(__APPLE__)
    struct kevent events[SN_POLL_EVENTS];
    struct timespec now = { 0, 0 };
    int got = kevent(poller, NULL, 0, events, SN_POLL_EVENTS, &now);
    for (int i = 0; i < got; i++) {
        poll_desc *pd = events[i].udata;
        bool write = events[i].filter == EVFILT_WRITE, claimed = false;
        sn_task *t = write ? ready(&pd->w, &pd->wh, &claimed) : ready(&pd->r, &pd->rh, &claimed);
        take(t, claimed, woke, &n, taken);
    }
#else
    struct epoll_event events[SN_POLL_EVENTS];
    int got = epoll_wait(poller, events, SN_POLL_EVENTS, 0);
    for (int i = 0; i < got; i++) {
        poll_desc *pd = events[i].data.ptr;
        uint32_t e = events[i].events;
        bool claimed = false;
        if (e & (EPOLLIN | EPOLLRDHUP | EPOLLHUP | EPOLLERR)) {
            sn_task *t = ready(&pd->r, &pd->rh, &claimed);
            take(t, claimed, woke, &n, taken);
        }

        claimed = false;
        if (e & (EPOLLOUT | EPOLLHUP | EPOLLERR)) {
            sn_task *t = ready(&pd->w, &pd->wh, &claimed);
            take(t, claimed, woke, &n, taken);
        }
    }
#endif
    return n;
}
#endif

SN_HELD int sn_net_poller(void) {
    int made = atomic_load(&poller);
    if (made >= 0) {
        return made;
    }

    sn_mutex_lock(&poller_mu);
    made = atomic_load(&poller);
    if (made < 0) {
#if defined(_WIN32)
        made = sn_winsock_start() && make_port() ? 0 : -1;
#elif defined(__APPLE__)
        made = kqueue();
        if (made >= 0 && fcntl(made, F_SETFD, FD_CLOEXEC) < 0) {
            close(made);
            made = -1;
        }
#else
        made = epoll_create1(EPOLL_CLOEXEC);
#endif
        atomic_store(&poller, made);
    }

    sn_mutex_unlock(&poller_mu);
    return made;
}

#if defined(_WIN32)
// make_port makes the completion port and opens the AFD driver's device
// on it, a name of the runtime's own under the driver's, as every
// poller built on it opens one; a completion that a poll made at once
// is still queued on the port, which the poller takes as it takes any.
// The port lets any number of its threads run at once: with the
// system's default, one a processor, a worker that ever looked at the
// port counts against it while it runs tasks, and with every worker
// busy the system would hold the keeper's deadline back.
SN_HELD_STATIC static bool make_port(void) {
    port = CreateIoCompletionPort(INVALID_HANDLE_VALUE, NULL, 0, MAXDWORD);
    if (port == NULL) {
        return false;
    }

    UNICODE_STRING name;
    RtlInitUnicodeString(&name, L"\\Device\\Afd\\Snello");
    OBJECT_ATTRIBUTES attrs;
    InitializeObjectAttributes(&attrs, &name, 0, NULL, NULL);
    IO_STATUS_BLOCK iosb;
    NTSTATUS st = NtCreateFile(&afd, SYNCHRONIZE, &attrs, &iosb, NULL, 0, FILE_SHARE_READ | FILE_SHARE_WRITE, FILE_OPEN,
                               0, NULL, 0);
    return NT_SUCCESS(st) && CreateIoCompletionPort(afd, port, SN_KEY_POLL, 0) != NULL &&
           SetFileCompletionNotificationModes(afd, FILE_SKIP_SET_EVENT_ON_HANDLE);
}

SN_HELD_STATIC static void winsock_start(void) {
    WSADATA data;
    winsock_code = WSAStartup(MAKEWORD(2, 2), &data);
    winsock_up = winsock_code == 0;
}

SN_HELD bool sn_winsock_start(void) {
    sn_once_call(&winsock_once, winsock_start);
    if (!winsock_up) {
        WSASetLastError(winsock_code); // why, for the caller's failure to read
    }

    return winsock_up;
}
#endif

// expire marks a direction's deadline passed and takes out the task
// waiting in its word, if one is, waking it to find the deadline passed.
// It runs on the thread that fires the deadline's timer when the
// deadline comes, and at once for a deadline set in the past.
SN_HELD_STATIC static void expire(poll_desc *pd, bool write) {
    atomic_store(write ? &pd->wd : &pd->rd, -1);
    _Atomic uintptr_t *word = write ? &pd->w : &pd->r;
    uintptr_t old = atomic_load(word);
    if (old > pd_ready && atomic_compare_exchange_strong(word, &old, pd_none)) {
        sn_handoff_wake(write ? &pd->wh : &pd->rh, (sn_task *)old);
        promise(-1);
    }
}

SN_HELD_STATIC static void expire_read(void *pd) { expire(pd, false); }

SN_HELD_STATIC static void expire_write(void *pd) { expire(pd, true); }

// passed reports whether fd's deadline for the direction has passed.
SN_HELD_STATIC static bool passed(int fd, bool write) {
    poll_desc *pd = pd_of(fd);
    return atomic_load(write ? &pd->wd : &pd->rd) < 0;
}

// watch registers a descriptor just made with the poller, a socket for
// both directions and a pipe's end or a child's pidfd for the one it
// has, dirs, its words and deadlines cleared of what an earlier
// descriptor with the same number left. On a failure it returns the
// name of the call that failed, and NULL otherwise.
SN_HELD_STATIC static const char *watch(int fd, int dirs) {
    if (sn_net_poller() < 0) { // made here when the main task makes a socket before the scheduler starts
#if defined(_WIN32)
        return "CreateIoCompletionPort";
#elif defined(__APPLE__)
        return "kqueue";
#else
        return "epoll_create1";
#endif
    }

    poll_desc *pd = pd_of(fd);
    atomic_store(&pd->r, pd_none);
    atomic_store(&pd->w, pd_none);
    atomic_store(&pd->rd, 0);
    atomic_store(&pd->wd, 0);
    atomic_store(&pd->rt.home,
                 0); // the wheel of the first deadline this socket arms, not the last socket's with its number
    atomic_store(&pd->wt.home, 0);
#if defined(_WIN32)
    // Nothing is registered: a wait arms a poll of the socket the driver
    // knows, its base provider's, under any layered provider, as every
    // poller over the driver asks for. A poll the last socket with the
    // number left armed completes on its own and frees itself.
    (void)dirs; // a pipe's end is not a socket, and is bound to the port by sn_net_watch_handle
    atomic_store(&pd->rpoll, NULL);
    atomic_store(&pd->wpoll, NULL);
    DWORD bytes = 0;
    SOCKET base = INVALID_SOCKET;
    if (WSAIoctl((SOCKET)fd, SIO_BASE_HANDLE, NULL, 0, &base, sizeof base, &bytes, NULL, NULL) != 0) {
        return "WSAIoctl";
    }

    pd->base = (HANDLE)base;
    return NULL;
#elif defined(__APPLE__)
    struct kevent ev[2];
    int n = 0;
    if ((dirs & SN_WATCH_READ) != 0) {
        EV_SET(&ev[n++], (uintptr_t)fd, EVFILT_READ, EV_ADD | EV_CLEAR, 0, 0, pd);
    }

    if ((dirs & SN_WATCH_WRITE) != 0) {
        EV_SET(&ev[n++], (uintptr_t)fd, EVFILT_WRITE, EV_ADD | EV_CLEAR, 0, 0, pd);
    }

    return kevent(poller, ev, n, NULL, 0, NULL) == 0 ? NULL : "kevent";
#else
    uint32_t events = EPOLLET;
    if ((dirs & SN_WATCH_READ) != 0) {
        events |= EPOLLIN | EPOLLRDHUP;
    }

    if ((dirs & SN_WATCH_WRITE) != 0) {
        events |= EPOLLOUT;
    }

    struct epoll_event ev = { .events = events, .data.ptr = pd };
    return epoll_ctl(poller, EPOLL_CTL_ADD, fd, &ev) == 0 ? NULL : "epoll_ctl";
#endif
}

static void unwatch(int fd);

SN_HELD int sn_net_watch(int fd, int dirs) { return watch(fd, dirs) == NULL ? 0 : last_error(); }

SN_HELD void sn_net_unwatch(int fd) { unwatch(fd); }

#if defined(__APPLE__) || defined(_WIN32)
// A child's exit record is never returned to the allocator, as a
// socket's is not: on Windows its completion may be in a worker's
// batch, read from the port before the record was let go, or posted to
// the port again by the keeper, and would name freed memory. A record
// let go is kept for the next child; a completion of the child before
// readies the record for the next, whose wait finds its child running
// and waits again. On macOS the record is readied by the signal thread
// alone, for a child on its list, and needs no poller.
typedef struct proc_desc {
    poll_desc pd;
    struct proc_desc *next;
} proc_desc;

static sn_mutex procs_mu = SN_MUTEX_INIT;
static proc_desc *procs_free; // under procs_mu

SN_HELD_STATIC static proc_desc *proc_desc_take(void) {
    sn_mutex_lock(&procs_mu);
    proc_desc *d = procs_free;
    if (d != NULL) {
        procs_free = d->next;
    }

    sn_mutex_unlock(&procs_mu);
    if (d == NULL) {
        d = calloc(1, sizeof *d);
        if (d == NULL) {
            sn_panic(SN_STR("out of memory", 13));
        }
    }

    atomic_store(&d->pd.r, pd_none); // an event of the child before, which no wait took
    return d;
}

// sn_net_watch_proc makes a child's record: on Windows the port must
// be there for the wait packet that readies it, with the record as the
// packet's context, and NULL says it cannot be; on macOS the signal
// thread readies it, and the record is always made.
SN_HELD void *sn_net_watch_proc(int64_t pid) {
    (void)pid;
#if defined(_WIN32)
    if (sn_net_poller() < 0) {
        return NULL;
    }
#endif
    return &proc_desc_take()->pd;
}

SN_HELD int sn_net_wait_proc(void *pd) { return wait_pd(pd, -1, false); }

// sn_net_ready_proc readies a child's record from the signal thread, as
// the poller readies a socket's: the task waiting on it, if one is, is
// taken out and woken, its promise kept by the waker, and else the
// record stays ready for the next wait.
SN_HELD void sn_net_ready_proc(void *pd) {
    poll_desc *d = pd;
    bool claimed = false;
    sn_task *t = ready(&d->r, &d->rh, &claimed);
    if (t != NULL) {
        if (claimed) {
            sn_wake_queue(t); // the second half of the wake, whose claim ready made
        }

        promise(-1);
    }
}

// sn_net_unwatch_proc lets a child's exit record go, to the next child,
// as the comment above says.
SN_HELD void sn_net_unwatch_proc(void *pd) {
    proc_desc *d = (proc_desc *)pd; // the record is the first field
    sn_mutex_lock(&procs_mu);
    d->next = procs_free;
    procs_free = d;
    sn_mutex_unlock(&procs_mu);
}
#endif

#if defined(_WIN32)
SN_HELD int sn_net_watch_handle(void *h) {
    if (sn_net_poller() < 0) {
        return sn_errno_of(ERROR_INVALID_HANDLE);
    }

    if (CreateIoCompletionPort(h, port, SN_KEY_PIPE, 0) == NULL ||
        !SetFileCompletionNotificationModes(h, FILE_SKIP_SET_EVENT_ON_HANDLE)) {
        return sn_errno_of(GetLastError());
    }

    return 0;
}

SN_HELD void *sn_net_pipe_op(void) {
    pipe_op *op = calloc(1, sizeof *op);
    if (op == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    return op;
}

SN_HELD void *sn_net_pipe_overlapped(void *op) { return &((pipe_op *)op)->ov; }

SN_HELD int sn_net_wait_op(void *op) { return wait_pd(&((pipe_op *)op)->pd, -1, false); }

SN_HELD void sn_net_wait_op_done(void *op) {
    poll_desc *pd = &((pipe_op *)op)->pd;
    uintptr_t self = (uintptr_t)sn_self();
    atomic_store_explicit(&pd->rh, SN_HANDOFF_OPEN, memory_order_relaxed);
    for (;;) {
        uintptr_t old = pd_ready;
        if (atomic_compare_exchange_strong(&pd->r, &old, pd_none)) {
            return; // the completion came
        }

        old = pd_none;
        if (!atomic_compare_exchange_strong(&pd->r, &old, self)) {
            continue;
        }

        promise(1);
        sn_park();
        uintptr_t mine = self;
        if (atomic_compare_exchange_strong(&pd->r, &mine, pd_none)) {
            promise(-1); // a wake for something else: the completion is still to come
            continue;
        }

        sn_handoff_wait(&pd->rh);
        return;
    }
}

SN_HELD void sn_net_pipe_op_free(void *op) { free(op); }
#endif

// wait_fd waits until fd may be ready to read, or to write: at once
// when an event came since the caller's call failed, and otherwise by
// parking until the poller wakes the task, or the direction's deadline
// does, when it returns SN_NET_TIMED_OUT, or a cancel of the task's
// scope does, when it returns what sn_net_cancelled says, before it
// parks too. The park may also end for an
// earlier wait's wake, which the scheduler keeps, so every caller tries
// its call again and waits again while the socket is not ready; the
// task takes itself out of the word before it returns, unless the
// poller or the deadline took it out first, and then waits for that
// waker to be done with it.
SN_HELD_STATIC static int wait_fd(int fd, bool write) { return wait_pd(pd_of(fd), fd, write); }

SN_HELD int sn_net_wait_fd(int fd, bool write) { return wait_fd(fd, write); }

// wait_pd is wait_fd on the descriptor's record, which a child's exit
// event on macOS has without a descriptor number; fd is the number the
// poll on Windows needs.
SN_HELD_STATIC static int wait_pd(poll_desc *pd, int fd, bool write) {
#if !defined(_WIN32)
    (void)fd;
#endif
    _Atomic uintptr_t *word = write ? &pd->w : &pd->r;
    _Atomic int64_t *dl = write ? &pd->wd : &pd->rd;
    sn_handoff *woken = write ? &pd->wh : &pd->rh;
    uintptr_t self = (uintptr_t)sn_self();
    // No waker is at the handoff: a direction has one task waiting at a
    // time, the last waker that took it out was waited for, and the
    // program cannot close a socket while a task waits on it, since a
    // Conn is held by one task. The compare-and-swap that puts the task
    // in the word orders this before any waker's look.
    atomic_store_explicit(woken, SN_HANDOFF_OPEN, memory_order_relaxed);
    for (;;) {
        if (atomic_load(dl) < 0) {
            return SN_NET_TIMED_OUT;
        }

        int cut = sn_net_cancelled();
        if (cut != 0) {
            return cut;
        }

        uintptr_t old = pd_ready;
        if (atomic_compare_exchange_strong(word, &old, pd_none)) {
            return 0;
        }

        promise(1);
        old = pd_none;
        if (atomic_compare_exchange_strong(word, &old, self)) {
            break;
        }

        promise(-1);
        if (old != pd_ready) {
            sn_panic(SN_STR("net: two tasks wait on one socket", 33));
        }
    }

#if defined(_WIN32)
    int failed =
        fd >= 0 ? arm_poll(fd, write) : 0; // a poll for the wake, which a completion of it brings; a child's
                                           // exit and a pipe's operation complete through the port on their own
    if (failed != 0) {
        uintptr_t me = self;
        if (atomic_compare_exchange_strong(word, &me, pd_none)) {
            promise(-1);
        } else {
            sn_handoff_wait(woken);
        }

        return failed;
    }
#endif
    if (atomic_load(dl) >= 0) { // a deadline marked after the look above sees the task in the word and wakes it
        sn_park();
    }

    uintptr_t mine = self;
    if (atomic_compare_exchange_strong(word, &mine, pd_none)) {
        promise(-1);
    } else {
        sn_handoff_wait(woken);
    }

    return atomic_load(dl) < 0 ? SN_NET_TIMED_OUT : sn_net_cancelled();
}

// sn_net_cancelled is a wait's code for the state of the running task's
// scope.
SN_HELD int sn_net_cancelled(void) {
    switch (sn_cancel_wait_state(sn_self())) {
    case SN_SCOPE_TIMED_OUT:
        return SN_NET_TIMED_OUT;
    case SN_SCOPE_CANCELLED:
        return SN_NET_CANCELLED;
    default:
        return 0;
    }
}

// sn_net_setDeadline sets when fd's reads, or its writes, stop waiting:
// d nanoseconds from now, at once for a d of zero or less, and never
// when set is 0. Setting it again moves it, and a deadline that passed
// no longer holds once it is set again, as in Go. The old timer is taken
// out before the new one is marked armed, so that it cannot fire after
// and mark the new deadline passed.
SN_HELD int64_t sn_net_setDeadline(int64_t fd, int64_t write, int64_t d, int64_t set) {
    poll_desc *pd = pd_of((int)fd);
    _Atomic int64_t *dl = write ? &pd->wd : &pd->rd;
    sn_deadline *at = write ? &pd->wt : &pd->rt;
    sn_deadline_disarm(at);
    if (set == 0) {
        atomic_store(dl, 0);
    } else if (d <= 0) {
        expire(pd, write != 0);
    } else {
        atomic_store(dl, 1);
        sn_deadline_arm(at, d, write ? expire_write : expire_read, pd);
    }

    return 0;
}

// unwatch forgets a socket that is being closed, whose registration the
// system drops with the close. A task still waiting on it, which only a
// program closing a socket another task uses could leave, is woken to
// find it closed.
SN_HELD_STATIC static void unwatch(int fd) {
    poll_desc *pd = pd_of(fd);
#if defined(_WIN32)
    cancel_polls(pd);
#endif
    sn_deadline_disarm(&pd->rt); // a timer of this socket's must not fire on the next one with its number
    sn_deadline_disarm(&pd->wt);
    atomic_store(&pd->rd, 0);
    atomic_store(&pd->wd, 0);
    _Atomic uintptr_t *words[2] = { &pd->r, &pd->w };
    sn_handoff *woken[2] = { &pd->rh, &pd->wh };
    for (int i = 0; i < 2; i++) {
        uintptr_t old = atomic_exchange(words[i], pd_none);
        if (old > pd_ready) {
            sn_handoff_wake(woken[i], (sn_task *)old);
            promise(-1);
        }
    }
}

// fail closes s, when there is one, and returns the error of the call
// named, storing the name for the library's message, "bind: address
// already in use".
SN_HELD_STATIC static int64_t fail(int s, const char *name, sn_string *call) {
    int code = last_error();
    if (s >= 0) {
        unwatch(s);
        CLOSE_SOCKET(s);
    }

    *call = SN_STR(name, (int64_t)strlen(name));
    return code;
}

// port_of returns the port of a socket address of either family.
SN_HELD_STATIC static int64_t port_of(const struct sockaddr_storage *a) {
    return ntohs(a->ss_family == AF_INET6 ? ((const struct sockaddr_in6 *)a)->sin6_port
                                          : ((const struct sockaddr_in *)a)->sin_port);
}

// listen_on opens a socket listening on addr and stores its descriptor;
// dual makes a socket on the IPv6 address :: take IPv4 connections too,
// their addresses mapped into IPv6, as Go makes every listener on a
// wildcard address, which some systems do not by default. A failure
// returns the error number and stores the call that failed.
SN_HELD_STATIC static int64_t listen_on(const struct sockaddr_storage *addr, socklen_t len, bool dual, int64_t *fd,
                                        sn_string *call) {
    int s = NEW_SOCKET(addr->ss_family);
    if (s < 0) {
        return fail(s, "socket", call);
    }

#if !defined(_WIN32) // where a second socket may bind the address a first one holds, which is no listener's wish
    int yes = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof yes);
#endif
    int no = 0;
    if (dual && setsockopt(s, IPPROTO_IPV6, IPV6_V6ONLY, (const void *)&no, sizeof no) != 0) {
        return fail(s, "setsockopt", call);
    }

    if (bind(s, (const struct sockaddr *)addr, len) != 0) {
        return fail(s, "bind", call);
    }

    if (listen(s, backlog()) != 0) {
        return fail(s, "listen", call);
    }

    const char *failed = prepare(s);
    if (failed == NULL) {
        failed = watch(s, SN_WATCH_READ | SN_WATCH_WRITE);
    }

    if (failed != NULL) {
        return fail(s, failed, call);
    }

    *fd = s;
    return 0;
}

// sn_net_listen opens a TCP socket listening on the IP address host at
// the port, 0 for one the system picks, and stores the descriptor and
// the port it listens on; a failure stores the call that failed. A
// wildcard address listens on every address of the machine: "0.0.0.0"
// on IPv4's, "::" on both families', and an empty host on both where
// the system has IPv6 and on IPv4's where it has not, as Go's empty
// host. A host that is not an IP address is SN_NET_NOT_ADDRESS, a name
// the library looks up; the port is in range.
SN_HELD int64_t sn_net_listen(sn_string host, int64_t port, int64_t *fd, int64_t *got, sn_string *call) {
    bool every = host.len == 0;
    struct sockaddr_storage addr;
    socklen_t len = (socklen_t)sn_net_parse(every ? SN_STR("::", 2) : host, port, &addr);
    if (len == 0) {
        return SN_NET_NOT_ADDRESS;
    }

    bool dual = addr.ss_family == AF_INET6 && IN6_IS_ADDR_UNSPECIFIED(&((struct sockaddr_in6 *)&addr)->sin6_addr);
    int64_t code = listen_on(&addr, len, dual, fd, call);
    if (every && (code == EAFNOSUPPORT || code == EPROTONOSUPPORT || code == EADDRNOTAVAIL)) { // a system without IPv6
        len = (socklen_t)sn_net_parse(SN_STR("0.0.0.0", 7), port, &addr);
        code = listen_on(&addr, len, false, fd, call);
    }

    if (code != 0) {
        return code;
    }

    len = sizeof addr;
    if (getsockname((int)*fd, (struct sockaddr *)&addr, &len) != 0) {
        return fail((int)*fd, "getsockname", call);
    }

    *got = port_of(&addr);
    return 0;
}

// sn_net_accept waits for a connection on the listening socket and
// stores its descriptor. A connection its client reset before it was
// accepted is skipped, as Go's runtime skips it, for the next one: on
// Darwin it is accepted and then refuses its socket options, elsewhere
// accept reports it aborted. Either is the client's failure, and a
// server that stopped on it could be stopped by any client.
SN_HELD int64_t sn_net_accept(int64_t fd, int64_t *out) {
    for (;;) {
        if (passed((int)fd, false)) {
            return SN_NET_TIMED_OUT;
        }

        int c = ACCEPT_SOCKET((int)fd);
        if (c >= 0) {
            if (connected(c) != NULL) {
                CLOSE_SOCKET(c);
                continue;
            }

            if (watch(c, SN_WATCH_READ | SN_WATCH_WRITE) != NULL) {
                int code = last_error();
                CLOSE_SOCKET(c);
                return code;
            }

            *out = c;
            return 0;
        }

        int code = last_error();
        if (code == ECONNABORTED || code == EINTR) {
            continue;
        }

        if (code != EAGAIN && code != EWOULDBLOCK) {
            return code;
        }

        int waited = wait_fd((int)fd, false);
        if (waited != 0) {
            return waited;
        }
    }
}

// sn_net_dial connects to the IP address host at the port and stores
// the connection's descriptor, or on a failure the call that failed. A
// host that is not an IP address is SN_NET_NOT_ADDRESS, a name the
// library looks up; a connection not made within timeout nanoseconds,
// when it is above zero, is SN_NET_TIMED_OUT, the deadline held on the
// socket's writes while it connects and cleared once it has. A connection in progress is checked
// again after every wake, as Go checks it: a wake can come for another
// wait, so a socket with no error yet is connected only once it has a
// peer.
SN_HELD int64_t sn_net_dial(sn_string host, int64_t port, int64_t timeout, int64_t *fd, sn_string *call) {
    struct sockaddr_storage addr;
    socklen_t addr_len = (socklen_t)sn_net_parse(host, port, &addr);
    if (addr_len == 0) {
        return SN_NET_NOT_ADDRESS;
    }

    int s = NEW_SOCKET(addr.ss_family);
    if (s < 0) {
        return fail(s, "socket", call);
    }

    const char *failed = connected(s);
    if (failed == NULL) {
        failed = watch(s, SN_WATCH_READ | SN_WATCH_WRITE);
    }

    if (failed != NULL) {
        return fail(s, failed, call);
    }

    if (timeout > 0) {
        sn_net_setDeadline(s, 1, timeout, 1);
    }

    if (connect(s, (struct sockaddr *)&addr, addr_len) != 0) {
#if defined(_WIN32)
        if (last_error() != EWOULDBLOCK) { // Windows' word for a connection in progress
#else
        if (last_error() != EINPROGRESS && last_error() != EINTR) {
#endif
            return fail(s, "connect", call);
        }

        for (;;) {
            int waited = wait_fd(s, true);
            if (waited != 0) {
                unwatch(s);
                CLOSE_SOCKET(s);
                return waited;
            }

            int err = 0;
            socklen_t len = sizeof err;
            if (getsockopt(s, SOL_SOCKET, SO_ERROR, (void *)&err, &len) != 0) {
                return fail(s, "getsockopt", call);
            }

            int code = err; // the system's own, which fail reads again
            err = SOCKET_ERROR_OF(err);

            if (err == EINPROGRESS || err == EALREADY || err == EINTR) {
                continue;
            }

            if (err != 0 && err != EISCONN) {
#if defined(_WIN32)
                WSASetLastError(code);
#else
                errno = code;
#endif
                return fail(s, "connect", call);
            }

            struct sockaddr_storage peer;
            socklen_t plen = sizeof peer;
            if (err == EISCONN || getpeername(s, (struct sockaddr *)&peer, &plen) == 0) {
                break;
            }
        }
    }

    if (timeout > 0) {
        sn_net_setDeadline(s, 1, 0, 0); // the timeout was the dial's: the connection has none
    }

    *fd = s;
    return 0;
}

// sn_net_addrs stores the local and the remote address of a socket,
// as Go prints them, "127.0.0.1:8080" or "[::1]:8080", the remote one
// empty when it has no peer, as a listening socket.
SN_HELD int64_t sn_net_addrs(int64_t fd, sn_string *local, sn_string *remote) {
    struct sockaddr_storage a;
    socklen_t len = sizeof a;
    if (getsockname((int)fd, (struct sockaddr *)&a, &len) != 0) {
        return last_error();
    }

    *local = sn_net_format((struct sockaddr *)&a, true);
    len = sizeof a;
    *remote = getpeername((int)fd, (struct sockaddr *)&a, &len) == 0 ? sn_net_format((struct sockaddr *)&a, true)
                                                                     : SN_STR("", 0);
    return 0;
}

// sn_net_read reads what the connection has into buf and stores how
// much, 0 once the other side has closed, or for a buf of no bytes. A
// read a signal interrupts is asked again, as the file reads are.
SN_HELD int64_t sn_net_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    if (buf->len == 0) {
        *n = 0;
        return 0;
    }

    for (;;) {
        if (passed((int)fd, false)) { // as Go's: a passed deadline fails the read even with bytes waiting
            return SN_NET_TIMED_OUT;
        }

        int64_t got = READ_SOCKET((int)fd, buf->ptr, (size_t)buf->len);
        if (got >= 0) {
            *n = got;
            return 0;
        }

        if (last_error() == EINTR) {
            continue;
        }

        if (last_error() != EAGAIN && last_error() != EWOULDBLOCK) {
            return last_error();
        }

        int waited = wait_fd((int)fd, false);
        if (waited != 0) {
            return waited;
        }
    }
}

// sn_net_write writes all of buf to the connection and stores how
// much it wrote: the length of buf, or on a failure what went before
// it, which the library's Error carries as Go's Write returns it.
SN_HELD int64_t sn_net_write(int64_t fd, sn_slice_u8 buf, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    int64_t done = 0;
    *n = 0;
    while (done < buf.len) {
        if (passed((int)fd, true)) {
            *n = done;
            return SN_NET_TIMED_OUT;
        }

        int64_t put = SEND_SOCKET((int)fd, buf.ptr + done, (size_t)(buf.len - done));
        if (put >= 0) {
            done += put;
            continue;
        }

        if (last_error() == EINTR) {
            continue;
        }

        if (last_error() != EAGAIN && last_error() != EWOULDBLOCK) {
            *n = done;
            return last_error();
        }

        int waited = wait_fd((int)fd, true);
        if (waited != 0) {
            *n = done;
            return waited;
        }
    }

    *n = done;
    return 0;
}

// sn_net_closeConn closes a socket, a connection or a listener, and
// forgets what the poller still promised for it.
SN_HELD int64_t sn_net_closeConn(int64_t fd) {
    unwatch((int)fd);
    return CLOSE_SOCKET((int)fd) == 0 ? 0 : last_error();
}
