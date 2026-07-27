---
title: Kernel settings
weight: 26
toc: true
---

# Kernel settings

A Linux kernel has about 1500 tunable settings, called sysctls. Each one
is a file under `/proc/sys`, and writing the file changes the setting.
The kernel ships a default for every one of them.

Most Linux distributions do not run on those defaults. They ship a set
of files under `/usr/lib/sysctl.d`, and systemd applies them at boot.
liken runs no systemd. It applies its own set instead, listed below.

## How the values are applied

Two programs apply kernel settings, and they apply them in the same
order.

1. liken's own values, listed on this page.
2. The values in the machine's `spec.sysctls`.

Init applies both at boot, before k3s starts. The liken operator
applies both again on every pass, about every ten seconds. Each pass
writes the value and reads it back, so a setting that something else on
the machine changed returns within one pass, without a reboot.

Because `spec.sysctls` is applied second, a name there overrides
liken's value for that parameter:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  sysctls:
    vm.max_map_count: "524288"
```

`status.sysctls` reports both sets together, read back from the kernel:

```console
$ kubectl get machine node-1 -o jsonpath='{.status.sysctls}' | jq
{
  "kernel.pid_max": "4194304",
  "net.core.default_qdisc": "fq_codel",
  "vm.max_map_count": "524288",
  ...
}
```

A parameter liken could not write is missing from that map, because a
failed write is never read back. So the map lists the parameters that
currently hold, not the parameters somebody asked for.

If a value in `spec.sysctls` fails to apply, the machine reports
`SysctlsApplied` as false and becomes Degraded. If one of liken's own
values fails, the machine stays Ready and the condition reports
`DefaultsIncomplete`. The reason for the difference is that liken's
values ship with the release, so every machine running that release
would report the same fault in the same pass, and degrading a whole
fleet at once hides the one machine that has a real problem.

## The values liken sets

This list holds only the parameters where liken differs from the
kernel. Every other parameter keeps the kernel's own default.

### Memory

| Parameter | Value | Why |
| --- | --- | --- |
| `vm.watermark_scale_factor` | `100` | Reclaims memory steadily in the background. At the kernel's default, reclaim starts only when allocation is close to failing, and then many allocations stall at once. |
| `vm.max_map_count` | `262144` | The kernel allows 65530 memory mappings per process, which a container runtime exhausts. The failure is confusing: a request for memory fails on a machine that has memory free. |

### Watches

| Parameter | Value | Why |
| --- | --- | --- |
| `fs.inotify.max_user_instances` | `8192` | Every program that watches files on the machine runs as root and draws on one quota: kubelet, containerd, k3s, init, and the liken operator. The kernel allows 128. |
| `fs.inotify.max_user_watches` | `524288` | The same quota, counted in watched files rather than watchers. |

Both are ceilings. An unused ceiling costs no memory.

### Processes

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.pid_max` | `4194304` | The kernel allows 32768 process IDs, a limit that predates the container. A machine running many short-lived containers reaches it, and then no program can start. This is the largest value a 64-bit kernel accepts. |

### Networking

| Parameter | Value | Why |
| --- | --- | --- |
| `net.ipv4.ip_forward` | `1` | A node that cannot forward packets cannot route pod traffic. |
| `net.core.default_qdisc` | `fq_codel` | The queueing discipline a network interface gets when it comes up. The kernel's default puts every packet in one queue, so a large transfer delays every small request behind it. `fq_codel` gives each flow a queue of its own. |
| `net.ipv4.tcp_slow_start_after_idle` | `0` | Stops a connection restarting slowly after every quiet moment. An API server watch stays open for hours and carries a burst, a silence, and another burst, which is the worst case for the kernel's default. |
| `net.ipv4.tcp_mtu_probing` | `1` | Recovers from a network path that drops oversized packets without saying so. Pod-to-pod traffic inside VXLAN encapsulation travels such a path. |
| `net.unix.max_dgram_qlen` | `512` | The queue depth of a local datagram socket. The kernel allows 10, and a program that fills the queue blocks. |

### Reliability

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.panic_on_oops` | `1` | A kernel bug that does not kill the machine outright leaves it running in a state it does not understand. liken has somewhere better to go: the machine reboots into the system slot it already proved, and the crash journal carries the log across the reboot, so the next boot reports the crash. |

### Hardening

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.kptr_restrict` | `1` | Hides real kernel addresses, which are what an attack needs to work out where the kernel loaded. |
| `fs.protected_symlinks` | `1` | Refuses to follow another user's symbolic link in a directory that anyone may write to. |
| `fs.protected_hardlinks` | `1` | Refuses a hard link to a file the person making the link cannot read. |
| `fs.protected_regular` | `2` | Refuses to create a file where another user already left one, in a directory that anyone may write to. |
| `fs.protected_fifos` | `1` | The same protection for named pipes. |

### Interface settings

A network setting under `net.ipv4.conf` exists three times: once under
`all`, once under `default`, and once for each interface. The kernel
copies `default` into an interface when that interface appears, and the
machine's own network card appears before these values are applied. So
`default` reaches the interfaces that Kubernetes creates later, and
`all` is what reaches the network card. liken sets both.

| Parameter | Value | Why |
| --- | --- | --- |
| `net.ipv4.conf.all.rp_filter`, `net.ipv4.conf.default.rp_filter` | `2` | Drops a packet whose source address no interface would route a reply to. This is the loose form of the check. Do not set the strict form, `1`: pod traffic routinely arrives on a different interface from the one that would reply, and the strict form drops it. |
| `net.ipv4.conf.all.accept_source_route`, `net.ipv4.conf.default.accept_source_route` | `0` | Refuses a packet that carries its own return path. Nothing on a server uses this, and it is a way to reach an address that ordinary routing protects. |
| `net.ipv4.conf.all.promote_secondaries`, `net.ipv4.conf.default.promote_secondaries` | `1` | Keeps an interface's other addresses when its primary address is removed. The kernel's default deletes them all. |

## Settings that are not sysctls

Not every kernel setting is a sysctl. Three other kinds matter.

**The kernel command line** is read once, at boot. It carries the
settings the kernel needs before any program runs. liken builds this
line itself, and there is no field to add to it. `status.boot.commandLine`
reports the line the machine booted with.

**Kernel modules** are drivers and other kernel parts that load on
demand. Some sysctls need one: `net.core.default_qdisc` cannot name
`fq_codel` unless the `sch_fq_codel` module is loaded. liken loads a
fixed list at boot, and `spec.modules` adds to it. See the Machine
reference.

**Compiled settings** are fixed when the kernel is built, and liken
does not build kernels. The build's own configuration ships in the
image at `/lib/modules/<release>/config`. Read it to find out whether
some kernel feature is built in, available as a module, or absent:

```console
$ grep CONFIG_NET_SCH_FQ_CODEL /lib/modules/$(uname -r)/config
CONFIG_NET_SCH_FQ_CODEL=m
```
