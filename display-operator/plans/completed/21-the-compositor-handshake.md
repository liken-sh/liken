# The compositor handshake

Plan 21. Built on 2026-09-16.

The operator's liveness check on the compositor is a Wayland exchange.
The probe dials the socket, sends `wl_display.sync`, and waits for the
reply, so a compositor whose process is frozen reads the same as one
whose process exited: not serving. Every reader of the compositor's
state takes the same probe: the once-a-second socket watch, the pass
that publishes the slice, the placement pass, and the claim prepare.

## The problem

A weston stopped with `SIGSTOP` keeps its socket accepting. The kernel
completes a connect on a listening socket whether or not the process
behind it runs, so the check that only connected and closed read the
compositor as serving. On the testbed it read serving for 124 s, until
weston's listen backlog of 128 filled with the operator's own
once-a-second connects and the kernel refused the next one. The screen
did not change for any of those 124 s, the `Display` reported
`Connected` True with every surface still in `status.surfaces`, and the
screen's owner deleted the pods by hand.

A crash is the case the operator already handled. On the same testbed
a killed compositor tainted the slices 2.6 s after the kill, and the
client was back 13 s after the compositor returned. A crash that lasted
under a second never reached the slice, and the client's own window
watchdog healed it in 16 s.

## The design

`probeCompositor` in `weston.go` dials the socket, sends
`wl_display.sync` on object 1 through the operator's own wire code, and
waits up to 2 s for any event. A running event loop answers with the
callback's `done` event in microseconds; the rest of the bound is
margin for a loaded machine. Only a running event loop answers, so the
reply is the fact the connect could not give.

The probe reports two failures, because they have two repairs. `Down`
is a socket that refuses the connect or ends it under the probe, which
is a container the kubelet starts again on its own. `Hung` is a socket
that accepts and answers nothing within the bound, which is a process
that is still running, so the kubelet restarts nothing. A frozen compositor
whose backlog has already filled refuses the connect and reads `Down`,
which is still not serving. Every failure carries the socket's own
words, and a prepare the probe refuses puts the reason and those words
in its error.

The socket watch repairs a `Hung` compositor. The kubelet restarts a
process that exits, and a frozen process does not exit. A stopped
process takes no `SIGTERM` either, so the restart the mode path and the
canvas heal order does not reach it. The watch is the one reader that
probes on a clock, so it is the one reader that measures how long a
freeze has lasted. A clock, `hungCompositor`, counts continuous `Hung`
readings on the once-a-second watch. When `Hung` has held for
`compositorHungLimit`, 10 s, the watch sends `SIGKILL` to the weston
process through the pod's shared process namespace, the same namespace
the mode path already uses for `SIGTERM`. It sends one kill per outage.
Any `Down` or `Serving` reading ends the outage and resets the clock. A
kill that fails is logged and retried on the next tick. The compositor
exits on `SIGKILL`, the kubelet starts the container again, and the
`Down` that follows is the path the taint and the clients already take.

The kill takes the same restart path and the same lock as a mode switch
and a heal, so it can wait behind a mode switch that is in flight for
up to 10 s, the switch's own timeout. A switch against a frozen
compositor fails on that timeout in any case. The kill counts under
`display_compositor_restarts_total{reason="hung"}` beside `heal` and
`mode`. A kill that found no compositor counts nothing.

The placement pass takes the same probe. While the compositor does not
answer, the pass drops its memo of the placements it sent, resets the
`display_surfaces` gauge, and for every `Display` on this node clears
`status.surfaces` and `status.layout` and sets a `CompositorServing`
condition to False, with the reason `Down` or `Hung` and the socket's
words as the message. The serving path sets the condition True. Its
`lastTransitionTime` moves only on a change, and a `Compositor` printer
column shows the condition beside `Connected` and `Responsive`.

The column reads the condition's reason, so `kubectl` prints `Serving`,
`Down`, or `Hung`. The reason names the failure; the status says only
whether there is one.

Two metrics join the table from plan 14. `display_compositor_serving`
is a gauge, 1 while the probe reads serving and 0 while it does not.
`display_compositor_container_restarts_total` counts the restarts of
the weston sidecar from the `restartCount` the kubelet reports on the
operator's own pod. The compositor is a native sidecar, so the count is
in `status.initContainerStatuses`, and the operator reads its own pod's
name and namespace from the downward API. The first read is a baseline,
and the counter takes only the growth after it, so an operator
container that restarted alone does not count the restarts that
happened before it ran, and a replaced pod, whose count starts from
zero again, adds nothing. The counter is separate from
`display_compositor_restarts_total{reason}` because that one counts
only the restarts this operator orders, by reason; the kubelet's count
is the only one that includes a compositor that exited on its own.
Neither series carries a node label, because the `PodMonitor` relabels
the node onto every series the pod serves.

## How the work is proved

The unit drill in `weston_test.go` runs the probe against five sockets:
one that answers the handshake, one that does not exist, the socket
file a dead compositor left behind, one that accepts and never answers,
and one that closes at the handshake. The first reads `Serving`, the
one that never answers reads `Hung`, and the other three read `Down`.
The placement drills run the pass under a compositor that answers and
under each of the two failures, and check that the status empties, the
condition carries the reason and the socket's words, and its time moves
only on a change. The restart counter's drill reads a pod's status at a
baseline, after one and two restarts, and after a replacement that
counts from zero again.

The repair has drills of its own in `weston_test.go`. A clock fed
`Hung` readings orders one kill once the bound has run out, and no
second kill in the same outage. A freeze shorter than the bound orders
none. A freeze that ends and starts again gets its own bound. A watch
on a socket that accepts and never answers, with the bound set to zero,
calls the repair once and never again on the ticks that follow.

The drill on liken-1 is still owed: stop weston with `SIGSTOP`, and
measure the seconds from the stop to the taint on the slices, to the
eviction of the clients, and to the `CompositorServing` condition on
the `Display`, then to the kill at the 10 s bound, to the container
restart, and to the clients' return. The numbers to compare against are
the crash path's 2.6 s to the taint and 13 s to the client's return.
