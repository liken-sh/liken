# The machine reports its last crash

Milestone 35. Completed. The next boot preserves the kernel's crash
records from pstore on machineState and publishes a one-line summary
as status.lastCrash.

A kernel panic is the one failure that liken's observability cannot
report. Milestone 15 makes every host log stream a pod's stdout, but a
panic ends the kernel that those relays run on. The trace goes to the
serial console, the baked `panic=10` argument reboots the machine ten
seconds later, and the firmware falls back to the proven slot. The
machine comes back healthy, and no record says why it went down. A
fleet that upgrades itself by trial boots then loses the one fact that
explains a fallback.

## The crash records in pstore

The kernel already keeps this record, and it costs nothing to use. At
the moment of a panic or an oops, the kmsg-dump facility writes the
tail of the kernel log, 10 KiB by default, into the platform store
that the machine gives it. On a UEFI machine, that store is the
firmware's variable memory, through the `efi_pstore` backend. The
record stays through the reboot, in hardware that has no relation to
the disks. On the next boot, the kernel gives the records as plain
files under `/sys/fs/pstore`: a first line that says `Panic#1 Part1`,
then log lines, in parts of about one kilobyte each, because EFI
variables are small.

The backend is a module in the vendored Ubuntu kernel, so the image's
fixed module list now includes `efi-pstore`. It loads at the top of
boot, with the other OS modules. The order is important: the backend
must be registered before this boot's own crashes, not only in time to
read the last boot's records. A machine that panics while storage
settles still leaves a record, because the journal opened first. The
gap is the few hundred milliseconds before the module list loads, and
a panic in that time leaves no record, on any OS built this way.

A BIOS machine has no EFI runtime, so the module does not load, and
the machine has no crash journal in this milestone. The future answer
there is ramoops or pstore-blk. Both need a reserved memory region or
a dedicated block device, and both wait for a reason to exist.

## The boot step: preserve, then clear

Init gains one step, after storage settles (`init/crash.go`). It reads
the records, copies them without change into a crash store on
machineState, one directory per crash named by its moment, and then
deletes the originals. The order is necessary in both directions. The
copy must land first, with the files and their directory synced,
because a delete of a pstore file erases the backing firmware
variable, the only copy of the evidence. The delete must occur,
because variable memory is a few hundred kilobytes shared with the
boot entries, and a journal that small must be empty after every read,
or the next crash has no space to record itself. A boot that stops
between the two steps is safe: the next boot finds the crash directory
already present, does not copy again, and does the clear again.

The crash store keeps the newest ten crashes and prunes the others.
There is no age limit. An old crash stays on record, and its timestamp
gives its age. An operator who does not want it deletes the directory,
and the next boot reports nothing.

A machine whose machineState fell back to memory has no durable place
to preserve to, so the rule inverts: pstore is that machine's durable
store, and the records stay in it. Only crashes older than the newest
one leave, to keep the variable memory from filling. The report is the
same in both cases.

## The stub in status

The facts file holds a summary, and not the trace. Kubernetes reads
Machine status on every list and watch, and the kernel log tail is
kilobytes of text, so status gets one stub, `lastCrash`. The stub has
the time of the crash, the kernel's reason word (`Panic` or `Oops`),
the kernel's own message line, and the directory that holds the full
records. The time comes from the machine's own clock at the moment of
the crash, usually the hardware clock, because a crash rarely waits
for the boot's first sync. The reason field is an open string, not an
enum, because the vocabulary belongs to the kernel, and a status write
must not fail because of a new word.

Every boot derives the stub again from the preserved records. This is
the status file's reconstructibility rule: erase the Machine status,
reboot the machine, and the same fact comes back, because the store is
the fact and the status is only a reading of it. The same line prints
on the console at every boot that holds a record, in the same words,
which is the console-parity principle.

The operator changes nothing. The facts file is the base of the
Machine status, copied whole, so the new fact goes to the API as soon
as the schema permits it. The schema change is the necessary half,
because the API server prunes what the schema does not declare.

## What this milestone leaves out

Retrieval stays manual. The full records are in the crash store, and
the stub names the directory. A shell, a debug pod, or the stick in a
dead machine reads them. A `machine-logs` container that publishes
them to the log plane was designed and set aside, because the records
arrive only once per crash. The other deferral is the silent hang: a
machine that stops without a panic records nothing, and only a
hardware watchdog finds it. That is its own milestone, if the project
needs one.

## The drill

The lab proof destroys the machine on purpose, in two ways. By hand:
boot a UEFI guest, run `echo c > /proc/sysrq-trigger`, and watch the
next boot print the crash line and publish `lastCrash` with the sysrq
message. By release machinery: a `FAULT=panic` release panics PID 1 at
startup, the kernel panic follows, and the record stays through the
fallback to the proven slot. The machine came back on the old release,
and the status says why.
