# display-operator

A Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver that publishes each monitor output of a graphics card as a
device that a pod can claim, on a
[`liken`](https://github.com/liken-sh/liken) cluster. The operator's
pod runs the Weston compositor in its own container, and the kubelet
restarts that container when it stops. A pod that claims an output
gets its own Wayland socket, and the window that the pod draws appears
on that screen.

With this operator you give a workload a screen from a manifest. A
kiosk browser can run on a lobby screen from a `Deployment`, a
dashboard can run on a wall monitor, and a video can play on a TV. The
claim selects the screen by its connector or by the monitor that is
plugged in. The scheduler picks the machine, and the kubelet mounts
the socket in the container. You do not need SSH, configuration on
the host, or a privileged pod.

The operator is one of `liken`'s
[extension operators for claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/):
optional workloads that you install like any other manifest, and that
a cluster does not need in order to run. `liken`'s own DRA driver
publishes each graphics card as a device. This operator claims the
card with an ordinary `liken.sh` claim and publishes one device for
each connector under `display.liken.sh`, so a workload can claim one
monitor output. It uses no private interface of `liken`, only the
claims, `ResourceSlices`, and CDI files that any DRA driver uses.

## The manual

**[liken.sh/display](https://liken.sh/display/)** is the manual. It
also serves the deployment manifests as raw YAML, so you can install
the operator from it:

* [Install the operator](docs/content/docs/guides/install.md)
* [Put a window on a screen](docs/content/docs/guides/claim.md)
* [Devices](docs/content/docs/reference/devices.md): the device
  class, the attributes, and what a claim gives a pod
* [API](docs/content/docs/reference/api.md): the HTTP routes that
  return what a screen shows

On a cluster whose machines publish their graphics cards, and after
you create the device class that your workloads claim through, the
install is:

    kubectl apply -n liken-system \
      -f https://liken.sh/display/deploy/displays.yaml \
      -f https://liken.sh/display/deploy/layouts.yaml \
      -f https://liken.sh/display/deploy/deviceclasses.yaml \
      -f https://liken.sh/display/deploy/rbac.yaml \
      -f https://liken.sh/display/deploy/operator.yaml \
      -f https://liken.sh/display/deploy/api.yaml
    kubectl apply -f https://liken.sh/display/deploy/api-authentication.yaml

[`deploy/`](deploy/) is the source of those files. It is a `kustomize`
base with these parts:

* The `Display` and `Layout` `CustomResourceDefinitions`.
* The RBAC.
* The `DaemonSet`, whose pod claims the graphics card on its own node.
* The `DeviceClasses` `display-gpu`, `display-render`, and
  `display-i2c`. The `DaemonSet`'s claim selects the card through
  them, so the operator's pod does not start without them.
* The `display-api` `Deployment` and `Service`.
* The `RoleBinding` in `kube-system` that lets `display-api` read the
  cluster's client certificate authority. It has its own command,
  because `-n liken-system` refuses an object in `kube-system`.

The class that your workloads claim through is a policy of your
cluster, so you create it. The install guide gives the YAML for
`display-output`, the class to start with.

## The design

[`plans/`](plans/README.md) holds the design documents. For example,
they explain why the image is a library closure on `scratch`, and how
a monitor that you plug in to an empty connector appears without a
restart of the operator. The pattern that every device operator
follows is in
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

## The build

    go build ./...
    go test ./...

The images build through the bake file at the top of the repository,
from the top of the repository:

    docker buildx bake display-operator display-capture display-api --load

One `Dockerfile` builds three images, and each contains the operator's
static binary:

* `ghcr.io/liken-sh/display-operator` is the [`weston`](../weston/)
  base image plus the binary. The `DaemonSet` runs it.
* `ghcr.io/liken-sh/display-capture` is the [`ffmpeg`](../ffmpeg/)
  base image plus the same binary. The capture container of the
  operator's pod runs it.
* `ghcr.io/liken-sh/display-api` is the binary on `scratch`. The
  `display-api` `Deployment` runs it.

The `Dockerfile` names each base image without a tag, as in
`FROM weston`, and the bake file builds that base from its own
directory at the same commit.

The EDID fixtures in `testdata` come from real monitors. To read a
monitor's EDID, run `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`.

## License

MIT. See [LICENSE](LICENSE).
