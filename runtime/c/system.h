// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The system's threads, locks and conditions, as the runtime's files
// call them: on Linux and macOS each is the POSIX thread call itself,
// defined here inline so that it compiles to that call; on Windows each
// is the system's slim reader-writer lock, condition variable and
// thread, the primitives the system's own runtime libraries are built
// on.
#ifndef SN_SYSTEM_H
#define SN_SYSTEM_H

#include <stdatomic.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

// SN_MAX_ALIGN is the alignment that suits a value of any type, which
// the Universal CRT's stddef.h, found before the compiler's own, does
// not give as max_align_t: on Windows sixteen, the most the system's
// allocator aligns a block to.
#if defined(_WIN32)
#define SN_MAX_ALIGN 16
#else
#define SN_MAX_ALIGN _Alignof(max_align_t)
#endif

// The units a wait's time is converted between, and the sleep a yield
// is on macOS.
enum { sn_ms_per_s = 1000, sn_ns_per_ms = 1000000, sn_ns_per_s = 1000000000, sn_yield_ns = 1000 };

// SN_SYSTEM_CALL marks a wrapper of a system call, which is always
// written into its caller, so that a call of it compiles to the call
// it wraps and nothing more, as if the caller had made it.
#define SN_SYSTEM_CALL static inline __attribute__((always_inline))

#if defined(_WIN32)

#define WIN32_LEAN_AND_MEAN
#include <windows.h>

#include <malloc.h>

// An sn_mutex is a lock no thread holds when it is SN_MUTEX_INIT, and
// an sn_cond a condition no thread waits on when SN_COND_INIT.
typedef SRWLOCK sn_mutex;
typedef CONDITION_VARIABLE sn_cond;
#define SN_MUTEX_INIT SRWLOCK_INIT
#define SN_COND_INIT  CONDITION_VARIABLE_INIT

// sn_thread_start_args is what a thread sn_thread_start starts runs:
// the function and its argument, in a block the thread frees.
typedef struct {
    void *(*main)(void *);
    void *arg;
} sn_thread_start_args;

SN_SYSTEM_CALL void sn_mutex_init(sn_mutex *m) { InitializeSRWLock(m); }
SN_SYSTEM_CALL void sn_mutex_lock(sn_mutex *m) { AcquireSRWLockExclusive(m); }
SN_SYSTEM_CALL void sn_mutex_unlock(sn_mutex *m) { ReleaseSRWLockExclusive(m); }
SN_SYSTEM_CALL void sn_cond_wait(sn_cond *c, sn_mutex *m) { SleepConditionVariableSRW(c, m, INFINITE, 0); }
SN_SYSTEM_CALL void sn_cond_signal(sn_cond *c) { WakeConditionVariable(c); }
SN_SYSTEM_CALL void sn_cond_broadcast(sn_cond *c) { WakeAllConditionVariable(c); }

// sn_cond_wait_ms waits on c, holding m, for at most ms milliseconds,
// and reports whether it was woken before they passed; a wake may come
// with no reason, as with every condition, so the caller looks again.
SN_SYSTEM_CALL bool sn_cond_wait_ms(sn_cond *c, sn_mutex *m, int64_t ms) {
    return SleepConditionVariableSRW(c, m, (DWORD)ms, 0) != 0;
}

// sn_system_random fills buf with n bytes of the system's random
// source and reports whether it did: ProcessPrng, the generator the
// system's other random calls draw from, which never fails once found.
// Its library has no import library to link it by, so it is looked up
// in the system's own directory, where no other library of its name
// can take its place; a seed is drawn once, so the lookup is made at
// the call.
typedef BOOL(WINAPI *sn_process_prng)(PBYTE, SIZE_T);

SN_SYSTEM_CALL bool sn_system_random(void *buf, size_t n) {
    HMODULE lib = LoadLibraryExW(L"bcryptprimitives.dll", NULL, LOAD_LIBRARY_SEARCH_SYSTEM32);
    sn_process_prng prng = lib == NULL ? NULL : (sn_process_prng)(void *)GetProcAddress(lib, "ProcessPrng");
    return prng != NULL && prng(buf, n);
}

