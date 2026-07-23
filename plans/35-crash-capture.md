# The machine reports its last crash

Milestone 35 — Done

A kernel panic is the one failure that liken's observability cannot
see. Milestone 15 made every host log stream a pod's stdout, but a
panic ends the kernel that those relays run on. The trace goes to the
serial console, the baked `panic=10` argument reboots the machine ten
seconds later, and the firmware falls back to the proven slot. The
machine returns healthy, and nothing anywhere says why it went down.
For a fleet that upgrades itself by trial boots, that silence is
expensive: the one fact that would explain a fallback is the fact the
system loses.

## pstore, the kernel's own black box

The kernel already has the answer, and it costs nothing to adopt. At
the moment of a panic or an oops, the kmsg-dump facility writes the
tail of the kernel log, 10 KiB by default, into whatever platform
store the machine offers. On a UEFI machine, that store is the
firmware's variable memory, through the `efi_pstore` backend. The
record survives the reboot in hardware that has nothing to do with
the disks. On the next boot, the kernel serves the records as plain
files under `/sys/fs/pstore`: a first line that says `Panic#1 Part1`,
then log lines, split into parts of about one kilobyte each because
EFI variables are small.

The backend is a module in the vendored Ubuntu kernel, so the image's
fixed module list now carries `efi-pstore`. It loads at the top of
boot, with the other OS modules, and the order matters for a reason
that is easy to miss: the backend must be registered before this
boot's own crashes, not just in time to read the last boot's. A
machine that panics while storage settles still leaves a record,
because the journal opened first. The inherent gap is the few hundred
milliseconds before the module list loads; a panic there dies
unrecorded, on any OS built this way.

A BIOS machine has no EFI runtime, so the module refuses to load and
the machine has no crash journal in this milestone. The future answer
there is ramoops or pstore-blk, which need a reserved memory region
or a dedicated block device; both wait for a reason to exist.

## The boot step: preserve, then clear

Init gains one step, after storage settles (`init/crash.go`). It
reads the records, copies them verbatim into a crash store on
machineState, one directory per crash named by its moment, and only
then deletes the originals. The order is load-bearing in both
directions. The copy must land first, with the files and their
directory synced, because deleting a pstore file erases the backing
firmware variable, the only copy of the evidence. And the delete must
happen, because variable memory is a few hundred kilobytes shared
with the boot entries: a journal that small must be emptied after
every read, or the next crash finds no room to record itself. A boot
that dies between the two steps is safe: the next boot finds the
crash directory already present, skips the copy, and retries only the
clear.

The crash store keeps the newest ten crashes and prunes the rest.
There is no age bound. An old crash stays on record, and its
timestamp says how old the news is; an operator who wants it gone
deletes the directory, and the next boot reports nothing.

A machine whose machineState fell back to memory has nowhere durable
to preserve to, so the rule inverts: pstore is that machine's durable
store, and the records stay in it. Only crashes older than the newest
leave, to keep the variable memory from filling. The report works the
same either way.

## The stub in status

The facts file carries a summary, not the trace. Machine status is
read on every list and watch, and the kernel log tail is kilobytes of
text, so status gets one stub, `lastCrash`: the time (the machine's
own clock at the moment of the crash, usually the hardware clock,
because a crash rarely waits for the boot's first sync), the kernel's
reason word (`Panic` or `Oops`), the kernel's own message line, and
the directory that holds the full records. The reason field is an
open string, not an enum, because the vocabulary belongs to the
kernel, and a status write must not fail over a new word.

Every boot re-derives the stub from the preserved records. This is
the status file's reconstructibility rule doing its work: erase the
Machine status, reboot the machine, and the same fact comes back,
because the store is the fact and the status is only a reading of it.
The same line prints on the console at every boot that holds a
record, in the same words, which is the console-parity principle.

The operator changes not at all. The facts file is the base of the
Machine status, copied wholesale, so the new fact flows to the API
the moment the schema allows it. The schema change is the load-bearing
half: the API server prunes what the schema does not declare.

## What this milestone leaves out

Retrieval stays manual. The full records sit in the crash store, and
the stub names the directory; a shell, a debug pod, or the stick in a
dead machine reads them. A `machine-logs` container that publishes
them to the log plane was designed and set aside, unconvincing for
records that arrive once per crash. The other deferral is the silent
hang: a machine that wedges without panicking records nothing, and
only a hardware watchdog catches it. That is its own milestone, if it
earns one.

## The drill

The lab proof is deliberate destruction, twice over. Once by hand:
boot a UEFI guest, `echo c > /proc/sysrq-trigger`, and watch the next
boot print the crash line and publish `lastCrash` with the sysrq
message. Once by release machinery: a `FAULT=panic` release panics
PID 1 at startup, the kernel panic follows, and the record rides the
fallback to the proven slot, which is exactly the story this
milestone exists to tell: the machine came back on the old release,
and now it says why.
