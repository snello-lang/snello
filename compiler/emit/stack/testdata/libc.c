// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The stack each C library function the runtime calls uses, which
// TestCLibraryStack holds the allowances of stack.go to. Each function
// runs on a thread of its own whose stack is a region filled with a
// pattern, called from a frame that paints the stack below it again
// first, since the thread's start writes there, and reads after the
// call returns, before the thread's exit writes deeper, how far below
// its stack pointer the call wrote. A function that ends the process
// runs in a child, on a stack the parent reads once the child has
// ended. A function is driven down its heavier paths: a large
// allocation as well as a small one, a format of every kind of value.
// The program is built without the compiler's own versions of the C
// library's functions, so that every call is made, and linked to bind
// every imported function as it starts, as a Snello program is, so that
// no first call runs the dynamic linker's resolver on the stack. It
// prints a line a function, its name and its bytes.

#define _GNU_SOURCE
#define _DARWIN_C_SOURCE

#include <arpa/inet.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <net/if.h>
#include <netdb.h>
#include <netinet/in.h>
#include <pthread.h>
#include <sched.h>
#include <setjmp.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <os/lock.h>
#include <sys/event.h>
#include <sys/random.h>
#include <sys/sysctl.h>
#else
#include <sys/epoll.h>
#include <sys/prctl.h>
#endif

enum { stack_bytes = 1 << 20, paint = 0xA5 };

static uint8_t *region;   // the painted stack the thread runs on
static size_t reached;    // how far below the caller's frame the call wrote
static uintptr_t *ended;  // for a call that ends the process, where its caller's frame ends, shared with the parent
static volatile long sink;
static volatile size_t count = 64;
static volatile int signalled;
static _Thread_local volatile char local[64];

void __stack_chk_fail(void);

// Shared state the cases set up before the measured call.
static pthread_mutex_t mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t cond = PTHREAD_COND_INITIALIZER;
static pthread_attr_t attr;
static void *block;
static char text[256], dir[64], path[128];
static int fd = -1, server = -1, client = -1, accepted = -1, queue = -1;
static struct sockaddr_in addr;
static DIR *listing;
static sigjmp_buf jump;
static struct addrinfo *found;
static FILE *file;

static size_t deepest(uintptr_t entry);

// stack_pointer is where the caller's frame ends as it calls: inlined,
// the stack pointer of the function it is written in.
#if defined(__aarch64__)
#define stack_pointer() ({ uintptr_t sp_; __asm__ volatile("mov %0, sp" : "=r"(sp_)); sp_; })
#elif defined(__x86_64__)
#define stack_pointer() ({ uintptr_t sp_; __asm__ volatile("mov %%rsp, %0" : "=r"(sp_)); sp_; })
#endif

static void *nothing(void *a) { return a; }

// signaller wakes the case of pthread_cond_wait once it waits.
static void *signaller(void *a) {
    struct timespec d = {0, 10 * 1000 * 1000};
    nanosleep(&d, NULL);
    pthread_mutex_lock(&mutex);
    signalled = 1;
    pthread_cond_signal(&cond);
    pthread_mutex_unlock(&mutex);
    return a;
}

// trampoline paints the stack below its own frame again, calls f, and
// reads how far below its stack pointer f wrote. The painting is a loop of
// its own, not memset, whose frame would write where it paints.
static __attribute__((noinline)) void *trampoline(void *f) {
    uintptr_t entry = stack_pointer();
    for (volatile uint8_t *p = region; (uintptr_t)p < entry; p++) {
        *p = paint;
    }

    ((void (*)(void))f)();
    reached = deepest(entry);
    return NULL;
}

// ender paints as trampoline does and calls f, which ends the process,
// after it records where its own frame ends for the parent to read. A
// signal f raises is handled on a stack of its own, as the runtime
// installs no handler and its default action writes no frame.
static __attribute__((noinline)) void *ender(void *f) {
    static uint8_t alternate[1 << 16];
    stack_t ss = {.ss_sp = alternate, .ss_size = sizeof alternate};
    sigaltstack(&ss, NULL);
    uintptr_t entry = stack_pointer();
    for (volatile uint8_t *p = region; (uintptr_t)p < entry; p++) {
        *p = paint;
    }

    *ended = entry;
    ((void (*)(void))f)();
    return NULL;
}