// sn_yield gives the processor to another thread ready to run, if any.
SN_SYSTEM_CALL void sn_yield(void) { SwitchToThread(); }

// sn_aligned_alloc returns n bytes at a multiple of align from the C
// library, for a block the runtime keeps until the program ends; the
// Universal CRT has no aligned_alloc, and its own call takes the two
// the other way round.
SN_SYSTEM_CALL void *sn_aligned_alloc(size_t align, size_t n) { return _aligned_malloc(n, align); }

// sn_lock_file and sn_unlock_file hold a stream for a run of writes, so
// that another thread's do not come between them.
SN_SYSTEM_CALL void sn_lock_file(FILE *f) { _lock_file(f); }
SN_SYSTEM_CALL void sn_unlock_file(FILE *f) { _unlock_file(f); }

// sn_page_size is the size of the system's pages, asked of the system
// once, as a guarded stack's spawn reads it.
SN_SYSTEM_CALL size_t sn_page_size(void) {
    static _Atomic size_t page;
    size_t size = atomic_load_explicit(&page, memory_order_relaxed);
    if (size == 0) {
        SYSTEM_INFO info;
        GetSystemInfo(&info);
        size = info.dwPageSize;
        atomic_store_explicit(&page, size, memory_order_relaxed);
    }

    return size;
}

// sn_sleep_ns sleeps for about ns nanoseconds, on a high-resolution
// waitable timer the thread makes the first time, since the system's
// Sleep counts in milliseconds and rounds to its tick of 15.6 ms; on a
// system without one, it sleeps the milliseconds rounded up.
SN_SYSTEM_CALL void sn_sleep_ns(int64_t ns) {
    static _Thread_local HANDLE timer;
    if (timer == NULL) {
        timer = CreateWaitableTimerExW(NULL, NULL, CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_ALL_ACCESS);
    }

    LARGE_INTEGER due = { .QuadPart = -(ns + 99) / 100 }; // relative, in ticks of 100 ns
    if (timer != NULL && SetWaitableTimer(timer, &due, 0, NULL, NULL, FALSE)) {
        WaitForSingleObject(timer, INFINITE);
        return;
    }

    Sleep((DWORD)((ns + sn_ns_per_ms - 1) / sn_ns_per_ms));
}

// A sn_jmp_buf is where a panic's mark keeps the context it jumps back
// to: the runtime's own setjmp, a pair of functions of sched.c's that
// save the registers the system's convention has a callee keep and
// unwind nothing, where the C library's longjmp on Windows unwinds the
// frames between by the system's exception handling, which the
// runtime's own stacks and context switch are not built to be walked
// by. On arm64 they are x19 to x28, the frame and link registers, the
// stack pointer and d8 to d15; on x64 rbx, rbp, rdi, rsi, r12 to r15,
// the stack pointer and the return address, MXCSR and the x87 control
// word, and xmm6 to xmm15, at sixteen bytes each from byte 96.
#if defined(__aarch64__)
typedef uint64_t sn_jmp_buf[21];
#else
typedef uint64_t sn_jmp_buf[32];
#endif
int sn_setjmp_regs(sn_jmp_buf b) __attribute__((returns_twice));
_Noreturn void sn_longjmp_regs(sn_jmp_buf b);
#define sn_setjmp(b)  sn_setjmp_regs(b)
#define sn_longjmp(b) sn_longjmp_regs(b)

// sn_thread_entry is where a thread sn_thread_start starts begins;
// snello.c's, defined once so that the program holds one function of its
// name.
DWORD WINAPI sn_thread_entry(void *p);

// sn_thread_start starts a thread running main(arg) that nothing joins,
// and reports whether the system started it.
SN_SYSTEM_CALL bool sn_thread_start(void *(*main)(void *), void *arg) {
    sn_thread_start_args *a = HeapAlloc(GetProcessHeap(), 0, sizeof *a);
    if (a == NULL) {
        return false;
    }

    a->main = main;
    a->arg = arg;
    HANDLE h = CreateThread(NULL, 0, sn_thread_entry, a, 0, NULL);
    if (h == NULL) {
        HeapFree(GetProcessHeap(), 0, a);
        return false;
    }

    CloseHandle(h);
    return true;
}

