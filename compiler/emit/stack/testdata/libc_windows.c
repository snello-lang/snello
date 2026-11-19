// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The stack each function of the system's libraries and of the C
// library the runtime calls on Windows uses, which TestCLibraryStack
// holds the allowances of stack.go to, as libc.c measures them on Linux
// and macOS. Each function runs on a thread of its own, called from a
// frame that paints the stack below it first, downward a byte at a
// time, so that the system's guard page grows the stack page by page as
// it would for the call, and reads after the call returns how far below
// its stack pointer the call wrote. The thread's stack limit, which the
// stack probe of a frame larger than a page compares with, is moved up
// to the caller's frame for the call, as the runtime sets a small
// stack's, so that the probe writes the pages it checks and the paint
// shows them. A function that ends the process runs in a child the
// program starts again under itself as the child's debugger, which
// reads the child's painted stack when the system reports the child's
// end, before its memory goes. A function is driven down its heavier
// paths: a large allocation as well as a small one, a format of every
// kind of value. The program is built without the compiler's own
// versions of the C library's functions, so that every call is made. It
// prints a line a function, its name and its bytes, or its name and why
// it could not be measured, which no reader takes for a figure.

#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2tcpip.h>
#include <mswsock.h>
#include <iphlpapi.h>
#include <windows.h>
#include <winternl.h>
#include <fcntl.h>
#include <io.h>
#include <math.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

#pragma comment(lib, "ws2_32")
#pragma comment(lib, "iphlpapi")
#pragma comment(lib, "ntdll")
#pragma comment(lib, "synchronization")

// painted is how far below the caller's frame the stack is painted, and
// threadStack what the thread a call runs on reserves; paint is the
// byte the stack is painted with; ending is the argument that starts
// the program as the child of a call that ends the process,
// debugWait how long the parent waits for the child's next event, page
// a page's size, and console_chunk the most a write or a read of the
// console the runtime makes takes, in UTF-16 units.
enum { painted = 256 << 10, threadStack = 4 << 20, paint = 0xA5, debugWait = 60000, page = 4096, console_chunk = 256 };
#define ENDING "ending"

static int64_t reached; // how far below the caller's frame the call wrote, or -1
static volatile long sink;
static volatile size_t count = 64;

// Shared state the cases set up before the measured call.
static SRWLOCK lock = SRWLOCK_INIT;
static CONDITION_VARIABLE cond = CONDITION_VARIABLE_INIT;
static volatile LONG signalled, word = 1;
static void *block;
static char text[256];
static wchar_t wide[MAX_PATH], dir[MAX_PATH], path[MAX_PATH], other[MAX_PATH];
static HANDLE file = INVALID_HANDLE_VALUE, port, timer, found_handle = INVALID_HANDLE_VALUE, console_in, console_out;
static WIN32_FIND_DATAW found_data;
static SOCKET server = INVALID_SOCKET, client = INVALID_SOCKET, accepted = INVALID_SOCKET;
static struct sockaddr_in addr;
static ADDRINFOW *found;
static FILE *stream;
static int fd = -1;

// stack_pointer is where the caller's frame ends as it calls: inlined,
// the stack pointer of the function it is written in.
#if defined(__aarch64__)
#define stack_pointer() ({ uintptr_t sp_; __asm__ volatile("mov %0, sp" : "=r"(sp_)); sp_; })
#elif defined(__x86_64__)
#define stack_pointer() ({ uintptr_t sp_; __asm__ volatile("mov %%rsp, %0" : "=r"(sp_)); sp_; })
#endif

// paint_below paints the painted bytes below entry, from entry down, so
// that each page is touched after the one above it, as the guard page
// that grows the stack requires.
static __attribute__((always_inline)) inline void paint_below(uintptr_t entry) {
    for (volatile uint8_t *p = (uint8_t *)entry - 1; p >= (uint8_t *)entry - painted; p--) {
        *p = paint;
    }
}

// deepest reads how far below the frame they end at the painted bytes
// at low, the stack itself or a copy of it, were written, or -1 when
// all of them were, so that the call may have gone further.
static int64_t deepest(const uint8_t *low) {
    size_t i = 0;
    while (i < painted && low[i] == paint) {
        i++;
    }

    return i == 0 ? -1 : (int64_t)(painted - i);
}

// limit_to moves the thread's stack limit to the page of entry and
// returns the limit it was, for the call's stack probe to write every
// page below it that a frame checks.
static void *limit_to(uintptr_t entry) {
    NT_TIB *tib = (NT_TIB *)NtCurrentTeb();
    void *was = tib->StackLimit;
    tib->StackLimit = (void *)(entry & ~(uintptr_t)(page - 1));
    return was;
}