// quit ends the child an abort signals, so that no crash is reported.
static void quit(int sig) {
    (void)sig;
    _exit(0);
}

// deepest reads how far below entry the painted region was written.
static size_t deepest(uintptr_t entry) {
    size_t i = 0;
    while (i < stack_bytes && region[i] == paint) {
        i++;
    }

    uintptr_t at = (uintptr_t)region + i;
    return at < entry ? entry - at : 0;
}

// run_ending measures a call that ends the process: it runs in a child
// whose stack and recorded frame are memory the parent shares, and the
// parent reads them once the child has ended.
static size_t run_ending(void (*f)(void)) {
    int flags = MAP_SHARED | MAP_ANONYMOUS;
    region = mmap(NULL, stack_bytes, PROT_READ | PROT_WRITE, flags, -1, 0);
    ended = mmap(NULL, sizeof *ended, PROT_READ | PROT_WRITE, flags, -1, 0);
    *ended = 0;
    pid_t pid = fork();
    if (pid == 0) {
        struct sigaction sa = {.sa_handler = quit, .sa_flags = SA_ONSTACK};
        sigaction(SIGABRT, &sa, NULL);
        pthread_attr_t a;
        pthread_attr_init(&a);
        pthread_attr_setstack(&a, region, stack_bytes);
        pthread_t t;
        pthread_create(&t, &a, ender, (void *)f);
        pthread_join(t, NULL);
        _exit(1);
    }

    int status;
    waitpid(pid, &status, 0);
    size_t n = *ended == 0 ? 0 : deepest(*ended);
    munmap(region, stack_bytes);
    munmap(ended, sizeof *ended);
    return n;
}