#else

#include <errno.h>
#include <pthread.h>
#include <sched.h>
#include <setjmp.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>

#if defined(__APPLE__)
#include <sys/random.h>
#endif

typedef pthread_mutex_t sn_mutex;
typedef pthread_cond_t sn_cond;
#define SN_MUTEX_INIT              PTHREAD_MUTEX_INITIALIZER
#define SN_COND_INIT               PTHREAD_COND_INITIALIZER

SN_SYSTEM_CALL void sn_mutex_init(sn_mutex *m) { pthread_mutex_init(m, NULL); }
SN_SYSTEM_CALL void sn_mutex_lock(sn_mutex *m) { pthread_mutex_lock(m); }
SN_SYSTEM_CALL void sn_mutex_unlock(sn_mutex *m) { pthread_mutex_unlock(m); }
SN_SYSTEM_CALL void sn_cond_wait(sn_cond *c, sn_mutex *m) { pthread_cond_wait(c, m); }
SN_SYSTEM_CALL void sn_cond_signal(sn_cond *c) { pthread_cond_signal(c); }
SN_SYSTEM_CALL void sn_cond_broadcast(sn_cond *c) { pthread_cond_broadcast(c); }

SN_SYSTEM_CALL bool sn_thread_start(void *(*main)(void *), void *arg) {
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
    pthread_t id;
    int code = pthread_create(&id, &attr, main, arg);
    pthread_attr_destroy(&attr);
    return code == 0;
}

// The calls ISO C does not have are macros on Linux and macOS, written
// out where a file uses one, so that only a file that asks the C
// library for the call's declaration, by the feature macros it defines
// first, compiles it, as it compiled the call before.
#define sn_system_random(buf, n)   (getentropy(buf, n) == 0)
#define sn_aligned_alloc(align, n) aligned_alloc(align, n)
#define sn_lock_file(f)            flockfile(f)
#define sn_unlock_file(f)          funlockfile(f)
#define sn_page_size()             ((size_t)sysconf(_SC_PAGESIZE))
#define sn_sleep_ns(ns)                                                                                                \
    do {                                                                                                               \
        struct timespec sn_until_ = { (time_t)((ns) / sn_ns_per_s), (long)((ns) % sn_ns_per_s) };                      \
        nanosleep(&sn_until_, NULL);                                                                                   \
    } while (0)

// sn_yield gives the processor to another thread ready to run, if any.
// On macOS sched_yield lowers the yielding thread's priority for a
// quantum, so a thread that yields while it holds a lock can lose the
// processor for that long to the threads waiting for the lock; a sleep
// of a microsecond gives the processor up and keeps the priority.
#if defined(__APPLE__)
#define sn_yield() sn_sleep_ns(sn_yield_ns)
#else
#define sn_yield() sched_yield()
#endif

// sn_cond_wait_ms is defined where the POSIX clocks it reads are, in a
// file that asks for them.
#if defined(CLOCK_REALTIME)
SN_SYSTEM_CALL bool sn_cond_wait_ms(sn_cond *c, sn_mutex *m, int64_t ms) {
    struct timespec until;
    clock_gettime(CLOCK_REALTIME, &until); // the clock the condition's wait reads
    until.tv_sec += ms / sn_ms_per_s;
    until.tv_nsec += (ms % sn_ms_per_s) * sn_ns_per_ms;
    if (until.tv_nsec >= sn_ns_per_s) {
        until.tv_sec++;
        until.tv_nsec -= sn_ns_per_s;
    }

    return pthread_cond_timedwait(c, m, &until) != ETIMEDOUT;
}
#endif

typedef sigjmp_buf sn_jmp_buf;
#define sn_setjmp(b)  sigsetjmp(b, 0)
#define sn_longjmp(b) siglongjmp(b, 1)

#endif

#endif
