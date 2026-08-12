---
title: liken
---

# `liken`

![The liken mark: a patch of lichen drawn as hexagonal tiles](/brand/liken.svg "A patch of crustose lichen. Each tile is one areole.")

`liken` is a small operating system. It boots a machine directly into
Kubernetes.

A machine runs the Linux kernel, k3s, and one small `init` process.
There is no systemd, no shell, and no package manager. The whole
system ships as one image of about 400 MB, and a machine with 1 GB of
memory can run it.

You manage the machines with `kubectl`, the same way you manage the
workloads. A five-machine cluster, 14 hours after a rolling
upgrade:

    $ kubectl get clusters
    NAME      STATUS   MACHINES   VERSION          NEWEST           AVAILABLE        AGE
    homelab   Ready    5/5        2026.08.12-001   2026.08.12-001   2026.08.12-001   18d

    $ kubectl get machines
    NAME     ROLE       STATUS   UPTIME   ADDRESS        LIKEN            KERNEL                 AGE
    node-1   leader     Ready    14h      10.10.0.1/24   2026.08.12-001   7.1.5-070105-generic   18d
    node-2   leader     Ready    14h      10.10.0.2/24   2026.08.12-001   7.1.5-070105-generic   18d
    node-3   leader     Ready    13h      10.10.0.3/24   2026.08.12-001   7.1.5-070105-generic   18d
    node-4   follower   Ready    13h      10.10.0.4/24   2026.08.12-001   7.1.5-070105-generic   18d
    node-5   follower   Ready    13h      10.10.0.5/24   2026.08.12-001   7.1.5-070105-generic   18d

Each machine also reports the hardware it found, in its status. Ask
with the same tools you already use:

    $ kubectl get machine node-4 -o json \
        | jq '.status.hardware | {cpus, memoryBytes, disks: [.blockDevices[].model]}'
    {
      "cpus": 4,
      "memoryBytes": 16512733184,
      "disks": [
        "Samsung SSD 970 EVO Plus 500GB",
        "WDC WD40EFRX-68N32N0"
      ]
    }

The project is [one repository](https://github.com/liken-sh/liken).
It contains a Go init written for this project, the Linux kernel and
k3s from their upstream releases, and the build that assembles them
into a bootable image. Read the repository to see what your machine
runs.

Releases are published at
[releases.liken.sh](https://releases.liken.sh/). Upgrades come
straight from the channel: you catalog a release and set the
version, and the machines reboot into it one turn at a time.

[The manual](/docs/) tells you how to run your own cluster.
[How liken works](/docs/concepts/how-liken-works/) explains the
model, and [Install a cluster](/docs/guides/install/) gives the
steps from a downloaded release to `kubectl get nodes`.
