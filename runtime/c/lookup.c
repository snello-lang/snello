// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Names and addresses, for the net package. An address is read from
// text and printed as IPv4 "127.0.0.1:8080" and IPv6
// "[::1]:8080". A host name is looked up through the system's resolver,
// getaddrinfo, which reads the hosts file and asks the name servers and
// blocks for as long as they take to answer, or to fail to: a name
// server that does not answer holds it for seconds. A task's lookup
// must not hold its worker so long, which also watches the sockets
// between tasks: it runs on a thread of a pool of the runtime's own
// while the task parks, and the thread that finishes it wakes the task.
// A thread starts when a lookup finds none idle, up to resolvers_most
// of them, and one left idle for resolver_idle seconds ends. A lookup
// with a timeout parks on a timer as well, and the first of the two to
// come wakes the task; a lookup its task gave up on runs to its end on
// its thread, since getaddrinfo cannot be stopped, and is freed there.

#define _GNU_SOURCE // glibc's EAI_NODATA and the socket calls under -std=c11
#define _DARWIN_C_SOURCE

#include "internal.h"

#include <errno.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#if defined(_WIN32)
// Each in a block of its own, as each needs the one before it.
#include <winsock2.h>

#include <ws2tcpip.h>

#include <iphlpapi.h>
#pragma comment(lib, "ws2_32")   // the sockets' and the lookups'
#pragma comment(lib, "iphlpapi") // if_nametoindex's
#else
#include <arpa/inet.h>
#include <net/if.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>
#endif

enum {
    resolvers_most = 500, // threads looking names up at once
    resolver_idle = 10,   // seconds a thread with no lookup waits for one before it ends

    address_text = INET6_ADDRSTRLEN + IF_NAMESIZE + 8, // the longest address printed: brackets, a zone and a port
    reason_text = 256,                                 // the longest reason a failed lookup gives, cut there
};

// A lookup's answer, the system's list of addresses, and the call that
// frees it: on Windows the wide-character call's, whose name is read as
// UTF-16, converted from Snello's UTF-8, where the other one reads it in
// the code page of the system's locale.
#if defined(_WIN32)
typedef ADDRINFOW address_info;
#define free_address_info FreeAddrInfoW
#else
typedef struct addrinfo address_info;
#define free_address_info freeaddrinfo
#endif

// sn_net_parse reads the IP address host into addr, with the port,
// and returns the length of addr: IPv4 "127.0.0.1", or IPv6 "::1", with
// a zone after a '%' naming the interface by name or by number,
// "fe80::1%en0". It reads these forms alone; any other, such as
// "127.1", is a name, for which it returns 0.
SN_HELD int sn_net_parse(sn_string host, int64_t port, struct sockaddr_storage *addr) {
    char text[address_text];
    if (host.len <= 0 || host.len >= (int64_t)sizeof text || memchr(host.ptr, '\0', (size_t)host.len) != NULL) {
        return 0;
    }

    memcpy(text, host.ptr, (size_t)host.len);
    text[host.len] = '\0';
    memset(addr, 0, sizeof *addr);
    struct sockaddr_in *a4 = (struct sockaddr_in *)addr;
    if (inet_pton(AF_INET, text, &a4->sin_addr) == 1) {
        a4->sin_family = AF_INET;
        a4->sin_port = htons((uint16_t)port);
        return sizeof *a4;
    }

    char *zone = strchr(text, '%');
    if (zone != NULL) {
        *zone++ = '\0';
    }

    struct sockaddr_in6 *a6 = (struct sockaddr_in6 *)addr;
    if (inet_pton(AF_INET6, text, &a6->sin6_addr) != 1) {
        return 0;
    }

    if (zone != NULL) {
        unsigned index = if_nametoindex(zone);
        if (index == 0) { // no interface of that name: a number, or nothing an address can be on
            char *end = NULL;
            unsigned long n = strtoul(zone, &end, 10);
            if (*zone == '\0' || *end != '\0' || n == 0 || n > UINT32_MAX) {
                return 0;
            }

            index = (unsigned)n;
        }

        a6->sin6_scope_id = index;
    }

    a6->sin6_family = AF_INET6;
    a6->sin6_port = htons((uint16_t)port);
    return sizeof *a6;
}

