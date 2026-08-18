# Device operators

Milestone 56. Completed. It is the pattern that milestones 57, 58,
and 59 build. A device operator claims raw hardware from liken through an
ordinary `liken.sh` DRA claim, runs the daemon that manages that
hardware, and publishes what the daemon holds as its own DRA devices,
under its own driver name, in its own ResourceSlices. Each operator is
its own repository and an ordinary Kubernetes workload. liken does not
deploy one, and nothing in a liken release names one. A cluster that
never deploys one behaves exactly as it does today.

This document holds what every instance shares. Each instance document
holds its own hardware, its own daemon, its own delivery, and its own
drill.

## The problem the pattern answers

liken publishes the facts about hardware that no other layer can
observe. That is milestone 38's rule, and the pattern keeps it. The
rule applies in both directions: there are facts about hardware that
liken cannot observe, because a running daemon is what makes them
true.

liken walks the buses the kernel enumerates and publishes what it
finds. That walk finds a Bluetooth adapter, and it does not find which
controllers are paired, because the paired set is in bluetoothd. It
finds a graphics device, and it does not find which of the machine's
monitors is free, because the compositor is what drives them. In both
cases the useful device is one level up from the hardware liken
publishes, and reading it means running a daemon.

The layer that publishes those devices must be the layer that runs the
daemon. liken must not be that layer. The daemon would then ship in the
read-only root that every machine boots, and every machine would hold
it for the one machine that uses it.

## The pattern

An operator does four things, and each one is a public DRA contract.

* It claims the raw device from liken, exclusively, through an ordinary
  `liken.sh` claim.
* It runs the daemon that owns that hardware, holding the claim for as
  long as it runs.
* It publishes one device for each thing the daemon holds, in its own
  ResourceSlices, under its own driver name.
* It writes its own CDI files, so a consumer's container receives that
  one device's delivery and nothing else.

A DRA driver name is a DNS name, and an operator's name is
`<domain>.liken.sh`: `bluetooth.liken.sh` for milestone 58,
`display.liken.sh` for milestone 57. The subdomain states which
operator published a device, so a DeviceClass or a claim reads as the
domain it belongs to. The name states the contract family the operator
implements. It does not state which repository builds it, and the
repositories are separate ones, which the next section covers.

The operator uses no private interface into liken. Every piece of the
four steps above is the public contract that any DRA driver on any
Kubernetes cluster gets, and that is what lets an operator run
anywhere.

## Separate repositories

The boundary this pattern draws is between the OS and a workload. An
operator is on the workload side of that line, so each operator is its
own repository, apart from liken's.

A repository publishes two artifacts.

* **A container image.** It holds the operator and the daemon it
  runs. The images publish to ghcr.io, which is the working assumption,
  so a cluster pulls one without a private registry.
* **A Kustomization.** A kustomize base holding the Deployment, the
  DeviceClasses, the RBAC, and the ResourceClaimTemplate. A person may
  consume it through their own GitOps, with patches of their own, or
  read it as reference and write their own manifests. It is an offer,
  not a mechanism. liken does not apply it and does not name it.

The base system does not grow, and no rule has to hold it there.
Nothing about an operator is in a liken release, so the read-only root
that every machine boots holds none of the daemons these operators
run, and no edit to an operator can change what the system image
holds.

The release cadences do not couple either. A system image change
reboots a machine and an operator image change restarts a pod. Neither
release names the other, so each moves when its own work is done.

liken's whole contract with an operator is the public device model: a
claimable adapter, a claimable display device, and the DRA rules the
rest of this document states.

## How a cluster deploys one

A person applies the operator's Kustomization the way they apply every
other workload, through their own GitOps or with `kubectl` for a trial.
Nothing in liken participates.

Two things a person would otherwise write down come out of the claims
instead.

* **Placement.** The operator claims the raw device, and only a machine
  that has that hardware publishes one, so the scheduler puts the pod
  where the hardware is. No node selector states it, and no label has
  to be maintained.
