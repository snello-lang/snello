// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Sockets and the poller, as _docs/design/06-tasks-and-channels.md
// promises: a program reads and writes a connection as blocking code
// and runs as event-driven code. Every socket is non-blocking; an
// operation that would block registers the descriptor with the
// system's poller, kqueue or epoll, for one event, and parks the task;
// one thread waits on the poller and wakes the task the event names,
// which retries. A write to a connection the other side closed fails
// with EPIPE rather than raising SIGPIPE, per socket, so that the rest
// of the program, its pipes included, keeps the system's behavior.
// These are the system calls the net package declares
// without a body, under the names the compiler gives them: each
// returns the system's error number, zero on success, and stores its
// result through its last parameter, as the os functions do.

#define _DEFAULT_SOURCE // as sched.c: the socket calls under -std=c11
#define _DARWIN_C_SOURCE

#include "teak.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <pthread.h>
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

// prepare makes a socket non-blocking and closed on exec, and keeps a
// write to a connection the other side closed from raising SIGPIPE,
// which would end the program: the write fails with EPIPE instead, as
// an error the library returns. Linux says so per write, with
// MSG_NOSIGNAL; Darwin per socket. On a failure it returns the name of
// the call that failed, for the library's message, and NULL otherwise.
static const char *prepare(int fd) {
#if defined(__APPLE__)
    int yes = 1;
    if (setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &yes, sizeof yes) != 0) {
        return "setsockopt";
    }
#endif
    if (fcntl(fd, F_SETFD, FD_CLOEXEC) != 0) {
        return "fcntl";
    }

    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) != 0) {
        return "fcntl";
    }

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
    FILE *f = fopen("/proc/sys/net/core/somaxconn", "r");
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
#define TK_NOSIGNAL MSG_NOSIGNAL
#else
#define TK_NOSIGNAL 0
#endif

static pthread_mutex_t poller_mu = PTHREAD_MUTEX_INITIALIZER;
static int poller = -1;

// A waiter is the slot a descriptor's events wake through: the task
// waiting on it, or nothing. An event names the slot, not the task, and
// the task empties the slot before it leaves its wait, under the slot's
// lock, which the poller wakes under: so an event that arrives after a
// wait ended, a park cut short by another wake, finds no task, where
// naming the task would wake one that may be gone. The slot also holds
// whether its descriptor is registered with the poller, a wake promised
// from outside the workers that the deadlock detector counts: the
// promise is made once per registration and kept once per event, so a
// wait renewed before its event fired, which replaces the registration
// and so brings one event for two waits, promises nothing twice. The
// slots live as long as the program, one per descriptor number.
typedef struct {
    pthread_mutex_t mu;
    tk_task *t;
    bool armed;
} waiter;

static waiter **waiters;
static int nwaiters;

static waiter *waiter_of(int fd) {
    pthread_mutex_lock(&poller_mu);
    if (fd >= nwaiters) {
        int n = nwaiters == 0 ? 64 : nwaiters;
        while (n <= fd) {
            n *= 2;
        }

        waiter **more = realloc(waiters, (size_t)n * sizeof *more);
        if (more == NULL) {
            tk_panic(TK_STR("out of memory", 13));
        }

        memset(more + nwaiters, 0, (size_t)(n - nwaiters) * sizeof *more);
        waiters = more;
        nwaiters = n;
    }

    if (waiters[fd] == NULL) {
        waiter *w = calloc(1, sizeof *w);
        if (w == NULL) {
            tk_panic(TK_STR("out of memory", 13));
        }

        pthread_mutex_init(&w->mu, NULL);
        waiters[fd] = w;
    }

    waiter *w = waiters[fd];
    pthread_mutex_unlock(&poller_mu);
    return w;
}

// disarm keeps the promise of w's registration, once the event came or
// once the descriptor closed and no event can come; the caller holds
// w->mu.
static void disarm(waiter *w) {
    if (w->armed) {
        w->armed = false;
        tk_outside(-1);
    }
}

// wake wakes the task waiting in w, if one still is, and then spends
// the registration the event came for: the task is on a queue before
// the promise is kept, as the timer thread wakes before it counts down,
// so an idle worker looking at that moment finds work and not a program
// in which nothing can run.
static void wake(waiter *w) {
    pthread_mutex_lock(&w->mu);
    if (w->t != NULL) {
        tk_wake(w->t);
        w->t = NULL;
    }

    disarm(w);
    pthread_mutex_unlock(&w->mu);
}

