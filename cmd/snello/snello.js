// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Snello's support for Windows' debuggers, cdb and WinDbg: the tasks
// command, !tasks, which lists a debug build's tasks with their states,
// as snello.py's tasks command does for lldb and gdb, and asks the
// debugger for a waiting task's frames, which it does not give yet. The values of a program, its strings, slices, options,
// results, enums and maps, are shown by the visualizers the build
// embeds in the program's PDB. snello debug loads the script with
// .scriptload.

"use strict";

// What the runtime and the compiler publish: the table of a debug
// build's runtime, and the trace's table of names.
const DEBUGGER_TABLE = "sn_debugger";
const TRACE_NAMES = "sn_trace_names";
const TRACE_NAMES_COUNT = "sn_trace_names_count";
const GENERATED_FILE = "<generated>";

// The table's form: the address of the list's head, then its 32-bit
// numbers in this order.
const TABLE_FIELDS = ["next", "sp", "state", "run", "saved_fp", "saved_pc", "resumed_sp", "stack_room", "running", "runnable", "parking", "parked", "done", "woken"];
const WORD = 8;
const NUMBER = 4;
const NAME_PAIR = 2 * WORD;
const TEXT_LIMIT = 4096; // the longest name read
const TEXT_CHUNK = 64; // the bytes of a name read at once
const MAX_FRAMES = 128;
const DECIMAL = "0n"; // a number's prefix in the debugger's commands, whose radix may be sixteen

// How the tasks are listed.
const NOT_DEBUG_BUILD = "no tasks to list: the program is not a debug build of Snello";
const NO_TASKS = "no task listed yet";
const TABLE_UNREADABLE = "no tasks to list: the runtime's table cannot be read";
const STATE_NAMES = { running: "running", runnable: "ready", parking: "parking", parked: "parked" };
const NOT_STARTED = "not started";
const UNREADABLE = "<unreadable>";
const WAITING = ["parked", "runnable"];

