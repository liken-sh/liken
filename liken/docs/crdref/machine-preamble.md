A Machine declares one machine: its network interfaces, its disks,
and the kernel modules it loads. The machine writes what it
observes and what it does into `status`. [Install a
cluster](/docs/guides/install/) writes your first Machine
manifests, and the hardware report in that guide proposes one from
the hardware it finds. [Kernel settings](/docs/reference/sysctls/)
and [Resource limits](/docs/reference/rlimits/) list the values the
OS applies before `spec.sysctls` and `spec.rlimits` override them.
