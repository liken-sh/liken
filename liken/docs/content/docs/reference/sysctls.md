---
title: Kernel settings
weight: 26
toc: true
---

# Kernel settings

The Linux kernel has many tunable settings, called sysctls. Each one is
a file under `/proc/sys`, and writing the file changes the setting.
Most distributions change some of the kernel's defaults with files
under `/usr/lib/sysctl.d`, which systemd applies at boot. `liken` runs
no systemd, so it applies its own values, listed below.

## How the values are applied

A machine's kernel settings come from the values on this page and from
the Machine's
[`spec.sysctls`](/docs/reference/machine/#spec--sysctls). When
`spec.sysctls` sets a parameter, its value overrides `liken`'s value:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  sysctls:
    vm.max_map_count: "524288"
```

`init` writes `liken`'s values and then the values of `spec.sysctls`
at boot, before k3s starts. After that, `machine-operator` writes them
again on each reconcile pass, but only a parameter whose current value
differs, and for a parameter that `spec.sysctls` sets, it writes only
the spec's value. The kernel sends no event when a file under
`/proc/sys` changes, so every ten seconds `machine-operator` also reads
each parameter and starts a pass when one has changed. When another
program changes a setting, the operator writes it back within about
ten seconds, and an edit to `spec.sysctls` takes effect without a
reboot.

`status.sysctls` lists the values that the kernel reports for both sets:

```console
$ kubectl get machine node-1 -o jsonpath='{.status.sysctls}' | jq
{
  "kernel.pid_max": "4194304",
  "net.core.default_qdisc": "fq_codel",
  "vm.max_map_count": "524288",
  ...
}
```

A parameter whose write failed is missing from the map.

When a value in `spec.sysctls` fails to apply, the `SysctlsApplied`
condition is `False` with the reason `ApplyFailed`, and the machine is
Degraded. When one of `liken`'s own values fails, the condition stays
`True` with the reason `DefaultsIncomplete`, its message names the
parameter, and the machine stays Ready. Every machine on a release
applies the same values, so a bad value fails on the whole fleet at
once, and a Degraded fleet would hide the machine that has a fault of
its own.

## The values `liken` sets

This list has only the parameters where `liken`'s value differs from
the kernel's default. Every other parameter keeps the kernel's default.

### Memory

| Parameter | Value | Why |
| --- | --- | --- |
| `vm.watermark_scale_factor` | `100` | Starts background memory reclaim earlier. With the kernel's default, reclaim on a small machine starts only when free memory is almost gone, and then many allocations stall at once. |
| `vm.max_map_count` | `262144` | The kernel's default allows a process 65530 memory mappings. A container runtime or a program that maps many files can use them all, and then a request for memory fails on a machine that has free memory. |

### Watches

| Parameter | Value | Why |
| --- | --- | --- |
| `fs.inotify.max_user_instances` | `8192` | The inotify limits apply to each user. kubelet, containerd, k3s, `init`, and the `liken` operators all run as root, so they share one quota. The kernel's default of 128 instances is too few for them. |
| `fs.inotify.max_user_watches` | `524288` | The quota of watched files for the same user. |

A watch uses kernel memory only while it exists, so a high limit costs
nothing until programs use it.

### Processes

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.pid_max` | `4194304` | The kernel's default allows 32768 process IDs. A machine that runs many short-lived containers can use them all, and then no program can start. This is the largest value that a 64-bit kernel accepts. |

### Networking

| Parameter | Value | Why |
| --- | --- | --- |
| `net.ipv4.ip_forward` | `1` | Kubernetes networking needs the node to forward packets between pods. |
| `net.core.default_qdisc` | `fq_codel` | The queueing discipline that an interface uses when it comes up. The kernel's default puts all packets in one queue, so a large transfer delays every small request behind it. `fq_codel` gives each flow its own queue. |
| `net.ipv4.tcp_slow_start_after_idle` | `0` | By default, a TCP connection that was idle starts sending slowly again. An API server watch stays open for hours and sends bursts of data between silences, so with the default every burst starts slowly. |
| `net.ipv4.tcp_mtu_probing` | `1` | Lets TCP try a smaller packet size when a connection stalls because a network path drops large packets and sends no ICMP message. Traffic between pods on different nodes has a VXLAN header, so its largest packet is smaller than the interface's MTU. |
| `net.unix.max_dgram_qlen` | `512` | The queue length of a Unix domain datagram socket. The kernel's default is 10, and a program that sends to a full queue blocks. |

### Reliability

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.panic_on_oops` | `1` | After a kernel bug, the kernel can keep running in an unknown state and corrupt what it writes. With this value it panics instead. The `panic=10` kernel argument reboots the machine ten seconds later, and pstore keeps the kernel log across the reboot, so the next boot reports the crash. When the panic happens on the first boot of a new release, the machine boots its previous release. |
| `kernel.softlockup_panic` | `1` | Panics when a CPU stays in kernel mode for more than 20 seconds. With the kernel's default, the kernel prints a warning, and the machine stays hung until a person turns off its power. |
| `kernel.hardlockup_panic` | `1` | Panics when a CPU runs with interrupts disabled for too long, for the same reason. |

### Hardening

| Parameter | Value | Why |
| --- | --- | --- |
| `kernel.kptr_restrict` | `1` | Shows zeros in place of kernel addresses to a reader without `CAP_SYSLOG`. An exploit can use the addresses to find where the kernel loaded. |
| `fs.protected_symlinks` | `1` | Refuses to follow a symbolic link that another user owns in a world-writable sticky directory such as `/tmp`. |
| `fs.protected_hardlinks` | `1` | Refuses to create a hard link to a file that the user does not own and cannot both read and write. |
| `fs.protected_regular` | `2` | Refuses to open, for creation, a file that another user owns in a world-writable or group-writable sticky directory. |
| `fs.protected_fifos` | `1` | Refuses the same for a named pipe (FIFO) in a world-writable sticky directory. |

Everything on the host runs as root, so these protections matter for a
pod that runs as another user and shares a directory with the host.

### Interface settings

Each setting under `net.ipv4.conf` exists under `all`, under `default`,
and under each interface. The kernel copies the `default` value into an
interface when the interface appears. The machine's network card
appears before `liken` applies its values, so `default` affects only
the interfaces that Kubernetes creates later. `liken` sets `all` too,
so the values also apply to the network card.

| Parameter | Value | Why |
| --- | --- | --- |
| `net.ipv4.conf.all.rp_filter`, `net.ipv4.conf.default.rp_filter` | `2` | Drops a packet when no interface has a route back to its source address. This is the loose mode of the check. Do not set the strict mode, `1`, because pod traffic often arrives on a different interface from the one that the route back uses, and the strict mode drops it. |
| `net.ipv4.conf.all.accept_source_route`, `net.ipv4.conf.default.accept_source_route` | `0` | Refuses a packet that sets its own route. Servers do not use source routing, and it can reach addresses that normal routing protects. |
| `net.ipv4.conf.all.promote_secondaries`, `net.ipv4.conf.default.promote_secondaries` | `1` | When an interface's primary address is removed, makes a secondary address the primary. With the kernel's default, the kernel deletes the secondary addresses too. |

### Settings of a module

Some parameters exist only after a kernel module loads. `liken` sets
each of these only when its file exists, and a machine without the
file reports no fault. When the module loads after boot, the
ten-second check of `machine-operator` finds the new file and applies
the value.

The kernel accepts one write of these parameters and refuses a
different value after that with `EBUSY`. So when `spec.sysctls` sets
one of them, `init` and `machine-operator` write only the spec's
value, and a change to that value takes effect at the next boot.

| Parameter | Value | Module | Why |
| --- | --- | --- | --- |
| `net.vsock.child_ns_mode` | `local` | `vsock`, which `vhost_vsock` loads | Gives each pod's network namespace its own vsock addresses, so two pods can each start a guest at the same address, and no pod can connect to another pod's guests. `machine-operator` offers `/dev/vhost-vsock` to a claim only while this parameter reads `local`. See [Virtualization devices](/docs/reference/devices/#virtualization-devices). |

## Settings that are not sysctls

**Resource limits** are limits that the kernel applies to each
process, such as how many files it can have open. No sysctl changes
them. `liken` sets its own values, and `spec.rlimits` overrides them.
[Resource limits](/docs/reference/rlimits/) lists both.

**Disk settings** are attributes of each disk's request queue under
`/sys/block`, not under `/proc/sys`. `liken` sets the largest request
size on the disk that holds `clusterState`, and `spec.disks` changes
it on any disk. [Disk settings](/docs/reference/disks/) has the
default and the reason for it.

**The kernel command line** has the settings that the kernel reads at
boot, before any program runs. `liken` writes the command line, and no
field adds to it.
[`status.boot.commandLine`](/docs/reference/machine/#statusboot--commandline)
shows the command line that the machine booted with.

**Kernel modules** are drivers and other kernel code that load
separately from the kernel. Some sysctls need a module:
`net.core.default_qdisc` accepts `fq_codel` only when the
`sch_fq_codel` module is loaded. `liken` loads a fixed list of modules
at boot, and
[`spec.modules`](/docs/reference/machine/#spec--modules) adds to it.

**Compiled settings** are fixed when the kernel is built. `liken`
boots Ubuntu's mainline build of the upstream kernel, with Ubuntu's
generic configuration, so you cannot change these settings. The build
configuration is in the image at `/lib/modules/<release>/config`. Read
it to find out whether a feature is built in, available as a module,
or absent:

```console
$ grep CONFIG_NET_SCH_FQ_CODEL /lib/modules/$(uname -r)/config
CONFIG_NET_SCH_FQ_CODEL=m
```
