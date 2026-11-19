// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A panic's trace: the walk over the panicking task's frame pointers,
// the lookup of each return address in the table the compiler wrote,
// snello.h's sn_trace, and the lines written for the frames, one a
// frame. Nothing here allocates: the frames go in a buffer each thread
// owns, and a line is written from a buffer of the frame's size.

#include "internal.h"

#include <stdio.h>
#include <string.h>

// A walk keeps at most trace_frames frames, and a deeper stack is
// shown to that depth; a frame is at least frame_least bytes, the
// saved frame pointer and the return address, and aligned to
// frame_align: sixteen bytes where the convention keeps a frame's
// record there, on Linux and macOS, and on Windows, whose arm64
// convention saves other registers below the record, the eight of a
// pointer.
#if defined(_WIN32)
enum { frame_least = 16, frame_align = 8, no_region = -1 };
#else
enum { frame_least = 16, frame_align = 16, no_region = -1 };
#endif

// Where sn_setjmp_regs keeps each register of Windows' x64 convention in
// its buffer, by word, which the unwinder starts from.
enum {
    saved_rbx = 0,
    saved_rbp = 1,
    saved_rdi = 2,
    saved_rsi = 3,
    saved_r12 = 4,
    saved_r13 = 5,
    saved_r14 = 6,
    saved_r15 = 7,
    saved_rsp = 8,
    saved_rip = 9,
};

static _Thread_local uintptr_t frames[SN_TRACE_FRAMES]; // the return addresses, innermost first
static _Thread_local int nframes;

// The table's parts, each at its offset after sn_trace_parts.
static const sn_trace_anchor *anchors(void) { return (const sn_trace_anchor *)(sn_trace_parts + sn_trace.anchors); }
static const sn_trace_row *rows(void) { return (const sn_trace_row *)(sn_trace_parts + sn_trace.rows); }
static const sn_trace_frame *frames_of(void) { return (const sn_trace_frame *)(sn_trace_parts + sn_trace.frames); }
static const char *named(int64_t offsets, int32_t i) {
    return (const char *)sn_trace_parts + sn_trace.strings + ((const int32_t *)(sn_trace_parts + offsets))[i];
}

// slide is what the loader moved the program by from the addresses it
// was linked with, which the anchors hold: the distance of the table
// from where it was linked, which its reference holds.
static uintptr_t slide(void) { return (uintptr_t)&sn_trace - sn_trace.reference; }

// covered reports whether a return address is inside a function of
// the object the program was linked from, and which anchor's, in
// *anchor and *offset; a return address outside every function is the C
// library's or the loader's, where the program's frames end.
static bool covered(uintptr_t address, const sn_trace_anchor **anchor, int64_t *offset) {
    if (sn_trace.reference == SN_TRACE_UNWRITTEN) { // a program built with no table
        return false;
    }

    uintptr_t moved = slide();
    int64_t lo = 0, hi = sn_trace.nanchors; // the anchors are in order of address: the last at or below the address
    while (lo < hi) {
        int64_t mid = lo + (hi - lo) / 2;
        if (anchors()[mid].address + moved <= address) {
            lo = mid + 1;
        } else {
            hi = mid;
        }
    }

    if (lo == 0) {
        return false;
    }

    const sn_trace_anchor *a = &anchors()[lo - 1];
    int64_t d = (int64_t)(address - (a->address + moved));
    if (a->address == 0 || d >= a->size) {
        return false;
    }

    *anchor = a;
    *offset = d;
    return true;
}

// within reports which of the n regions holds a frame at f, or none.
static int within(uintptr_t f, const sn_stack_region *regions, int n) {
    for (int i = 0; i < n; i++) {
        if (f >= (uintptr_t)regions[i].low && f + frame_least <= (uintptr_t)regions[i].high) {
            return i;
        }
    }

    return no_region;
}

// sn_trace_fill walks from fp: a frame is followed only when it is
// aligned, above the last of its region, within one of the n regions
// of stack when any is given, and its return address is inside the
// object, so that a corrupt frame or the C library's, which keeps no
// frame pointer on some systems, ends the walk instead of faulting it.
// A zero frame pointer ends it too: a task's first frame has one, and
// the main thread's chain ends in one where the C library keeps frame
// pointers. A task's regions are its own stack and the guarded one a
// panic moved it to, whose frames link across.
int sn_trace_fill(void *fp, const sn_stack_region *regions, int n) {
    nframes = 0;
    uintptr_t last = 0;
    int region = no_region;
    for (uintptr_t f = (uintptr_t)fp; f != 0 && nframes < SN_TRACE_FRAMES; f = ((uintptr_t *)f)[0]) {
        if (f % frame_align != 0) {
            break;
        }

        if (n > 0) {
            int r = within(f, regions, n);
            if (r < 0) {
                break;
            }

            if (r != region) {
                region = r;
                last = 0;
            }
        }

        if (f <= last) {
            break;
        }

        uintptr_t ret = ((uintptr_t *)f)[1];
        const sn_trace_anchor *a;
        int64_t offset;
        if (ret == 0 || !covered(ret - 1, &a, &offset)) { // less one: inside the call, not after it
            break;
        }

        frames[nframes++] = ret - 1;
        last = f;
    }

    return nframes;
}

