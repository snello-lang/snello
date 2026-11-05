// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Sockets and the poller, as _docs/design/06-tasks-and-channels.md
// promises: a program reads and writes a connection as blocking code
// and runs as event-driven code. Every socket is non-blocking and is
// registered with the system's poller, kqueue or epoll, once, when it
// is made; an operation that would block parks the task until the
// poller says the socket changed, and one thread waits on the poller
// and wakes the task the event is for, which retries. A write to a
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

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <sys/event.h>
#include <sys/sysctl.h>
#else
#include <sys/epoll.h>
#endif

// last_error reads errno through a call the compiler can neither
// inline nor merge: glibc declares the function behind errno const, so
// a loop that parks between two reads could otherwise read the errno of
// the thread the task left (sched.c says why a task may move).
static __attribute__((noinline)) int last_error(void) {
    __asm__ volatile("" ::: "memory");
    return errno;
}

// SN_SOCK_FLAGS makes a socket non-blocking and closed on exec as it is
// made, where the system can: Linux takes both as flags to socket and
// accept4, as Go asks for them, so a socket is never open across an exec
// for a moment and costs no further call. Darwin has neither flag, and
// prepare sets both with fcntl, as Go does there.
#if defined(SOCK_NONBLOCK) && defined(SOCK_CLOEXEC)
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
static const char *prepare(int fd) {
#if defined(__APPLE__)
    int yes = 1;
    if (setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof yes) != 0) {
        return "setsockopt";
    }
#endif
#if SN_SOCK_FLAGS == 0
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
static const char *connected(int fd) {
    const char *failed = prepare(fd);
    if (failed != NULL) {
        return failed;
    }

    int yes = 1;
    return setsockopt(fd, IPPROTO_TCP, TCP_NODELAY, &yes, sizeof yes) == 0 ? NULL : "setsockopt";
}

// backlog is how many connections a listening socket keeps waiting for
// Accept: the system's largest, as Go asks for, read once.
static int backlog(void) {
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
}

#if defined(MSG_NOSIGNAL)
#define SN_NOSIGNAL MSG_NOSIGNAL
#else
#define SN_NOSIGNAL 0
#endif

static pthread_mutex_t poller_mu = PTHREAD_MUTEX_INITIALIZER;
static int poller = -1;

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
// by the poller, which wakes it and then keeps the promise, as the
// timer thread does, or by the task itself, after a wake that came for
// something else.
//
// Each direction also has its deadline, as Go's pollDesc has rd and wd:
// 0 for none, -1 once it has passed, and above zero while its timer is
// in the timers' heap, kept at rt or wt. The timer fires on the timers'
// thread: it marks the deadline passed and takes out the task waiting,
// if one is, which finds it passed. A task puts itself in its word and
// then looks at the deadline, and the timer marks the deadline and then
// looks at the word, both sequentially consistent, so one of the two
// sees the other and no task waits past its deadline.
enum { pd_none = 0, pd_ready = 1 };

// pd_timeout is what a wait, and the calls below, return once the
// deadline has passed: no error number, so that the library tells it
// from the system's own ETIMEDOUT, which Go words differently.
enum { pd_timeout = -2 };

typedef struct {
    _Atomic uintptr_t r;
    _Atomic uintptr_t w;
    _Atomic int64_t rd;
    _Atomic int64_t wd;
    int64_t rt; // under the timers' lock
    int64_t wt;
} poll_desc;

// The descriptors are found by number in a table that never moves, so a
// wait finds its own without a lock: chunks of 4096, for descriptor
// numbers below 2^24, each made when a socket first needs it and kept,
// so an event still on its way to a socket since closed names memory
// that is there.
enum { pd_chunk = 4096, pd_chunks = 4096 };

static _Atomic(poll_desc *) pds[pd_chunks];

