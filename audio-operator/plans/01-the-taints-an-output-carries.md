# The taints an output carries

Plan 01. Built. The operator publishes three taints on an output: one
that says what happens to the pod holding the claim now, and two that
each name one reason the output cannot play. This document states why
there are three keys and not two, and how the bug that produced the
third one was found.

## The problem

An output that cannot play stays in the slice. Deleting it strands the
next consumer, because the allocation still names the device, the
kubelet's prepare call retries against a device that is in no slice,
and nothing bounds that retry. So the operator taints the device
instead, and the taint has to carry two different pieces of
information: what happens to the pod that holds the output, and why the
output cannot play.

## The design

| Key | Effect | What it says |
|---|---|---|
| `audio.liken.sh/disconnected` | `NoExecute` | the output cannot play right now |
| `audio.liken.sh/no-monitor` | `NoSchedule` | no monitor answers on this HDMI output |
| `audio.liken.sh/no-sink` | `NoSchedule` | PipeWire holds no node for this PCM device |

A consumer tolerates `audio.liken.sh/disconnected` with its own
`tolerationSeconds`, which sets how long the pod may hold an output
that cannot play before the pod ends. That number debounces a monitor
somebody unplugs for a moment. No consumer tolerates either
`NoSchedule` taint.

## Why two taints and not one

The allocator treats a tolerated taint as allocatable. With only the
`NoExecute` taint, a consumer that tolerated it would be scheduled onto
an output that plays into nothing, fail, be evicted when the toleration
ran out, and be scheduled again. An untolerated `NoSchedule` taint
holds the pod Unschedulable until the output can really play, which is
what makes a claim created ahead of the monitor park instead of loop.

## The bug that produced the third key

One `if` applied both taints, for the condition
`!hasSink || (output.HDMI && !output.Monitor)`.

Before the operator declared its own PipeWire nodes, those two
conditions described the same fact. WirePlumber built the sinks, and an
HDMI output with no monitor had no sink, so pairing the two under one
key cost nothing.

The node declaration changed that. The operator now declares a sink
node for every playback PCM device from its own ALSA enumeration, so an
unplugged HDMI port keeps its sink. The device then published a
`sinkName` attribute and a `no-sink` taint at the same time. Those two
statements contradict each other, and a reader of the slice cannot tell
which one to believe.

## How it was confirmed

On `liken-1`, `card0-pcm8` and `card0-pcm9` are HDMI ports with nothing
plugged into them. Both carried `sinkName=liken.audio.card0-pcm8` and
`sinkName=liken.audio.card0-pcm9` together with the `no-sink` taint. A
`pw-dump` showed all four declared nodes present in the graph.

## What was ruled out

* **That `parseSinks` drops a node in a state other than `running`.**
  It reads no state field. Three of the four nodes were `suspended`
  while they still mapped to their outputs.
* **That the slice was stale from a pass that ran before the nodes
  existed.** `pool.generation` was still 1 after fifteen minutes of a
  sixty-second backstop, so every pass recomputed the same answer. A
  pass that ran before the nodes existed would have published no
  `sinkName` at all.

## The fix

Each reason carries its own key. The unplugged monitor and the missing
node are separate conditions in `sliceDevices`, and one function,
`publishSink`, writes either the `sinkName` attribute or the `no-sink`
taint. A device cannot carry both.

The reasons stay separate facts because each one has its own repair. A
missing monitor is a cable, and somebody plugs it back in. A missing
node is an output PipeWire could not create, which the `nofail` flag on
each declared object leaves possible, and only a restart creates it.

## Deploying it

The fix is committed. Redeploying it restarts PipeWire, which takes the
socket from every consumer, so the deploy waits for a moment when
nothing is playing.