// sn_net_format prints the address in addr, "127.0.0.1",
// "::1", "fe80::1%en0", an IPv4 address mapped into IPv6 as the IPv4
// one, which a socket listening on both families reports for an IPv4
// peer; with the port when port is true, "127.0.0.1:8080", "[::1]:8080".
// It returns an owned string, empty for a family that is not IP.
SN_HELD sn_string sn_net_format(const struct sockaddr *addr, bool port) {
    char ip[address_text];
    int at = 0;
    if (addr->sa_family == AF_INET) {
        const struct sockaddr_in *a4 = (const struct sockaddr_in *)addr;
        inet_ntop(AF_INET, &a4->sin_addr, ip, sizeof ip);
        at = a4->sin_port;
    } else if (addr->sa_family == AF_INET6) {
        const struct sockaddr_in6 *a6 = (const struct sockaddr_in6 *)addr;
        at = a6->sin6_port;
        if (IN6_IS_ADDR_V4MAPPED(&a6->sin6_addr)) {
            inet_ntop(AF_INET, &a6->sin6_addr.s6_addr[12], ip, sizeof ip);
        } else {
            inet_ntop(AF_INET6, &a6->sin6_addr, ip, sizeof ip);
            if (a6->sin6_scope_id != 0) {
                size_t n = strlen(ip);
                char name[IF_NAMESIZE];
                if (if_indextoname(a6->sin6_scope_id, name) != NULL) {
                    snprintf(ip + n, sizeof ip - n, "%%%s", name);
                } else {
                    snprintf(ip + n, sizeof ip - n, "%%%u", (unsigned)a6->sin6_scope_id);
                }
            }

            if (port) {
                char text[address_text];
                int n = snprintf(text, sizeof text, "[%s]:%d", ip, ntohs((uint16_t)at));
                return sn_string_clone(SN_STR(text, n));
            }
        }
    } else {
        return SN_STR("", 0);
    }

    if (port) {
        char text[address_text];
        int n = snprintf(text, sizeof text, "%s:%d", ip, ntohs((uint16_t)at));
        return sn_string_clone(SN_STR(text, n));
    }

    return sn_string_clone(SN_STR(ip, (int)strlen(ip)));
}

// A lookup is one name to look up, shared by the task that asked and
// the thread that looks it up, each holding one of its two references;
// the last to let go frees it. The task is in waiter before the lookup
// is queued, and stays there until a waker takes it out: the thread,
// which stores the answer and sets done first, or the timer of a
// timeout; the first to take it out wakes it
// through the handoff, and the other finds waiter empty. So the task
// never takes itself out, and leaves only once its waker is done with
// it. A task in waiter is a wake promised from outside the workers,
// which the deadlock detector counts, so that a program whose tasks all
// wait on lookups is not taken for one in which nothing can run.
typedef struct lookup {
    struct lookup *next; // in the queue, while no thread has taken it
    char *name;
    address_info *found;
    int code; // getaddrinfo's
    int err;  // errno, for a code of EAI_SYSTEM
    _Atomic bool done;
    _Atomic uintptr_t waiter;
    sn_handoff woken;
    _Atomic int refs;
    sn_deadline timer;
} lookup;

// The pool: the lookups no thread has taken yet, oldest first, and its
// threads, the idle ones waiting on ready. A lookup put in the queue
// signals an idle thread when there is one for it that no earlier
// lookup has, and starts a thread otherwise, so that no lookup waits
// behind another while the pool can grow.
static struct {
    sn_mutex mu;
    sn_cond ready;
    lookup *head;
    lookup *tail;
    int queued;
    int idle;
    int threads;
} pool = { .mu = SN_MUTEX_INIT, .ready = SN_COND_INIT };

// release lets go of one reference to l, and frees it with the last.
SN_HELD_STATIC static void release(lookup *l) {
    if (atomic_fetch_sub(&l->refs, 1) == 1) {
        if (l->found != NULL) {
            free_address_info(l->found);
        }

        free(l->name);
        free(l);
    }
}

// take_out wakes the task waiting on l, unless the other waker took it
// out first, and keeps its promise: the task is on a queue before the
// count goes down.
SN_HELD_STATIC static void take_out(lookup *l) {
    uintptr_t task = atomic_exchange(&l->waiter, 0);
    if (task != 0) {
        sn_handoff_wake(&l->woken, (sn_task *)task);
        sn_outside(-1);
    }
}

// lookup_expire is the timer's: the timeout has passed, and the task is
// woken unless the lookup woke it first. The task tells the two apart
// by done, and takes the answer when it has one.
SN_HELD_STATIC static void lookup_expire(void *arg) { take_out(arg); }