* **Cardinality.** The unit is one operator for each adapter or each
  graphics device, not one for each cluster. A workload with a
  ResourceClaimTemplate against the raw device's DeviceClass and
  `replicas: N` says that: a Deployment when the operator holds no
  state, a StatefulSet when each replica has its own volume. All three
  operators ship as Deployments. The Bluetooth operator was the one
  case that looked like it needed a volume, and it did not: an ordinal
  names a replica, a bond belongs to a radio, and the two do not track
  each other. Its keys are in a Secret named for the adapter, so the
  state is keyed by the hardware that owns it. The raw devices are
  exclusive, so each replica's claim allocates a distinct one, and the
  scheduler spreads
  the replicas to wherever the hardware is. A replica past the number
  of devices parks Pending and costs nothing. Nobody writes down which
  machine has the radio.

Deleting the workload stops the operator, and liken's own inventory
never changed while it ran. The operator's slices outlive it: a slice
delete must not couple to the pod's shutdown, because the shutdown
signal cannot tell a rollout from a removal, and a restart must never
delete a device while a claim holds it. The Node owner reference on
each slice removes it with a dead node, and a permanent removal is
two deletes, the workload and the slice, which each operator's README
states by name.

## The raw claim is the arbitration

An operator acquires its raw hardware through a normal exclusive claim
against liken's driver. DRA's exclusivity is the whole of the
arbitration. The claim holder is the only manager of that hardware on
that machine, there is no coordination protocol between the two
drivers, and a pod that needs the raw device for something else
competes for the same claim and can win it.

The claim is also what makes the ordering work. The operator's pod
starts after the machine boots, and the raw device is published from
the boot, so the operator has a claim to take whenever it starts and
liken has nothing to hold back.

## A claim ahead of the hardware

An operator publishes a device that a person can claim before the
hardware is ready to serve it: a controller that is paired and switched
off, a monitor that is cabled and asleep. Kubernetes allows this by
design, and the behavior was verified against the v1.36 sources.

* DRA allocation is always deferred. There is no immediate binding
  mode.
* A pod whose claim matches no device parks as Unschedulable. There is
  no timeout on it, so nothing fails the pod while it waits.
* A ResourceSlice add or update is a registered scheduler event. The
  scheduler requeues the parked pod when the slice changes, within
  informer latency.

## A loss is a taint

One rule governs every disappearance: never delete a published device
while a claim holds it.

Deleting it strands the next consumer. The claim's allocation still
names the device, and `NodePrepareResources` for the next pod retries
against a device that is not in any slice. The retry has no bound, so
the pod stays in `ContainerCreating` and nothing corrects it. KEP-5322
would have bounded exactly this case. It was closed as not-planned in
March 2026, so the bound does not exist and will not.

The published device stays, and a loss applies a `NoExecute` device
taint to it instead. Device taints are the `DRADeviceTaints` feature,
which is beta and on by default in v1.36. The rest is Kubernetes
machinery that already runs:

* The taint-eviction controller ends the pod that holds the claim.
* `tolerationSeconds` on the claim's toleration is the debounce. A
  device that drops for two seconds and comes back is not a loss, and
  the number that says so belongs to the workload, not to the operator.
* A return clears the taint, and the scheduler places the consumer
  again.

The loss is two taints with different keys, not one. The `NoExecute`
taint is the one above, which the workload tolerates for its
debounce. A `NoSchedule` taint with its own key goes on whenever the
device cannot deliver, and nothing tolerates it, because the
allocator treats a tolerated taint as allocatable: with one taint,
a claim on an absent device allocates, the prepare call fails, the
eviction ends the pod, and the workload loops through schedule and
evict forever. The untolerated `NoSchedule` parks the claim as
Unschedulable instead, which is the wait a person can see.

## A pod is one session

The unit of consumption is one pod for one session. This is a limit in
Kubernetes and the container runtimes below it, not a choice, and each
layer was verified upstream.

* CRI carries CDI devices only in `ContainerConfig`, at container
  create. There is no update message that carries them.
* CDI has no re-apply operation. A spec file is read when the container
  is created.
* NRI can update a running container, and its post-create updates reach
  cgroup settings only. Device nodes are not among them.

So a running pod's device set cannot change. Hardware that arrives
after the pod started is not visible to that pod, and the pattern does
not pretend otherwise. The pod is the session, the taint ends it, and
the scheduler starts the next one.