#if defined(_WIN32) && defined(__x86_64__)
int sn_trace_unwind(const uint64_t *regs) {
    CONTEXT c = { .ContextFlags = CONTEXT_CONTROL | CONTEXT_INTEGER };
    c.Rbx = regs[saved_rbx];
    c.Rbp = regs[saved_rbp];
    c.Rdi = regs[saved_rdi];
    c.Rsi = regs[saved_rsi];
    c.R12 = regs[saved_r12];
    c.R13 = regs[saved_r13];
    c.R14 = regs[saved_r14];
    c.R15 = regs[saved_r15];
    c.Rsp = regs[saved_rsp];
    c.Rip = regs[saved_rip]; // in sn_panic, whose own frame the first step leaves
    nframes = 0;
    while (nframes < SN_TRACE_FRAMES) {
        DWORD64 base = 0;
        RUNTIME_FUNCTION *fn = RtlLookupFunctionEntry(c.Rip, &base, NULL);
        if (fn == NULL) { // code with no unwind data, which no function of the program's is: the walk ends
            break;
        }

        void *handler = NULL;
        DWORD64 frame = 0;
        RtlVirtualUnwind(UNW_FLAG_NHANDLER, base, c.Rip, fn, &c, &handler, &frame, NULL);
        uintptr_t ret = (uintptr_t)c.Rip;
        const sn_trace_anchor *a;
        int64_t offset;
        if (ret == 0 || !covered(ret - 1, &a, &offset)) { // less one: inside the call, not after it
            break;
        }

        frames[nframes++] = ret - 1;
    }

    return nframes;
}
#endif

int sn_trace_copy(uintptr_t *into) {
    memcpy(into, frames, (size_t)nframes * sizeof frames[0]);
    return nframes;
}

// row_at finds the row that covers offset among an anchor's rows, the
// last whose offset is not past it, or NULL before the first.
static const sn_trace_row *row_at(const sn_trace_anchor *a, int64_t offset) {
    int64_t lo = a->first, hi = a->first + a->count;
    while (lo < hi) { // the first row past offset is at hi when the loop ends
        int64_t mid = lo + (hi - lo) / 2;
        if (rows()[mid].offset <= offset) {
            lo = mid + 1;
        } else {
            hi = mid;
        }
    }

    return hi > a->first ? &rows()[hi - 1] : NULL;
}

// name_of finds the name the program gives a function's symbol, among
// the names in order of symbol, or NULL for a function of the
// runtime's own, which the trace does not show.
static const char *name_of(const char *symbol) {
    int64_t lo = 0, hi = sn_trace_names_count;
    while (lo < hi) {
        int64_t mid = lo + (hi - lo) / 2;
        int c = strcmp(sn_trace_names[mid].symbol, symbol);
        if (c == 0) {
            return sn_trace_names[mid].name;
        }

        if (c < 0) {
            lo = mid + 1;
        } else {
            hi = mid;
        }
    }

    return NULL;
}

// sn_trace_each gives each of the n frames of a walk to line, in turn,
// innermost first: for each, the chain of its row, a frame of a Snello
// function at a time, and nothing for a frame of the runtime's.
void sn_trace_each(const uintptr_t *walked, int n,
                   void (*line)(void *ctx, const char *name, const char *file, int32_t at), void *ctx) {
    for (int i = 0; i < n; i++) {
        const sn_trace_anchor *a;
        int64_t offset;
        if (!covered(walked[i], &a, &offset)) {
            continue;
        }

        const sn_trace_row *r = row_at(a, offset);
        for (int32_t k = r != NULL ? r->frame : SN_TRACE_NONE; k != SN_TRACE_NONE; k = frames_of()[k].caller) {
            const sn_trace_frame *f = &frames_of()[k];
            const char *name = name_of(named(sn_trace.symbols, f->function));
            if (name != NULL) {
                line(ctx, name, named(sn_trace.files, f->file), f->line);
            }
        }
    }
}

// write_line writes one line of a trace to the standard error stream:
// a tab, the name, " at ", the file, a colon and the line; the file
// alone when the optimizer merged the code of two lines and kept no
// line for it.
static void write_line(void *ctx, const char *name, const char *file, int32_t at) {
    (void)ctx;
    char line[SN_TRACE_LINE_ROOM];
    line[0] = '\t';
    int n = sn_trace_line(line + 1, sizeof line - 1, name, file, at);
#if defined(_WIN32)
    (void)sn_write_fd(2, (const uint8_t *)line, n + 1); // a console's own text, for a file's name that is not ASCII
#else
    fwrite(line, 1, (size_t)n + 1, stderr);
#endif
}

// sn_trace_line writes a line of a trace into room bytes at line: the
// name, " at ", the file, a colon and the line number, the file alone
// when the optimizer merged the code of two lines and kept no line for
// it, and a newline, which a line too long for the room is cut to keep.
// It returns the length written.
int sn_trace_line(char *line, size_t room, const char *name, const char *file, int32_t at) {
    int n = at > 0 ? snprintf(line, room, "%s at %s:%d", name, file, (int)at)
                   : snprintf(line, room, "%s at %s", name, file);
    if (n < 0) {
        n = 0;
    }

    if ((size_t)n >= room - 1) {
        n = (int)room - 2;
    }

    line[n++] = '\n';
    return n;
}

void sn_trace_write(void) {
    sn_trace_each(frames, nframes, write_line, NULL);
    nframes = 0;
}