// poll_main waits on the poller and wakes the slot each event names.
static void *poll_main(void *arg) {
    (void)arg;
    for (;;) {
#if defined(__APPLE__)
        struct kevent events[64];
        int n = kevent(poller, NULL, 0, events, 64, NULL);
        for (int i = 0; i < n; i++) {
            wake(events[i].udata);
        }
#else
        struct epoll_event events[64];
        int n = epoll_wait(poller, events, 64, -1);
        for (int i = 0; i < n; i++) {
            wake(events[i].data.ptr);
        }
#endif
    }

    return NULL;
}

// fatal stops the program for a call to the poller that failed, which
// the program can neither see nor mend: the task would otherwise park
// for an event that was never asked for. The message names the call
// and the system's reason, as a panic names its cause.
static _Noreturn void fatal(const char *call) {
    char buf[160];
    int n = snprintf(buf, sizeof buf, "%s: %s", call, strerror(last_error()));
    tk_panic(TK_STR(buf, n));
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
            tk_panic(TK_STR("cannot make the poller", 22));
        }

        pthread_t id;
        pthread_attr_t attr;
        pthread_attr_init(&attr);
        pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
        if (pthread_create(&id, &attr, poll_main, NULL) != 0) {
            tk_panic(TK_STR("cannot start the poller", 23));
        }

        pthread_attr_destroy(&attr);
    }

    pthread_mutex_unlock(&poller_mu);
}

// wait_fd registers the running task for one event on fd, readable or
// writable, and parks it until the event comes. A wake that comes
// before the park is kept by the scheduler, so nothing is lost between
// the two. The park may also end for an earlier wait's wake, so every
// caller retries its call and waits again while the socket is not
// ready; the slot is emptied either way before the wait returns. The
// registration is a wake promised from outside the workers until its
// event is delivered, which the slot keeps count of: a wait renewed
// before the event fired replaces the registration, and promises
// nothing more, since the one event to come keeps both.
static void wait_fd(int fd, bool write) {
    if (poller < 0) {
        poller_start();
    }

    waiter *w = waiter_of(fd);
    pthread_mutex_lock(&w->mu);
    w->t = tk_self();
    if (!w->armed) {
        w->armed = true;
        tk_outside(1);
    }

    pthread_mutex_unlock(&w->mu);
#if defined(__APPLE__)
    struct kevent ev;
    EV_SET(&ev, (uintptr_t)fd, write ? EVFILT_WRITE : EVFILT_READ, EV_ADD | EV_ONESHOT, 0, 0, w);
    if (kevent(poller, &ev, 1, NULL, 0, NULL) != 0) {
        fatal("kevent");
    }
#else
    struct epoll_event ev = {.events = (write ? EPOLLOUT : EPOLLIN) | EPOLLONESHOT, .data.ptr = w};
    if (epoll_ctl(poller, EPOLL_CTL_MOD, fd, &ev) != 0 && epoll_ctl(poller, EPOLL_CTL_ADD, fd, &ev) != 0) {
        fatal("epoll_ctl");
    }
#endif
    tk_park();
    pthread_mutex_lock(&w->mu);
    w->t = NULL;
    pthread_mutex_unlock(&w->mu);
}

// unwatch forgets a descriptor that is being closed: the system drops
// its registration with the close, so an event it still promised can
// never come, and the promise is kept here instead. A later socket
// with the same number starts from a slot that promises nothing.
static void unwatch(int fd) {
    pthread_mutex_lock(&poller_mu);
    waiter *w = fd < nwaiters ? waiters[fd] : NULL;
    pthread_mutex_unlock(&poller_mu);
    if (w != NULL) {
        pthread_mutex_lock(&w->mu);
        disarm(w);
        pthread_mutex_unlock(&w->mu);
    }
}

