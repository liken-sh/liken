# A mode list read too early goes stale

The slice can hold a shorter mode list than the kernel does, until
something makes the operator read again. The adoption of
2026.08.19-004 measured it: the operator's first pass ran while the
compositor was still bringing the link up, the connector answered
six modes with the three largest missing, and the slice published
that. Minutes later the kernel's own file held all sixteen names,
and the slice still said six. A restart of the pod republished the
full list.

The gap is in what triggers a read. The operator re-reads the
connectors on a hotplug uevent, and the probe that recovered the
full list came from the compositor's own startup, which raises no
uevent. A mode list that grows without a hotplug is invisible to
the operator.

The shapes worth weighing:

* Read again on a timer. A periodic pass would converge every
  stale list, at the cost of the operator's only polling loop.
  Write-on-divergence keeps the write cheap; the read is the cost.
* Wait out the compositor. The first pass could hold until the
  compositor reports its outputs, so the early degraded answer is
  never read. This fixes the restart case measured here and not a
  list that changes later.
* Treat a shrunken list as suspect. The EDID did not change, so a
  connector whose mode count fell while its EDID held still is
  probably mid-probe. Skipping the write keeps the last good list,
  which is stale in the other direction.