## Slice writes debounce

An operator that publishes on kernel or daemon events settles a burst
before it writes. liken's init settles a burst of uevents before it
re-walks (`init/disklinks.go`), and these daemons do the same for the
same reason, plus one that is specific to DRA: every ResourceSlice
write wakes every DRA-pending pod in the cluster, because the
ResourceSlice scheduler event has no queueing hint. Hardware that
flaps in a loop must not turn into a cluster-wide scheduling storm.

## Two drivers on one machine

Two DRA drivers publishing on one node coexist by construction, and
liken's code already has the pieces. The rules do not change
because an operator's code ships from liken's own repository.

* A device's identity is the triple `<driver>/<pool>/<device>`, so two
  drivers cannot collide in the API even if they choose the same device
  name.
* Slice names include the driver name as a suffix.
  `kubernetes/resourceslices.go` builds the name as
  `nodeName + "-" + DriverName`, which allows a second driver on
  the same node.
* liken's CDI refresh reads only the spec files whose names start with
  its own prefix. `machine-operator/cdi.go` writes
  `liken.sh-<claimUID>.json` and parses with the same prefix, so a
  second driver's files in `/var/run/cdi` are untouched.

What nothing enforces is that two drivers do not deliver the same
`/dev` path. There is no registry of claimed device nodes, and neither
Kubernetes nor CDI has one. liken's delivery walk boundary is the whole
guarantee, and each instance states its own: liken must not publish, as
part of the raw device, the nodes that the operator republishes.

## What each instance supplies

The pattern above is fixed. Four things change with the hardware, and
an instance document answers each one.

* **The daemon it runs.** What owns the hardware, what state it holds
  that liken cannot read, and what happens to that state when the pod
  restarts.
* **The identity its devices have.** The name a published device
  takes, and the evidence that the name survives a reboot. A name built
  from a number the kernel assigns at boot names different hardware
  after the next one.
* **The privilege it needs.** The namespaces and capabilities the
  daemon cannot run without, each one justified by what the kernel
  checks. Every operator also takes the two hostPath mounts every DRA
  driver takes: the kubelet's plugin registry socket directory, so the
  kubelet finds the driver, and `/var/run/cdi`, so the prepared claims
  land where the container runtime reads them.
* **The form its delivery takes.** liken's own driver injects device
  nodes only. CDI also delivers environment variables and mounts, and an
  operator stacked on top of liken may use them, so an instance states
  what a consumer's container receives and why that is the whole of it.

## What was considered and set aside

* **An optional liken feature.** liken has a vocabulary for
  capabilities that are not part of the base system (milestone 17), and
  the `flux` feature seeds a workload from the image at first boot
  (milestone 14). An operator looks like the next entry in that list.
  It is not one. What a liken feature is for draws the line: a feature
  delivers a capability that is beneath GitOps. flux is seeded by the
  OS because nothing else can bootstrap flux, and a storage client must
  be on the host before a workload mounts a volume. These operators are
  above GitOps. Nothing in a machine's boot path needs a game
  controller or a screen, so the cluster's own workload machinery can
  deliver one, and OS seeding is the wrong mechanism for it. Three
  concrete objections came with the feature framing. The feature's
  manifests would name an operator image version inside a liken
  release, which couples two release cadences that have no reason to
  move together. The unit is one operator for each adapter or each
  graphics device, not one for each cluster, and a cluster-level flag
  cannot say that without configuration beside it. Deploying would
  depend on which machine holds the hardware, and a cluster-level
  feature must not require per-machine hardware knowledge. The last two
  are objections to any cluster-level flag, and the claims answer both,
  which "How a cluster deploys one" states. The first one is still valid.
* **Build the refined shape into liken's machine-operator.** This is
  the shortest path and it fails milestone 38's test. The refined set
  is a fact that another layer holds, and reading it means running that
  layer's daemon, which puts the daemon in the OS image for one class
  of peripheral. The churn does not belong on the machine operator's
  reconcile loop either, which runs on a ten-second ticker matched to
  the kubelet's lease cadence (`machine-operator/main.go`). Hardware
  that returns should not wait ten seconds, and the OS driver should
  not run faster for it.
