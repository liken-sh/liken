# The claim takes any sound card, and a node serves only one

Open problem. The operator's controller claim now pins to `bus == "pci"`.
That narrows which card the operator gets. It does not say which card
the operator should get, and it does not let one machine serve two.

## What happened on 2026-08-17

liken-1 publishes two devices whose `liken.sh` subsystem attribute is
`sound`:

* `pci-0000-00-1f-3`, driver `snd_hda_intel`, bus `pci`, named "Intel
  Corporation Alder Lake-N PCH High Definition Audio Control". This is
  ALSA card 0. Its playback PCM devices are wired to the machine's HDMI
  connectors, so each one carries an ELD and the monitor identity that
  comes from it.
* `usb-1-6-1-0`, driver `snd-usb-audio`, bus `usb`, named "CSCTEK USB
  Audio and HID". This is ALSA card 1. It has one playback PCM and no
  ELD, so no monitor identity.

The `audio-controller` DeviceClass selects any device whose subsystem is
`sound`, and both of these answer it. The operator's claim named that
class and carried no selector of its own, so nothing in the claim chose
between the two. The allocation took the HDA controller on every earlier
run. A roll of the operator on 2026-08-17 gave it the USB device.

The operator then published one device, `card1-pcm0`, with no
`monitor.liken.sh/id`. A consumer's claim that selects a monitor by that
attribute matched nothing and stayed Pending. The same pod's display
claim stayed Pending with it, because a pod's claims allocate together
or not at all. The visible symptom was a movie that would not schedule.
The pod's other claim went to the display operator, which had published
its outputs correctly and was not at fault.

## Which card the operator serves, and who decides

`deploy/operator.yaml` now carries this selector on the controller
request:

    has(device.attributes["liken.sh"].bus) &&
    device.attributes["liken.sh"].bus == "pci"

That selector is in the shipped base, so it makes the choice for every
deployment of this operator. It forecloses a real case: a USB DAC or a
USB audio interface that somebody bought and connected on purpose, on a
machine whose built-in card drives nothing. Under this selector the operator will not serve that device.
On a machine with no PCI sound card at all, the claim parks Pending and
the pod never starts.

Whether the choice belongs in the base is open. Two shapes, and the
difference between them is which mistake a person makes when they
deploy `deploy/` without reading it:

* The base ships the selector, as it does today. A person who does not
  read the manifests gets the built-in card. That is the right card on
  most machines and the wrong one on a machine bought for a USB
  interface. The failure on that machine is a Pending pod, and a Pending
  pod is visible in `kubectl get pods` with an event that names the
  unsatisfied claim.
* The base ships no selector, and each deployment adds its own. A person
  who does not read the manifests gets whichever device the allocation
  takes, which is what produced the 2026-08-17 failure. That failure is
  not visible as a failure. The pod runs, the operator publishes
  outputs, and the outputs are the wrong ones. The error surfaces one
  hop away, in a consumer whose claim no longer matches anything.

Neither shape serves two cards on one machine.

## Two controllers on one machine cannot both be served

`EnsureResourceSlice` in `slices.go` writes one object per node.
`sliceName` returns `nodeName + "-" + DriverName`, so on liken-1 the
object is `liken-1-audio.liken.sh`. Both the create path and the update
path set `spec.pool.name` to the node name and
`spec.pool.resourceSliceCount` to the literal `1`. Nothing in the object
name, the pool name, or the count mentions the controller the pod
claimed.

Raise `replicas` to 2 on a machine that has two sound controllers, and
both pods land on that machine, each holding a different controller. The
two device lists are disjoint. `readOutputs` enumerates `/dev/snd`,
which holds only the PCM nodes the pod's own claim delivered, and it
names each device `card<N>-pcm<M>`, so one replica builds `card0-pcm3`
and its siblings while the other builds `card1-pcm0`. The delivery is
what makes the lists disjoint, and liken-1 shows it: the machine has two
sound cards, the pinned claim delivers the PCI one, and
`liken-1-audio.liken.sh` holds `card0-pcm3`, `card0-pcm7`, `card0-pcm8`,
and `card0-pcm9` and nothing from card 1. Both replicas would then call
`EnsureResourceSlice` against the same object name.

