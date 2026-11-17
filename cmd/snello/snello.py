# Copyright 2026 The Snello Programming Language. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

"""Snello's support for lldb and gdb, which `snello debug` loads.

Values are shown as the program writes them: a string as its text, a
slice as its elements, an option as Some(v) or None, a result as Ok(v)
or Err(e), an enum as its case and fields, and a map as its count. The
command `tasks` lists every task of a debug build with its state, and a
waiting task's stack in the form of a panic's trace.

Both backends describe a type under the C backend's names, so one set
of rules serves both. The tasks are read through the table a debug
build's runtime publishes, sn_debugger, so that this file holds no
layout of the runtime's. The half for lldb or for gdb is chosen by the
debugger that imports the file.
"""

import struct
import sys

# The names the backends describe the runtime's records and the
# compiler's types by: an enum's tag is of the enumeration TAG_PREFIX
# and the enum's name, and each case is CASE_PREFIX, the length of the
# enum's name, the name and the case's, joined by CASE_SEPARATOR; a
# member named as C may not name one has CLASH_SUFFIX after it.
STRING_TYPE = "sn_string"
SLICE_PREFIX = "sn_slice_"
OPTION_PREFIX = "sn_opt_"
RESULT_PREFIX = "sn_res_"
MAP_PREFIX = "sn_map"
TAG_PREFIX = "sn_tag_"
CASE_PREFIX = "sn_case_"
CASE_SEPARATOR = "_"
TAG_MEMBER = "tag"
CLASH_SUFFIX = "_"

# The members of those records.
PTR = "ptr"
LEN = "len"
HAS = "has"
VAL = "val"
OK = "ok"
ERR = "err"
COUNT = "count"

# How a value is shown.
NONE = "None"
SOME = "Some(%s)"
OK_FORMAT = "Ok(%s)"
ERR_FORMAT = "Err(%s)"
CASE_FORMAT = "%s(%s)"
PART_SEPARATOR = ", "
LENGTH = "len=%d"
ELEMENT = "[%d]"
ELEMENT_OPEN, ELEMENT_CLOSE = "[", "]"
QUOTE = '"'
CUT = "..."
UNREADABLE = "<unreadable>"
NO_SUMMARY = "{...}"
NO_INDEX = -1

# What the runtime and the compiler publish for the list of tasks and
# the frames' names: the table of a debug build's runtime, and the
# trace's table of names.
DEBUGGER_TABLE = "sn_debugger"
TRACE_NAMES = "sn_trace_names"
TRACE_NAMES_COUNT = "sn_trace_names_count"
GENERATED_FILE = "<generated>"
PATH_SEPARATOR = "/"

# The table's form: the address of the list's head, then its 32-bit
# numbers in this order.
TABLE_FIELDS = (
    "next",
    "sp",
    "state",
    "run",
    "saved_fp",
    "saved_pc",
    "stack_room",
    "running",
    "runnable",
    "parking",
    "parked",
    "done",
    "woken",
)
WORD = 8
NUMBER = 4
POINTER_FORMAT = "<Q"
NUMBER_FORMAT = "<i"
NAME_PAIR = 2 * WORD

# The walk over a waiting task's frames keeps the panic trace's rules: a
# frame pointer aligned to FRAME_ALIGN, above the last, within the
# task's stack, no more than MAX_FRAMES of them, and none past a return
# address of zero; a name is read TEXT_CHUNK bytes at a time.
FRAME_ALIGN = 16
MAX_FRAMES = 128
NO_ADDRESS = 0
TEXT_CHUNK = 64
TEXT_LIMIT = (
    4096  # the longest name read, so that one with no end in readable memory stops
)
TEXT_END = b"\0"
TEXT_ENCODING = "utf-8"
NO_LINE = 0

# How the tasks are listed.
NOT_DEBUG_BUILD = "no tasks to list: the program is not a debug build of Snello\n"
NO_TASKS = "no task listed yet\n"
TABLE_UNREADABLE = "no tasks to list: the runtime's table cannot be read\n"
TASK_LINE = "task %d: %s\n"
NOT_STARTED = "task %d: not started, %s\n"
FRAME_LINE = "\t%s at %s:%d\n"
STATE_NAMES = {
    "running": "running",
    "runnable": "ready",
    "parking": "parking",
    "parked": "parked",
}
WAITING = ("parked", "runnable")

