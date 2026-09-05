# The volume condition moved in CSI 1.13

The design reports a volume's health through the `VolumeCondition` in
every `NodeGetVolumeStats` answer, which the kubelet exposes as
`kubelet_volume_stats_health_status_abnormal`. CSI spec 1.13.0 removed
that field and the `VOLUME_CONDITION` capability, and replaced them
with an alpha `NodeGetVolumeHealth` RPC that the kubelet does not call
yet.

The driver pins spec 1.12.0, the newest release that carries the
condition, and the kubelet's own client still reads it. What is not
known is when the kubelet moves to the new RPC, and whether it keeps
reading the old field for a driver that still sends it.

Until that is known, the condition stays where the design puts it, and
the events and the metrics carry the same facts, so nothing is reported
in one place only. When the kubelet adopts `NodeGetVolumeHealth`, the
driver serves both for one release and then drops the field.

The lab drill of plan 03 added a fact: the kubelet on a `liken` node
posted no `kubelet_volume_stats_health_status_abnormal` series while
four volumes were mounted. The metric is behind the `CSIVolumeHealth`
feature gate, which k3s does not enable by default, so on `liken` today
the condition reaches nothing. The events and the metrics carry the
same facts, and a `liken` release that enables the gate is the fix on
that side.
