---
title: Resource limits
weight: 27
toc: true
---

# Resource limits

A resource limit is a ceiling the kernel puts on one process: how many
files it may hold open, how many processes one user may run, how large
a core dump may be. Every limit has two halves. The soft limit is the
one in force. The hard limit is the ceiling on the soft limit. A
process may lower either half, and may raise its soft limit up to its
hard limit, but it can never raise its hard limit.

Two rules make these limits different from
[sysctls](/docs/reference/sysctls/). The kernel fixes a
process's limits when the process starts, and it copies them from the
parent. So a limit reaches a program only if whoever started that
program held the limit first. Nothing can change a program that is
already running.

Most Linux distributions set these limits for each service, in the
`Limit` directives of its systemd unit, and systemd applies them when
it starts the service. `liken` runs no systemd. It applies its own set
instead, listed below.

## How the values are applied

`init` applies two sets of limits to itself, in this order.

1. `liken`'s own values, listed on this page.
2. The values in the machine's
   [`spec.rlimits`](/docs/reference/machine/#spec--rlimits).

Both run at boot, before k3s starts. `init` is the first process on the
machine, so every process started after this point inherits the
result: k3s, containerd, the containerd shims, and every container.
Inheritance is the whole mechanism. There is no file to write and no
per-container setting.

Because `spec.rlimits` is applied second, a name there overrides
`liken`'s value for that resource:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  rlimits:
    nofile: "524288"
    memlock: "67108864"
```

An edit to `spec.rlimits` takes effect at the next boot, not on the
next reconcile pass. This is the one way resource limits differ from
sysctls in practice. The kernel fixes a process's limits when it
forks, so nothing can raise the ceiling of a k3s that is already
running. The operator stages the edit and reports it, and
[`rebootPolicy`](/docs/reference/machine/#spec--rebootpolicy) says
who starts the boot that applies it. A container
that is already running keeps its old limits until its pod restarts,
which the reboot does anyway.

## Writing a value

A value uses the same form a systemd unit file uses.

| Form | Meaning |
| --- | --- |
| `"1048576"` | Sets both halves to 1048576. |
| `"infinity"` | Sets both halves to no limit. |
| `"1024:1048576"` | Sets the soft limit to 1024 and the hard limit to 1048576. |

A soft limit above the hard limit is refused, because no process may
exceed its hard limit.

These resource names are accepted: `nofile`, `nproc`, `core`,
`memlock`, `stack`, `fsize`, and `nice`. Any other name is refused,
so a misspelling reports itself instead of applying nothing.

## The values `liken` sets

This list holds only the resources where `liken` differs from the
kernel. Every other resource keeps the kernel's own default. Both
values match k3s's own systemd unit.

| Resource | Value | Why |
| --- | --- | --- |
| `nofile` | `1048576` | The kernel allows 1024 open files, with a ceiling of 4096. A container runtime and its workloads exhaust this quickly, and a program that runs out reports `Too many open files` on a machine with no shortage of memory or disk. 1048576 is also the kernel's `fs.nr_open` default, which is the largest value any process may ask for. |
| `nproc` | `infinity` | The kernel derives this ceiling from the machine's memory, so a small machine gets a small one. Root is exempt from the check, which hides the problem until a workload runs as an ordinary user. Kubernetes counts processes per pod, in cgroups, which is where a limit on a workload belongs. |

## The value `liken` does not set

k3s's unit also sets `LimitCORE=infinity`, and `liken` does not.

The kernel already grants an unlimited hard limit for core dumps, so
the only difference is the soft limit, which the kernel leaves at 0.
On a systemd machine, raising it is safe because `kernel.core_pattern`
sends each dump to `systemd-coredump`, which bounds its size and
expires it. `liken` has no such collector, so `core_pattern` keeps the
kernel's own value and a crashing process writes a file the size of
its memory into its working directory. For a container that directory
is inside its own writable layer, which is on the same filesystem
as the cluster's datastore.

Nothing is blocked by this. A program that needs a core dump can raise
its own soft limit, because the hard limit is already unlimited. A
deployment that needs core dumps from every process can set `core` in
`spec.rlimits`.

## Reading the result

`status.rlimits` reports what the kernel holds, read back after both
sets are applied:

```console
$ kubectl get machine node-1 -o jsonpath='{.status.rlimits}' | jq
{
  "memlock": "67108864",
  "nofile": "524288",
  "nproc": "infinity"
}
```

A resource `liken` could not set is missing from that map, because a
failed write is never read back. So the map lists the limits that
currently hold, not the limits somebody asked for.

`status.boot.rlimits` reports what the manifest of that boot declared.
It holds the request rather than the result, so it is the record the
operator compares a later edit against.

On the machine itself, the kernel reports the same limits in its own
layout. Read them for the first process, and for any process below it:

```console
$ cat /proc/1/limits
Limit                     Soft Limit   Hard Limit   Units
...
Max open files            1048576      1048576      files
```

Read the same file for a process inside a container to confirm that
the limits reached the workload. `/proc/sys` is the wrong place to
look: resource limits belong to a process, so no file under `/proc/sys`
reports or changes them.
