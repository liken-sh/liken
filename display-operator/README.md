# display-operator

A Kubernetes DRA driver that publishes each of a graphics card's
monitor outputs as a device. A pod claims one screen, by connector or
by which monitor is plugged in, and receives the Wayland socket and the
app-id that puts its window on that screen.

This is an instance of liken's device operator pattern. liken publishes
the hardware facts no other layer can observe, and which monitor is
free is not one of them: the compositor drives the monitors, so the
layer that publishes outputs runs the compositor. Weston does not
belong in the read-only root every machine boots, because only some
machines have screens. So the operator is an ordinary workload. It
claims the card through a `liken.sh` claim, runs Weston with the kiosk
shell in the same pod, and publishes what the compositor drives under
`display.liken.sh`.

Without the operator, nothing arbitrates the screens: two clients with
the same app-id cover each other, and an app-id that matches no output
lands on the first monitor. With it, an output is a resource the
scheduler allocates once. The second pod parks, and the app-id only
routes a surface the claim already assigned.

The operator uses no private interface into liken. The raw claim, the
ResourceSlices it writes, and the CDI files it leaves for the runtime
are the public contracts any DRA driver gets. A cluster that never
deploys it behaves as it does now.

## What it publishes

One device for each **connector** on the card, not for each monitor
plugged in. A cabled monitor that is asleep still reports itself, so
its output publishes untainted and a claim on it starts a pod. An empty
connector publishes with the `disconnected` taint, so a claim on it
parks instead of failing.

The device name is the connector in lowercase, because a DRA device
name must be a DNS label. The rest are attributes, and every one but
`connector` and `appId` comes from the monitor:

| Attribute | Type | What it is |
|---|---|---|
| `connector` | string | the kernel's connector name: `HDMI-A-1` |
| `appId` | string | what the compositor routes a surface by: `hdmi-a-1` |
| `manufacturer` | string | the EDID's three-letter PNP id: `GSM` is LG |
| `model` | string | the monitor name the EDID states |
| `serial` | string | the serial the EDID states |
| `widthPixels`, `heightPixels` | int | the mode the compositor drives |
| `widthMillimeters`, `heightMillimeters` | int | the panel's physical size |
| `monitor.liken.sh/id` | string | the pairing identity, see below |

    $ kubectl get resourceslice liken-1-display.liken.sh -o yaml
    spec:
      driver: display.liken.sh
      nodeName: liken-1
      devices:
        - name: hdmi-a-1
          attributes:
            connector: {string: "HDMI-A-1"}
            appId: {string: "hdmi-a-1"}
            manufacturer: {string: "GSM"}
            model: {string: "LG HDR WQHD"}
            serial: {string: "202NTRLCC070"}
            widthPixels: {int: 3840}
            heightPixels: {int: 1600}
            widthMillimeters: {int: 879}
            heightMillimeters: {int: 366}
            monitor.liken.sh/id: {string: "gsm-7716-lg-hdr-wqhd"}
        - name: dp-1
          attributes:
            connector: {string: "DP-1"}
            appId: {string: "dp-1"}
          taints:
            - key: display.liken.sh/disconnected
              effect: NoExecute

A connector name is stable across reboots on one machine, but not
across machines, and it says nothing about which monitor is plugged
into it. That is why the EDID facts publish beside it, from sysfs
(`/sys/class/drm/<card>-<connector>/`) rather than from the compositor.
Reading a file needs no protocol and no version agreement with Weston.

`monitor.liken.sh/id` is the pairing identity. It pairs a screen with
that screen's speakers, which the audio operator publishes from the
same monitor's HDMI ELD. Both drivers build the value the same way,
byte for byte, because the scheduler compares them and one character of
disagreement parks the claim: the lowercase PNP id, the four-hex
product code, then the lowercase monitor name with spaces turned to
dashes (`gsm-5b09-lg-ultrawide`). A monitor with no name keeps the
two-part form, `boe-095f`. Two monitors of one model share one value,
so a constraint is satisfied by either pairing.

## Deploying it

    kubectl apply -k deploy/

Or reference `deploy/` from your own GitOps. The base assumes the
namespace `liken-system` exists.

The operator runs as a DaemonSet, so a pod lands on every node and
nobody states which machine has the monitors. Each pod claims the card
on its node. A node with no graphics card publishes no matching
device, so the claim parks that pod Pending, and it costs nothing. A
node with more than one card serves only the card the claim took.

The base ships three DeviceClasses. `display-gpu` and `display-render`
are the raw devices the operator claims from liken; `display-output` is
what a consumer claims. The card node allocates once, which makes this
pod the only program setting a mode on the card. The render node is
shareable, so holding it takes nothing from the transcoders on the same
GPU.

## Claiming an output

Select a screen by its connector, by its monitor, or take any that
fits. The monitor selector survives somebody moving a cable, and it
guards the attribute first, because a selector that reads a missing
attribute fails the whole allocation:

    # by connector
    device.attributes["display.liken.sh"].connector == "HDMI-A-1"

    # by monitor, cable-independent
    has(device.attributes["display.liken.sh"].model) &&
    device.attributes["display.liken.sh"].model == "LG HDR WQHD"

    # any screen at least 1920 wide
    has(device.attributes["display.liken.sh"].widthPixels) &&
    device.attributes["display.liken.sh"].widthPixels >= 1920