Each pass does three things:

1. It GETs `liken-1-audio.liken.sh`.
2. It calls `sameDevices` on what it read against what it built. The
   other replica's list never equals its own, so this comparison is
   always false.
3. It sets `spec.pool.generation` to the value it read plus one, sets
   `spec.devices` to its own list alone, and PUTs.

Step 3 is the collision. The assignment writes
`current.Spec.Devices = devices`, which replaces the list rather than
appending to it, so each write removes the other replica's devices from
the slice. The other replica's next pass reads that, finds
its own devices gone, and writes them back, removing the first
replica's. The PUT carries the `resourceVersion` from the GET, so two
writes that race give `ErrConflict` to the loser, which reads again and
writes again on its next pass. That check orders the two writers. It
does not stop either one from overwriting the other.

`backstopInterval` is 60 seconds, so with no ALSA or PipeWire events at
all each replica reconciles at least once a minute. The published set
alternates at least that often, and each alternation raises the pool
generation by one.

Nothing marks the pool as damaged while this happens. The scheduler keys
a pool on the driver and the pool name, and it marks a pool incomplete
when the number of slices it found does not equal `resourceSliceCount`.
Here there is one slice and the count says 1, so the pool reads as
complete on every pass while it holds half the machine's outputs.

What the alternation does to a consumer is not established. The
scheduler allocates against a slice it read, and a device that leaves
the slice between that allocation and the kubelet's prepare call is a
case this operator has never run. Nobody has tested it, and this
document does not guess at the outcome.

## The bluetooth operator has the same slice name