// trampoline paints the stack below its own frame, calls f with the
// stack limit at its frame, and reads how far below its stack pointer f
// wrote. The painting is a loop of its own, not memset, whose frame
// would write where it paints.
static __attribute__((noinline)) DWORD WINAPI trampoline(void *f) {
    uintptr_t entry = stack_pointer();
    paint_below(entry);
    void *was = limit_to(entry);
    ((void (*)(void))f)();
    ((NT_TIB *)NtCurrentTeb())->StackLimit = was;
    reached = deepest((const uint8_t *)(entry - painted));
    return 0;
}

static int64_t run(void (*f)(void)) {
    HANDLE t = CreateThread(NULL, threadStack, trampoline, (void *)f, STACK_SIZE_PARAM_IS_A_RESERVATION, NULL);
    if (t == NULL) {
        return -1;
    }

    WaitForSingleObject(t, INFINITE);
    CloseHandle(t);
    return reached;
}

// ender says where its frame ends on its output for the parent, paints
// as trampoline does after the write, whose frames would write over the
// paint, and calls f, which ends the process, with the stack limit at
// its frame: a call, never a jump, so that f's frame starts below it.
static __attribute__((noinline, disable_tail_calls)) void ender(void (*f)(void)) {
    uintptr_t entry = stack_pointer();
    printf("%llu\n", (unsigned long long)entry);
    fflush(stdout);
    paint_below(entry);
    limit_to(entry);
    f();
}

// run_ending measures a call that ends the process: it runs in a child,
// the program started again with the call's name, under this process as
// its debugger, which reads the child's painted stack when the system
// reports the child's end, before its memory is released. It returns -1
// for a child it could not start, wait for or read.
static int64_t run_ending(const char *name) {
    wchar_t self[MAX_PATH], line[2 * MAX_PATH];
    GetModuleFileNameW(NULL, self, MAX_PATH);
    _snwprintf(line, sizeof line / sizeof line[0], L"\"%ls\" %hs %hs", self, ENDING, name);
    SECURITY_ATTRIBUTES inherit = {sizeof inherit, NULL, TRUE};
    HANDLE out_read, out_write;
    if (!CreatePipe(&out_read, &out_write, &inherit, 0)) {
        return -1;
    }

    SetHandleInformation(out_read, HANDLE_FLAG_INHERIT, 0);
    STARTUPINFOW si = {.cb = sizeof si, .dwFlags = STARTF_USESTDHANDLES, .hStdOutput = out_write, .hStdError = out_write};
    PROCESS_INFORMATION pi;
    if (!CreateProcessW(NULL, line, NULL, NULL, TRUE, DEBUG_ONLY_THIS_PROCESS, NULL, NULL, &si, &pi)) {
        CloseHandle(out_read);
        CloseHandle(out_write);
        return -1;
    }

    CloseHandle(out_write);
    int64_t n = -1;
    for (bool ended = false; !ended;) {
        DEBUG_EVENT ev;
        if (!WaitForDebugEvent(&ev, debugWait)) {
            DebugActiveProcessStop(pi.dwProcessId);
            TerminateProcess(pi.hProcess, 1);
            break;
        }

        if (ev.dwProcessId != pi.dwProcessId) { // none but the child is debugged; a child before it was stopped
            ContinueDebugEvent(ev.dwProcessId, ev.dwThreadId, DBG_CONTINUE);
            continue;
        }

        DWORD how = DBG_CONTINUE;
        switch (ev.dwDebugEventCode) {
        case CREATE_PROCESS_DEBUG_EVENT:
            CloseHandle(ev.u.CreateProcessInfo.hFile);
            break;
        case LOAD_DLL_DEBUG_EVENT:
            CloseHandle(ev.u.LoadDll.hFile);
            break;
        case EXCEPTION_DEBUG_EVENT:
            if (ev.u.Exception.ExceptionRecord.ExceptionCode != EXCEPTION_BREAKPOINT) {
                how = DBG_EXCEPTION_NOT_HANDLED; // the child's own, which ends it as it would without a debugger
            }
            break;
        case EXIT_PROCESS_DEBUG_EVENT: {
            char said[32] = {0};
            DWORD got = 0;
            ReadFile(out_read, said, sizeof said - 1, &got, NULL);
            uintptr_t entry = (uintptr_t)strtoull(said, NULL, 10);
            static uint8_t copy[painted];
            SIZE_T read = 0;
            if (entry != 0 && ReadProcessMemory(pi.hProcess, (void *)(entry - painted), copy, painted, &read) && read == painted) {
                n = deepest(copy);
            }

            ended = true;
            break;
        }
        }

        ContinueDebugEvent(ev.dwProcessId, ev.dwThreadId, how);
    }

    CloseHandle(out_read);
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return n;
}

