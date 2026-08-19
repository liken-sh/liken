# display-operator

A Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver that publishes each monitor output of a graphics card as a
claimable device on a [`liken`](https://github.com/liken-sh/liken)
cluster. It runs the Weston compositor in its pod. A pod that claims
an output receives the Wayland socket and the app-id that puts its
window, fullscreen, on that screen.

That makes a screen something you give a workload from a manifest.
A kiosk browser runs on the lobby screen from a `Deployment`, a
dashboard runs on a wall monitor, and a movie runs on the TV. The
claim names the screen, by connector or by which monitor is plugged
in. The scheduler finds the machine, and the container receives the
socket. This needs no SSH, no configuration on the host, and no
privileged pod.

The operator is one of `liken`'s
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/):
optional workloads, installed like any other manifest, that a cluster
runs fine without. What it needs from `liken` is the card. `liken`'s
own DRA driver publishes the raw hardware. This operator claims the
card through an ordinary `liken.sh` claim, and it publishes the
outputs at the grain a workload asks for: one device per connector
under `display.liken.sh`. It uses no private interface into `liken`:
the claim, the `ResourceSlices`, and the CDI files are the public
contracts any DRA driver gets.

## The manual

**[display.liken.sh](https://display.liken.sh)** is the manual, and
it serves the deployment manifests as raw YAML, so an install starts
and ends there:

* [Install the operator](docs/content/docs/guides/install.md)
* [Put a window on a screen](docs/content/docs/guides/claim.md)
* [Devices](docs/content/docs/reference/devices.md): the class, the
  attributes, and what a claim delivers

The short version, on a cluster whose machine publishes its graphics
card, after you create the device classes (the install guide gives
their YAML):

    kubectl apply -n liken-system \
      -f https://display.liken.sh/deploy/deviceclasses.yaml \
      -f https://display.liken.sh/deploy/rbac.yaml \
      -f https://display.liken.sh/deploy/operator.yaml

[`deploy/`](deploy/) is the source of those files: a `kustomize` base
with the three generic `DeviceClasses`, the RBAC, and the `DaemonSet`
whose pod claims the card on its own node. The base ships
`display-gpu` and `display-render`, which the claim template names
and the operator cannot start without, and `display-output`, which
your workloads claim. A class that picks one monitor is cluster
policy, yours to create; the install guide gives an example.

## The design

[`plans/`](plans/README.md) holds the design documents: why the image
is a library closure on scratch, and how a monitor arrives on a dark
connector without a restart. The pattern this operator is an instance
of is documented in `liken`'s repository, in
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

## The build

    go build ./...
    go test ./...
    docker build --target weston -t weston .
    docker build -t display-operator .

One `Dockerfile` builds two images. `ghcr.io/liken-sh/weston` is the
compositor and every library it loads, on nothing else.
`ghcr.io/liken-sh/display-operator` is that image plus the operator's
static binary, and it is the image the `DaemonSet` runs. The EDID
fixtures in `testdata` are read off real monitors with
`od -An -tx1 /sys/class/drm/<card>-<connector>/edid`.

## License

MIT. See [LICENSE](LICENSE).