# lldb's names: the category the formatters are added to, the settings
# that bound what a summary reads, and its own defaults for them when
# they cannot be read.
CATEGORY = "snello"
MAX_SUMMARY = "target.max-string-summary-length"
MAX_CHILDREN = "target.max-children-count"
DEFAULT_SUMMARY = 1024
DEFAULT_CHILDREN = 256

# The runtime's header, whose inline functions gdb is told to skip.
HEADER = "snello.h"


class Unreadable(Exception):
    """A read of the stopped process that returned less than was asked."""


def unpack(data, form, size):
    if len(data) != size:
        raise Unreadable()
    return struct.unpack(form, data)[0]


def case_of(enumerator):
    """The case an enumerator of an enum's tag names: what follows the
    enum's name, whose length the enumerator gives, sn_case_5_Shape_Rect."""
    rest = enumerator[len(CASE_PREFIX) :]
    length, _, named = rest.partition(CASE_SEPARATOR)
    if not length.isdigit():
        return enumerator
    return named[int(length) + len(CASE_SEPARATOR) :]


def payload_members(case):
    """The names the member of an enum's union holding a case's fields
    may have: the case's own, or with CLASH_SUFFIX after it where C may
    not name a member so."""
    return (case, case + CLASH_SUFFIX)


class Memory:
    """What the listing of the tasks reads of the stopped process,
    through the debugger in use."""

    def read(self, address, size):
        raise NotImplementedError

    def symbol_address(self, name):
        raise NotImplementedError

    def function(self, pc):
        raise NotImplementedError

    def line(self, pc):
        raise NotImplementedError

    def word(self, address):
        return unpack(self.read(address, WORD), POINTER_FORMAT, WORD)

    def number(self, address):
        return unpack(self.read(address, NUMBER), NUMBER_FORMAT, NUMBER)

    def text(self, address):
        out = bytearray()
        size = TEXT_CHUNK
        while address != NO_ADDRESS and len(out) < TEXT_LIMIT:
            chunk = self.read(address, size)
            if not chunk:
                if size == 1:
                    break
                size = 1  # a chunk reaching past the readable memory: the rest a byte at a time
                continue
            end = chunk.find(TEXT_END)
            if end >= 0:
                out += chunk[:end]
                break
            out += chunk
            address += len(chunk)
        return out.decode(TEXT_ENCODING, "replace")


def program_names(mem):
    """The names the program gives its functions, by symbol, from the
    trace's table."""
    base = mem.symbol_address(TRACE_NAMES)
    count_at = mem.symbol_address(TRACE_NAMES_COUNT)
    if base is None or count_at is None:
        return {}
    names = {}
    for i in range(mem.word(count_at)):
        entry = base + i * NAME_PAIR
        names[mem.text(mem.word(entry))] = mem.text(mem.word(entry + WORD))
    return names


def frames_of(mem, table, task):
    """The return addresses of a task's frames from its saved context,
    innermost first, by the panic trace's rules; a frame that cannot be
    read ends them."""
    pcs = []
    try:
        sp = mem.word(task + table["sp"])
        fp = mem.word(sp + table["saved_fp"])
        pc = mem.word(sp + table["saved_pc"])
        last = sp
        while pc != NO_ADDRESS and len(pcs) < MAX_FRAMES:
            pcs.append(pc)
            if fp == NO_ADDRESS or fp % FRAME_ALIGN != 0 or fp <= last:
                break
            if fp - sp > table["stack_room"]:
                break
            pc = mem.word(fp + WORD)
            last, fp = fp, mem.word(fp)
    except Unreadable:
        pass
    return pcs


def all_tasks(mem, table):
    """The tasks on the runtime's list, oldest first: the list ends at a
    task met before, or one that cannot be read."""
    tasks, seen = [], set()
    try:
        task = mem.word(table["head"])
        while task != NO_ADDRESS and task not in seen:
            seen.add(task)
            tasks.append(task)
            task = mem.word(task + table["next"])
    except Unreadable:
        pass
    return list(reversed(tasks))


