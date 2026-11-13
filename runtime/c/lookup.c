// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Names and addresses, for the net package. An address is read from
// text and printed as Go prints it, IPv4 "127.0.0.1:8080" and IPv6
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

#include <arpa/inet.h>
#include <errno.h>
#include <net/if.h>
#include <netdb.h>
#include <netinet/in.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>

enum {
    resolvers_most = 500, // threads looking names up at once, as many as Go lets its resolver have
    resolver_idle = 10,   // seconds a thread with no lookup waits for one before it ends

    address_text = INET6_ADDRSTRLEN + IF_NAMESIZE + 8, // the longest address printed: brackets, a zone and a port
};

// sn_net_parse reads the IP address host into addr, with the port,
// and returns the length of addr: IPv4 "127.0.0.1", or IPv6 "::1", with
// a zone after a '%' naming the interface by name or by number,
// "fe80::1%en0". It reads the forms Go reads, and no other, such as
// "127.1": anything else is a name, for which it returns 0.
int sn_net_parse(sn_string host, int64_t port, struct sockaddr_storage *addr) {
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

// sn_net_format prints the address in addr as Go prints it, "127.0.0.1",
// "::1", "fe80::1%en0", an IPv4 address mapped into IPv6 as the IPv4
// one, which a socket listening on both families reports for an IPv4
// peer; with the port when port is true, "127.0.0.1:8080", "[::1]:8080".
// It returns an owned string, empty for a family that is not IP.
sn_string sn_net_format(const struct sockaddr *addr, bool port) {
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
    struct addrinfo *found;
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
    pthread_mutex_t mu;
    pthread_cond_t ready;
    lookup *head;
    lookup *tail;
    int queued;
    int idle;
    int threads;
} pool = {.mu = PTHREAD_MUTEX_INITIALIZER, .ready = PTHREAD_COND_INITIALIZER};

// release lets go of one reference to l, and frees it with the last.
static void release(lookup *l) {
    if (atomic_fetch_sub(&l->refs, 1) == 1) {
        if (l->found != NULL) {
            freeaddrinfo(l->found);
        }

        free(l->name);
        free(l);
    }
}

// take_out wakes the task waiting on l, unless the other waker took it
// out first, and keeps its promise: the task is on a queue before the
// count goes down.
static void take_out(lookup *l) {
    uintptr_t task = atomic_exchange(&l->waiter, 0);
    if (task != 0) {
        sn_handoff_wake(&l->woken, (sn_task *)task);
        sn_outside(-1);
    }
}

// lookup_expire is the timer's: the timeout has passed, and the task is
// woken unless the lookup woke it first. The task tells the two apart
// by done, and takes the answer when it has one.
static void lookup_expire(void *arg) {
    take_out(arg);
}

// resolve looks l's name up, for a stream socket, any family, the
// addresses in the order the system sorts them, by RFC 6724 where it
// does, as Go's resolver returns them through the system's.
static void resolve(lookup *l) {
    struct addrinfo hints = {.ai_family = AF_UNSPEC, .ai_socktype = SOCK_STREAM};
    l->code = getaddrinfo(l->name, NULL, &hints, &l->found);
    l->err = l->code == EAI_SYSTEM ? errno : 0;
    atomic_store(&l->done, true);
    take_out(l);
    release(l);
}

// resolver_main is a thread of the pool: it takes the lookups from the
// queue one at a time, and ends once it has waited resolver_idle
// seconds with none.
static void *resolver_main(void *arg) {
    (void)arg;
    pthread_mutex_lock(&pool.mu);
    for (;;) {
        if (pool.head == NULL) {
            struct timespec until;
            clock_gettime(CLOCK_REALTIME, &until); // the clock the condition's wait reads
            until.tv_sec += resolver_idle;
            pool.idle++;
            int waited = 0;
            while (pool.head == NULL && waited != ETIMEDOUT) {
                waited = pthread_cond_timedwait(&pool.ready, &pool.mu, &until);
            }

            pool.idle--;
            if (pool.head == NULL) {
                pool.threads--;
                pthread_mutex_unlock(&pool.mu);
                return NULL;
            }
        }

        lookup *l = pool.head;
        pool.head = l->next;
        if (pool.head == NULL) {
            pool.tail = NULL;
        }

        pool.queued--;
        pthread_mutex_unlock(&pool.mu);
        resolve(l);
        pthread_mutex_lock(&pool.mu);
    }
}

// submit puts l in the queue for a thread of the pool. It returns 0, or
// the error number of a thread that could not be started for a pool
// with none, which then has no thread to look l up and has not queued
// it.
static int submit(lookup *l) {
    int code = 0;
    pthread_mutex_lock(&pool.mu);
    if (pool.tail == NULL) {
        pool.head = l;
    } else {
        pool.tail->next = l;
    }

    pool.tail = l;
    pool.queued++;
    if (pool.queued <= pool.idle) {
        pthread_cond_signal(&pool.ready);
    } else if (pool.threads < resolvers_most) {
        pthread_t id;
        pthread_attr_t attr;
        pthread_attr_init(&attr);
        pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
        code = pthread_create(&id, &attr, resolver_main, NULL);
        pthread_attr_destroy(&attr);
        if (code == 0) {
            pool.threads++;
        } else if (pool.threads > 0) {
            code = 0; // a thread the pool has takes it when it is free
        } else {
            pool.head = pool.tail = NULL; // the only lookup queued: none before it found a thread either
            pool.queued--;
        }
    }

    pthread_mutex_unlock(&pool.mu);
    return code;
}

// answer stores the addresses l found, printed, in out, a []string that
// holds nothing, and returns what sn_net_lookup returns.
static int64_t answer(lookup *l, void *out, sn_string *reason) {
    switch (l->code) {
    case 0:
        break;
    case EAI_NONAME:
#if defined(EAI_NODATA) && EAI_NODATA != EAI_NONAME
    case EAI_NODATA:
#endif
        return SN_NET_NO_SUCH_HOST;
    case EAI_SYSTEM:
        return l->err != 0 ? l->err : EMFILE; // as Go's: glibc has been seen to fail so with no error number, out of descriptors
    default: {
        const char *why = gai_strerror(l->code);
        *reason = sn_string_clone(SN_STR(why, (int)strlen(why)));
        return SN_NET_RESOLVER_FAILED;
    }
    }

    int64_t n = 0;
    for (struct addrinfo *a = l->found; a != NULL; a = a->ai_next) {
        n += a->ai_family == AF_INET || a->ai_family == AF_INET6;
    }

    if (n == 0) {
        return SN_NET_NO_SUCH_HOST;
    }

    sn_slice *addrs = out;
    sn_slice_make(addrs, 0, n, sizeof(sn_string));
    sn_string *at = addrs->ptr;
    for (struct addrinfo *a = l->found; a != NULL; a = a->ai_next) {
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
// reason in reason, or the system's error number, EINVAL for a name
// holding a zero byte, which no name can, as Go reports it.
int64_t sn_net_lookup(sn_string host, int64_t timeout, void *out, sn_string *reason) {
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
