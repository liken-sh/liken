# btmon on request

Plan 11. Built 2026-10-07. The drill in "How it will be proved" has not run.

It makes the `btmon` sidecar's trace a setting of the radio,
`Adapter.spec.btmon`, off by default. A person turns the trace on for
one radio while they chase a fault, and a change takes effect in
seconds with no restart of the pod.

## The problem

The `btmon` sidecar traces the radio's HCI link and the management
channel into its container log, on every radio, all the time. It exists
because a stall of a bonded controller can last for hours, and the fix
is a roll of the pod, which ends every process that saw the stall. The
trace keeps the evidence in the log.

The trace is verbose, and it holds keys. btmon decodes each packet in
full, and it prints key material in plain text: the link keys in
`Load Link Keys`, the long term keys, and the radio's identity key in
`Set Privacy` (read in `monitor/packet.c` in BlueZ 5.82). A drill of
plan 10 on 2026-10-07 found the identity key in the `btmon` log on
`stick-1` (measured). Anybody who can read the pod's logs can read
those keys.

btmon has no setting that shortens its packet output. Its `--priority`
flag filters only the log messages that daemons send through the
monitor channel.

## The design

### The field

`Adapter.spec.btmon` is a boolean. An absent field is `false`. With
`true`, the `btmon` container traces that radio as it does today. With
`false`, the container runs no `btmon` and writes nothing.

The container stays in the pod either way, because a `DaemonSet`'s pod
template is the same for every radio.

### From the field to the container

The `btmon` container runs a small Go program, `start-btmon`, in the
`bluetoothd` image, which has no shell. The program reads one file,
`btmon`, in the pod's settings volume. When the file says `true`, it
starts `btmon` with today's arguments as its child, with the same
terminal and the same output. When it says `false` or the file is
missing, it runs no child.

The program opens an inotify watch on the settings directory before it
reads the file, then reads the file once, and starts or stops the
child on each change after that. A watch that fails ends the program,
and the kubelet starts the container again, which opens the watch and
reads the file again. A child that exits on its own is started again
while the file says `true`, after a short delay so that a `btmon` that
fails at once does not spin.

`bondfetch` writes the file before any other container starts, from
the same `Adapter` it reads for privacy, so a radio with the trace on
traces from the adapter's registration, which is when bluetoothd tells
the kernel which bonded devices may reconnect. The operator writes the
file again on each pass when `spec.btmon` differs from it, so a change
reaches the container in seconds and no controller disconnects. The
operator's container mounts the settings volume read-write for this.
It writes only the `btmon` file, never the `privacy` file, which
records the value bluetoothd started with.

### Status

`Adapter.status.btmon` reports the value in the file, and a printer
column shows it. Each change the operator writes posts a
`BtmonChanged` `Event` on the `Adapter` that names the new value.

## The manual

The `Adapter` reference documents the field, the status field, the
printer column, and the `Event`. The page that describes the `btmon`
sidecar today says that the trace is off until a person turns it on,
and that it prints keys in plain text, so a person who turns it on
knows that anybody who reads the pod's logs can read the keys.

## How it will be proved

Tests, on the Go toolchain alone:

- `start-btmon` starts the child when the file says `true`, stops it
  when the file changes to `false`, starts it again on `true`, runs no
  child with no file, and starts a child again after it exits. The
  child is a seam, so a test runs a fake program and no `btmon`.
- `bondfetch` writes `true`, `false`, and `false` for an `Adapter` that
  does not exist.
- The operator writes the file and posts one `BtmonChanged` `Event` when
  the field differs from the file, and writes nothing when they agree.

The drill, on `liken-1`, on the radio on `stick-1`: with the field
absent, the `btmon` container's log stays empty across a controller's
button press. Setting `spec.btmon: true` starts the trace within
seconds with no new pod, and a button press appears in it. Setting it
back to `false` stops the trace, and the pod's restart count does not
change.

## What was considered and set aside

- **Levels of detail.** A wrapper could keep one line for each packet,
  or redact keys from the full output. btmon offers neither, so either
  one is a filter this project maintains over btmon's text, which
  changes between releases. The field is a switch, and a person who
  needs less can read the trace with `grep`.
- **A restart of the pod to apply a change**, as privacy does. The
  kernel forces nothing here, and a change to a log setting should not
  disconnect every controller on the radio.
- **Leaving the trace on by default.** The trace catches a stall that a
  person did not expect, which argues for on. The keys in plain text
  argue for off, and a person who chases a stall turns the trace on.