def list_tasks(mem, out):
    """Writes each task of a debug build with its state, and a parked or
    ready task's frames, as a panic's trace shows them."""
    at = mem.symbol_address(DEBUGGER_TABLE)
    if at is None:
        out(NOT_DEBUG_BUILD)
        return
    try:
        table = {"head": mem.word(at)}
        for i, field in enumerate(TABLE_FIELDS):
            table[field] = mem.number(at + WORD + i * NUMBER)
    except Unreadable:
        out(TABLE_UNREADABLE)
        return
    try:
        names = program_names(mem)
    except Unreadable:
        names = {}
    program_names_shown = set(
        names.values()
    )  # a debugger may give a function of the program's own its name and not its symbol
    waiting = tuple(table[k] for k in WAITING)
    tasks = all_tasks(mem, table)
    if not tasks:
        out(NO_TASKS)
    for number, task in enumerate(tasks, 1):
        try:
            state = mem.number(task + table["state"]) & ~table["woken"]
            sp = mem.word(task + table["sp"])
            started = sp == NO_ADDRESS or mem.word(sp + table["saved_fp"]) != NO_ADDRESS
        except Unreadable:
            out(TASK_LINE % (number, UNREADABLE))
            continue
        if state == table["done"]:
            continue
        if state in waiting and not started:
            try:
                entry = mem.function(mem.word(task + table["run"]))
            except Unreadable:
                entry = UNREADABLE
            out(NOT_STARTED % (number, names.get(entry, entry)))
            continue
        label = next(
            (STATE_NAMES[k] for k in STATE_NAMES if table[k] == state), str(state)
        )
        out(TASK_LINE % (number, label))
        if state not in waiting or sp == NO_ADDRESS:
            continue  # a running task's frames are its thread's, and a parking one's context is not whole yet
        for pc in frames_of(mem, table, task):
            symbol = mem.function(pc - 1)  # less one: inside the call, not after it
            where = mem.line(pc - 1)
            name_of = names.get(
                symbol, symbol if symbol in program_names_shown else None
            )
            if name_of is None or where is None:
                continue
            path, line = where
            name = path.rsplit(PATH_SEPARATOR, 1)[-1]
            if name == GENERATED_FILE or line == NO_LINE:
                continue
            out(FRAME_LINE % (name_of, name, line))