static DWORD WINAPI nothing(void *a) {
    (void)a;
    return 0;
}

// signaller wakes the case of SleepConditionVariableSRW once it waits.
static DWORD WINAPI signaller(void *a) {
    Sleep(10);
    AcquireSRWLockExclusive(&lock);
    signalled = 1;
    WakeConditionVariable(&cond);
    ReleaseSRWLockExclusive(&lock);
    return (DWORD)(uintptr_t)a;
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
CASE(_aligned_malloc, _aligned_free(_aligned_malloc(1 << 16, 64)))
CASE(memcpy, memcpy(text, text + 128, count))
CASE(memmove, memmove(text + 1, text, 200))
CASE(memset, memset(text, 1, sizeof text))
CASE(memcmp, sink = memcmp(text, text + 1, 200))
CASE(memchr, sink = memchr(text, 2, sizeof text) != NULL)
CASE(strlen, sink = (long)strlen("hello"))
CASE(strchr, sink = strchr("hello", 'l') != NULL)
CASE(strcmp, sink = strcmp("hello", "help"))
CASE(wcslen, sink = (long)wcslen(L"hello"))
CASE(wcsncmp, sink = wcsncmp(L"hello", L"help", 4))
CASE(strtoul, sink = (long)strtoul("12345", NULL, 10))
CASE(strerror, sink = (long)strlen(strerror(ENOENT)))
CASE(snprintf, snprintf(text, sizeof text, "%s %d %lld %x %p %.3f %g %c", "x", 1, (long long)-1, 255, (void *)text, 3.14159, 1e300, 'z'))
CASE(__stdio_common_vsprintf, snprintf(text, sizeof text, "%s %d %lld %x %p %.3f %g %c", "x", 1, (long long)-1, 255, (void *)text, 3.14159, 1e300, 'z'))
CASE(_errno, sink = *_errno())
CASE(GetLastError, sink = (long)GetLastError())
CASE(SetLastError, SetLastError(ERROR_SUCCESS))
CASE(InitializeSRWLock, { SRWLOCK l; InitializeSRWLock(&l); })
CASE(AcquireSRWLockExclusive, { AcquireSRWLockExclusive(&lock); ReleaseSRWLockExclusive(&lock); })
CASE(ReleaseSRWLockExclusive, { AcquireSRWLockExclusive(&lock); ReleaseSRWLockExclusive(&lock); })
CASE(WakeConditionVariable, WakeConditionVariable(&cond))
CASE(WakeAllConditionVariable, WakeAllConditionVariable(&cond))
static void setup_SleepConditionVariableSRW(void) {
    signalled = 0;
    HANDLE t = CreateThread(NULL, 0, signaller, NULL, 0, NULL);
    CloseHandle(t);
}
CASE(SleepConditionVariableSRW, {
    AcquireSRWLockExclusive(&lock);
    SleepConditionVariableSRW(&cond, &lock, 0, 0);
    while (!signalled) {
        SleepConditionVariableSRW(&cond, &lock, INFINITE, 0);
    }
    ReleaseSRWLockExclusive(&lock);
})
CASE(WaitOnAddress, { LONG differs = 0; WaitOnAddress(&word, &differs, sizeof word, 0); LONG same = 1; WaitOnAddress(&word, &same, sizeof word, 1); })
CASE(WakeByAddressSingle, WakeByAddressSingle((void *)&word))
CASE(CreateThread, CloseHandle(CreateThread(NULL, 0, nothing, NULL, 0, NULL)))
CASE(WaitForSingleObject, { HANDLE t = CreateThread(NULL, 0, nothing, NULL, 0, NULL); WaitForSingleObject(t, INFINITE); CloseHandle(t); })
CASE(Sleep, Sleep(1))
CASE(SwitchToThread, SwitchToThread())
CASE(VirtualAlloc, block = VirtualAlloc(NULL, 1 << 20, MEM_RESERVE | MEM_COMMIT, PAGE_READWRITE))
CASE(VirtualFree, VirtualFree(block, 0, MEM_RELEASE))
CASE(GetSystemInfo, { SYSTEM_INFO si; GetSystemInfo(&si); sink = (long)si.dwPageSize; })
CASE(GetActiveProcessorCount, sink = (long)GetActiveProcessorCount(ALL_PROCESSOR_GROUPS))
CASE(GetProcessAffinityMask, { DWORD_PTR p, s; GetProcessAffinityMask(GetCurrentProcess(), &p, &s); })
CASE(GetCurrentProcess, sink = (long)(uintptr_t)GetCurrentProcess())
CASE(GetCurrentProcessId, sink = (long)GetCurrentProcessId())
CASE(QueryPerformanceCounter, { LARGE_INTEGER c; QueryPerformanceCounter(&c); })
CASE(QueryPerformanceFrequency, { LARGE_INTEGER f; QueryPerformanceFrequency(&f); })
CASE(GetSystemTimePreciseAsFileTime, { FILETIME ft; GetSystemTimePreciseAsFileTime(&ft); })
CASE(GetProcessHeap, sink = (long)(uintptr_t)GetProcessHeap())
CASE(HeapAlloc, block = HeapAlloc(GetProcessHeap(), 0, 1 << 20))
CASE(HeapFree, HeapFree(GetProcessHeap(), 0, block))
CASE(LocalFree, LocalFree(LocalAlloc(LMEM_FIXED, 64)))
CASE(GetModuleHandleW, sink = (long)(uintptr_t)GetModuleHandleW(L"kernel32.dll"))
CASE(GetProcAddress, sink = GetProcAddress(GetModuleHandleW(L"kernel32.dll"), "Sleep") != NULL)
CASE(LoadLibraryExW, sink = LoadLibraryExW(L"bcryptprimitives.dll", NULL, LOAD_LIBRARY_SEARCH_SYSTEM32) != NULL)
CASE(GetCommandLineW, sink = (long)wcslen(GetCommandLineW()))
CASE(GetEnvironmentStringsW, block = GetEnvironmentStringsW())
CASE(FreeEnvironmentStringsW, FreeEnvironmentStringsW(block))
CASE(SetEnvironmentVariableW, { SetEnvironmentVariableW(L"SNELLO_MEASURE", L"x"); SetEnvironmentVariableW(L"SNELLO_MEASURE", NULL); })
CASE(MultiByteToWideChar, MultiByteToWideChar(CP_UTF8, 0, "h\xc3\xa9llo \xe2\x82\xac", -1, wide, MAX_PATH))
CASE(WideCharToMultiByte, WideCharToMultiByte(CP_UTF8, 0, L"héllo €", -1, text, sizeof text, NULL, NULL))
CASE(FormatMessageW, FormatMessageW(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, ERROR_FILE_NOT_FOUND, 0, wide, MAX_PATH, NULL))
CASE(GetComputerNameExW, { DWORD n = MAX_PATH; GetComputerNameExW(ComputerNameDnsHostname, wide, &n); })
CASE(GetTempPathW, GetTempPathW(MAX_PATH, wide))
CASE(GetCurrentDirectoryW, GetCurrentDirectoryW(MAX_PATH, wide))
CASE(SetCurrentDirectoryW, SetCurrentDirectoryW(wide))
CASE(GetFullPathNameW, GetFullPathNameW(L"a\\..\\b.txt", MAX_PATH, wide, NULL))
CASE(CreateIoCompletionPort, port = CreateIoCompletionPort(INVALID_HANDLE_VALUE, NULL, 0, 0))
CASE(PostQueuedCompletionStatus, PostQueuedCompletionStatus(port, 0, 1, NULL))
CASE(GetQueuedCompletionStatusEx, { OVERLAPPED_ENTRY e[4]; ULONG n; GetQueuedCompletionStatusEx(port, e, 4, &n, 0, FALSE); GetQueuedCompletionStatusEx(port, e, 4, &n, 0, FALSE); })
CASE(CreateWaitableTimerExW, timer = CreateWaitableTimerExW(NULL, NULL, CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_ALL_ACCESS))
CASE(SetWaitableTimer, { LARGE_INTEGER due = {.QuadPart = -10000}; SetWaitableTimer(timer, &due, 0, NULL, NULL, FALSE); })
CASE(CancelWaitableTimer, CancelWaitableTimer(timer))
CASE(CloseHandle, { CloseHandle(timer); CloseHandle(port); })
static void setup_CreateFileW(void) {
    GetTempPathW(MAX_PATH, wide);
    _snwprintf(dir, MAX_PATH, L"%lsmeasure%lu", wide, GetCurrentProcessId());
    _snwprintf(path, MAX_PATH, L"%ls\\f", dir);
    _snwprintf(other, MAX_PATH, L"%ls\\g", dir);
}
CASE(CreateDirectoryW, CreateDirectoryW(dir, NULL))
CASE(CreateFileW, file = CreateFileW(path, GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, NULL))
CASE(WriteFile, { DWORD n; WriteFile(file, text, sizeof text, &n, NULL); })
CASE(ReadFile, { DWORD n; SetFilePointer(file, 0, NULL, FILE_BEGIN); ReadFile(file, text, sizeof text, &n, NULL); })
CASE(GetFileInformationByHandle, { BY_HANDLE_FILE_INFORMATION i; GetFileInformationByHandle(file, &i); })
CASE(GetFileInformationByHandleEx, { FILE_ATTRIBUTE_TAG_INFO i; GetFileInformationByHandleEx(file, FileAttributeTagInfo, &i, sizeof i); })
CASE(GetFileSizeEx, { LARGE_INTEGER s; GetFileSizeEx(file, &s); })
CASE(SetFileCompletionNotificationModes, SetFileCompletionNotificationModes(file, FILE_SKIP_SET_EVENT_ON_HANDLE))
CASE(CancelIoEx, CancelIoEx(file, NULL))
static void setup__open_osfhandle(void) { CloseHandle(file); file = CreateFileW(path, GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, 0, NULL); }
CASE(_open_osfhandle, fd = _open_osfhandle((intptr_t)file, _O_BINARY))
CASE(_get_osfhandle, sink = (long)_get_osfhandle(fd))
CASE(_setmode, _setmode(fd, _O_BINARY))
CASE(_lseeki64, _lseeki64(fd, 0, SEEK_END))
CASE(_commit, _commit(fd))
CASE(_close, _close(fd))
CASE(GetFileAttributesW, sink = (long)GetFileAttributesW(path))
CASE(SetFileAttributesW, SetFileAttributesW(path, FILE_ATTRIBUTE_NORMAL))
CASE(FindFirstFileExW, { _snwprintf(wide, MAX_PATH, L"%ls\\*", dir); found_handle = FindFirstFileExW(wide, FindExInfoBasic, &found_data, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH); })
CASE(FindNextFileW, while (FindNextFileW(found_handle, &found_data)) {})
CASE(FindClose, FindClose(found_handle))
CASE(MoveFileExW, MoveFileExW(path, other, MOVEFILE_REPLACE_EXISTING))
CASE(DeleteFileW, DeleteFileW(other))
CASE(RemoveDirectoryW, RemoveDirectoryW(dir))
CASE(RtlInitUnicodeString, { UNICODE_STRING s; RtlInitUnicodeString(&s, L"\\Device\\Afd\\Snello"); })
CASE(NtCreateFile, { HANDLE h; IO_STATUS_BLOCK io; OBJECT_ATTRIBUTES a = {.Length = sizeof a}; sink = NtCreateFile(&h, SYNCHRONIZE, &a, &io, NULL, 0, 0, FILE_OPEN, 0, NULL, 0); })
CASE(NtDeviceIoControlFile, { IO_STATUS_BLOCK io; sink = NtDeviceIoControlFile(NULL, NULL, NULL, NULL, &io, 0, NULL, 0, NULL, 0); })
CASE(RtlNtStatusToDosError, sink = (long)RtlNtStatusToDosError(STATUS_INVALID_HANDLE))
CASE(RtlLookupFunctionEntry, { DWORD64 base; sink = RtlLookupFunctionEntry((DWORD64)(uintptr_t)&run, &base, NULL) != NULL; })
CASE(RtlVirtualUnwind, {
    CONTEXT context;
    RtlCaptureContext(&context);
    DWORD64 base;
#if defined(_M_ARM64) || defined(__aarch64__)
    DWORD64 pc = context.Pc;
#else
    DWORD64 pc = context.Rip;
#endif
    PRUNTIME_FUNCTION f = RtlLookupFunctionEntry(pc, &base, NULL);
    void *data;
    DWORD64 frame;
    if (f != NULL) {
        RtlVirtualUnwind(UNW_FLAG_NHANDLER, base, pc, f, &context, &data, &frame, NULL);
    }
})
// The console the console's cases read and write, opened once by the
// first that needs it: the process's own, or one it is given when it
// has none, as a test's runner may start it with no console. Its input
// is put in the mode an interactive program reads it in, lines echoed
// as they are typed, and the read's line is typed into it first, as
// the keys that type it, a read with no line waiting for one forever:
// one that cannot be typed whole ends the program.
static void open_console(void) {
    if (console_out != NULL) {
        return;
    }

    AllocConsole(); // fails, and changes nothing, where the process has a console
    console_in = CreateFileW(L"CONIN$", GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, 0, NULL);
    console_out = CreateFileW(L"CONOUT$", GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, 0, NULL);
    SetConsoleMode(console_in, ENABLE_LINE_INPUT | ENABLE_ECHO_INPUT | ENABLE_PROCESSED_INPUT);
}
static void setup_GetConsoleMode(void) { open_console(); }
CASE(GetConsoleMode, { // the console's handles, and the standard error's, a pipe under a test's runner, which is no console
    DWORD mode;
    GetConsoleMode(console_in, &mode);
    GetConsoleMode(console_out, &mode);
    GetConsoleMode(GetStdHandle(STD_ERROR_HANDLE), &mode);
})
static void setup_WriteConsoleW(void) { open_console(); }
CASE(WriteConsoleW, {
    static wchar_t chunk[console_chunk];
    for (int i = 0; i < console_chunk; i++) {
        chunk[i] = i % 64 == 63 ? L'\n' : L'\u00e9';
    }

    DWORD n;
    WriteConsoleW(console_out, L"h\u00e9llo \u20ac\r\n", 9, &n, NULL);
    WriteConsoleW(console_out, chunk, console_chunk, &n, NULL);
})
static void setup_ReadConsoleW(void) {
    open_console();
    const wchar_t typed[] = L"ab\r";
    INPUT_RECORD keys[ARRAYSIZE(typed) - 1] = {0};
    for (size_t i = 0; i < ARRAYSIZE(keys); i++) {
        keys[i].EventType = KEY_EVENT;
        keys[i].Event.KeyEvent.bKeyDown = TRUE;
        keys[i].Event.KeyEvent.wRepeatCount = 1;
        keys[i].Event.KeyEvent.wVirtualKeyCode = typed[i] == L'\r' ? VK_RETURN : 0;
        keys[i].Event.KeyEvent.uChar.UnicodeChar = typed[i];
    }

    DWORD n = 0;
    if (!WriteConsoleInputW(console_in, keys, ARRAYSIZE(keys), &n) || n != ARRAYSIZE(keys)) {
        printf("ReadConsoleW was not measured: its line could not be typed, %lu\n", GetLastError());
        exit(1);
    }
}
CASE(ReadConsoleW, { static wchar_t got[console_chunk]; DWORD n; ReadConsoleW(console_in, got, console_chunk, &n, NULL); })
CASE(WSAStartup, { WSADATA d; WSAStartup(MAKEWORD(2, 2), &d); })
CASE(WSASocketW, client = WSASocketW(AF_INET, SOCK_STREAM, IPPROTO_TCP, NULL, 0, WSA_FLAG_OVERLAPPED | WSA_FLAG_NO_HANDLE_INHERIT))
CASE(setsockopt, { int one = 1; setsockopt(client, SOL_SOCKET, SO_REUSEADDR, (const char *)&one, sizeof one); })
CASE(WSAIoctl, { SOCKET base; DWORD n; WSAIoctl(client, SIO_BASE_HANDLE, NULL, 0, &base, sizeof base, &n, NULL, NULL); })
CASE(ioctlsocket, { u_long yes = 1; ioctlsocket(client, FIONBIO, &yes); yes = 0; ioctlsocket(client, FIONBIO, &yes); })
static void setup_bind(void) {
    server = WSASocketW(AF_INET, SOCK_STREAM, IPPROTO_TCP, NULL, 0, WSA_FLAG_OVERLAPPED);
    addr = (struct sockaddr_in){.sin_family = AF_INET, .sin_addr.s_addr = htonl(INADDR_LOOPBACK)};
}
CASE(bind, bind(server, (struct sockaddr *)&addr, sizeof addr))
CASE(listen, listen(server, 16))
CASE(getsockname, { int n = sizeof addr; getsockname(server, (struct sockaddr *)&addr, &n); })
CASE(connect, connect(client, (struct sockaddr *)&addr, sizeof addr))
CASE(accept, accepted = accept(server, NULL, NULL))
CASE(send, send(client, "x", 1, 0))
CASE(recv, recv(accepted, text, 1, 0))
CASE(getsockopt, { int e; int n = sizeof e; getsockopt(client, SOL_SOCKET, SO_ERROR, (char *)&e, &n); })
CASE(getpeername, { struct sockaddr_storage p; int n = sizeof p; getpeername(client, (struct sockaddr *)&p, &n); })
CASE(WSAGetLastError, sink = WSAGetLastError())
CASE(WSASetLastError, WSASetLastError(0))
CASE(closesocket, { closesocket(accepted); closesocket(client); closesocket(server); })
CASE(htons, sink = htons(80))
CASE(ntohs, sink = ntohs(80))
CASE(inet_pton, { struct in6_addr a; inet_pton(AF_INET6, "fe80::1:2:3:4", &a); inet_pton(AF_INET, "127.0.0.1", &a); })
CASE(inet_ntop, { struct in6_addr a; inet_pton(AF_INET6, "fe80::1:2:3:4", &a); inet_ntop(AF_INET6, &a, text, sizeof text); })
CASE(if_nametoindex, sink = (long)if_nametoindex("loopback_0"))
CASE(if_indextoname, if_indextoname(1, text))
static void setup_FreeAddrInfoW(void) { ADDRINFOW h = {.ai_socktype = SOCK_STREAM}; GetAddrInfoW(L"localhost", NULL, &h, &found); }
CASE(FreeAddrInfoW, FreeAddrInfoW(found))
CASE(__acrt_iob_func, sink = (long)(uintptr_t)__acrt_iob_func(2))
static void setup_fputs(void) { stream = fopen("NUL", "w"); }
CASE(fputs, fputs("x", stream))
CASE(fputc, fputc('y', stream))
CASE(fwrite, fwrite("z", 1, 1, stream))
CASE(__stdio_common_vfprintf, fprintf(stream, "%s %d %lld %x %p %.3f %g %c", "x", 1, (long long)-1, 255, (void *)text, 3.14159, 1e300, 'z'))
CASE(_lock_file, { _lock_file(stream); _unlock_file(stream); })
CASE(_unlock_file, { _lock_file(stream); _unlock_file(stream); })
CASE(fflush, { fflush(stream); fclose(stream); })
CASE(exit, exit(0))
CASE(abort, abort())
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
CASE(ceil, sink = (long)(ceil(2.5) * 100))
CASE(floor, sink = (long)(floor(2.5) * 100))
CASE(round, sink = (long)(round(2.5) * 100))
CASE(trunc, sink = (long)(trunc(2.5) * 100))
CASE(_dsign, sink = _dsign(-2.5))

// report prints a function's line: its name and the bytes it used, or
// its name and why it has no figure.
static void report(const char *name, int64_t bytes) {
    if (bytes < 0) {
        printf("%s was not measured: it could not be run, or wrote below all %d bytes painted\n", name, painted);
        return;
    }

    printf("%s %lld\n", name, (long long)bytes);
}

#define MEASURE(name) {#name, NULL, case_##name}
#define MEASURE_AFTER(name) {#name, setup_##name, case_##name}

int main(int argc, char **argv) {
    struct measure {
        const char *name;
        void (*setup)(void);
        void (*call)(void);
    };
    static const struct measure ending[] = {MEASURE(exit), MEASURE(abort)};
    if (argc == 3 && strcmp(argv[1], ENDING) == 0) { // the child of run_ending
        for (size_t i = 0; i < sizeof ending / sizeof ending[0]; i++) {
            if (strcmp(argv[2], ending[i].name) == 0) {
                ender(ending[i].call);
            }
        }

        return 1;
    }

    static const struct measure cases[] = {
        MEASURE(malloc), MEASURE(calloc), MEASURE_AFTER(realloc), MEASURE_AFTER(free), MEASURE(_aligned_malloc),
        MEASURE(memcpy), MEASURE(memmove), MEASURE(memset), MEASURE(memcmp), MEASURE(memchr), MEASURE(strlen), MEASURE(strchr),
        MEASURE(strcmp), MEASURE(wcslen), MEASURE(wcsncmp), MEASURE(strtoul), MEASURE(strerror), MEASURE(snprintf),
        MEASURE(__stdio_common_vsprintf), MEASURE(_errno), MEASURE(GetLastError), MEASURE(SetLastError),
        MEASURE(InitializeSRWLock), MEASURE(AcquireSRWLockExclusive), MEASURE(ReleaseSRWLockExclusive), MEASURE(WakeConditionVariable),
        MEASURE(WakeAllConditionVariable), MEASURE_AFTER(SleepConditionVariableSRW), MEASURE(WaitOnAddress), MEASURE(WakeByAddressSingle),
        MEASURE(CreateThread), MEASURE(WaitForSingleObject), MEASURE(Sleep), MEASURE(SwitchToThread), MEASURE(VirtualAlloc), MEASURE(VirtualFree),
        MEASURE(GetSystemInfo), MEASURE(GetActiveProcessorCount), MEASURE(GetProcessAffinityMask), MEASURE(GetCurrentProcess),
        MEASURE(GetCurrentProcessId), MEASURE(QueryPerformanceCounter), MEASURE(QueryPerformanceFrequency),
        MEASURE(GetSystemTimePreciseAsFileTime), MEASURE(GetProcessHeap), MEASURE(HeapAlloc), MEASURE(HeapFree), MEASURE(LocalFree),
        MEASURE(GetModuleHandleW), MEASURE(GetProcAddress), MEASURE(LoadLibraryExW), MEASURE(GetCommandLineW), MEASURE(GetEnvironmentStringsW),
        MEASURE(FreeEnvironmentStringsW), MEASURE(SetEnvironmentVariableW), MEASURE(MultiByteToWideChar), MEASURE(WideCharToMultiByte),
        MEASURE(FormatMessageW), MEASURE(GetComputerNameExW), MEASURE(GetTempPathW), MEASURE(GetCurrentDirectoryW), MEASURE(SetCurrentDirectoryW),
        MEASURE(GetFullPathNameW), MEASURE(CreateIoCompletionPort), MEASURE(PostQueuedCompletionStatus), MEASURE(GetQueuedCompletionStatusEx),
        MEASURE(CreateWaitableTimerExW), MEASURE(SetWaitableTimer), MEASURE(CancelWaitableTimer), MEASURE(CloseHandle),
        {"CreateDirectoryW", setup_CreateFileW, case_CreateDirectoryW}, MEASURE(CreateFileW), MEASURE(WriteFile), MEASURE(ReadFile),
        MEASURE(GetFileInformationByHandle), MEASURE(GetFileInformationByHandleEx), MEASURE(GetFileSizeEx),
        MEASURE(SetFileCompletionNotificationModes), MEASURE(CancelIoEx), MEASURE_AFTER(_open_osfhandle), MEASURE(_get_osfhandle),
        MEASURE(_setmode), MEASURE(_lseeki64), MEASURE(_commit), MEASURE(_close), MEASURE(GetFileAttributesW), MEASURE(SetFileAttributesW),
        MEASURE(FindFirstFileExW), MEASURE(FindNextFileW), MEASURE(FindClose), MEASURE(MoveFileExW), MEASURE(DeleteFileW),
        MEASURE(RemoveDirectoryW), MEASURE(RtlInitUnicodeString), MEASURE(NtCreateFile), MEASURE(NtDeviceIoControlFile),
        MEASURE(RtlNtStatusToDosError), MEASURE(RtlLookupFunctionEntry), MEASURE(RtlVirtualUnwind),
        MEASURE_AFTER(GetConsoleMode), MEASURE_AFTER(WriteConsoleW), MEASURE_AFTER(ReadConsoleW),
        MEASURE(WSAStartup), MEASURE(WSASocketW), MEASURE(setsockopt), MEASURE(WSAIoctl), MEASURE(ioctlsocket), MEASURE_AFTER(bind),
        MEASURE(listen), MEASURE(getsockname), MEASURE(connect), MEASURE(accept), MEASURE(send), MEASURE(recv), MEASURE(getsockopt),
        MEASURE(getpeername), MEASURE(WSAGetLastError), MEASURE(WSASetLastError), MEASURE(closesocket), MEASURE(htons), MEASURE(ntohs),
        MEASURE(inet_pton), MEASURE(inet_ntop), MEASURE(if_nametoindex), MEASURE(if_indextoname), MEASURE_AFTER(FreeAddrInfoW),
        MEASURE(__acrt_iob_func), MEASURE_AFTER(fputs), MEASURE(fputc), MEASURE(fwrite), MEASURE(__stdio_common_vfprintf),
        MEASURE(_lock_file), MEASURE(_unlock_file), MEASURE(fflush),
        MEASURE(sin), MEASURE(cos), MEASURE(tan), MEASURE(asin), MEASURE(acos), MEASURE(atan), MEASURE(atan2), MEASURE(sinh),
        MEASURE(cosh), MEASURE(tanh), MEASURE(exp), MEASURE(log), MEASURE(log10), MEASURE(log2), MEASURE(pow), MEASURE(fmod),
        MEASURE(hypot), MEASURE(cbrt), MEASURE(sqrt), MEASURE(ceil), MEASURE(floor), MEASURE(round), MEASURE(trunc), MEASURE(_dsign),
    };

    setvbuf(stdout, NULL, _IONBF, 0); // a line as each case ends
    for (size_t i = 0; i < sizeof ending / sizeof ending[0]; i++) {
        report(ending[i].name, run_ending(ending[i].name));
    }

    for (size_t i = 0; i < sizeof cases / sizeof cases[0]; i++) {
        if (cases[i].setup != NULL) {
            cases[i].setup();
        }

        report(cases[i].name, run(cases[i].call));
    }

    return 0;
}
