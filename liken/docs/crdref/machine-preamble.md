A `Machine` declares one machine: its network interfaces, its disks,
the kernel modules it loads, its kernel settings, and its node labels
and taints. The machine writes what it finds and does into `status`.
[Install a cluster](/docs/guides/install/) writes your first `Machine`
manifests, and the hardware report in that guide proposes one from
the hardware it finds. [Kernel settings](/docs/reference/sysctls/)
and [Resource limits](/docs/reference/rlimits/) list the values that
the OS applies before `spec.sysctls` and `spec.rlimits` override them.
