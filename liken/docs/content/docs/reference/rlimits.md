---
title: Resource limits
weight: 27
toc: true
---

# Resource limits

A resource limit is a limit that the kernel applies to one process:
how many files it may have open, how many processes one user may run,
how large a core dump may be. Every limit has a soft limit and a hard
limit. The kernel enforces the soft limit, and the hard limit is the
highest value the soft limit can take. A process may lower either
one, and may raise its soft limit up to its hard limit, but it cannot
raise its hard limit.

Resource limits work differently from
[sysctls](/docs/reference/sysctls/). A process gets its limits from
its parent when it starts, so a limit reaches a program only if the
process that started it already had the limit. You cannot change the
limits of a program that is already running.

Most Linux distributions set these limits for each service with the
`Limit` directives of its systemd unit, and systemd applies them when
it starts the service. `liken` has no systemd, so it sets the limits
listed below itself.

## How the values are applied

At boot, before k3s starts, `init` applies two sets of limits to
itself, in this order:

1. `liken`'s own values, listed on this page.
2. The values in the machine's
   [`spec.rlimits`](/docs/reference/machine/#spec--rlimits).

`init` is the first process on the machine, so k3s, containerd, the
containerd shims, and every container inherit the result. No file and
no per-container setting is needed.

Because `spec.rlimits` is applied second, a resource that you set
there overrides `liken`'s value:

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

An edit to `spec.rlimits` takes effect at the next boot, because a
running k3s keeps the limits it started with. The operator stages the
edit and reports it, and
[`rebootPolicy`](/docs/reference/machine/#spec--rebootpolicy) sets
who starts the reboot that applies it. The reboot also restarts every
container, so each container gets the new limits.

## Writing a value

A value has the same form as in a systemd unit file.

| Form | Meaning |
| --- | --- |
| `"1048576"` | Sets the soft and the hard limit to 1048576. |
| `"infinity"` | Removes the soft and the hard limit. |
| `"1024:1048576"` | Sets the soft limit to 1024 and the hard limit to 1048576. |

A soft limit above the hard limit is refused.

The accepted resource names are `nofile`, `nproc`, `core`, `memlock`,
`stack`, `fsize`, and `nice`. Any other name is refused, so a
misspelled name is an error and is not silently ignored.

## The values `liken` sets

`liken` sets only the resources below, and every other resource keeps
the kernel's default. Both values are the same as in k3s's own systemd
unit.

| Resource | Value | Why |
| --- | --- | --- |
| `nofile` | `1048576` | The kernel's default is 1024 open files, with a hard limit of 4096. A container runtime and its workloads use that up quickly, and a program that runs out reports `Too many open files` even when the machine has memory and disk to spare. 1048576 is also the kernel's default `fs.nr_open`, the largest value a process may ask for. |
| `nproc` | `infinity` | The kernel computes the default from the machine's memory, so a small machine gets a low limit. The limit does not apply to root, so the problem shows only when a workload runs as an ordinary user. To limit the processes of a pod, use the kubelet's per-pod limit, which it enforces through the pod's cgroup. |

## Core dumps

k3s's unit also sets `LimitCORE=infinity`, and `liken` does not.

The kernel's default hard limit for core dumps is already unlimited,
so the unit only raises the soft limit from 0. On a systemd machine
that is safe, because `kernel.core_pattern` sends each dump to
`systemd-coredump`, which limits their size and deletes old ones.
`liken` has no such collector, so `core_pattern` keeps the kernel's
default, and a crashing process writes a file as large as its memory
into its working directory. For a container, that directory is in its
writable layer, on the same filesystem as the cluster's datastore.

A program that needs a core dump can raise its own soft limit,
because the hard limit is already unlimited. To get core dumps from
every process, set `core` in `spec.rlimits`.

## Reading the result

`status.rlimits` shows the limits that `init` has, read back from the
kernel after both sets are applied:

```console
$ kubectl get machine node-1 -o jsonpath='{.status.rlimits}' | jq
{
  "memlock": "67108864",
  "nofile": "524288",
  "nproc": "infinity"
}
```

`init` reads each limit back from the kernel, so when a value fails to
apply, the map shows the value the kernel kept, not the value you
asked for. The boot log has a line for each limit that failed.

`status.boot.rlimits` shows the `spec.rlimits` of the manifest that
the current boot used. It does not include `liken`'s own values. The
operator compares a later edit of `spec.rlimits` with it.

On the machine, the kernel shows the limits of each process in
`/proc/<pid>/limits`. Read it for the first process, or for any other:

```console
$ cat /proc/1/limits
Limit                     Soft Limit   Hard Limit   Units
...
Max open files            1048576      1048576      files
```

Read the same file for a process in a container to confirm that the
limits reached the workload. Resource limits belong to a process, so
no file under `/proc/sys` shows or changes them.
