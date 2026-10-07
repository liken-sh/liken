---
title: Extension operators
weight: 20
aliases:
  - /docs/concepts/hardware-operators/
---

# Extension operators

`liken` itself only boots machines into Kubernetes. To do more with
the cluster, such as put a dashboard on a monitor, play a film on the
TV, or run a telescope, you install extension operators. Each one is a
Kubernetes operator for one area: you describe what you want as
Kubernetes resources, and the operator makes it happen. Each operator
that is built has its own manual on this site, and its source is a directory of the
[`liken` repository](https://github.com/liken-sh/liken).

* [Claiming hardware](/docs/concepts/claiming-hardware/):
  `display-operator`, `audio-operator`, and `bluetooth-operator` let a
  pod claim one monitor, one speaker, or one game controller.
* [Mounting storage](/docs/concepts/mounting-storage/):
  `git-csi-driver` mounts a git repository as a volume, and
  `per-node-csi-driver` gives a pod a directory on the node it runs
  on.
* [Running a home theater](/docs/concepts/running-a-home-theater/):
  `media-operator`, `library-operator`, `people-operator`, and
  `equipment-operator` turn a machine connected to a TV into a media
  player, with a catalog of your films and series, the people who
  watch them, and control of the receiver and the TV.
* [Running an observatory](/docs/concepts/running-an-observatory/):
  `observatory-operator` runs a telescope's mount, cameras, and other
  equipment through INDI. `astrophotography-operator` will run imaging
  sessions on that telescope. It is planned, and not built yet.

## Install only what you need

The operating system installs none of these operators. It boots each
machine into Kubernetes and publishes the machine's own hardware as
devices, as [Devices](/docs/reference/devices/) describes, and it
stops there. You install the operators that your equipment and your
workloads need, and a cluster with none of them works the same way.
To take `liken` into a new area, you usually add an operator, and the
operating system stays as it is.

## Use something else in place of an operator

An operator works with the rest of the cluster only through
Kubernetes objects: the devices it claims, the resources it defines,
and the `Event`s and conditions it posts. Other software can use the
same objects, so you can run something else beside an operator, or in
its place. For example:

* [`library-operator`](https://liken.sh/library/) writes the `.nfo`
  files and the artwork beside your media in the formats that Kodi and
  Jellyfin read. Jellyfin can read the same library with no operator
  at all. When you run both and name the Jellyfin server in the
  `Catalog`'s `spec.jellyfin`, `library-operator` keeps each person's
  playback progress the same in both places.
* [`observatory-operator`](https://liken.sh/observatory/) serves a
  telescope's devices on its INDI server. KStars drives the telescope
  through that server, and guides through PHD2's event server.

An operator that uses devices claims them with a `ResourceClaim`, the
same way any workload does. So the dependencies go one way: the
operators that publish devices do not depend on the operators that
claim them.

## Device classes and ingress are yours to decide

An operator ships a `DeviceClass` only for its own pod, so it can
claim the hardware it manages. `liken` ships no `DeviceClass` for
your workloads. You write those yourself, and each operator's manual
shows how.

`liken` gives a cluster no ingress controller and no load balancer.
The operating system turns off the add-ons that k3s bundles, such as
the Traefik ingress controller and the load balancer for `Service`
objects of type `LoadBalancer`. You can turn each one on again in the
`features` field of the [`Cluster`](/docs/reference/cluster/) spec.
You decide which services are reachable from outside the cluster, and
how: every operator's `Service` has the default type, `ClusterIP`, and
no operator ships an `Ingress`. Two operators, `bluetooth-operator`
and `equipment-operator`, run on the host network, so their metrics
ports are open on each node's own address.
