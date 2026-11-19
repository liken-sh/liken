# WirePlumber polls for the PipeWire socket

Open question, recorded on 2026-11-18. Nothing fails today. A review
of the comments against the code asked whether the reason for the
timer holds. The code was checked on 2026-11-19: `awaitPipewire` at
`audio-operator/restarts.go:207-217` calls `time.Sleep(pause)` in a
loop until `pipewireReturnLimit`. No unit test can show the question,
because it is about which events PipeWire's socket directory reports.

When WirePlumber exits, `runWireplumber` in
`audio-operator/restarts.go` calls `awaitPipewire` to wait until
PipeWire accepts connections again, for up to `pipewireReturnLimit`,
6 minutes. `awaitPipewire` connects to the socket, and on a failure
sleeps and tries again, with a pause that doubles from 100 ms
(`firstSocketPause`) up to 2 s (`longestSocketPause`). The comment on
the constants gives the reason: PipeWire sends no event that a client
can wait on while it is down.

The root `AGENTS.md`, under "Keep state current with events", names a
timer that reads state again to find a change as a defect, and gives
an inotify watch followed by one look at the directory as a source
that has events. PipeWire creates its socket when it starts, so an
inotify watch on the socket's directory reports the new socket.
`volumeSwaps` in `volumeswaps.go` already watches a directory with
inotify, and `runPipewire` uses it on the drop-in directory. While
PipeWire is down, the timer wakes at most every 2 s, for up to 6
minutes.

## What would confirm it

A test in a `synctest` bubble cannot show an inotify event, so the
question is settled by reading: whether PipeWire creates the socket
before it accepts connections, and whether the socket's directory is
on a file system that reports inotify events to the WirePlumber
container.

## What a fix has to decide

Whether the watch replaces the timer, with one connection attempt
after each event in the socket's directory, or the timer stays and its
comment gives the failure that the watch would miss.
