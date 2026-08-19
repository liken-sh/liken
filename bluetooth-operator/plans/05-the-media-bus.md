# 05, The media bus

Built. The drill on liken-1 runs with release 2026.08.19-004.

This plan is this operator's half of liken's
[milestone 60, Bluetooth audio](https://github.com/liken-sh/liken/blob/main/plans/60-bluetooth-audio.md).
That document is the design record: why the sound server stays out of
this pod, why the permission to do Bluetooth audio is a claimable
device, and how the audio operator's claim spans two drivers. This
document records only what this repository decided while it built its
share. It supersedes [plan 01](01-bluetooth-audio.md), whose "How
A2DP works" section remains the citation record.

## The problem

A paired Bluetooth speaker creates no kernel device. Its audio exists
only while a sound server is connected to `bluetoothd`'s D-Bus socket
and keeps a media endpoint registered, and BlueZ advertises no A2DP
until an endpoint registers. The socket is in this pod. The sound
server is in the audio operator's pod. Nothing declared that
dependency, and nothing could schedule against it.

## The design

The operator publishes one more device in the slice it already
writes: the media bus, one per adapter. A claim on it delivers a
read-only mount of `/var/run/bluetooth.liken.sh/dbus` and
`DBUS_SYSTEM_BUS_ADDRESS` naming the socket inside. The device is
exclusive, which is `resource.k8s.io/v1`'s default: one radio serves
one sound server.

The decisions this repository made:

* **The name is the adapter's MAC plus `-media`**, in the same form a
  controller's name takes. The suffix is the whole distinction
  between the two kinds of device on the prepare and refresh paths,
  which read the allocated name and nothing else. A controller's name
  is six octets, so the two can never collide.
* **Three attributes and never `input`.** The bus carries `address`,
  `kind: mediaBus`, and `sound.liken.sh/supportsSound: true`. The
  qualified attribute is the one the audio operator's class selects,
  in a domain neither driver owns. The bus never carries `input`,
  so the `bluetooth-input` class never matches it.
* **A departed adapter taints the bus `NoSchedule`, not `NoExecute`.**
  The claim holder is the machine's one sound server, so an eviction
  would end its other playback too, and that playback does not need
  the radio. `NoSchedule` parks the next claim and leaves the running
  holder alone. The controllers keep their `NoExecute`, because a
  controller's holder loses everything when the radio goes.
* **The slice outlives the paired set.** The bus publishes as soon as
  `bluetoothd` names the adapter, so unpairing the last controller
  shrinks the slice to the bus instead of deleting it. The delete
  path runs only while no adapter has ever answered.
* **The bus volume is a hostPath.** A CDI mount names a host path,
  and a prepared claim stays correct for the whole boot. An
  emptyDir's host path is under `/var/lib/kubelet/pods/<uid>`, so it
  changes with every restart of this pod, and every prepared claim
  would go stale with it. The socket's address does not change: plan
  01 placed it at `/var/run/bluetooth.liken.sh/dbus` so that this
  change would replace a volume and not an address.
* **The CDI kind stays `bluetooth.liken.sh/controller`.** A CDI kind
  namespaces one writer's device IDs. It is not a taxonomy of the
  devices, and a second kind would rename every ID this driver has
  already written.

## What was considered and set aside

* **PipeWire in this pod**, plan 01. liken's milestone 60 records the
  three costs that set it aside: two sound stacks on one machine, two
  sockets where `PIPEWIRE_REMOTE` names one, and a fallback that adds
  a third operator.
* **A `NoExecute` taint on a departed adapter's bus.** It would treat
  the sound server like a controller's consumer, and the section
  above says why that is wrong.
* **Reporting the bus unclaimed in the pairing API.** Milestone 60
  notes that pairing a speaker works only while a sound server holds
  the bus, and the API can say so. Until the audio operator registers
  an endpoint, every request would carry the same report, so the
  report waits for the half that makes it informative.

## What the drill must show

The drill runs on liken-1, with the radio and the paired devices it
already serves. The audio half of milestone 60 does not exist yet, so
the drill proves the contract with a scratch claim, and milestone
60's own drills prove the rest later.

1. The slice holds the bus with its three attributes and no taints,
   beside the paired devices.
2. A claim through the `bluetooth-input` class, aimed at the bus,
   parks unallocated. The fence holds.
3. A claim through the `sound-card` class, narrowed to this driver,
   allocates the bus. Its pod receives the mount and the variable,
   and the socket file is present at the delivered path.
4. While that claim holds the bus, an identical second claim parks.
   The bus is exclusive.
5. With the scratch claims gone, a fresh audio operator pod allocates
   the sound card and the bus in one claim, and its slice of sinks
   still publishes.
6. After a restart of this operator's pod, the socket serves at the
   same host path, and the running claim holders keep their devices.