// resolve looks l's name up, for a stream socket, any family, the
// addresses in the order the system sorts them, by RFC 6724 where it
// does.
SN_HELD_STATIC static void resolve(lookup *l) {
    address_info hints = { .ai_family = AF_UNSPEC, .ai_socktype = SOCK_STREAM };
#if defined(_WIN32)
    int wide = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, l->name, -1, NULL, 0);
    wchar_t *name = wide > 0 ? malloc((size_t)wide * sizeof *name) : NULL;
    if (!sn_winsock_start()) {
        l->code = WSANOTINITIALISED;
    } else if (wide <= 0) {
        l->code = EAI_NONAME; // a name that is not UTF-8 names no host
    } else if (name == NULL) {
        l->code = EAI_MEMORY;
    } else {
        (void)MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, l->name, -1, name, wide);
        l->code = GetAddrInfoW(name, NULL, &hints, &l->found);
    }

    free(name);
#else
    l->code = getaddrinfo(l->name, NULL, &hints, &l->found);
#endif
#if defined(EAI_SYSTEM)
    l->err = l->code == EAI_SYSTEM ? errno : 0;
#endif
    atomic_store(&l->done, true);
    take_out(l);
    release(l);
}

// resolver_main is a thread of the pool: it takes the lookups from the
// queue one at a time, and ends once it has waited resolver_idle
// seconds with none.
SN_HELD_STATIC static void *resolver_main(void *arg) {
    (void)arg;
    sn_mutex_lock(&pool.mu);
    for (;;) {
        if (pool.head == NULL) {
            int64_t until = sn_time_mono() + (int64_t)resolver_idle * sn_ns_per_s;
            pool.idle++;
            for (int64_t left = until - sn_time_mono(); pool.head == NULL && left > 0; left = until - sn_time_mono()) {
                sn_cond_wait_ms(&pool.ready, &pool.mu, (left + sn_ns_per_ms - 1) / sn_ns_per_ms);
            }

            pool.idle--;
            if (pool.head == NULL) {
                pool.threads--;
                sn_mutex_unlock(&pool.mu);
                return NULL;
            }
        }

        lookup *l = pool.head;
        pool.head = l->next;
        if (pool.head == NULL) {
            pool.tail = NULL;
        }

        pool.queued--;
        sn_mutex_unlock(&pool.mu);
        resolve(l);
        sn_mutex_lock(&pool.mu);
    }
}

// submit puts l in the queue for a thread of the pool. It returns 0, or
// the error number of a thread that could not be started for a pool
// with none, which then has no thread to look l up and has not queued
// it.
SN_HELD_STATIC static int submit(lookup *l) {
    int code = 0;
    sn_mutex_lock(&pool.mu);
    if (pool.tail == NULL) {
        pool.head = l;
    } else {
        pool.tail->next = l;
    }

    pool.tail = l;
    pool.queued++;
    if (pool.queued <= pool.idle) {
        sn_cond_signal(&pool.ready);
    } else if (pool.threads < resolvers_most) {
        code = sn_thread_start(resolver_main, NULL) ? 0 : EAGAIN;
        if (code == 0) {
            pool.threads++;
        } else if (pool.threads > 0) {
            code = 0; // a thread the pool has takes it when it is free
        } else {
            pool.head = pool.tail = NULL; // the only lookup queued: none before it found a thread either
            pool.queued--;
        }
    }

    sn_mutex_unlock(&pool.mu);
    return code;
}

// resolver_reason writes the system's words for the resolver's failure
// code into why, which holds reason_text bytes, and returns their
// length: on Windows formatted for this call, in UTF-16 and converted to
// UTF-8, since the C library's gai_strerror there writes into one
// buffer every thread shares and in the system's code page, without
// the line's end the system's message closes with, and the code's
// number where the system has no words for it.
SN_HELD_STATIC static int resolver_reason(int code, char why[reason_text]) {
#if defined(_WIN32)
    wchar_t text[reason_text];
    DWORD units = FormatMessageW(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, (DWORD)code, 0, text,
                                 reason_text, NULL);
    while (units > 0 && (text[units - 1] == L'\n' || text[units - 1] == L'\r' || text[units - 1] == L' ')) {
        units--;
    }

    int n = units == 0 ? 0 : WideCharToMultiByte(CP_UTF8, 0, text, (int)units, why, reason_text, NULL, NULL);
    if (n == 0) { // no words for the code, or none that fit: its number
        n = snprintf(why, reason_text, "error %d", code);
    }

    return n;
#else
    const char *text = gai_strerror(code);
    size_t n = strlen(text);
    n = n < reason_text ? n : reason_text;
    memcpy(why, text, n);
    return (int)n;
#endif
}