static size_t run(void (*f)(void)) {
    uint8_t *s = mmap(NULL, stack_bytes, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    region = s;
    pthread_attr_t a;
    pthread_attr_init(&a);
    pthread_attr_setstack(&a, s, stack_bytes);
    pthread_t t;
    pthread_create(&t, &a, trampoline, (void *)f);
    pthread_join(t, NULL);
    pthread_attr_destroy(&a);
    munmap(s, stack_bytes);
    return reached;
}

// A case is one function, measured by the body of its call, after the
// setup of the state it needs where it has one, setup_ and its name.
#define CASE(name, ...) \
    static void case_##name(void) { __VA_ARGS__; }

CASE(malloc, { free(malloc(16)); free(malloc(1 << 20)); free(malloc(64 << 20)); })
CASE(calloc, { free(calloc(100, 100)); free(calloc(1 << 20, 16)); })
static void setup_realloc(void) { block = malloc(16); }
CASE(realloc, { block = realloc(block, 1 << 20); block = realloc(block, 32 << 20); free(block); })
static void setup_free(void) { block = malloc(1 << 20); }
CASE(free, free(block))
CASE(aligned_alloc, free(aligned_alloc(64, 1 << 16)))
CASE(posix_memalign, { void *p; if (posix_memalign(&p, 4096, 1 << 16) == 0) free(p); })
CASE(memcpy, memcpy(text, path, sizeof path))
CASE(__memcpy_chk, __builtin___memcpy_chk(text, path, count, sizeof text))
CASE(memmove, memmove(text + 1, text, 200))
CASE(memset, memset(text, 1, sizeof text))
CASE(memcmp, sink = memcmp(text, text + 1, 200))
CASE(memchr, sink = memchr(text, 2, sizeof text) != NULL)
CASE(strlen, sink = (long)strlen("hello"))
CASE(strchr, sink = strchr("hello", 'l') != NULL)
CASE(strtoul, sink = (long)strtoul("12345", NULL, 10))
CASE(strerror, sink = (long)strlen(strerror(ENOENT)))
CASE(gai_strerror, sink = (long)strlen(gai_strerror(EAI_NONAME)))
CASE(snprintf, snprintf(text, sizeof text, "%s %d %lld %x %p %.3f %g %c", "x", 1, (long long)-1, 255, (void *)text, 3.14159, 1e300, 'z'))
CASE(pthread_mutex_init, { pthread_mutex_t m; pthread_mutex_init(&m, NULL); })
CASE(pthread_mutex_lock, { pthread_mutex_lock(&mutex); pthread_mutex_unlock(&mutex); })
CASE(pthread_mutex_unlock, { pthread_mutex_lock(&mutex); pthread_mutex_unlock(&mutex); })
CASE(pthread_cond_init, { pthread_cond_t c; pthread_cond_init(&c, NULL); })
CASE(pthread_cond_signal, pthread_cond_signal(&cond))
CASE(pthread_cond_broadcast, pthread_cond_broadcast(&cond))
CASE(pthread_cond_timedwait, { struct timespec ts = {0, 0}; pthread_mutex_lock(&mutex); pthread_cond_timedwait(&cond, &mutex, &ts); pthread_mutex_unlock(&mutex); })
static void setup_pthread_cond_wait(void) {
    signalled = 0;
    pthread_t t;
    pthread_create(&t, NULL, signaller, NULL);
    pthread_detach(t);
}
CASE(pthread_cond_wait, { pthread_mutex_lock(&mutex); while (!signalled) pthread_cond_wait(&cond, &mutex); pthread_mutex_unlock(&mutex); })
CASE(pthread_attr_init, pthread_attr_init(&attr))
static void setup_pthread_attr_setdetachstate(void) { pthread_attr_init(&attr); }
CASE(pthread_attr_setdetachstate, pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED))
static void setup_pthread_attr_destroy(void) { pthread_attr_init(&attr); }
CASE(pthread_attr_destroy, pthread_attr_destroy(&attr))
static void setup_pthread_create(void) { pthread_attr_init(&attr); pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED); }
CASE(pthread_create, { pthread_t t; pthread_create(&t, &attr, nothing, NULL); })
CASE(mmap, block = mmap(NULL, 1 << 20, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0))
static void setup_mprotect(void) { block = mmap(NULL, 1 << 20, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0); }
CASE(mprotect, mprotect(block, 1 << 16, PROT_READ | PROT_WRITE))
static void setup_munmap(void) { block = mmap(NULL, 1 << 20, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0); }
CASE(munmap, munmap(block, 1 << 20))
CASE(clock_gettime, { struct timespec ts; clock_gettime(CLOCK_MONOTONIC, &ts); })
CASE(nanosleep, { struct timespec d = {0, 1}; nanosleep(&d, NULL); })
CASE(sched_yield, sched_yield())
CASE(sysconf, sink = sysconf(_SC_PAGESIZE) + sysconf(_SC_NPROCESSORS_ONLN))
CASE(getpid, sink = getpid())
CASE(getrlimit, { struct rlimit r; getrlimit(RLIMIT_NOFILE, &r); })
CASE(setrlimit, { struct rlimit r; getrlimit(RLIMIT_NOFILE, &r); setrlimit(RLIMIT_NOFILE, &r); })
CASE(getentropy, getentropy(text, 64))
CASE(gethostname, gethostname(text, sizeof text))
CASE(socket, client = socket(AF_INET, SOCK_STREAM, 0))
static void setup_setsockopt(void) { client = socket(AF_INET, SOCK_STREAM, 0); }
CASE(setsockopt, { int one = 1; setsockopt(client, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one); })
static void setup_bind(void) { server = socket(AF_INET, SOCK_STREAM, 0); addr = (struct sockaddr_in){.sin_family = AF_INET, .sin_addr.s_addr = htonl(INADDR_LOOPBACK)}; }
CASE(bind, bind(server, (struct sockaddr *)&addr, sizeof addr))
CASE(listen, listen(server, 16))
CASE(getsockname, { socklen_t n = sizeof addr; getsockname(server, (struct sockaddr *)&addr, &n); })
static void setup_connect(void) { client = socket(AF_INET, SOCK_STREAM, 0); }
CASE(connect, connect(client, (struct sockaddr *)&addr, sizeof addr))
CASE(accept, accepted = accept(server, NULL, NULL))
CASE(fcntl, fcntl(client, F_SETFL, fcntl(client, F_GETFL, 0) | O_NONBLOCK))
CASE(send, send(client, "x", 1, 0))
CASE(read, read(accepted, text, 1))
CASE(write, write(accepted, "y", 1))
CASE(getsockopt, { int e; socklen_t n = sizeof e; getsockopt(client, SOL_SOCKET, SO_ERROR, &e, &n); })
CASE(getpeername, { struct sockaddr_storage p; socklen_t n = sizeof p; getpeername(client, (struct sockaddr *)&p, &n); })
CASE(close, { close(accepted); close(client); close(server); })
CASE(inet_pton, { struct in6_addr a; inet_pton(AF_INET6, "fe80::1:2:3:4", &a); inet_pton(AF_INET, "127.0.0.1", &a); })
CASE(inet_ntop, { struct in6_addr a; inet_pton(AF_INET6, "fe80::1:2:3:4", &a); inet_ntop(AF_INET6, &a, text, sizeof text); })
CASE(if_nametoindex, sink = if_nametoindex("lo0") + if_nametoindex("lo"))
CASE(if_indextoname, if_indextoname(1, text))
static void setup_freeaddrinfo(void) { struct addrinfo h = {.ai_socktype = SOCK_STREAM}; getaddrinfo("localhost", NULL, &h, &found); }
CASE(freeaddrinfo, freeaddrinfo(found))
static void setup_open(void) { snprintf(dir, sizeof dir, "/tmp/libc.XXXXXX"); mkdtemp(dir); snprintf(path, sizeof path, "%s/f", dir); }
CASE(open, fd = open(path, O_CREAT | O_RDWR, 0600))
CASE(lseek, lseek(fd, 0, SEEK_SET))
CASE(fstat, { struct stat st; fstat(fd, &st); })
CASE(fsync, fsync(fd))
CASE(stat, { struct stat st; stat(path, &st); })
CASE(lstat, { struct stat st; lstat(path, &st); })
CASE(chmod, chmod(path, 0644))
CASE(fstatat, { struct stat st; fstatat(AT_FDCWD, path, &st, 0); })
CASE(openat, { int d = openat(AT_FDCWD, dir, O_RDONLY | O_DIRECTORY); close(d); })
CASE(fdopendir, listing = fdopendir(open(dir, O_RDONLY | O_DIRECTORY)))
CASE(readdir, while (readdir(listing) != NULL) {})
CASE(rewinddir, rewinddir(listing))
CASE(dirfd, sink = dirfd(listing))
CASE(closedir, closedir(listing))
static void setup_mkdir(void) { snprintf(text, sizeof text, "%s/d", dir); }
CASE(mkdir, mkdir(text, 0700))
CASE(rmdir, rmdir(text))
static void setup_rename(void) { snprintf(text, sizeof text, "%s/g", dir); }
CASE(rename, rename(path, text))
CASE(unlinkat, unlinkat(AT_FDCWD, text, 0))
static void setup_unlink(void) { close(fd); fd = open(path, O_CREAT | O_RDWR, 0600); close(fd); }
CASE(unlink, { unlink(path); rmdir(dir); })
CASE(getcwd, getcwd(path, sizeof path))
static void setup_chdir(void) { getcwd(path, sizeof path); }
CASE(chdir, chdir(path))
CASE(fopen, file = fopen("/dev/null", "w"))
CASE(fputs, fputs("x", file))
CASE(fputc, fputc('y', file))
CASE(fwrite, fwrite("z", 1, 1, file))
CASE(fflush, fflush(file))
CASE(fclose, fclose(file))
static void setup_fscanf(void) { file = fopen("/dev/zero", "r"); }
CASE(fscanf, { int v; sink = fscanf(file, "%d", &v); fclose(file); })
CASE(sigsetjmp, sink = sigsetjmp(jump, 1))
CASE(siglongjmp, { if (sigsetjmp(jump, 1) == 0) siglongjmp(jump, 1); })
// A thread's first use of a thread-local variable, which macOS reaches
// through a call that makes the thread's storage for them.
CASE(thread_local, { local[0] = 1; sink = local[0]; })
CASE(exit, exit(0))
CASE(abort, abort())
CASE(__stack_chk_fail, __stack_chk_fail())
CASE(sin, sink = (long)(sin(0.5) * 100))
CASE(cos, sink = (long)(cos(0.5) * 100))
CASE(tan, sink = (long)(tan(0.5) * 100))
CASE(asin, sink = (long)(asin(0.5) * 100))
CASE(acos, sink = (long)(acos(0.5) * 100))
CASE(atan, sink = (long)(atan(0.5) * 100))
CASE(atan2, sink = (long)(atan2(0.5, 2) * 100))
CASE(sinh, sink = (long)(sinh(0.5) * 100))
CASE(cosh, sink = (long)(cosh(0.5) * 100))
CASE(tanh, sink = (long)(tanh(0.5) * 100))
CASE(exp, sink = (long)(exp(0.5) * 100))
CASE(log, sink = (long)(log(0.5) * 100))
CASE(log10, sink = (long)(log10(0.5) * 100))
CASE(log2, sink = (long)(log2(0.5) * 100))
CASE(pow, sink = (long)(pow(0.5, 3.3) * 100))
CASE(fmod, sink = (long)(fmod(7.5, 0.5) * 100))
CASE(hypot, sink = (long)(hypot(3, 4) * 100))
CASE(cbrt, sink = (long)(cbrt(27) * 100))
CASE(sqrt, sink = (long)(sqrt(2) * 100))
#if defined(__APPLE__)
CASE(bzero, bzero(text, sizeof text))
CASE(clock_gettime_nsec_np, sink = (long)clock_gettime_nsec_np(CLOCK_UPTIME_RAW))
CASE(sysctlbyname, { int v; size_t n = sizeof v; sysctlbyname("kern.ipc.somaxconn", &v, &n, NULL, 0); })
CASE(os_unfair_lock_lock, { os_unfair_lock l = OS_UNFAIR_LOCK_INIT; os_unfair_lock_lock(&l); os_unfair_lock_unlock(&l); })
CASE(os_unfair_lock_unlock, { os_unfair_lock l = OS_UNFAIR_LOCK_INIT; os_unfair_lock_lock(&l); os_unfair_lock_unlock(&l); })
CASE(kqueue, queue = kqueue())
CASE(kevent, { struct kevent ev; EV_SET(&ev, 0, EVFILT_READ, EV_ADD, 0, 0, NULL); struct timespec z = {0, 0}; kevent(queue, &ev, 1, NULL, 0, NULL); kevent(queue, NULL, 0, &ev, 1, &z); close(queue); })
CASE(__error, sink = *__error())
#else
CASE(pthread_mutex_destroy, { pthread_mutex_t m = PTHREAD_MUTEX_INITIALIZER; pthread_mutex_destroy(&m); })
CASE(pthread_condattr_init, { pthread_condattr_t a; pthread_condattr_init(&a); })
CASE(pthread_condattr_setclock, { pthread_condattr_t a; pthread_condattr_init(&a); pthread_condattr_setclock(&a, CLOCK_MONOTONIC); })
CASE(pthread_condattr_destroy, { pthread_condattr_t a; pthread_condattr_init(&a); pthread_condattr_destroy(&a); })
CASE(sched_getaffinity, { cpu_set_t set; sched_getaffinity(0, sizeof set, &set); sink = CPU_COUNT(&set); })
CASE(prctl, { char name[17]; prctl(PR_GET_NAME, name); })
CASE(epoll_create1, queue = epoll_create1(0))
CASE(epoll_ctl, { struct epoll_event ev = {.events = EPOLLIN}; epoll_ctl(queue, EPOLL_CTL_ADD, 0, &ev); })
CASE(epoll_wait, { struct epoll_event ev; epoll_wait(queue, &ev, 1, 0); close(queue); })
CASE(__errno_location, sink = *__errno_location())
CASE(__sched_cpucount, { cpu_set_t set; CPU_ZERO(&set); CPU_SET(0, &set); sink = CPU_COUNT(&set); })
#endif

