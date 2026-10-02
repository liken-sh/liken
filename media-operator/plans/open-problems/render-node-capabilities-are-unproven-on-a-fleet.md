# Render node capabilities are unproven on a fleet

[Plan 42](../completed/42-render-node-capabilities.md) built the
capabilities agent, and its query ran on one workstation GPU outside
Kubernetes. No cluster has run the `media-capabilities` `DaemonSet`
yet, so four facts are unchecked:

- The scheduler allocates the agent's claim, a request with
  `allocationMode: All` on render nodes that other claims already
  hold, because `liken` publishes them with
  `allowMultipleAllocations`.
- The kubelet accepts a prepare of a `media.liken.sh` claim that
  answers with no device, and starts the pod.
- A claim through `media-decode-10bit`, paired by
  `resource.kubernetes.io/pciBusID`, schedules only on a GPU whose
  driver states `scale10bit`, and receives that GPU's render node.
- The Coffee Lake GPU that failed `scale_vaapi` on 10-bit frames
  states `scale10bit: false`. The agent publishes what the driver
  states, so if the iHD driver lists `P010` on that GPU's video
  processor, the device states `true`, and the claim does not keep the
  worker off it. That outcome is the driver's defect, and the agent
  does not correct it, but the proof records which one the driver
  gives.

The proof runs on the lab: deploy the base, read each node's
`<node>-media.liken.sh` slice, and schedule one pod through
`media-decode-10bit`.