// ipv4 reads the IPv4 address host, "127.0.0.1", into addr.
static bool ipv4(tk_string host, struct in_addr *addr) {
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
static int64_t fail(int s, const char *name, tk_string *call) {
    int code = last_error();
    if (s >= 0) {
        unwatch(s);
        close(s);
    }

    *call = TK_STR(name, (int64_t)strlen(name));
    return code;
}

// tk_net_listen opens a TCP socket listening on the IPv4 address host,
// "0.0.0.0" for every address of the machine, at the port, 0 for one
// the system picks, and stores the descriptor and the port it listens
// on; a failure stores the call that failed. A host that is not an
// IPv4 address is -1, which the library words; the port is in range.
int64_t tk_net_listen(tk_string host, int64_t port, int64_t *fd, int64_t *got, tk_string *call) {
    struct sockaddr_in addr = {.sin_family = AF_INET, .sin_port = htons((uint16_t)port)};
    if (!ipv4(host, &addr.sin_addr)) {
        return -1;
    }

    int s = socket(AF_INET, SOCK_STREAM, 0);
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

// tk_net_accept waits for a connection on the listening socket and
// stores its descriptor. A connection its client reset before it was
// accepted is skipped, as Go's runtime skips it, for the next one: on
// Darwin it is accepted and then refuses its socket options, elsewhere
// accept reports it aborted. Either is the client's failure, and a
// server that stopped on it could be stopped by any client.
int64_t tk_net_accept(int64_t fd, int64_t *out) {
    for (;;) {
        int c = accept((int)fd, NULL, NULL);
        if (c >= 0) {
            if (connected(c) != NULL) {
                close(c);
                continue;
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

        wait_fd((int)fd, false);
    }
}

// tk_net_dial connects to the IPv4 address host at the port and stores
// the connection's descriptor, or on a failure the call that failed. A
// host that is not an IPv4 address is -1, which the library words. A
// connection in progress is checked again after every wake, as Go
// checks it: a wake can come for another wait, so a socket with no
// error yet is connected only once it has a peer.
int64_t tk_net_dial(tk_string host, int64_t port, int64_t *fd, tk_string *call) {
    struct sockaddr_in addr = {.sin_family = AF_INET, .sin_port = htons((uint16_t)port)};
    if (!ipv4(host, &addr.sin_addr)) {
        return -1;
    }

    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) {
        return fail(s, "socket", call);
    }

    const char *failed = connected(s);
    if (failed != NULL) {
        return fail(s, failed, call);
    }

    if (connect(s, (struct sockaddr *)&addr, sizeof addr) != 0) {
        if (last_error() != EINPROGRESS && last_error() != EINTR) {
            return fail(s, "connect", call);
        }

        for (;;) {
            wait_fd(s, true);
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

    *fd = s;
    return 0;
}

// address formats the IPv4 address and port of a socket as Go prints
// them, "127.0.0.1:8080", as an owned string.
static tk_string address(const struct sockaddr_in *a) {
    char ip[INET_ADDRSTRLEN];
    if (inet_ntop(AF_INET, &a->sin_addr, ip, sizeof ip) == NULL) {
        return TK_STR("", 0);
    }

    char text[INET_ADDRSTRLEN + 8];
    int n = snprintf(text, sizeof text, "%s:%d", ip, ntohs(a->sin_port));
    uint8_t *p = tk_alloc((size_t)n);
    memcpy(p, text, (size_t)n);
    return TK_OWNED(p, n);
}

// tk_net_addrs stores the local and the remote address of a socket,
// the remote one empty when it has no peer, as a listening socket.
int64_t tk_net_addrs(int64_t fd, tk_string *local, tk_string *remote) {
    struct sockaddr_in a;
    socklen_t len = sizeof a;
    if (getsockname((int)fd, (struct sockaddr *)&a, &len) != 0) {
        return last_error();
    }

    *local = address(&a);
    len = sizeof a;
    *remote = getpeername((int)fd, (struct sockaddr *)&a, &len) == 0 ? address(&a) : TK_STR("", 0);
    return 0;
}

// tk_net_read reads what the connection has into buf and stores how
// much, 0 once the other side has closed, or for a buf of no bytes. A
// read a signal interrupts is asked again, as the file reads are.
int64_t tk_net_read(int64_t fd, tk_slice_u8 *buf, int64_t *n) {
    tk_check_slice((tk_slice *)buf);
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

        if (last_error() == EINTR) {
            continue;
        }

        if (last_error() != EAGAIN && last_error() != EWOULDBLOCK) {
            return last_error();
        }

        wait_fd((int)fd, false);
    }
}

// tk_net_write writes all of buf to the connection and stores how
// much it wrote, the length of buf.
int64_t tk_net_write(int64_t fd, tk_slice_u8 buf, int64_t *n) {
    tk_check_slice((tk_slice *)&buf);
    int64_t done = 0;
    while (done < buf.len) {
        ssize_t put = send((int)fd, buf.ptr + done, (size_t)(buf.len - done), TK_NOSIGNAL);
        if (put >= 0) {
            done += put;
            continue;
        }

        if (last_error() == EINTR) {
            continue;
        }

        if (last_error() != EAGAIN && last_error() != EWOULDBLOCK) {
            return last_error();
        }

        wait_fd((int)fd, true);
    }

    *n = done;
    return 0;
}

// tk_net_closeConn closes a socket, a connection or a listener, and
// forgets what the poller still promised for it.
int64_t tk_net_closeConn(int64_t fd) {
    unwatch((int)fd);
    return close((int)fd) == 0 ? 0 : last_error();
}
