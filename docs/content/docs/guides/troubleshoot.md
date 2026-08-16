---
title: Troubleshoot
weight: 80
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
carry, run the hardware report. The report names the driver and
keeps that disk out of the proposed layout.
[Install a cluster](/docs/guides/install/#first-run-the-hardware-report)
describes the report.

## A machine does not join the cluster

Install the first leader first. A follower that boots before any
leader serves waits for one.

A declared network port with no cable delays each boot, because the
machine waits a maximum of thirty seconds for that port. Remove the
port from the manifest, or connect the cable.

If the machine still does not appear in `kubectl get machines`,
watch its console. Every step of the boot prints there, and a boot
that fails holds its reason on the screen.

## A machine shows Lost

The machine stopped sending its heartbeat, so the cluster operator
wrote the phase on its behalf. The machine is powered off, or it
cannot reach the cluster. When the machine returns, its next report
overwrites the phase. If the machine is running and `Lost`, check
its network path to the leaders.

## A machine shows Blocked

A change exists that the system refuses to stage. The condition that
is `False` names the reason:

* `StagingRejected`: the spec asks for something the machine cannot
  do. The common case is a storage role that is smaller in the spec
  than on the disk, after a reinstallation with a different layout.
  The condition's message gives the sizes.
  [Install a cluster](/docs/guides/install/#to-reinstall-a-machine-that-liken-installed)
  gives the order of the corrections.
* `RejectedLastBoot`: the machine tried the change in a boot, and
  the boot failed. See the next section.

## A machine returned to the old version

The machine tried the new version one time, the trial failed, and
the machine fell back to the slot it had proved. Its conditions show
`RejectedLastBoot`, and
[`status.boot.systemRejection`](/docs/reference/machine/#statusbootsystemrejection)
records what happened. The machine does not try that version again
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
* `AwaitingTurn`: the machine waits for the cluster's disruption
  budget. The default budget is one machine at a time, and only one
  leader is down at a time whatever the budget says.
* `Downloading`: the machine still fetches or verifies the release.
  A slow link makes this step long. The machine retries a failed
  download on its own.
* `AwaitingPodRefresh`: the machine runs the new release and waits
  for its operator pod to be recreated from the new template, which
  happens after a leader boots the release.

If the Cluster's `Progressing` condition is `False` with the reason
`RolloutStalled`, a machine with a granted turn did not return. The
cluster grants no more turns until you examine that machine, so the
rest of the fleet is safe while you do.

## A machine crashed

A kernel crash survives the reboot. The next boot reads the crash
from the machine's crash journal and reports it in the Machine's
[`status.lastCrash`](/docs/reference/machine/#statuslastcrash), with
the log lines that the kernel wrote as it failed:

    kubectl get machine <name> -o jsonpath='{.status.lastCrash}' | jq

## A machine needs a reboot and nothing is staged

A machine converges to its documents, so a machine that already
matches them stages nothing and reboots for nothing. Some faults
clear only at boot anyway. A kernel driver that bound the wrong
device holds it until the machine restarts, and no edit takes it
back.

[`liken request-reboot`](/docs/reference/cli/#liken-request-reboot)
asks for that boot:

    ./liken request-reboot mycluster <name>

The machine waits for its reboot turn, cordons, and drains, the same
as a machine applying a staged change. If its `rebootPolicy` is
`Manual`, it reports `RebootPending` and waits for
[`liken approve-reboot`](/docs/reference/cli/#liken-approve-reboot).
The `RebootRequestHonored` condition reports where the request is.

## A pod with a device claim stays Pending

[Give a workload a device](/docs/guides/devices/#when-a-claim-does-not-schedule)
gives the checks, in order.

## Reading logs

[`liken stern`](/docs/reference/cli/#liken-stern) tails the logs of
many pods at once, with the deployment's credential:

    ./liken stern mycluster <pod-name-pattern>

When the logs do not explain a problem, the Cluster's
[`spec.runtime`](/docs/reference/cluster/#specruntime) section raises
the log level of k3s or containerd, one field each, and says what
each level costs.
