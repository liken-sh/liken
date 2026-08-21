# 06, Restarting WirePlumber when the bus dies

A liveness probe on the WirePlumber container that reads whether the
adapter still advertises a media profile, so the kubelet restarts the
one container whose registration is gone.

This answers the open problem "A2DP does not survive a Bluetooth pod
restart", which [plan 04](04-bluetooth-sinks.md) opened.

## The problem

When the Bluetooth operator's pod restarts, its `dbus-daemon` exits
and every connection to the media bus dies. The operator container
reads that at once: its BlueZ signal channel closes, the process
exits, and the kubelet restarts it within seconds.

WirePlumber does not. It keeps running with a dead file descriptor,
registers nothing again, and reports Ready. From that moment the
radio advertises no audio, no speaker can hold a connection, and
every container in the pod is Ready.

The socket cannot report this. A new `dbus-daemon` listens on the
same path, so the file is there and a fresh connection to it
succeeds. What is broken is the descriptor WirePlumber still holds to
the socket that went away.

## Why WirePlumber stays

libdbus sets exit-on-disconnect for a bus connection by default, so a
D-Bus client normally ends when the bus does, and its service manager
starts it again. PipeWire's D-Bus support plugin clears that flag:

    dbus_connection_set_exit_on_disconnect(this->conn, false);

A plugin that ended its host process over a bus disconnect would be
wrong, so the choice is right for PipeWire. The plugin detects the
loss, closes the connection, and emits a `disconnected` event.
`spa/plugins/bluez5/bluez5-dbus.c` registers no listener for that
event, so nothing reconnects and nothing re-registers. Both facts are
upstream, and neither is configurable.

The repair is therefore the one systemd would perform: end the
process and start it again. WirePlumber is a native sidecar, so the
kubelet restarts that container alone and leaves PipeWire and the
card's sinks running.

## The design

`endpoints.go` adds an `endpoints-registered` mode to the operator's
binary, and `deploy/operator.yaml` names it as the WirePlumber
container's startup probe and liveness probe. The binary is already
in that container, so the probe needs no shell and no second image.

### The fact the probe reads

bluetoothd adds a media profile's UUID to `org.bluez.Adapter1.UUIDs`
when a client registers an endpoint for it, and removes it when that
client's connection ends. `org.bluez.Media1.SupportedUUIDs` names the
media profiles this bluetoothd hosts. An overlap between the two
means an endpoint is registered.

Measured on `liken-1`, on the same radio, before and after the
Bluetooth pod restarted:

| state | `Media1.SupportedUUIDs` | `Adapter1.UUIDs` | overlap |
| --- | --- | --- | --- |
| registered | `110a`, `110b` | `110a`, `110c`, `110e`, `1200`, `1800`, `1801`, `180a` | `110a` |
| lost | `110a`, `110b` | `110c`, `110e`, `1200`, `1800`, `1801`, `180a` | none |

`110c`, `110e`, `1200`, `1800`, `1801`, and `180a` survive the loss,
because bluetoothd publishes them for itself. Only the media profile
follows a client's connection.

Asking BlueZ for both lists keeps a profile number out of the code.
The pod registers the `a2dp_source` role today, so the overlap is
AudioSource. A deployment that registered `a2dp_sink` would overlap
on AudioSink and pass unchanged, where a check that named one profile
would turn that configuration change into a restart loop that never
ends.

### The four states

One state fails, and it is the one a restart repairs.

* No delivered bus. The claim allocated no media bus, so this
  machine's WirePlumber registers nothing and never should. Pass.
* No answer from the bus. The Bluetooth operator's pod is down. Its
  own kubelet restarts it, and a WirePlumber that restarted now would
  find nothing to register. Pass.
* No adapter. The radio is unplugged. Pass.
* An adapter that advertises no media profile bluetoothd hosts. The
  registration is gone. **Fail.**

Each state prints the fact it found, because a failing exec probe
reaches a reader as one line in `kubectl describe`, and the next
question is always which state the check saw.

### Why the startup probe reads the same fact

The kubelet disables the liveness probe until the startup probe
succeeds. Registration takes a few seconds after WirePlumber opens
the bus, so the startup probe is what keeps the liveness probe from
ending a container that is still doing the work. The allowance is 60
seconds.

The startup probe this replaces ran `wpctl status`, which connects to
PipeWire rather than to WirePlumber. PipeWire's own startup probe
already proves the socket answers before this container starts,
because the kubelet starts native sidecars in order and waits for
each one's startup probe. The new probe reads WirePlumber's own work
instead of repeating that.

### What this rejects

* **A ping on the bus socket.** `org.freedesktop.DBus.Peer.Ping` over
  a connection the probe opens proves the bus is alive. The bus is
  alive. It was alive through the whole outage this plan repairs.
* **Ending the whole pod.** The operator already reads the loss, but
  it holds no grant to delete its own pod, and its exit restarts only
  its own container. Ending the pod would also restart PipeWire and
  interrupt the card's audio, which the sidecar restart does not.
* **Publishing the fact and repairing nothing.** A taint on the
  speakers while the endpoints are gone is honest, and it leaves the
  machine with no Bluetooth audio until a person acts.
* **A fix upstream.** A listener on the `disconnected` event that
  rebuilds the bluez5 monitor would make this probe unnecessary. It
  is the right fix and it is not this project's to ship.

### What this costs

* A `bluetoothd` that restarts in a loop restarts WirePlumber with
  it. "The bus was replaced once" and "the bus is replaced every
  minute" are the same fact to this probe, and the kubelet's backoff
  is what bounds the churn.
* The check runs one D-Bus call every 20 seconds on every node that
  claimed a media bus.

## The drill

Run on `liken-1` on 2026-08-21, in release `2026.08.21-002`, against
the `studio-pa` speaker. Nobody acted after the first step.

| time | WirePlumber restarts | `110a` on the adapter | the speaker |
| --- | --- | --- | --- |
| 18:11:43 | 0 | present | connected, no taint |
| 18:11:44 | delete the Bluetooth operator's pod | | |
| 18:11:53 | 0 | absent | disconnected |
| 18:12:28 | 1 | present | disconnected |
| 18:13:12 | 1 | present | connected, no taint |

The kubelet restarted the WirePlumber container 35 seconds after the
registration was lost, and the speaker was playable again 89 seconds
after the pod was deleted. PipeWire's restart count stayed 0 through
all of it, and the card's five ALSA sinks never left the graph.

The event the kubelet recorded states the whole finding:

    Liveness probe failed: the adapter advertises none of the 2 media
    profile(s) bluetoothd hosts; WirePlumber's endpoints are gone and
    only a restart of this container registers them again

Before this release the same failure was permanent, and the repair
was a person deleting the audio pod.