* **An exclusion list in liken, so liken does not publish the raw
  device.** A field on the Machine that names hardware liken must not
  publish would make the hardware inventory depend on what else the
  cluster runs. A cluster-wide switch does not repair that: a switch
  that changes which devices a machine publishes describes the hardware
  from outside the hardware. The list also has a boot ordering hole. It
  applies at boot, and the operator that justifies it is a pod that
  starts later, so a machine that reboots without that pod publishes
  nothing where the hardware is. The DRA claim has no ordering hole,
  because the claim itself is what assigns the device to the operator.
* **Userspace fd-passing instead of DRA.** One privileged manager pod
  holds every device and passes file descriptors to consumers over a
  Unix socket. It loses the scheduler completely: a pod cannot ask for
  a device, cannot park until one exists, and cannot be evicted when
  one leaves. Every placement decision moves into an application
  protocol that the cluster cannot read. It is noted here because it is
  the only true fix for in-place hot-plug. A manager that re-exports
  devices through an indirection like uhid or uinput can add and remove
  them under a running consumer, which is what InputPlumber and Wolf
  do. That is a possible extension on top of this pattern, for a
  consumer that must outlive its sessions, and it is not the primary
  model.
* **A fixed roster of pre-published slot devices.** Publish `slot-1`
  through `slot-4` up front, using partitionable device counters for
  the capacity, and bind real hardware to a slot when it arrives. It
  works against the model at every point. The slot is an identity that
  no hardware has, so the operator must invent the mapping and keep it;
  a claim on `slot-2` says nothing about which hardware a pod receives;
  and a person cannot add a fifth. Device identity that comes from the
  hardware needs none of it.

## The instances

* **Milestone 57, [the display operator](57-the-display-operator.md).**
  `display.liken.sh`. It claims the GPU's exclusive display device,
  runs a Weston compositor, and publishes one device for each monitor
  output.
* **Milestone 58, [the Bluetooth operator](58-the-bluetooth-operator.md).**
  `bluetooth.liken.sh`. It claims the Bluetooth adapter, runs
  bluetoothd, and publishes one device for each paired controller.
* **Milestone 59, [the audio operator](59-the-audio-operator.md).**
  `audio.liken.sh`. It claims the audio controller, runs PipeWire, and
  publishes one device for each physical output.

All three are proposed. Each has its own drill, because the
pattern is only proven by hardware.

## Open questions

These were the questions this milestone could not answer. Each one
below records what happened to it.

* **The repositories.** Answered: `liken-sh` holds all three, as
  [display-operator](https://github.com/liken-sh/display-operator),
  [bluetooth-operator](https://github.com/liken-sh/bluetooth-operator),
  and [audio-operator](https://github.com/liken-sh/audio-operator),
  each MIT-licensed with its own images on ghcr.io.
* **How the Kustomization publishes.** Answered: the git form. Each
  repository has a kustomize base in `deploy/`, and a person's
  GitOps references it by tag. No OCI artifact ships beside the
  images. The digest-pinned argument for OCI stands; nothing has
  needed it yet.
* **Where these instance plans live.** Answered, both ways. Milestones
  57, 58, and 59 stay here as the proposals they were, and each
  operator's own design documents are beside its code in that
  repository's `plans/`. The instance plan that supersedes part of
  this one says so and links back.
* **What the images hold.** Mostly answered. Every operator binary
  ships in a `FROM scratch` image. The Bluetooth pod is two
  containers, bluetoothd static against musl beside the operator, and
  the privilege lands only on the daemon's container. The display
  image is a computed library closure on scratch, as predicted, and it
  is 252 MB because Debian builds mesa with llvmpipe, which no liken
  machine runs:
  [LLVM is two thirds of the image](https://github.com/liken-sh/display-operator/blob/main/plans/open-problems/llvm-is-two-thirds-of-the-image.md).
  The audio image is still debian-slim and is the one that has not
  moved:
  [The image is still Debian](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/the-image-is-still-debian.md).
  The die-together reasoning held for Bluetooth and reversed for
  display: nothing in a pod spec binds one container's life to
  another's, and weston's death must end the operator, so that pod
  stays one container on purpose.