#define MEASURE(name) {#name, NULL, case_##name, false}
#define MEASURE_AFTER(name) {#name, setup_##name, case_##name, false}
#define MEASURE_ENDING(name) {#name, NULL, case_##name, true}

int main(void) {
    struct {
        const char *name;
        void (*setup)(void);
        void (*call)(void);
        bool ends; // the call ends the process
    } cases[] = {
        // First, before any case starts a thread a child of fork would lack.
        MEASURE_ENDING(exit), MEASURE_ENDING(abort), MEASURE_ENDING(__stack_chk_fail),
        MEASURE(malloc), MEASURE(calloc), MEASURE_AFTER(realloc), MEASURE_AFTER(free), MEASURE(aligned_alloc), MEASURE(posix_memalign),
        MEASURE(memcpy), MEASURE(__memcpy_chk), MEASURE(memmove), MEASURE(memset), MEASURE(memcmp), MEASURE(memchr), MEASURE(strlen), MEASURE(strchr),
        MEASURE(strtoul), MEASURE(strerror), MEASURE(gai_strerror), MEASURE(snprintf),
        MEASURE(pthread_mutex_init), MEASURE(pthread_mutex_lock), MEASURE(pthread_mutex_unlock), MEASURE(pthread_cond_init),
        MEASURE(pthread_cond_signal), MEASURE(pthread_cond_broadcast), MEASURE(pthread_cond_timedwait), MEASURE_AFTER(pthread_cond_wait),
        MEASURE(pthread_attr_init), MEASURE_AFTER(pthread_attr_setdetachstate), MEASURE_AFTER(pthread_attr_destroy), MEASURE_AFTER(pthread_create),
        MEASURE(mmap), MEASURE_AFTER(mprotect), MEASURE_AFTER(munmap),
        MEASURE(clock_gettime), MEASURE(nanosleep), MEASURE(sched_yield), MEASURE(sysconf), MEASURE(getpid), MEASURE(getrlimit),
        MEASURE(setrlimit), MEASURE(getentropy), MEASURE(gethostname),
        MEASURE(socket), MEASURE_AFTER(setsockopt), MEASURE_AFTER(bind), MEASURE(listen), MEASURE(getsockname), MEASURE_AFTER(connect), MEASURE(accept),
        MEASURE(fcntl), MEASURE(send), MEASURE(read), MEASURE(write), MEASURE(getsockopt), MEASURE(getpeername), MEASURE(close),
        MEASURE(inet_pton), MEASURE(inet_ntop), MEASURE(if_nametoindex), MEASURE(if_indextoname), MEASURE_AFTER(freeaddrinfo),
        MEASURE_AFTER(open), MEASURE(lseek), MEASURE(fstat), MEASURE(fsync), MEASURE(stat), MEASURE(lstat), MEASURE(chmod), MEASURE(fstatat),
        MEASURE(openat), MEASURE(fdopendir), MEASURE(readdir), MEASURE(rewinddir), MEASURE(dirfd), MEASURE(closedir), MEASURE_AFTER(mkdir),
        MEASURE(rmdir), MEASURE_AFTER(rename), MEASURE(unlinkat), MEASURE_AFTER(unlink), MEASURE(getcwd), MEASURE_AFTER(chdir),
        MEASURE(fopen), MEASURE(fputs), MEASURE(fputc), MEASURE(fwrite), MEASURE(fflush), MEASURE(fclose), MEASURE_AFTER(fscanf),
        MEASURE(sigsetjmp), MEASURE(siglongjmp), MEASURE(thread_local),
        MEASURE(sin), MEASURE(cos), MEASURE(tan), MEASURE(asin), MEASURE(acos), MEASURE(atan), MEASURE(atan2), MEASURE(sinh),
        MEASURE(cosh), MEASURE(tanh), MEASURE(exp), MEASURE(log), MEASURE(log10), MEASURE(log2), MEASURE(pow), MEASURE(fmod),
        MEASURE(hypot), MEASURE(cbrt), MEASURE(sqrt),
#if defined(__APPLE__)
        MEASURE(bzero), MEASURE(clock_gettime_nsec_np), MEASURE(sysctlbyname), MEASURE(os_unfair_lock_lock),
        MEASURE(os_unfair_lock_unlock), MEASURE(kqueue), MEASURE(kevent), MEASURE(__error),
#else
        MEASURE(pthread_mutex_destroy), MEASURE(pthread_condattr_init), MEASURE(pthread_condattr_setclock),
        MEASURE(pthread_condattr_destroy), MEASURE(sched_getaffinity), MEASURE(prctl), MEASURE(epoll_create1), MEASURE(epoll_ctl),
        MEASURE(epoll_wait), MEASURE(__errno_location), MEASURE(__sched_cpucount),
#endif
    };

    setvbuf(stdout, NULL, _IOLBF, 0); // a line as each case ends
    for (size_t i = 0; i < sizeof cases / sizeof cases[0]; i++) {
        if (cases[i].setup != NULL) {
            cases[i].setup();
        }

        size_t worst = cases[i].ends ? run_ending(cases[i].call) : run(cases[i].call);
        printf("%s %zu\n", cases[i].name, worst);
    }

    return 0;
}