Verified by reading `slices.go` in
[bluetooth-operator](https://github.com/liken-sh/bluetooth-operator). It
has the same `sliceName(nodeName)` returning `nodeName + "-" +
DriverName`, the same `ResourcePool{Name: nodeName, Generation: 1,
ResourceSliceCount: 1}`, and the same read, compare, write in
`EnsureResourceSlice`. Two replicas holding two adapters on one machine
would take turns overwriting `liken-1-bluetooth.liken.sh`, in exactly
the way described above.

It has a second collision that this operator does not have. Its
discovery walk starts at `/sys/bus/hid/devices` and keeps every HID
device whose bus type is `0005`, BUS_BLUETOOTH. It does not filter by
adapter. The comment at the top of `discovery.go` says why, and states
the assumption the walk rests on: "the operator holds one adapter, so
every controller it sees is on that adapter". Two replicas on one
machine break that assumption. Each one would see every controller
connected to either adapter, and both would publish the same device
names, because `deviceName` is built from the peer MAC alone.

The material for a filter is already in the uevent and thrown away.
`HID_PHYS` carries the adapter's address. `discovery.go` names the field
in that same comment and says the program does not use it, and no
production file in the repository reads it. It appears only in
`discovery_test.go` fixtures.

That operator's README tells a person to raise `replicas` to the number
of adapters. That instruction is correct for adapters on separate
machines. Nobody has run two adapters on one machine.

A different mismatch in the same StatefulSet is in
[Bonds follow the pod's ordinal and adapters do not](https://github.com/liken-sh/bluetooth-operator/blob/main/plans/open-problems/bonds-follow-the-ordinal-and-adapters-do-not.md).
That one is about which volume a replica mounts. This one is about which
object a replica writes. Neither one causes the other, and a fix for
either leaves the other in place.

## What a fix would have to provide

Three candidates. None chosen.

### Name the slice for the controller as well as the node

The API permits this. A pool is allowed to span several slices, and the
`ResourceSlice` type documentation says so in as many words: "A pool may
span more than one ResourceSlice, and exactly how many ResourceSlices
comprise a pool is determined by the driver." The object name is
validated only as a DNS subdomain, so `<node>-<driver>` is this
operator's own convention and not a rule. Kubernetes' driver helper
library does not use a fixed name at all. It sets `GenerateName` to
`<owner>-<driver>-` and lets the API server add the suffix, which is
what makes several slices per pool practical.

Two shapes, and the choice between them is where the cost lands.

Keep one pool per node and give each replica its own slice inside it.
The two replicas then have to agree, because the pool is keyed on the
driver and the pool name, and the API requires every slice in a pool to
carry the same generation: "Whenever a driver changes something about
one or more of the resources in a pool, it must change the generation in
all ResourceSlices which are part of that pool." Each slice also
declares `resourceSliceCount`, the total for the pool, which two
independent pods cannot compute without talking to each other. The
scheduler's handling of disagreement is not a repair. It keeps only the
highest generation it sees and drops every older slice, and it marks the
pool incomplete when the number of slices does not equal
`resourceSliceCount`. So this shape trades the overwrite for a
coordination problem between two pods that have no channel today.

Or give each replica its own pool, named for the node and the controller
together. Each pool then holds one slice with `resourceSliceCount` 1 and
its own generation, and no coordination is needed. The cost is that a
device's identity is the tuple of driver, pool name, and device name, so
the pool name becomes part of what a consumer's allocation refers to.
`spec.pool.name` is immutable after a slice is created, so a slice
cannot be renamed into a new pool. Moving to this shape means deleting
the existing slice and creating a new one, and this operator deliberately
never deletes its own slice, because a slice that left with the pod
would strand every prepared claim.

Either shape breaks the uninstall instruction in the README, which names
one slice by hand:

    kubectl delete resourceslice liken-1-audio.liken.sh

That line would have to name a set.

### One instance that claims every sound controller on its node

The publishing code already carries this. `readOutputs` groups PCM
devices with `byCard`, a map keyed by card number, and every device name
and every PipeWire lookup is keyed on the `(card, pcm)` pair, so a
second card produces `card1-pcm0` beside `card0-pcm3` with no change.
The drop-in that declares the PipeWire nodes is generated from whatever
`readOutputs` returned, so it covers two cards the same way.

The cost is in the claim. The request would set `allocationMode: All`
instead of taking one device, which the API documents as "all of the
matching devices in a pool" with "at least one device must exist on the
node for the allocation to succeed". Two properties of that mode are
the price. The first is that it fails when any matching device is
already allocated, so a replacement pod that starts while the outgoing
pod still holds a controller gets nothing rather than the rest. The
current Deployment uses `Recreate` for the same reason and would have to
keep it. The second is that `All` fails on a pool the scheduler has
marked incomplete, so this candidate depends on the slice writing being
correct first.

The arbitration has to survive the change as well.
`deploy/deviceclasses.yaml` states that a controller allocates once so
that the claim holder is the only sound server on that card. A claim
that takes every controller keeps that guarantee for one instance per
node. It gives up nothing only as long as nobody runs a second instance
beside it.

### State that this operator serves one card per node

Write it down in the README and delete the paragraph under "Deploying
it" that tells a person to raise `replicas` to the number of cards. That
paragraph is correct for cards on separate machines and wrong for two
cards on one, and it is the only thing in the repository that invites
the collision.

The cost is that a machine with two sound cards has no answer at all. A
person who wants both served would run a second deployment under a
second driver name, which means a second DeviceClass, a second CDI
prefix, and a second kubelet plugin directory. Nothing supports that
today, and this document does not propose it.

## What it waits on

A machine with two sound controllers that somebody wants both of served.
liken-1 has the hardware and not that intent. Nothing plays through the
CSCTEK device, and the PCI pin now keeps the operator off it. Until a
second card has a consumer, the size of this problem is a guess, and the
pin costs nothing.
