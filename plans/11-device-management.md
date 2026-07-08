# Device management

Milestone 11 — Not started

Explore device management: how does a shell-less, udev-less OS
expose `/dev` beyond the basics: USB devices arriving after
boot, GPUs, serial adapters? devtmpfs gives us the nodes, but
hotplug means fielding kernel uevents and loading modules,
which is the job udev does elsewhere. Then the Kubernetes half:
how workloads get to the hardware (device plugins, dynamic
resource allocation) and whether devices belong in
`status.hardware` alongside CPUs and memory.