// The debugger's text this reads: the address x prints a symbol at, a
// frame of k's, its module, function and offset and its source line,
// and a backtick inside an address.
const SYMBOL_ADDRESS = /^([0-9a-f`]+)\s/i;
const FRAME_FUNCTION = /!([^+\s]+)/;
const FRAME_SOURCE = /\[(.*) @ (\d+)\]/;
const ADDRESS_TICK = /`/g;

function initializeScript() {
    return [new host.apiVersionSupport(1, 7), new host.functionAlias(tasks, "tasks")];
}

// run runs a debugger command and returns the lines it printed.
function run(command) {
    const out = [];
    for (const line of host.namespace.Debugger.Utility.Control.ExecuteCommand(command)) {
        out.push(String(line));
    }

    return out;
}

// address returns the address of the program's symbol name, or null.
function address(name) {
    for (const line of run("x *!" + name)) {
        const m = SYMBOL_ADDRESS.exec(line.trim());
        if (m !== null && line.indexOf("!" + name) >= 0) {
            return host.parseInt64(m[1].replace(ADDRESS_TICK, ""), 16);
        }
    }

    return null;
}

// word returns the 64-bit word at at as an Int64, which a small value
// read may not come back as.
function word(at) {
    const value = host.memory.readMemoryValues(at, 1, WORD)[0];
    return typeof value === "number" ? host.Int64(value) : value;
}

function number(at) {
    return host.memory.readMemoryValues(at, 1, NUMBER, true)[0];
}

// text returns the UTF-8 text at at, up to its NUL, read a chunk at a
// time, and a byte at a time where a chunk would run into memory that
// cannot be read.
function text(at) {
    const bytes = [];
    if (at.compareTo(0) == 0) {
        return "";
    }

    while (bytes.length < TEXT_LIMIT) {
        let chunk;
        try {
            chunk = host.memory.readMemoryValues(at.add(bytes.length), TEXT_CHUNK, 1);
        } catch (e) {
            chunk = host.memory.readMemoryValues(at.add(bytes.length), 1, 1);
        }

        for (const b of chunk) {
            if (b == 0) {
                return utf8(bytes);
            }

            bytes.push(b);
        }
    }

    return utf8(bytes);
}

// utf8 decodes the bytes of UTF-8 text, as the compiler writes names.
function utf8(bytes) {
    let out = "";
    for (let i = 0; i < bytes.length; ) {
        const lead = bytes[i];
        const size = lead < 0x80 ? 1 : lead < 0xe0 ? 2 : lead < 0xf0 ? 3 : 4;
        let point = size == 1 ? lead : lead & (0xff >> (size + 1));
        for (let k = 1; k < size && i + k < bytes.length; k++) {
            point = (point << 6) | (bytes[i + k] & 0x3f);
        }

        out += String.fromCodePoint(point);
        i += size;
    }

    return out;
}

// programNames returns the names the program gives its functions, by
// symbol, from the trace's table.
function programNames() {
    const names = {};
    const base = address(TRACE_NAMES);
    const countAt = address(TRACE_NAMES_COUNT);
    if (base === null || countAt === null) {
        return names;
    }

    const count = word(countAt);
    for (let i = 0; count.compareTo(i) > 0; i++) {
        const entry = base.add(i * NAME_PAIR);
        names[text(word(entry))] = text(word(entry.add(WORD)));
    }

    return names;
}

// unwind returns a waiting task's frames, innermost first, as the
// debugger unwinds them from the task's saved context: its frame
// pointer, the stack pointer the switch's caller resumes with, and the
// return address the switch saved, since the frame pointers of x86-64
// make no chain on Windows. Each frame is its function and its
// source's file and line, or null for one with none. With them come
// the command and what the debugger printed of it, or the error a
// context that cannot be read gave, which taskList shows, for a reader
// of a task whose frames are missing.
function unwind(table, task) {
    const out = { frames: [], command: null, printed: [] };
    try {
        const sp = word(task.add(table.sp));
        const fp = word(sp.add(table.saved_fp));
        const pc = word(sp.add(table.saved_pc));
        const resumed = sp.add(table.resumed_sp);
        out.command = "k = 0x" + fp.toString(16) + " 0x" + resumed.toString(16) + " 0x" + pc.toString(16) + " " + DECIMAL + MAX_FRAMES;
        out.printed = run(out.command);
    } catch (e) {
        out.printed.push(String(e));
        return out;
    }

    for (const line of out.printed) {
        const f = FRAME_FUNCTION.exec(line);
        if (f === null) {
            continue;
        }

        const s = FRAME_SOURCE.exec(line);
        out.frames.push({ symbol: f[1], file: s === null ? null : s[1], line: s === null ? 0 : parseInt(s[2], 10) });
    }

    return out;
}

// taskList returns the tasks of a debug build, oldest first, as data a
// debugger's dx shows: each task's number, its state, the function a
// task not started runs, and the unwind of a parked or ready one, its
// frames, every one, with the command and what it printed; or the text
// that says why there is no list.
function taskList() {
    const at = address(DEBUGGER_TABLE);
    if (at === null) {
        return NOT_DEBUG_BUILD;
    }

    let table;
    try {
        table = { head: word(at) };
        TABLE_FIELDS.forEach((field, i) => {
            table[field] = number(at.add(WORD + i * NUMBER));
        });
    } catch (e) {
        return TABLE_UNREADABLE;
    }

    const all = [];
    const seen = new Set();
    try {
        for (let task = word(table.head); task.compareTo(0) != 0 && !seen.has(task.toString()); task = word(task.add(table.next))) {
            seen.add(task.toString());
            all.push(task);
        }
    } catch (e) {}

    all.reverse();
    const waiting = WAITING.map((k) => table[k]);
    const out = [];
    all.forEach((task, i) => {
        const shown = { number: i + 1, state: UNREADABLE, entry: null, frames: [], unwind: null };
        let state, sp, started;
        try {
            state = number(task.add(table.state)) & ~table.woken;
            sp = word(task.add(table.sp));
            started = sp.compareTo(0) == 0 || word(sp.add(table.saved_fp)).compareTo(0) != 0;
        } catch (e) {
            out.push(shown);
            return;
        }

        if (state == table.done) {
            return;
        }

        if (waiting.includes(state) && !started) {
            shown.state = NOT_STARTED;
            shown.entry = UNREADABLE;
            try {
                const f = FRAME_FUNCTION.exec(run("ln 0x" + word(task.add(table.run)).toString(16)).join(" "));
                shown.entry = f === null ? shown.entry : f[1];
            } catch (e) {}

            out.push(shown);
            return;
        }

        const label = Object.keys(STATE_NAMES).find((k) => table[k] == state);
        shown.state = label === undefined ? String(state) : STATE_NAMES[label];
        if (waiting.includes(state) && sp.compareTo(0) != 0) { // a running task's frames are its thread's, and a parking one's context is not whole yet
            const u = unwind(table, task);
            shown.frames = u.frames;
            shown.unwind = { command: u.command, printed: u.printed };
        }

        out.push(shown);
    });

    return out;
}

// tasks writes each task of a debug build with its state, and the
// frames of a parked or ready task the debugger gives, as a panic's
// trace shows them.
function tasks() {
    const log = (s) => host.diagnostics.debugLog(s + "\n");
    const list = taskList();
    if (typeof list === "string") {
        log(list);
        return;
    }

    if (list.length == 0) {
        log(NO_TASKS);
    }

    let names = {};
    try {
        names = programNames();
    } catch (e) {}

    const shownNames = new Set(Object.values(names)); // a debugger may give a function of the program's own its name and not its symbol
    for (const task of list) {
        if (task.state == NOT_STARTED) {
            log("task " + task.number + ": " + NOT_STARTED + ", " + (names[task.entry] || task.entry));
            continue;
        }

        log("task " + task.number + ": " + task.state);
        for (const f of task.frames) {
            const name = names[f.symbol] || (shownNames.has(f.symbol) ? f.symbol : null);
            if (name === null || f.file === null || f.line == 0) {
                continue;
            }

            const file = f.file.split(/[\\/]/).pop();
            if (file == GENERATED_FILE) {
                continue;
            }

            log("\t" + name + " at " + file + ":" + f.line);
        }
    }
}