static poll_desc *pd_of(int fd) {
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

// ready marks a word ready, or wakes the task waiting in it and keeps
// the promise: the task is on a queue before the count goes down, so an
// idle worker looking at that moment finds work and not a program in
// which nothing can run. The woken task tries its call again, which
// finds what the event was for.
static void ready(_Atomic uintptr_t *word) {
    uintptr_t old = atomic_load_explicit(word, memory_order_acquire);
    for (;;) {
        if (old == pd_ready) {
            return;
        }

        uintptr_t next = old == pd_none ? pd_ready : pd_none;
        if (atomic_compare_exchange_weak_explicit(word, &old, next, memory_order_acq_rel, memory_order_acquire)) {
            break;
        }
    }

    if (old != pd_none) {
        sn_wake((sn_task *)old);
        sn_outside(-1);
    }
}

// poll_main waits on the poller and passes each event to the word of
// its socket it is for.
static void *poll_main(void *arg) {
    (void)arg;
    for (;;) {
#if defined(__APPLE__)
        struct kevent events[64];
        int n = kevent(poller, NULL, 0, events, 64, NULL);
        for (int i = 0; i < n; i++) {
            poll_desc *pd = events[i].udata;
            ready(events[i].filter == EVFILT_WRITE ? &pd->w : &pd->r);
        }
#else
        struct epoll_event events[64];
        int n = epoll_wait(poller, events, 64, -1);
        for (int i = 0; i < n; i++) {
            poll_desc *pd = events[i].data.ptr;
            uint32_t e = events[i].events;
            if (e & (EPOLLIN | EPOLLRDHUP | EPOLLHUP | EPOLLERR)) {
                ready(&pd->r);
            }

            if (e & (EPOLLOUT | EPOLLHUP | EPOLLERR)) {
                ready(&pd->w);
            }
        }
#endif
    }

    return NULL;
}

// poller_start makes the poller and its thread once.
static void poller_start(void) {
    pthread_mutex_lock(&poller_mu);
    if (poller < 0) {
#if defined(__APPLE__)
        poller = kqueue();
#else
        poller = epoll_create1(0);
#endif
        if (poller < 0) {
            sn_panic(SN_STR("cannot make the poller", 22));
        }

        pthread_t id;
        pthread_attr_t attr;
        pthread_attr_init(&attr);
        pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
        if (pthread_create(&id, &attr, poll_main, NULL) != 0) {
            sn_panic(SN_STR("cannot start the poller", 23));
        }

        pthread_attr_destroy(&attr);
    }

    pthread_mutex_unlock(&poller_mu);
}

// expire marks a direction's deadline passed and takes out the task
// waiting in its word, if one is, waking it to find the deadline passed.
// It runs on the timers' thread when the deadline comes, and at once for
// a deadline set in the past.
static void expire(poll_desc *pd, bool write) {
    atomic_store(write ? &pd->wd : &pd->rd, -1);
    _Atomic uintptr_t *word = write ? &pd->w : &pd->r;
    uintptr_t old = atomic_load(word);
    if (old > pd_ready && atomic_compare_exchange_strong(word, &old, pd_none)) {
        sn_wake((sn_task *)old);
        sn_outside(-1);
    }
}

static void expire_read(void *pd) {
    expire(pd, false);
}

static void expire_write(void *pd) {
    expire(pd, true);
}

// passed reports whether fd's deadline for the direction has passed.
static bool passed(int fd, bool write) {
    poll_desc *pd = pd_of(fd);
    return atomic_load(write ? &pd->wd : &pd->rd) < 0;
}

// watch registers a socket just made with the poller, for both
// directions, its words and deadlines cleared of what an earlier socket
// with the same number left. On a failure it returns the name of the
// call that failed, and NULL otherwise.
static const char *watch(int fd) {
    if (poller < 0) {
        poller_start();
    }

    poll_desc *pd = pd_of(fd);
    atomic_store(&pd->r, pd_none);
    atomic_store(&pd->w, pd_none);
    atomic_store(&pd->rd, 0);
    atomic_store(&pd->wd, 0);
#if defined(__APPLE__)
    struct kevent ev[2];
    EV_SET(&ev[0], (uintptr_t)fd, EVFILT_READ, EV_ADD | EV_CLEAR, 0, 0, pd);
    EV_SET(&ev[1], (uintptr_t)fd, EVFILT_WRITE, EV_ADD | EV_CLEAR, 0, 0, pd);
    return kevent(poller, ev, 2, NULL, 0, NULL) == 0 ? NULL : "kevent";
#else
    struct epoll_event ev = {.events = EPOLLIN | EPOLLOUT | EPOLLRDHUP | EPOLLET, .data.ptr = pd};
    return epoll_ctl(poller, EPOLL_CTL_ADD, fd, &ev) == 0 ? NULL : "epoll_ctl";
#endif
}

// wait_fd waits until fd may be ready to read, or to write: at once
// when an event came since the caller's call failed, and otherwise by
// parking until the poller wakes the task, or the direction's deadline
// does, when it returns pd_timeout. The park may also end for an earlier
// wait's wake, which the scheduler keeps, so every caller tries its call
// again and waits again while the socket is not ready; the task takes
// itself out of the word before it returns, unless the poller or the
// deadline took it out first.
static int wait_fd(int fd, bool write) {
    poll_desc *pd = pd_of(fd);
    _Atomic uintptr_t *word = write ? &pd->w : &pd->r;
    _Atomic int64_t *dl = write ? &pd->wd : &pd->rd;
    uintptr_t self = (uintptr_t)sn_self();
    for (;;) {
        if (atomic_load(dl) < 0) {
            return pd_timeout;
        }

        uintptr_t old = pd_ready;
        if (atomic_compare_exchange_strong(word, &old, pd_none)) {
            return 0;
        }

        sn_outside(1);
        old = pd_none;
        if (atomic_compare_exchange_strong(word, &old, self)) {
            break;
        }

        sn_outside(-1);
        if (old != pd_ready) {
            sn_panic(SN_STR("net: two tasks wait on one socket", 33));
        }
    }

    if (atomic_load(dl) >= 0) { // a deadline marked after the look above sees the task in the word and wakes it
        sn_park();
    }

    uintptr_t mine = self;
    if (atomic_compare_exchange_strong(word, &mine, pd_none)) {
        sn_outside(-1);
    }

    return atomic_load(dl) < 0 ? pd_timeout : 0;
}

// sn_net_setDeadline sets when fd's reads, or its writes, stop waiting:
// d nanoseconds from now, at once for a d of zero or less, and never
// when set is 0. Setting it again moves it, and a deadline that passed
// no longer holds once it is set again, as in Go. The old timer is taken
// out before the new one is marked armed, so that it cannot fire after
// and mark the new deadline passed.
int64_t sn_net_setDeadline(int64_t fd, int64_t write, int64_t d, int64_t set) {
    poll_desc *pd = pd_of((int)fd);
    _Atomic int64_t *dl = write ? &pd->wd : &pd->rd;
    int64_t *at = write ? &pd->wt : &pd->rt;
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
static void unwatch(int fd) {
    poll_desc *pd = pd_of(fd);
    sn_deadline_disarm(&pd->rt); // a timer of this socket's must not fire on the next one with its number
    sn_deadline_disarm(&pd->wt);
    atomic_store(&pd->rd, 0);
    atomic_store(&pd->wd, 0);
    _Atomic uintptr_t *words[2] = {&pd->r, &pd->w};
    for (int i = 0; i < 2; i++) {
        uintptr_t old = atomic_exchange(words[i], pd_none);
        if (old > pd_ready) {
            sn_wake((sn_task *)old);
            sn_outside(-1);
        }
    }
}

// ipv4 reads the IPv4 address host, "127.0.0.1", into addr.
static bool ipv4(sn_string host, struct in_addr *addr) {
    char text[64];
    if (host.len >= (int64_t)sizeof text) {
        return false;
    }

    memcpy(text, host.ptr, (size_t)host.len);
    text[host.len] = '\0';
    return inet_pton(AF_INET, text, addr) == 1;
}

// fail closes s, when there is one, and returns the error of the call
// named, storing the name for the library's message, "bind: address
// already in use".
static int64_t fail(int s, const char *name, sn_string *call) {
    int code = last_error();
    if (s >= 0) {
        unwatch(s);
        close(s);
    }

    *call = SN_STR(name, (int64_t)strlen(name));
    return code;
}

// sn_net_listen opens a TCP socket listening on the IPv4 address host,
// "0.0.0.0" for every address of the machine, at the port, 0 for one
// the system picks, and stores the descriptor and the port it listens
// on; a failure stores the call that failed. A host that is not an
// IPv4 address is -1, which the library words; the port is in range.
int64_t sn_net_listen(sn_string host, int64_t port, int64_t *fd, int64_t *got, sn_string *call) {
    struct sockaddr_in addr = {.sin_family = AF_INET, .sin_port = htons((uint16_t)port)};
    if (!ipv4(host, &addr.sin_addr)) {
        return -1;
    }

    int s = socket(AF_INET, SOCK_STREAM | SN_SOCK_FLAGS, 0);
    if (s < 0) {
        return fail(s, "socket", call);
    }

    int yes = 1;
    setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof yes);
    socklen_t len = sizeof addr;
    if (bind(s, (struct sockaddr *)&addr, len) != 0) {
        return fail(s, "bind", call);
    }

    if (listen(s, backlog()) != 0) {
        return fail(s, "listen", call);
    }

    const char *failed = prepare(s);
    if (failed == NULL) {
        failed = watch(s);
    }

    if (failed != NULL) {
        return fail(s, failed, call);
    }

    if (getsockname(s, (struct sockaddr *)&addr, &len) != 0) {
        return fail(s, "getsockname", call);
    }

    *fd = s;
    *got = ntohs(addr.sin_port);
    return 0;
}

// sn_net_accept waits for a connection on the listening socket and
// stores its descriptor. A connection its client reset before it was
// accepted is skipped, as Go's runtime skips it, for the next one: on
// Darwin it is accepted and then refuses its socket options, elsewhere
// accept reports it aborted. Either is the client's failure, and a
// server that stopped on it could be stopped by any client.
int64_t sn_net_accept(int64_t fd, int64_t *out) {
    for (;;) {
        if (passed((int)fd, false)) {
            return pd_timeout;
        }

#if SN_SOCK_FLAGS != 0
        int c = accept4((int)fd, NULL, NULL, SN_SOCK_FLAGS);
#else
        int c = accept((int)fd, NULL, NULL);
#endif
        if (c >= 0) {
            if (connected(c) != NULL) {
                close(c);
                continue;
            }

            if (watch(c) != NULL) {
                int code = last_error();
                close(c);
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

        if (wait_fd((int)fd, false) == pd_timeout) {
            return pd_timeout;
        }
    }
}

// sn_net_dial connects to the IPv4 address host at the port and stores
// the connection's descriptor, or on a failure the call that failed. A
// host that is not an IPv4 address is -1, which the library words; a
// connection not made within timeout nanoseconds, when it is above
// zero, is pd_timeout, the deadline held on the socket's writes while it
// connects and cleared once it has. A connection in progress is checked
// again after every wake, as Go checks it: a wake can come for another
// wait, so a socket with no error yet is connected only once it has a
// peer.
int64_t sn_net_dial(sn_string host, int64_t port, int64_t timeout, int64_t *fd, sn_string *call) {
    struct sockaddr_in addr = {.sin_family = AF_INET, .sin_port = htons((uint16_t)port)};
    if (!ipv4(host, &addr.sin_addr)) {
        return -1;
    }

    int s = socket(AF_INET, SOCK_STREAM | SN_SOCK_FLAGS, 0);
    if (s < 0) {
        return fail(s, "socket", call);
    }

    const char *failed = connected(s);
    if (failed == NULL) {
        failed = watch(s);
    }

    if (failed != NULL) {
        return fail(s, failed, call);
    }

    if (timeout > 0) {
        sn_net_setDeadline(s, 1, timeout, 1);
    }

    if (connect(s, (struct sockaddr *)&addr, sizeof addr) != 0) {
        if (last_error() != EINPROGRESS && last_error() != EINTR) {
            return fail(s, "connect", call);
        }

        for (;;) {
            if (wait_fd(s, true) == pd_timeout) {
                unwatch(s);
                close(s);
                return pd_timeout;
            }

            int err = 0;
            socklen_t len = sizeof err;
            if (getsockopt(s, SOL_SOCKET, SO_ERROR, &err, &len) != 0) {
                return fail(s, "getsockopt", call);
            }

            if (err == EINPROGRESS || err == EALREADY || err == EINTR) {
                continue;
            }

            if (err != 0 && err != EISCONN) {
                errno = err;
                return fail(s, "connect", call);
            }

            struct sockaddr_in peer;
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

// address formats the IPv4 address and port of a socket as Go prints
// them, "127.0.0.1:8080", as an owned string.
static sn_string address(const struct sockaddr_in *a) {
    char ip[INET_ADDRSTRLEN];
    if (inet_ntop(AF_INET, &a->sin_addr, ip, sizeof ip) == NULL) {
        return SN_STR("", 0);
    }

    char text[INET_ADDRSTRLEN + 8];
    int n = snprintf(text, sizeof text, "%s:%d", ip, ntohs(a->sin_port));
    uint8_t *p = sn_alloc((size_t)n);
    memcpy(p, text, (size_t)n);
    return SN_OWNED(p, n);
}

// sn_net_addrs stores the local and the remote address of a socket,
// the remote one empty when it has no peer, as a listening socket.
int64_t sn_net_addrs(int64_t fd, sn_string *local, sn_string *remote) {
    struct sockaddr_in a;
    socklen_t len = sizeof a;
    if (getsockname((int)fd, (struct sockaddr *)&a, &len) != 0) {
        return last_error();
    }

    *local = address(&a);
    len = sizeof a;
    *remote = getpeername((int)fd, (struct sockaddr *)&a, &len) == 0 ? address(&a) : SN_STR("", 0);
    return 0;
}

// sn_net_read reads what the connection has into buf and stores how
// much, 0 once the other side has closed, or for a buf of no bytes. A
// read a signal interrupts is asked again, as the file reads are.
int64_t sn_net_read(int64_t fd, sn_slice_u8 *buf, int64_t *n) {
    sn_check_slice((sn_slice *)buf);
    if (buf->len == 0) {
        *n = 0;
        return 0;
    }

    for (;;) {
        if (passed((int)fd, false)) { // as Go's: a passed deadline fails the read even with bytes waiting
            return pd_timeout;
        }

        ssize_t got = read((int)fd, buf->ptr, (size_t)buf->len);
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

        if (wait_fd((int)fd, false) == pd_timeout) {
            return pd_timeout;
        }
    }
}

// sn_net_write writes all of buf to the connection and stores how
// much it wrote: the length of buf, or on a failure what went before
// it, which the library's Error carries as Go's Write returns it.
int64_t sn_net_write(int64_t fd, sn_slice_u8 buf, int64_t *n) {
    sn_check_slice((sn_slice *)&buf);
    int64_t done = 0;
    *n = 0;
    while (done < buf.len) {
        if (passed((int)fd, true)) {
            *n = done;
            return pd_timeout;
        }

        ssize_t put = send((int)fd, buf.ptr + done, (size_t)(buf.len - done), SN_NOSIGNAL);
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

        if (wait_fd((int)fd, true) == pd_timeout) {
            *n = done;
            return pd_timeout;
        }
    }

    *n = done;
    return 0;
}

// sn_net_closeConn closes a socket, a connection or a listener, and
// forgets what the poller still promised for it.
int64_t sn_net_closeConn(int64_t fd) {
    unwatch((int)fd);
    return close((int)fd) == 0 ? 0 : last_error();
}