if "lldb" in sys.modules:
    import lldb

    def setting(debugger, name, fallback):
        value = lldb.SBDebugger.GetInternalVariableValue(
            name, debugger.GetInstanceName()
        )
        try:
            return int(value.GetStringAtIndex(0))
        except (TypeError, ValueError):
            return fallback

    def shown(v):
        return v.GetSummary() or v.GetValue() or NO_SUMMARY

    def string_summary(valobj, internal_dict):
        n = valobj.GetChildMemberWithName(LEN).GetValueAsSigned()
        ptr = valobj.GetChildMemberWithName(PTR).GetValueAsUnsigned()
        room = setting(valobj.GetTarget().GetDebugger(), MAX_SUMMARY, DEFAULT_SUMMARY)
        if n <= 0 or ptr == NO_ADDRESS:
            return QUOTE + QUOTE
        error = lldb.SBError()
        data = valobj.GetProcess().ReadMemory(ptr, min(n, room), error)
        if not error.Success():
            return UNREADABLE
        more = CUT if n > room else ""
        return QUOTE + data.decode(TEXT_ENCODING, "replace") + more + QUOTE

    class SliceProvider:
        def __init__(self, valobj, internal_dict):
            self.valobj = valobj

        def update(self):
            self.ptr = self.valobj.GetChildMemberWithName(PTR)
            self.elem = self.ptr.GetType().GetPointeeType()
            room = setting(
                self.valobj.GetTarget().GetDebugger(), MAX_CHILDREN, DEFAULT_CHILDREN
            )
            self.n = max(
                0, min(self.valobj.GetChildMemberWithName(LEN).GetValueAsSigned(), room)
            )
            return False

        def num_children(self):
            return self.n

        def get_child_index(self, name):
            try:
                return int(name.lstrip(ELEMENT_OPEN).rstrip(ELEMENT_CLOSE))
            except ValueError:
                return NO_INDEX

        def get_child_at_index(self, i):
            return self.ptr.CreateChildAtOffset(
                ELEMENT % i, i * self.elem.GetByteSize(), self.elem
            )

        def has_children(self):
            return True

    def slice_summary(valobj, internal_dict):
        return (
            LENGTH
            % valobj.GetNonSyntheticValue()
            .GetChildMemberWithName(LEN)
            .GetValueAsSigned()
        )

    def option_summary(valobj, internal_dict):
        val = valobj.GetChildMemberWithName(VAL)
        has = valobj.GetChildMemberWithName(HAS)
        # an option of a pointer, the pointer alone: None when null
        if not has.IsValid():
            return (
                NONE
                if val.GetValueAsUnsigned() == NO_ADDRESS
                else SOME % val.GetValue()
            )
        if has.GetValueAsUnsigned() == 0:
            return NONE
        return SOME % shown(val)

    def result_summary(valobj, internal_dict):
        if valobj.GetChildMemberWithName(OK).GetValueAsUnsigned() != 0:
            return OK_FORMAT % shown(valobj.GetChildMemberWithName(VAL))
        return ERR_FORMAT % shown(valobj.GetChildMemberWithName(ERR))

    def is_enum(sbtype, internal_dict):
        t = sbtype.GetCanonicalType()
        if t.GetNumberOfFields() == 0:
            return False
        first = t.GetFieldAtIndex(0)
        return (
            first.GetName() == TAG_MEMBER
            and TAG_PREFIX in first.GetType().GetCanonicalType().GetName()
        )

    def enum_summary(valobj, internal_dict):
        case = case_of(valobj.GetChildMemberWithName(TAG_MEMBER).GetValue() or "")
        for member in payload_members(case):
            payload = valobj.GetChildMemberWithName(member)
            if payload.IsValid() and payload.GetNumChildren() > 0:
                parts = (
                    shown(payload.GetChildAtIndex(i))
                    for i in range(payload.GetNumChildren())
                )
                return CASE_FORMAT % (case, PART_SEPARATOR.join(parts))
        return case

    def map_summary(valobj, internal_dict):
        return LENGTH % valobj.GetChildMemberWithName(COUNT).GetValueAsSigned()

    class LLDBMemory(Memory):
        def __init__(self, target):
            self.target = target
            self.process = target.GetProcess()

        def read(self, address, size):
            error = lldb.SBError()
            data = self.process.ReadMemory(address, size, error)
            return data if error.Success() and data else b""

        def symbol_address(self, name):
            for s in self.target.FindSymbols(name):
                address = s.GetSymbol().GetStartAddress().GetLoadAddress(self.target)
                if address != lldb.LLDB_INVALID_ADDRESS:
                    return address
            return None

        def function(self, pc):
            return self.target.ResolveLoadAddress(pc).GetSymbol().GetName()

        def line(self, pc):
            entry = self.target.ResolveLoadAddress(pc).GetLineEntry()
            if not entry.IsValid():
                return None
            return entry.GetFileSpec().GetFilename(), entry.GetLine()

    def tasks(debugger, command, result, internal_dict):
        list_tasks(LLDBMemory(debugger.GetSelectedTarget()), result.write)

    def __lldb_init_module(debugger, internal_dict):
        module = __name__
        for command in (
            "type summary add -w %s -F %s.string_summary %s"
            % (CATEGORY, module, STRING_TYPE),
            "type summary add -w %s -x '^%s' -F %s.slice_summary"
            % (CATEGORY, SLICE_PREFIX, module),
            "type synthetic add -w %s -x '^%s' -l %s.SliceProvider"
            % (CATEGORY, SLICE_PREFIX, module),
            "type summary add -w %s -x '^%s' -F %s.option_summary"
            % (CATEGORY, OPTION_PREFIX, module),
            "type summary add -w %s -x '^%s' -F %s.result_summary"
            % (CATEGORY, RESULT_PREFIX, module),
            "type summary add -w %s -x '^%s' -F %s.map_summary"
            % (CATEGORY, MAP_PREFIX, module),
            "type summary add -w %s --recognizer-function %s.is_enum -F %s.enum_summary"
            % (CATEGORY, module, module),
            "type category enable %s" % CATEGORY,
            "command script add -o -f %s.tasks tasks" % module,
        ):
            debugger.HandleCommand(command)

