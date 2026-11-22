---
title: Troubleshoot
weight: 80
description: "Diagnose a liken machine from its Machine resource: the phase, the condition that is False, and the status field it names, for each failure from the stick menu to a Pending device claim. Use when a machine is Lost, Blocked, crashed, on the old version, waits for a reboot turn, stays drained without a reboot, refuses a disk, or would run another Cluster document at its next boot."
---

# Troubleshoot

A `liken` machine reports its problems in the Machine resource. Read
in this order: the phase, then the condition that is `False`, then
the status field that the condition's message names.

    kubectl get machines
    kubectl describe machine <name>

`Ready` means every condition is true. `Blocked` means the machine
refuses to apply a change, and waiting does not fix it. `Lost` means
the machine stopped sending its heartbeat. Each section below starts
from a symptom.

## The machine stays at the stick menu

The menu has no timeout, so the machine waits there until you select
an entry.

If the machine returned to the menu after an installation, the stick
is still connected and is first in the boot order. Power the machine
off, remove the stick, and power it on again. It then boots from its
own disk.

## The installation refuses a disk

`install as <name>` uses blank disks only, so it never erases data
that it did not write. To replace an installation that `liken` made,
select `wipe and reinstall as <name>` instead.
[Install a cluster](/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
explains both entries and how to replace a disk.

## The installation fails and stops

A failed installation prints its error, lists the machine's disks,
and leaves the error on the console. Fix the cause and boot the
installation again. It is safe to run an installation more than once.

If the error says that a disk needs a driver that the installer does
not have, run the hardware report. It names the driver and leaves
that disk out of the layout it proposes.
[Install a cluster](/docs/guides/install/#first-run-the-hardware-report)
describes the report.

## A machine does not join the cluster

Install the first leader first. A follower that boots before any
leader is running waits until one is.

A network port in the manifest with no cable delays each boot, because
the machine waits up to 30 seconds for that port. Remove the port from
the manifest, or connect the cable.

If the machine still does not appear in `kubectl get machines`,
watch its console. Each step of the boot prints there, and a boot
that fails leaves its reason on the screen.

## A machine shows Lost

`cluster-operator` marks a machine `Lost` when the machine's
heartbeat lease in `liken-system` has not changed for 40 seconds.
`cluster-operator` times this on its own clock, so a wrong clock on
the machine does not cause `Lost`, and the `MachineLost` event gives
the time of the last heartbeat on that clock. Either the machine is
powered off, or it cannot reach the cluster. When the machine comes
back, it writes its own phase again.

If the machine is running and `Lost`, check its network path to the
leaders. Also check whether `liken-machine-operator` restarted on that
node. The operator stops its heartbeat and exits when its reconcile
loop has been busy for 60 seconds, and the kubelet then starts it
again.

A `Lost` machine counts against every
[`MachineDisruptionBudget`](/docs/reference/machinedisruptionbudget/)
that selects it, so it can stop other machines from taking their
reboot turns. If the machine will not come back, remove it as in
[Remove machines](/docs/guides/remove-nodes/#a-machine-that-is-already-gone).

## A machine shows Blocked

The machine refuses to stage a change. The condition that is `False`
gives the reason:

* `StagingRejected`: the spec asks for something the machine cannot
  do. A common case is a reinstallation with a different layout, after
  which the spec gives a storage role a smaller size than its
  partition on the disk. The
  condition's message gives the sizes.
  [Install a cluster](/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
  gives the order of the fixes.
* `RejectedLastBoot`: the machine booted the change once, and the
  boot failed. See the next section.
* `DigestMismatch`: the `release.yaml` that the machine downloaded
  does not match the `digest` of its entry in the catalog. A second
  download gets the same bytes, so the machine stays on its release
  until you correct the entry. Compute the digest again as
  [Upgrade the fleet](/docs/guides/upgrade/#1-find-the-release)
  shows.
* Any other reason: the condition's message says what to change,
  such as the spec, the catalog, or a credentials `Secret`.

## A machine returned to the old version

The machine booted the new version once, the boot failed, and the
machine went back to the slot that it had booted successfully before.
Its conditions show `RejectedLastBoot`, and
[`status.boot.systemRejection`](/docs/reference/machine/#statusbootsystemrejection)
records what happened. The machine does not boot that version again
until [`spec.version`](/docs/reference/cluster/#spec--version)
points at a different release. [Roll back](/docs/guides/rollback/)
describes the fallback and how to fix it.

## The fleet stays on the old version

`kubectl get machines` shows each machine's version in the LIKEN
column. For a machine that did not change version, read its
conditions:

* `RebootPending`: the machine's `rebootPolicy` is `Manual`, which is
  the default, and the machine waits for you to approve the reboot.
  Read what it waits for, then approve the reboot with
  [`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
* `AwaitingTurn`: the machine waits for a reboot turn.
  [A machine waits for its reboot turn](#a-machine-waits-for-its-reboot-turn)
  shows how to find what it waits for.
* `Downloading`: the machine is still downloading or verifying the
  release. While it downloads, the machine takes no reboot turn for
  any other change, so the other changes and the release share one
  reboot.
* `DownloadRetrying`: a download failed, and the machine retries it
  on its own. The phase is still `Downloading`. A download that
  receives no bytes for one minute stops and retries. The first retry
  waits 10 seconds, and each later one waits twice as long, up to 2
  minutes. When the target release changes, the new download starts
  at once. The message gives the reason for the last failure and the
  time of the next retry. Unlike `Downloading`, this reason does not
  hold back a reboot turn, so the machine's other changes can still
  reboot.
* `StagingFailed`: the machine could not write or remove a staged
  record, and the message gives the error. When the target release
  changes while another release is staged, the machine downloads the
  new release only after it removes the old record.
* `AwaitingPodRefresh`: the machine runs the new release, and its
  `liken-machine-operator` pod still runs the old template.
  `cluster-operator` deletes that pod after a leader boots the
  release, and the DaemonSet creates it again from the new template.

If the Cluster's `Progressing` condition is `False` with the reason
`RolloutStalled`, a machine that was granted a reboot turn has not
come back within 10 minutes. The cluster grants no more turns until
that machine is back or removed, so the rest of the fleet stays as it
is.

## A machine waits for its reboot turn

A machine that reports `AwaitingTurn` has a request that the cluster
has not granted yet.
[The reboot turn](/docs/concepts/how-liken-works/#the-reboot-turn)
explains the requests and the budgets. Start with the requests:

    kubectl get disruptions -o wide

The wide listing adds the COMPLETE and REASON columns. Read the
request of the machine:

* APPROVED is `False`: the machine's `rebootPolicy` is `Manual`, and
  the request waits for your approval. Run
  [`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
* APPROVED is `True` and GRANTED is `False`: the request waits for a
  turn. The message of the `Granted` condition gives the cause:

      kubectl describe disruption <request>

  The cause can be a budget that has no room, with the machines that
  fill it, another leader that is out of service, a release that is
  still downloading, a stalled rollout, or a machine whose heartbeat
  lease `cluster-operator` has not seen renewed in the first 40
  seconds after it starts. When the machine itself is out of
  service, the message gives its phase, such as `Lost` or `Degraded`,
  and the request waits until the machine is back. A message that
  says the request waits for the next `cluster-operator` pass clears
  within seconds.

When a budget has no room, read the budgets:

    kubectl get mdb

The OUT column counts the selected machines that are out of service,
and ALLOWED is how many more can go out of service. The
`DisruptionAllowed` condition of a full budget is `False` with the
reason `BudgetFull`, and its message lists the machines that fill it:

    kubectl describe mdb <name>

A machine counts against a budget while it holds a reboot turn, and
also while it is `Lost`, `Degraded`, or unavailable for any other
reason. Bring that machine back, or remove it as in
[Remove machines](/docs/guides/remove-nodes/#a-machine-that-is-already-gone).
To allow more machines out of service at the same time, raise the
budget's `spec.maxUnavailable`.

The Cluster's
[`status.rebootTurns`](/docs/reference/cluster/#statusrebootturns)
lists each machine that holds a reboot turn, with the requests that
the turn covers. Each entry counts against the budgets, and
`machine-operator` drains and reboots its machine only while the
machine has an entry. The machine's `RebootApproved` condition can be
missing while the entry exists, because `cluster-operator` writes the
entry first and the condition second.

`cluster-operator` removes an entry when the `Machine` no longer
exists. It also removes an entry when the machine has no
`RebootApproved` condition, the `Machine` has changed since the entry
was written, and either the entry lists no request or the machine is
available and none of the entry's requests is still open. So a
machine that goes down during its turn keeps an entry that lists a
request until the machine comes back.

If the `Progressing` message says that reboot turns wait for a Cluster
schema that stores `status.rebootTurns`, the API server is serving the
Cluster CRD of an older release and drops that field from each write.
This happens briefly after the first leader boots a new release, and
while a leader on an older release restarts k3s. `cluster-operator`
grants no turn until the newer CRD is back. Each leader applies it
again when it boots the newer release.

## A machine stays drained and does not reboot

A request with `spec.hold` pauses its machine's reboot turn after the
drain, until the component that created the request removes the hold.
The HELD column reads `True` for that request:

    kubectl get disruptions

The `Held` condition's message gives the component that the machine
waits for, and the condition's time is when the hold began:

    kubectl describe disruption <request>

The component's own resource says why its work has not finished. For
`firmware-operator`, that is the `Firmware` that the request refers
to. While the machine is held, it counts against its budgets, so the
other machines' reboot turns wait too. To reboot without waiting for
the component, remove the hold:

    kubectl patch disruption <request> --type=json -p '[{"op":"remove","path":"/spec/hold"}]'

`Held` turns `False` with the reason `Released`, and the machine
reboots.

## A reboot did not keep a component's BootNext

A component that sets `spec.keepBootNext` on its request, such as
`firmware-operator` for a UEFI capsule, expects the reboot to boot its
own entry once. When its resource reports that the update did not run,
check whether `init` kept `BootNext` for the reboot:

    kubectl get machine <machine> -o jsonpath='{.status.firmware.bootOrderKept}'

`init` keeps `BootNext` only when this reads `true`. Each reboot puts
the slot that the machine last booted successfully first in
`BootOrder`. `false` means that slot was not first at power-on, so
either the firmware did not keep the order through the reset, or
someone changed it in the firmware's menu. `init` then cannot tell
which entry boots after the component's boot, so it does not keep
`BootNext`. An empty value means the machine has not yet booted a
release successfully: upgrade it once, and the next boot sets the
value. The machine's console log gives the request and the reason in
a line that starts with
`liken: system: init does not leave BootNext as the Disruption`.

## A machine crashed

A kernel crash record survives the reboot. The next boot reads the
crash from the machine's crash journal and reports it in the Machine's
[`status.lastCrash`](/docs/reference/machine/#statuslastcrash), with
the log lines that the kernel wrote as it failed:

    kubectl get machine <name> -o jsonpath='{.status.lastCrash}' | jq

## A module parameter did not take

Read the parameter values that the kernel reports for the module:

    kubectl get machine <name> -o jsonpath='{.status.modules}' | jq

Each declared parameter that the kernel exposes as a readable file
appears under the module's `parameters`, with the value written the
way the kernel writes it. A parameter that is missing there usually
has a wrong name. The kernel still loads the module, and logs
`unknown parameter ... ignored` once, in the machine's log at the
time of the load. If that line is in the log, check the parameter's
spelling against the driver's documentation and correct the spec. If
the line is not in the log, the name is right, and the driver
registers the parameter with no readable file. The kernel still
applied the value when the module loaded.

If the parameter's name is right but the `ModuleParametersApplied`
condition is `False`, the condition's message gives the cause:

* The module is built into the kernel, so it is never loaded and the
  parameter must be set another way.
* Something loaded the module before the declared modules ran, so it
  did not load with your parameters. The message names what loaded it
  first.

## A disk setting did not take

Read the request size that each disk has now, and its hardware limit:

    kubectl get machine <name> -o jsonpath='{.status.hardware.blockDevices}' | jq '.[] | {name, maxRequestSize, maxHardwareRequestSize}'

When the `DiskSettingsApplied` condition is `False`, its reason and
message name each entry of `spec.disks` that the machine skipped:
`DiskNotFound` for a name that no attached disk has,
`AboveHardwareLimit` for a value above `maxHardwareRequestSize`,
`InvalidSetting` for an entry that the machine cannot use, and
`ApplyFailed` for a write that the kernel refused. The disk of a
skipped entry keeps the default.
[Disk settings](/docs/reference/disks/#when-a-value-is-refused) has
the fix for each reason.

## A machine needs a reboot and nothing is staged

On its own, a machine reboots only to apply a staged change, so a
machine that already matches its documents does not reboot. Some faults clear only
at boot. A kernel driver that bound the wrong device keeps it until
the machine restarts, and no edit changes that. To reboot such a
machine, run
[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot):

    ./liken request-reboot mycluster <name>

The command creates the request `<name>.reboot-<boot>`, which
`kubectl get disruptions` lists. The machine waits for its reboot
turn, cordons, and drains, the same as a machine that applies a
staged change. If its `rebootPolicy` is `Manual`, it reports
`RebootPending` and waits for
[`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
The `RebootRequestHonored` condition reports the progress of the
request, and the request reads `Complete` when the machine has booted
again.

## The next boot would run another Cluster document

Each machine keeps copies of the `Cluster` document on its
`machineState` disk: a staged copy that waits for the restart or the
reboot that applies it, and a proven copy that the next boot runs when
nothing is staged. `status.boot.clusterManifestHash` is the document
that the machine runs now, and `status.nextBoot.clusterManifestHash`
is the one that its next boot would run:

    kubectl get machine <name> -o jsonpath='{.status.boot.clusterManifestHash}{"\n"}{.status.nextBoot}{"\n"}'

The `ClusterProven` condition compares the two. It is `True` with the
reason `Current` when they are the same document. While a `Cluster`
edit waits to apply, it is `False` with `ChangeStaged`, and
`ClusterConverged` reports how the edit is applied.

`NextBootDiffers` means that the proven copy is not the document that
the machine runs, so the next boot would run an older document, and
k3s would remove the features that the older document turns off until
the machine applies the current one again. The machine operator
replaces the proven copy with the running document in the pass that
finds the difference, and posts a `ClusterProvenRepaired` `Event`, so
`NextBootDiffers` stays only while the operator cannot write the
`machineState` disk. The log of the `liken-machine-operator` pod on
that machine has the error of each write that failed, as
[Reading logs](#reading-logs) shows. Do not reboot the machine while
`ClusterProven` is `NextBootDiffers`.

## A pod with a device claim stays Pending

[Give a workload a device](/docs/guides/devices/#when-a-claim-does-not-schedule)
gives the checks, in order.

## A pod with a device claim was evicted

When a device is unplugged, the pods whose claim held it are evicted,
and the `Machine` shows a `DeviceUnplugged` event with the device and
the claim. A pod that a `Deployment`, a `DaemonSet`, or another
controller owns is created again, and its claim is allocated to a
matching device on any machine. If the new pod stays `Pending`, no
device matches, so plug the device in again. A pod that no controller
owns is not created again, so create it yourself.

A pod that uses a named `ResourceClaim` that holds an unplugged device
is not evicted. Its container fails to start, and its events mention
`/dev/liken.sh/device-absent`. Delete the pods that use the claim,
then delete the claim and create it again. The new claim is allocated
to the device at its current location. The
[device reference](/docs/reference/devices/#an-unplugged-device-evicts-its-pods)
gives the rules.

## The recent history of a machine or the cluster

The operators post a Kubernetes `Event` for each change of a
condition, on a `Machine`, a `Cluster`, a `Disruption`, or a
`MachineDisruptionBudget`, and for each action they take, such as
marking a machine `Lost`, granting a reboot turn, refusing a spec,
or finding a kernel crash. `kubectl describe` lists the events of the last hour below the
status:

    kubectl describe machine <name>
    kubectl describe cluster <name>
    kubectl describe disruption <request>

A `Machine`, a `Cluster`, a `Disruption`, and a
`MachineDisruptionBudget` are cluster-scoped, so their events are in
the `default` namespace. `kubectl events` lists them only with
`-n default` or `-A`:

    kubectl events -n default --for machine/<name>

A `Warning` event needs a person to act. A `Normal` event records a
change that went as expected. The API server deletes an event one hour
after its last repeat, so facts that must last longer are in the
conditions and the status fields.

The event for a condition change has the condition's reason, such as
`StagingRejected` or `RejectedLastBoot`, and its message starts with
the condition's name. The other events are these:

| Reason | Object | Type | Meaning |
| --- | --- | --- | --- |
| `MachineJoined` | `Machine` | `Normal` | The machine's operator created the `Machine` from the boot manifest, because the cluster had none. |
| `MachineLost` | `Machine` | `Warning` | The heartbeat stopped. The message gives the time of the last heartbeat, on the cluster operator's clock. |
| `KernelCrashed` | `Machine` | `Warning` | The boot found a new kernel crash record. [`status.lastCrash`](/docs/reference/machine/#statuslastcrash) holds it. |
| `BootRefused` | `Machine` | `Warning` | `init` refused a boot and powered the machine off. [`status.lastFailStop`](/docs/reference/machine/#status--lastfailstop) holds the reason. |
| `Cordoned` | `Machine` | `Normal` | The operator cordoned the machine's `Node` before a reboot. |
| `Uncordoned` | `Machine` | `Normal` | The operator made the `Node` schedulable again, after the reboot or after the request was removed during the drain. The message says which. |
| `Roamed` | `Machine` | `Normal` | A radio moved from one access point of its network to another. The message gives both access points, with the channel and the band of each. |
| `Associated` | `Machine` | `Normal` | A radio associated with an access point, at boot or after it lost its previous one. The message gives the access point and the reason for the loss. |
| `DeviceUnplugged` | `Machine` | `Warning` | A device that a prepared claim held was unplugged, or appeared again at its address. The machine taints the device `liken.sh/unplugged`, and Kubernetes evicts the claim's pods. The message gives the device and the claim. The [device reference](/docs/reference/devices/#an-unplugged-device-evicts-its-pods) says what follows. |
| `BackstopRepaired` | `Machine` | `Warning` | The operator's periodic backstop pass found and corrected a change that no event had started a pass for. The machine is correct again. A missed event is a bug in `liken`, so report it with the message, which names each step that wrote. |
| `ClusterProvenRepaired` | `Machine` | `Normal` | The machine's proven copy of the `Cluster` document was not the document that the machine runs, so its next boot would have run another document. The operator wrote the running document as the proven copy. The message gives the hash of both documents. [The next boot would run another Cluster document](#the-next-boot-would-run-another-cluster-document) explains the copies. |
| `RebootTurnGranted` | `Machine` | `Normal` | The cluster granted the machine a reboot turn. |
| `RebootTurnReclaimed` | `Machine` | `Normal` | The machine no longer needs its reboot turn, so the turn no longer counts against the budgets. |
| `DisruptionDeleted` | `Disruption` | `Normal` | `cluster-operator` deleted a request that completed more than a day ago. |
| `MachineDisruptionBudgetCreated` | `Cluster` | `Normal` | `cluster-operator` created the `default` budget, which allows one machine out of service at a time. Edit the budget to change that limit. |
| `FluxDeployKeyMinted` | `Cluster` | `Normal` | The cluster operator created the deploy key. Register its public key at the forge. |
| `FluxEngineSeeded` | `Cluster` | `Normal` | The Flux engine was missing, so the cluster operator installed it from the manifest that the release includes. |

## Reading logs

[`liken stern`](/docs/reference/cli/#liken-stern) tails the logs of
many pods at once, with the deployment's credential:

    ./liken stern mycluster <pod-name-pattern>

When the logs do not explain a problem, the Cluster's
[`spec.runtime`](/docs/reference/cluster/#specruntime) section raises
the log level of k3s or containerd, with one field for each, and
describes what each level costs.

## Reaching a machine's filesystem

A `liken` machine has no shell and no SSH, and each of its own images
has one binary, so `kubectl exec` has nothing to run. `kubectl debug`
starts a shell on the node itself, with the host's processes visible
and the host's filesystem mounted at `/host`:

    kubectl debug node/<name> -it --profile=sysadmin --image=busybox:1.38.0

The pod runs privileged in the host's PID namespace, so `ps` lists
every process on the machine and `kill` can signal them. Delete the
debug pod when you are done, because `kubectl debug` does not remove
it.