// answer stores the addresses l found, printed, in out, a []string that
// holds nothing, and returns what sn_net_lookup returns.
SN_HELD_STATIC static int64_t answer(lookup *l, void *out, sn_string *reason) {
    switch (l->code) {
    case 0:
        break;
    case EAI_NONAME:
#if defined(EAI_NODATA) && EAI_NODATA != EAI_NONAME
    case EAI_NODATA:
#endif
        return SN_NET_NO_SUCH_HOST;
#if defined(EAI_SYSTEM)
    case EAI_SYSTEM:
        return l->err != 0 ? l->err : EMFILE; // glibc has been seen to fail so with no error number, out of descriptors
#endif
    default: {
        char why[reason_text];
        int n = resolver_reason(l->code, why);
        *reason = sn_string_clone(SN_STR(why, n));
        return SN_NET_RESOLVER_FAILED;
    }
    }

    int64_t n = 0;
    for (address_info *a = l->found; a != NULL; a = a->ai_next) {
        n += a->ai_family == AF_INET || a->ai_family == AF_INET6;
    }

    if (n == 0) {
        return SN_NET_NO_SUCH_HOST;
    }

    sn_slice *addrs = out;
    sn_slice_make(addrs, 0, n, sizeof(sn_string));
    sn_string *at = addrs->ptr;
    for (address_info *a = l->found; a != NULL; a = a->ai_next) {
        if (a->ai_family == AF_INET || a->ai_family == AF_INET6) {
            at[addrs->len++] = sn_net_format(a->ai_addr, false);
        }
    }

    return 0;
}

// sn_net_lookup looks the name host up and stores its addresses in out,
// a []string that holds nothing, as text, in the order the system gives
// them; an IP address is its own one address, as given. The task parks
// while a thread of the pool looks the name up, for at most timeout
// nanoseconds when that is above zero. It returns 0, SN_NET_NO_SUCH_HOST
// for a name with no address, SN_NET_TIMED_OUT once the timeout passes,
// SN_NET_RESOLVER_FAILED for a failure of the resolver's own with its
// reason in reason, what sn_net_cancelled says once the task's scope is
// cancelled, or the system's error number, EINVAL for a name
// holding a zero byte, which no name can.
SN_HELD int64_t sn_net_lookup(sn_string host, int64_t timeout, void *out, sn_string *reason) {
    struct sockaddr_storage addr;
    if (sn_net_parse(host, 0, &addr) != 0) {
        sn_slice *addrs = out;
        sn_slice_make(addrs, 1, 1, sizeof(sn_string));
        ((sn_string *)addrs->ptr)[0] = sn_string_clone(host);
        return 0;
    }

    if (host.len == 0) {
        return SN_NET_NO_SUCH_HOST;
    }

    if (memchr(host.ptr, '\0', (size_t)host.len) != NULL) {
        return EINVAL;
    }

    lookup *l = calloc(1, sizeof *l);
    char *name = malloc((size_t)host.len + 1);
    if (l == NULL || name == NULL) {
        sn_panic(SN_STR("out of memory", 13));
    }

    memcpy(name, host.ptr, (size_t)host.len);
    name[host.len] = '\0';
    l->name = name;
    atomic_init(&l->refs, 2);
    uintptr_t self = (uintptr_t)sn_self();
    sn_outside(1);
    atomic_init(&l->waiter, self); // before the lookup is queued: no waker can see l yet
    int code = submit(l);
    if (code != 0) {
        sn_outside(-1);
        free(name);
        free(l);
        return code;
    }

    if (timeout > 0) {
        sn_deadline_arm(&l->timer, timeout, lookup_expire, l);
    }

    // A park may end for an earlier wait's wake, which the scheduler
    // keeps, so the task parks again until a waker has taken it out, and
    // then waits for that waker to be done with it.
    while (atomic_load(&l->waiter) == self) {
        int cut = sn_net_cancelled();
        uintptr_t mine = self;
        if (cut != 0 && atomic_compare_exchange_strong(&l->waiter, &mine, 0)) {
            // Cancelled, and taken out by the task itself: no waker will
            // wake it, and the resolver's thread drops the answer.
            sn_outside(-1);
            if (timeout > 0) {
                sn_deadline_disarm(&l->timer);
            }

            release(l);
            return cut;
        }

        sn_park();
    }

    sn_handoff_wait(&l->woken);
    if (timeout > 0) {
        sn_deadline_disarm(&l->timer); // once it returns the timer does not run, and l may go
    }

    int64_t got = atomic_load(&l->done) ? answer(l, out, reason) : SN_NET_TIMED_OUT;
    release(l);
    return got;
}