Tolerate `display.liken.sh/disconnected`, the one taint a dark
connector carries. It is `NoExecute`, and its `tolerationSeconds` says
how long the pod may hold a dark screen before it ends. A claim on a
connector that has no monitor yet parks `Unschedulable`, visibly, and
starts on its own when one is plugged in.

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-screen
      namespace: house
    spec:
      devices:
        requests:
          - name: screen
            exactly:
              deviceClassName: display-output
              selectors:
                - cel:
                    expression: |
                      device.attributes["display.liken.sh"].connector == "HDMI-A-1"
              tolerations:
                - key: display.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

The consuming container references the claim and passes the app-id to
its own toolkit: chromium takes `--class=$(DISPLAY_APP_ID)`, mpv takes
`--wayland-app-id=$(DISPLAY_APP_ID)`.

    spec:
      resourceClaims:
        - name: screen
          resourceClaimName: kitchen-screen
      containers:
        - name: browser
          image: ...
          args: ["--class=$(DISPLAY_APP_ID)", "--kiosk", "https://..."]
          resources:
            claims:
              - name: screen

## What a consumer receives

A mount and three environment variables. No device node: a Wayland
client draws through the compositor, which holds the card.

| What | Value |
|---|---|
| mount | `/var/run/display.liken.sh`, the compositor's runtime directory |
| `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| `WAYLAND_DISPLAY` | `wayland-0` |
| `DISPLAY_APP_ID` | the allocated output's app-id, such as `hdmi-a-1` |

**The claim assigns the screen; the app-id only routes.** The
compositor refuses nothing, so a client that invents an app-id, or
presents one that matches no output, still lands on some screen. What
stops two clients from sharing a screen is that the second pod cannot
allocate an output the first holds. One container drives one screen: a
claim delivers one `DISPLAY_APP_ID`, so a pod that drives two screens
runs two containers.

## The privilege it takes

None. The pod declares no `hostNetwork`, adds no capability, drops
`ALL`, and runs unprivileged. libseat's noop backend opens the card
node with a plain `open()`, and the kernel hands DRM master to the
first opener, so everything this pod does to the hardware it does
through its claim.

Two things the spec states anyway. `hostUsers: true`, because the
kernel delivers uevents only to the initial user namespace, and a pod
in its own namespace would see a monitor plug in and get nothing.
`/var/run/display.liken.sh` is a hostPath, because the Wayland socket
has to be somewhere a consumer can mount, and the compositor creates it
with a zero umask so any uid can connect. Beside those, the pod takes
the two hostPath mounts every DRA driver takes, the kubelet plugin
registry and `/var/run/cdi`, and its own plugin socket directory.

## Disconnects and restarts

**A dark monitor is tainted, never deleted.** The device stays in the
slice with its `disconnected` taint, the taint evicts the holder after
its `tolerationSeconds`, and a monitor that returns clears it.
Deleting the device instead would strand the claim, because the kubelet
retries `NodePrepareResources` against a device in no slice with no
bound, and upstream declined to bound it (KEP-5322).

**The slice outlives the pod.** The operator retracts nothing when it
stops, and it publishes nothing when its sysfs walk comes back empty.
The Node owns the slice. To remove the operator for good, delete the
workload and then the slice:

    kubectl delete resourceslice <node>-display.liken.sh

**A running pod's device set never changes.** CRI carries CDI devices
at container creation only, so the pod is one session, and the taint is
what ends it so the scheduler can start the next.

**A monitor that arrives needs no restart.** The startup config has
an `[output]` section for every connector, dark or lit, and a preload
shim moves the compositor's hotplug subscription to the kernel's own
netlink group. When a monitor lands on a dark connector, the
compositor enables the head and routes the section's app-id. Every
other screen keeps its session. The same event clears the device's
taint, so a parked claim starts on its own. On unplug the compositor
destroys that output, and on replug it recreates it. A cable reseated
within the toleration costs nothing: the client's connection never
breaks, and its picture returns with the output. See
[plan 02](plans/completed/02-an-output-for-every-connector.md).

**The compositor and the operator exit together.** The operator starts
weston as its child and exits nonzero when weston exits; the kubelet
restarts both. A restart ends every client's Wayland connection, which
for most clients means the client's own pod restarts. This is the one
place a restart here is worse than the Bluetooth operator's, where the
prepared device nodes survive it.

## Not here yet

* **HDMI audio.** Each output carries an HDMI PCM on the audio
  controller, which the audio operator publishes. One claim holds a
  request against each driver with a `matchAttribute` constraint on
  `monitor.liken.sh/id`.
* **The drill.** No drill against a real card and two monitors has run
  yet. The plans state what one must show.
* **Metrics.** The operator prints to stderr and reports state through
  the taints. There is no metrics endpoint.

## The images

Two, from one `Dockerfile`. `ghcr.io/liken-sh/weston` is the compositor
and every library it loads, on nothing else.
`ghcr.io/liken-sh/display-operator` is that image plus the operator's
static binary, and it is the image the DaemonSet runs. The second
builds **from** the first, so the compositor the release tests is the
same bytes the pod runs, and the two share every layer.

The compositor's image exists because Debian ships every libweston
backend in one package, most of which this operator never loads.
`weston-closure.sh` copies only the four modules it uses and the loads
`ldd` cannot see.
[The compositor image](plans/completed/01-the-compositor-image.md) gives the
reasons.

## Building it

    go build ./...
    go test ./...
    docker build --target weston -t weston .
    docker build -t display-operator .

The Kubernetes libraries, the Go version, and the Debian suite are all
pinned, because the closure script targets one weston version and a
suite that moved past it must fail the build rather than ship modules
nobody read. The EDID fixtures in `testdata` are read off real monitors
with `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`.

## License

MIT. See [LICENSE](LICENSE).