else:
    import gdb

    def typedef_names(t):
        names = []
        while t.code == gdb.TYPE_CODE_TYPEDEF:
            names.append(t.name)
            t = t.target()
        names.append(t.tag or t.name or "")
        return names, t

    def shown(v):
        return str(v)

    class StringPrinter:
        def __init__(self, val):
            self.val = val

        def to_string(self):
            n = int(self.val[LEN])
            if n <= 0 or int(self.val[PTR]) == NO_ADDRESS:
                return QUOTE + QUOTE
            return self.val[PTR].lazy_string(encoding=TEXT_ENCODING, length=n)

        def display_hint(self):
            return "string"

    class SlicePrinter:
        def __init__(self, val):
            self.val = val

        def to_string(self):
            return LENGTH % int(self.val[LEN])

        def children(self):
            ptr = self.val[PTR]
            for i in range(max(0, int(self.val[LEN]))):
                yield ELEMENT % i, (ptr + i).dereference()

        def display_hint(self):
            return "array"

    class OptionPrinter:
        def __init__(self, val, struct_type):
            self.val = val
            self.pointer = HAS not in [
                f.name for f in struct_type.fields()
            ]  # an option of a pointer, the pointer alone

        def to_string(self):
            if self.pointer:
                return (
                    NONE
                    if int(self.val[VAL]) == NO_ADDRESS
                    else SOME % shown(self.val[VAL])
                )
            if not bool(self.val[HAS]):
                return NONE
            return SOME % shown(self.val[VAL])

    class ResultPrinter:
        def __init__(self, val):
            self.val = val

        def to_string(self):
            if bool(self.val[OK]):
                return OK_FORMAT % shown(self.val[VAL])
            return ERR_FORMAT % shown(self.val[ERR])

    class EnumPrinter:
        def __init__(self, val, struct_type):
            self.val = val
            self.struct_type = struct_type

        def to_string(self):
            case = case_of(str(self.val[TAG_MEMBER]))
            members = payload_members(case)
            for f in self.struct_type.fields():
                if f.name is not None:
                    continue
                union = self.val[f]
                for g in f.type.fields():
                    if g.name in members:
                        payload = union[g.name]
                        parts = [shown(payload[h]) for h in payload.type.fields()]
                        return CASE_FORMAT % (case, PART_SEPARATOR.join(parts))
            return case

    class MapPrinter:
        def __init__(self, val):
            self.val = val

        def to_string(self):
            return LENGTH % int(self.val[COUNT])

    def lookup(val):
        names, t = typedef_names(val.type.unqualified())
        if t.code != gdb.TYPE_CODE_STRUCT:
            return None
        for name in names:
            if name == STRING_TYPE:
                return StringPrinter(val)
            if name.startswith(SLICE_PREFIX):
                return SlicePrinter(val)
            if name.startswith(OPTION_PREFIX):
                return OptionPrinter(val, t)
            if name.startswith(RESULT_PREFIX):
                return ResultPrinter(val)
            if name.startswith(MAP_PREFIX):
                return MapPrinter(val)
        fields = t.fields()
        if (
            fields
            and fields[0].name == TAG_MEMBER
            and TAG_PREFIX in str(fields[0].type.strip_typedefs())
        ):
            return EnumPrinter(val, t)
        return None

    class GDBMemory(Memory):
        def __init__(self):
            self.inferior = gdb.selected_inferior()

        def read(self, address, size):
            try:
                return bytes(self.inferior.read_memory(address, size))
            except gdb.MemoryError:
                return b""

        def symbol_address(self, name):
            s = gdb.lookup_global_symbol(name) or gdb.lookup_static_symbol(name)
            if s is not None:
                return int(s.value().address)
            try:
                return int(gdb.parse_and_eval("(unsigned long)&%s" % name))
            except gdb.error:
                return None

        def function(self, pc):
            block = gdb.current_progspace().block_for_pc(pc)
            while block is not None and block.function is None:
                block = block.superblock
            if block is not None:
                return block.function.linkage_name or block.function.name
            text = gdb.execute("info symbol %d" % pc, to_string=True).split()
            return text[0] if text else ""

        def line(self, pc):
            sal = gdb.find_pc_line(pc)
            if sal.symtab is None:
                return None
            return sal.symtab.filename, sal.line

    class Tasks(gdb.Command):
        """List every task of a Snello debug build, and a waiting task's frames."""

        def __init__(self):
            super().__init__("tasks", gdb.COMMAND_STACK)

        def invoke(self, argument, from_tty):
            list_tasks(GDBMemory(), gdb.write)

    gdb.pretty_printers.append(lookup)
    Tasks()
    gdb.execute("skip -gfile */%s" % HEADER, to_string=True)
