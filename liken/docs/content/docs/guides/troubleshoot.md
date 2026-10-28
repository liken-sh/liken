---
title: Troubleshoot
weight: 80
description: "Diagnose a liken machine from its Machine resource: the phase, the condition that is False, and the status field it names, for each failure from the stick menu to a Pending device claim. Use when a machine is Lost, Blocked, crashed, on the old version, waits for a reboot turn, stays drained without a reboot, or refuses a disk."
---

# Troubleshoot

A `liken` machine reports its problems in the Machine resource. Read
in this order: the phase, then the condition that is `False`, then
the status field that the condition's message names.

    kubectl get machines
    kubectl describe machine <name>

The phase gives the machine's state in one word. `Ready` means every
condition is true. `Blocked` means a change exists that the system
refuses to apply, and more time will not fix it. `Lost` means the
machine's heartbeat stopped. The sections below start from the
symptom you see.

## The machine stays at the stick menu

The menu has no timeout, so a machine at the menu waits for a
person. Select an entry.

If the machine returned to the menu after an installation, the stick
is still connected. The stick is first in the boot order. Power the
machine off, remove the stick, and power it on again. It then boots
from its own disk.

## The installation refuses a disk

`install as <name>` claims blank disks only, so it does not erase
data that it did not write. To replace an installation that `liken`
made, select `wipe and reinstall as <name>` instead.
[Install a cluster](/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
explains both paths, and the disk-replacement case between them.

## The installation fails and holds

A failed installation prints its error, lists the machine's disks,
and holds the console. Correct the cause and boot the installation
again. An installation is idempotent, so a second attempt is safe.

If the error says that a disk needs a driver the boot path does not
have, run the hardware report. The report names the driver and
keeps that disk out of the proposed layout.
[Install a cluster](/docs/guides/install/#first-run-the-hardware-report)
describes the report.

## A machine does not join the cluster

Install the first leader first. If a follower boots before any leader
serves, the follower waits for a leader.

A declared network port with no cable delays each boot, because the
machine waits a maximum of thirty seconds for that port. Remove the
port from the manifest, or connect the cable.

If the machine still does not appear in `kubectl get machines`,
watch its console. Every step of the boot prints there, and a boot
that fails holds its reason on the screen.

## A machine shows Lost

The machine stopped sending its heartbeat, so the cluster operator
wrote the phase on its behalf. The cluster operator marks a machine
`Lost` when its heartbeat lease in `liken-system` has not changed for
40 seconds. It measures the 40 seconds on its own clock, from the
moment it saw the last change, so a machine whose clock is wrong does
not read `Lost` for that reason. The `MachineLost` event gives the
time of the last heartbeat, on the cluster operator's clock. The
machine is powered off, or it cannot reach the cluster. When the
machine returns, its next report overwrites the phase. If the machine
is running and `Lost`, check its network path to the leaders, and check
whether `liken-machine-operator` restarted on that node. The operator
stops its heartbeat and ends its own process when its reconcile loop has
been busy for 60 seconds, and the kubelet then starts it again.

A `Lost` machine counts against every
[`MachineDisruptionBudget`](/docs/reference/machinedisruptionbudget/)
that selects it, so while it stays, other machines wait for their
reboot turns. If the machine will
not return, remove it as in
[Remove machines](/docs/guides/remove-nodes/#a-machine-that-is-already-gone).

## A machine shows Blocked

A change exists that the system refuses to stage. The condition that
is `False` names the reason:

* `StagingRejected`: the spec asks for something the machine cannot
  do. The common case is a storage role that is smaller in the spec
  than on the disk, after a reinstallation with a different layout.
  The condition's message gives the sizes.
  [Install a cluster](/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
  gives the order of the corrections.
* `RejectedLastBoot`: the machine booted the change one time, and
  the boot failed. See the next section.

## A machine returned to the old version

The machine booted the new version one time, the trial failed, and
the machine returned to the slot it had proved. Its conditions show
`RejectedLastBoot`, and
[`status.boot.systemRejection`](/docs/reference/machine/#statusbootsystemrejection)
records what happened. The machine does not boot that version again
until [`spec.version`](/docs/reference/cluster/#spec--version)
points at a different release. [Roll back](/docs/guides/rollback/) describes the
fallback and the correction.

## The fleet stays on the old version

`kubectl get machines` shows each machine's version in the LIKEN
column. For a machine that did not move, read its conditions:

* `RebootPending`: the machine's `rebootPolicy` is `Manual`, which is
  the default, and the machine waits for you. Read what it waits for
  and grant the reboot with
  [`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
* `AwaitingTurn`: the machine waits for a reboot turn.
  [A machine waits for its reboot turn](#a-machine-waits-for-its-reboot-turn)
  shows how to find what it waits for.
* `Downloading`: the machine still fetches or verifies the release.
  A slow link makes this step long. While the release downloads,
  the machine takes no reboot turn for another change, so the
  release shares that reboot.
* `DownloadRetrying`: a download failed, and the machine retries it
  on its own. The phase is still `Downloading`. A download that
  receives no bytes for one minute stops and retries. A download that
  fails waits 10 seconds before the first retry, and the wait doubles
  after each failure up to 2 minutes. A change of the target release
  starts the new download without that wait. The condition's message
  gives the reason of the last failure and the time of the next
  retry. The reason stays `DownloadRetrying` until a download
  verifies. Unlike `Downloading`, it holds no reboot turn, so the
  machine's other changes can still take their turn.
* `StagingFailed`: the machine could not write or withdraw a staged
  record. The message gives the error. When the target changed while
  another release was staged, the machine does not download the new
  release until it withdraws the old record.
* `AwaitingPodRefresh`: the machine runs the new release and waits
  for the pod steward to replace its operator pod from the new
  template. The steward does that after a leader boots the release.

If the Cluster's `Progressing` condition is `False` with the reason
`RolloutStalled`, a machine with a granted turn did not return. The
cluster grants no more turns until you examine that machine, so the
rest of the fleet is safe while you do.

## A machine waits for its reboot turn

A machine that reports `AwaitingTurn` has a request that waits for
the cluster.
[The reboot turn](/docs/concepts/how-liken-works/#the-reboot-turn)
explains the requests and the budgets. Start with the requests:

    kubectl get disruptions -o wide

The wide listing adds the COMPLETE and REASON columns. Read the
request of the machine:

* APPROVED is `False`: the machine's `rebootPolicy` is `Manual`, and
  the request waits for you. Run
  [`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
* APPROVED is `True` and GRANTED is `False`: the request waits for a
  turn. The message of the `Granted` condition names the cause:

      kubectl describe disruption <request>

  The message names a budget with no room and the machines that fill
  it, another leader that is out of service, a release that still
  downloads, a stalled rollout, or the first 40 seconds after
  `cluster-operator` starts. A message that says the machine is out
  of service with no turn names its phase, such as `Lost` or
  `Degraded`: the request waits until the machine is back. A message
  that says it waits for the next sweep clears within seconds.

When a budget has no room, read the budgets:

    kubectl get mdb

The OUT column counts the selected machines that are out of service,
and ALLOWED is how many more can go. The `DisruptionAllowed`
condition of a full budget is `False` with the reason `BudgetFull`,
and its message names the machines that fill it:

    kubectl describe mdb <name>

A machine fills a budget while it holds a turn, and also while it is
`Lost`, `Degraded`, or unavailable for any other reason. Bring that
machine back, or remove it as in
[Remove machines](/docs/guides/remove-nodes/#a-machine-that-is-already-gone).
To allow more machines out at the same time, raise the budget's
`spec.maxUnavailable`.

The Cluster's
[`status.rebootTurns`](/docs/reference/cluster/#statusrebootturns)
names each machine that holds a reboot turn, with the requests that
the turn covers, and each entry counts against the budgets. An entry
is the grant: `machine-operator` acts on its machine's entry. An
entry can name a machine with no `RebootApproved` condition, because
`cluster-operator` records a turn before it writes the condition, and
a copy that stopped between the two writes leaves the record alone.
`cluster-operator` then writes the condition itself, when the machine
is available. It removes the entry when the Machine is gone. It also
removes the entry when the machine has no `RebootApproved` condition,
its `resourceVersion` differs from the entry's
`machineResourceVersion`, and the machine is available with no open
request in the entry. A machine that is down keeps an entry that
names a request until it returns. An entry that names no request
leaves when the machine reads `Lost`.

If the `Progressing` message says that reboot turns wait for a Cluster
schema that stores `status.rebootTurns`, the API server serves the
Cluster CRD of an older release, and drops the field from each write.
That happens for a short time after the first leader boots a new
release, and while a leader on an older release starts k3s again.
`cluster-operator` grants no turn until the newer CRD returns. The
leaders apply it again when each one boots the newer release.

## A machine stays drained and does not reboot

A request with `spec.hold` stops its machine's turn after the drain,
until the component that filed it removes the hold. The HELD column
reads `True` for that request:

    kubectl get disruptions

The `Held` condition's message names the component that the machine
waits for, and the condition's time is when the hold began:

    kubectl describe disruption <request>

The component's own resource says why its work has not finished. For
`firmware-operator`, that is the `Firmware` the request names. While
the machine is held, it counts against its budgets, so the other
machines' turns wait too. To let the reboot go ahead without the
component, remove the hold:

    kubectl patch disruption <request> --type=json -p '[{"op":"remove","path":"/spec/hold"}]'

`Held` turns `False` with the reason `Released`, and the machine
reboots.

## A reboot did not keep a component's BootNext

A component that sets `spec.keepBootNext` on its request, such as
`firmware-operator` for a UEFI capsule, expects the reboot to boot its
own entry once. When its resource reports that the update did not run,
read whether `init` kept `BootNext` for the reboot:

    kubectl get machine <machine> -o jsonpath='{.status.firmware.bootOrderKept}'

`init` keeps `BootNext` only when this reads `true`. `false` means the
proven slot did not lead `BootOrder` at power-on: the firmware did not
keep the order through the reset, or someone changed it in the
firmware's menu. The boot after the component's would then follow an
order that `init` does not control, so `init` refuses. An empty value
means the machine has not proven a release yet: upgrade it once, and
the next boot answers. The machine's console log names the request and
the reason in a line that starts with
`liken: system: refusing to leave BootNext`.

## A machine crashed

The machine keeps a record of a kernel crash through the reboot. The
next boot reads the crash from the machine's crash journal and reports
it in the Machine's
[`status.lastCrash`](/docs/reference/machine/#statuslastcrash), with
the log lines that the kernel wrote as it failed:

    kubectl get machine <name> -o jsonpath='{.status.lastCrash}' | jq

## A module parameter did not take

Read back what the kernel holds for the module:

    kubectl get machine <name> -o jsonpath='{.status.modules}' | jq

Each declared parameter that the kernel offers a readable file for
appears under that module's `parameters`, with the value in the
kernel's own spelling. A parameter that is missing there usually has
a wrong name. The kernel loads the module anyway and says so only
once, as `unknown parameter ... ignored`, in the machine's log
stream at the moment of the load. If that line is in the log, check
the parameter's spelling against the driver's documentation and
correct the spec. If it is not, the parameter is real but the
kernel offers no readable file for it; some drivers register a
parameter without one, and the setting still applied at the load.

If the parameter's name is right but the `ModuleParametersApplied`
condition is `False`, the condition's message names one of two
cases. The module is built into the kernel, so no load happened and
the setting must go elsewhere. Or the module was already loaded
before the declared modules ran, so the load that would have applied
your string did not run; the message names what loaded it first.

## A machine needs a reboot and nothing is staged

A machine converges to its documents, so a machine that already
matches them stages nothing and has no reason to reboot. Some faults
clear only at boot anyway. A kernel driver that bound the wrong
device holds it until the machine restarts, and no edit takes it
back.

[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot)
asks for that boot:

    ./liken request-reboot mycluster <name>

The command files the request `<name>.reboot-<boot>`, which
`kubectl get disruptions` lists. The machine waits for its reboot
turn, cordons, and drains, the same as a machine applying a staged
change. If its `rebootPolicy` is `Manual`, it reports `RebootPending`
and waits for
[`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
The `RebootRequestHonored` condition reports the progress of the
request, and the request reads `Complete` when the machine runs
another boot.

## A pod with a device claim stays Pending

[Give a workload a device](/docs/guides/devices/#when-a-claim-does-not-schedule)
gives the checks, in order.

## A pod with a device claim was evicted

A pod whose claim held a device that was unplugged is evicted, and its
`Machine` shows a `DeviceUnplugged` event that names the device and
the claim. A pod that a `Deployment`, a `DaemonSet`, or another
controller owns comes back as a new pod, and its claim is allocated to
a device that matches it, on any machine. If the new pod stays
`Pending`, no device matches: plug the device in again. A pod that no
controller owns does not come back; create it again.

A pod whose named `ResourceClaim` holds an unplugged device is not
evicted. Its container fails to start, and its events name
`/dev/liken.sh/device-absent`. Delete the pods that use the claim,
then delete the claim and create it again. The new claim is allocated
to the device where it is now. The
[device reference](/docs/reference/devices/#an-unplugged-device-evicts-its-pods)
gives the rules.

## The recent history of a machine or the cluster

The operators post a Kubernetes `Event` for each change of a
condition, and for each action they take: a machine marked `Lost`, a
reboot turn granted, a spec refused, a kernel crash found. The
conditions of a `Disruption` and of a `MachineDisruptionBudget` post
events too. `kubectl describe` lists the events of the last hour below
the status:

    kubectl describe machine <name>
    kubectl describe cluster <name>
    kubectl describe disruption <request>

A `Machine`, a `Cluster`, a `Disruption`, and a
`MachineDisruptionBudget` are in no namespace, so their events are
in the `default` namespace. `kubectl events` finds them only with
`-n default` or `-A`:

    kubectl events -n default --for machine/<name>

A `Warning` event needs a person. A `Normal` event records a change
that went as expected. The API server deletes an event one hour after
its last repeat, so the conditions and the status fields keep the
facts that must last longer.

The event of a condition change has the condition's reason, for
example `StagingRejected` or `RejectedLastBoot`, and its message
starts with the condition's name. The other events are these:

| Reason | Object | Type | Meaning |
| --- | --- | --- | --- |
| `MachineJoined` | `Machine` | `Normal` | The machine's operator created the `Machine` from the boot manifest, because the cluster held none. |
| `MachineLost` | `Machine` | `Warning` | The heartbeat stopped. The message gives the time of the last heartbeat, on the cluster operator's clock. |
| `KernelCrashed` | `Machine` | `Warning` | The boot found a new kernel crash record. [`status.lastCrash`](/docs/reference/machine/#statuslastcrash) holds it. |
| `BootRefused` | `Machine` | `Warning` | Init refused a boot and powered the machine off. [`status.lastFailStop`](/docs/reference/machine/#status--lastfailstop) holds the reason. |
| `Cordoned` | `Machine` | `Normal` | The operator cordoned the machine's `Node` before a reboot. |
| `Uncordoned` | `Machine` | `Normal` | The operator returned the `Node` to the scheduler after the reboot, or after the request went away during the drain. The message says which. |
| `Roamed` | `Machine` | `Normal` | A radio moved from one access point of its network to another. The message names both, with the channel and the band of each. |
| `Associated` | `Machine` | `Normal` | A radio associated with an access point: at boot, or again after it lost one. The message names the access point and the reason for the loss. |
| `DeviceUnplugged` | `Machine` | `Warning` | A device that a prepared claim held was unplugged, or enumerated again at its address. The machine taints it `liken.sh/unplugged`, and Kubernetes evicts the claim's pods. The message names the device and the claim. The [device reference](/docs/reference/devices/#an-unplugged-device-evicts-its-pods) says what follows. |
| `BackstopRepaired` | `Machine` | `Warning` | A pass that no event started changed the machine. The message names each step that wrote. The operator missed a wake, which is a bug in `liken`: report it with the message. The machine is correct again. |
| `RebootTurnGranted` | `Machine` | `Normal` | The cluster granted the machine a reboot turn. |
| `RebootTurnReclaimed` | `Machine` | `Normal` | The machine no longer needs its turn, and the turn returns to the budgets. |
| `DisruptionDeleted` | `Disruption` | `Normal` | `cluster-operator` deleted a request that completed more than a day ago. |
| `MachineDisruptionBudgetCreated` | `Cluster` | `Normal` | `cluster-operator` created the `default` budget, which allows one machine out of service at a time. Edit the budget to change the limit. |
| `FluxDeployKeyMinted` | `Cluster` | `Normal` | The cluster operator made the deploy key. Register its public half at the forge. |
| `FluxEngineSeeded` | `Cluster` | `Normal` | The flux engine was absent, and the cluster operator created it from the seed that the release carries. |

## Reading logs

[`liken stern`](/docs/reference/cli/#liken-stern) tails the logs of
many pods at once, with the deployment's credential:

    ./liken stern mycluster <pod-name-pattern>

When the logs do not explain a problem, the Cluster's
[`spec.runtime`](/docs/reference/cluster/#specruntime) section raises
the log level of k3s or containerd, one field each, and says what
each level costs.

## Reaching a machine's filesystem

A `liken` machine has no shell and no SSH, and its own images hold
one binary each, so `kubectl exec` has nothing to run. `kubectl debug`
gives a shell on the node itself, with the host's process table and
the host's filesystem mounted at `/host`:

    kubectl debug node/<name> -it --profile=sysadmin --image=busybox:1.37.0

The pod runs privileged in the host's PID namespace, so `ps` lists
every process on the machine and `kill` can signal them. Delete the
debug pod when you are done; `kubectl debug` does not remove it.
