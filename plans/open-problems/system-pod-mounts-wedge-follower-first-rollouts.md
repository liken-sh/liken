# A system pod's new mount wedges a follower-first rollout

Open problem. A release that adds a mount to a system pod's template
cannot roll out through a follower, because the follower runs the new
binary inside the old pod spec, and the new template only arrives when
a leader boots the new release.

## What happened

A multi-machine cluster moved from 2026.08.12-001 to 2026.08.12-002.
The conductor granted a follower the first turn. The machine came back
on -002 and went Degraded: `HostEntriesApplied` was `False` with
`writing /host/etc/hosts: open /host/etc/hosts.tmp-…: no such file or
directory`. The machine declared no `hostEntries` at all. The
conductor granted no further turns, and the rollout held with one
machine on -002 and the rest on -001.

A single-machine cluster proved the same release cleanly, because its
one machine is also the leader that writes the new template. A
multi-machine cluster with follower-first ordering is the only place
this wedge can appear.

## The mechanism

Three facts combine into the wedge:

- System pods run the static image tag `:installed`. A reboot restarts
  the container into the new binary, but the pod spec around it keeps
  the old template. The -002 machine operator writes `/etc/hosts`
  through a `/host/etc` mount that the -001 template does not declare.
- The machine-operator DaemonSet is a k3s AddOn. init renders
  `/var/lib/rancher/k3s/server/manifests/liken/machine-operator.yaml`
  at boot, on leaders only. A follower's boot cannot refresh the
  template, and the cluster operator only evicts stale pods.
- The conductor grants no turn while a machine is unwell. The follower
  stays Degraded until the template changes, the template changes only
  when a leader boots the new release, and the leader's turn waits on
  the follower.

Moving the cluster-operator Deployment onto the upgraded follower does
not help. The operator there runs the new release, but the AddOn owns
the template and the operator does not write it.

## The shape of an answer

Two halves, and the first is the guard:

- The machine operator should not fail a machine for a file it has no
  way to write and no need to write. A pod spec older than the binary
  is a normal state during every rollout. When the mount is missing
  and the spec declares no entries, the condition should say
  "waiting for the new template", not `ApplyFailed`, and the machine
  should stay well so the rollout can continue.
- Scheduling could refresh the template sooner. The conductor can
  detect when a release changes a system pod's template, and it could
  grant the first turn to a leader in that case, so the AddOn is current
  before any follower restarts into the new binary. Follower-first
  ordering stays the default, because proving a release on the
  machines outside the quorum is worth keeping. Node affinity cannot
  express this today: nodes have only `liken.sh/machine=true`, with
  no label naming the release a machine runs.

## The workaround

Patch the running DaemonSet by hand with the new release's volume and
mount, copied from a cluster that already runs it. The AddOn reapplies
the template only when its file's checksum changes, so the patch holds
until the first leader boots the new release and writes the same
shape.

The DaemonSet updates on the OnDelete strategy, so the patch alone
moves no pods. Delete the wedged machine's pod and the DaemonSet
recreates it from the patched template. The other machines need no
hand deletion: the cluster operator evicts each machine's stale pods
after its reboot. The wedged machine went Ready within seconds of the
recreated pod starting, and the rollout resumed.
