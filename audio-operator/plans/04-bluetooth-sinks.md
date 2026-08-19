# 04, Bluetooth sinks

Built, and the hands-free half drilled on liken-1 on 2026-08-19 with
release 2026.08.19-004. The playback drills wait for hands.

This plan is this operator's half of liken's
[milestone 60, Bluetooth audio](https://github.com/liken-sh/liken/blob/main/plans/60-bluetooth-audio.md).
That document is the design record: why one sound server serves the
card and the radio, why the bus is a claimable device, and why the
consumer contract does not change. The Bluetooth operator's plan 05
built the other half, the media bus this pod claims. This document
records only what this repository decided while it built its share.

## The problem

Since the media bus shipped, this pod's claim allocates it on every
machine with a radio, and the delivery sets `DBUS_SYSTEM_BUS_ADDRESS`
in all four containers. The pod held the bus and did nothing with
it: the image carried no Bluetooth plugin, the WirePlumber profile
disabled the monitor, and no paired speaker appeared anywhere a
workload could claim it.

## The design

The `declare` init container becomes the switch. When the claim
delivered a bus, it writes a WirePlumber fragment that enables the
bluez monitor; when it did not, it writes nothing and the pod is the
pod that ran before this plan. WirePlumber registers the media
endpoint over the delivered bus, builds a sink node when a speaker
connects, and the operator publishes each paired speaker as an
`audio.liken.sh` device beside the card's outputs. A consumer claims
a speaker exactly like an HDMI output and receives the same mount,
`PIPEWIRE_REMOTE`, and `PIPEWIRE_NODE`.

The decisions this repository made:

* **BlueZ is the source of membership, the graph is the source of
  state.** The operator reads the paired set over the delivered bus
  with the same D-Bus client shape the Bluetooth operator uses. A
  paired device whose profiles include AudioSink is a speaker. The
  sink node's name and the negotiated codec come from the same
  `pw-dump` read that answers everything else, so the slice and a
  prepare call can never disagree.
* **A speaker is named by its peer MAC**, lowercased with dashes,
  the same form the Bluetooth operator uses. The shape cannot
  collide with `card<n>-pcm<n>`, and the shape alone routes a
  prepare call to the right half of the graph.
* **The taints reuse this driver's own keys.** `disconnected`
  NoExecute when the speaker is not connected or has no node,
  `no-sink` NoSchedule while no node exists. A paired speaker that
  is switched off publishes with both, which is milestone 56's
  claim-ahead-of-connect: the consumer's pod parks, somebody
  switches the speaker on, and the pod starts.
* **A failed BlueZ read never shrinks the slice.** Membership is the
  paired set, and a bus that stopped answering says nothing about
  who is paired. The last-known speakers publish tainted instead.
* **A2DP only, stated twice.** The fragment restricts the monitor's
  roles to `a2dp_source`, the role that plays into a speaker, and
  names no headset backend. The role names what this machine does,
  not what the peer is: the first release named `a2dp_sink` and made
  the machine a receiver for phones, and the liken-1 drill caught
  the inversion when the speaker's card offered no playback profile.
  HFP and HSP
  would need an SCO socket in the host's network namespace, and this
  pod has no host network. Milestone 60 keeps the headset profiles
  out of scope until a real use appears.
* **The image gains `libspa-0.2-bluetooth` and nothing else.** The
  bluez5 plugin, the codec plugins that ship inside that one
  package, and the quirks database. The release workflow asserts
  the plugin, the SBC codec, and the database by name, the same way
  it asserts the ALSA plugin.
* **A bus that never answers ends the process**, like every other
  setup failure in this operator. The kubelet's restart with backoff
  is the retry, and plan 60's cost section accepts the coupling: the
  pod could not prepare its claim without the Bluetooth operator
  either.

## What was considered and set aside

* **Enabling the monitor from the operator container at runtime.**
  WirePlumber reads its configuration once at startup, so a switch
  that arrives later would need a WirePlumber restart anyway. The
  init container is where the order is already guaranteed.
* **Seeding only the SBC codec.** The other codec plugins ship in
  the same Debian package and load by name, one `dlopen` each.
  Dropping them would save little and cap every speaker at SBC. If
  the closure ever needs trimming, SBC alone still plays every A2DP
  speaker.
* **A separate slice for the Bluetooth sinks.** A speaker is the
  same kind of device as an output to every consumer, and a second
  slice would double the pool bookkeeping to say so.

## What the drill must show

The drill runs on liken-1, whose radio has the `studio-pa` speaker
paired. The parts that need no hands:

1. The fragment is written, WirePlumber loads the bluez monitor, and
   it connects to `bluetoothd` across the pod boundary.
2. A2DP appears on the radio while this pod holds the bus: the
   adapter's own profile list gains the audio source record.
3. `studio-pa` publishes as an `audio.liken.sh` device: named by its
   MAC, `connectionType: bluetooth`, tainted while the speaker is
   off, with the card's five sinks untouched beside it.
4. A consumer's claim on the speaker parks while it is off.
5. A Bluetooth pod restart, with the coupling plan 60 accepts:
   record what the audio pod does and how both settle.

The parts that need hands and ears are milestone 60's own drills:
the speaker switches on and the parked pod starts, playback through
the speaker, the mixed HDMI-and-speaker claim, and the coexistence
measurements with the pad.

The hands-free drill ran on 2026-08-19. Items 1 through 4 held
exactly as written: the fragment wrote, the monitor connected, six
A2DPSink endpoints registered, and `studio-pa` published with both
taints beside the card's sinks. Item 5 measured the coupling and
found a gap. The operator container notices the closed bus and
restarts within 3 seconds, but WirePlumber notices nothing: it keeps
running, logs nothing, and never re-registers the endpoints, so
A2DP stays dead while every pod reports Ready. The repair today is
a delete of the audio pod.
[The open problem](open-problems/a2dp-does-not-survive-a-bluetooth-pod-restart.md)
records what a fix must decide.
