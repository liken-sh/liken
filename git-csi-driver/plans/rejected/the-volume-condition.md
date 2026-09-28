# The volume condition

Rejected on 2026-09-05. The design first reported a volume's health
through the `VolumeCondition` in every `NodeGetVolumeStats` answer,
which the kubelet exposes as `kubelet_volume_stats_health_status_abnormal`.
Plans 03 to 06 were built and drilled with it.

What decided against it:

- CSI spec 1.13.0 removed the field and the `VOLUME_CONDITION`
  capability, and replaced them with an alpha `NodeGetVolumeHealth`
  RPC. A driver on the current spec cannot send the field.
- Kubernetes 1.36.3 vendors spec 1.9.0 and still reads the field, but
  only behind the `CSIVolumeHealth` feature gate, alpha and off by
  default since 1.21. k3s leaves it off. In every lab drill the kubelet
  posted no health series while volumes were mounted.
- No kubelet calls `NodeGetVolumeHealth` yet.

So the condition was a channel the spec had retired and the kubelet did
not consume. The driver moved to spec 1.13, stopped declaring the
capability, and reports health through the two channels that work: an
`Event` on the pod and the claim for every state change, and the gauge
`git_csi_volume_abnormal`, labeled `namespace` and `volume`, which is
alertable where the condition never was. `NodeGetVolumeHealth` is a
small plan for the day a kubelet calls it.
