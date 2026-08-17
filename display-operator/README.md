# display-operator

A Kubernetes DRA driver that publishes each of a graphics card's
monitor outputs as its own device. A pod claims one screen, by its
connector name or by what the monitor is, and receives the Wayland
socket and the app-id that put its window on that screen.

This is an instance of liken's device operator pattern (milestone 56).
liken publishes the facts about hardware that no other layer can
observe, and which monitor is free is not one of them: the compositor
is what drives the monitors, so the layer that publishes outputs has to
be the layer that runs the compositor. Weston does not belong in the
read-only root that every machine boots, where every machine would
carry it for the one machine that has screens. So the operator is an
ordinary workload. It claims the card's display device through an
ordinary `liken.sh` claim, runs Weston with the kiosk shell beside
itself in the same pod, and publishes what the compositor drives under
its own driver name, `display.liken.sh`.

Without it, a screen is a string in a config file that a client
repeats. Two clients that present one app-id both get the screen, one
covering the other, and an app-id that matches no output lands on
whichever monitor the compositor enumerated first. With it, an output
is a resource the scheduler allocates once: the second pod parks until
the first one ends, and the app-id is only how the compositor routes
what the claim already decided.

The operator uses no private interface into liken. The raw claim, the
ResourceSlices it writes, and the CDI files it leaves for the container
runtime are the public contracts that any DRA driver on any Kubernetes
cluster gets. A cluster that never deploys it behaves exactly as it
does now.

## What it publishes

One device for each **connector** the card has, not for each monitor
that is plugged in. A monitor that is cabled and asleep still reports
itself on the wire, so its output publishes with no taint and a claim
on it starts a pod. A connector with nothing on it publishes as well,
with both taints on it, so a claim on that one parks instead of
failing. What it takes to start it is in
[Not here yet](#not-here-yet), because a connector that was empty when
the operator started needs a restart of the operator, not only a
monitor.

The device name is the connector in lowercase, because a DRA device
name must be a DNS label. The connector publishes as an attribute in
the kernel's own spelling.

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

Every attribute but `connector` and `appId` comes from the monitor, so
an output with nothing on it publishes those two and nothing else.

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
            - key: display.liken.sh/no-output
              effect: NoSchedule

A connector name is a property of the card's own outputs, so it is the
same after a reboot on one machine. It is not the same across machines,
and it says nothing about which monitor is plugged into it, which is
why the EDID facts publish beside it.

`monitor.liken.sh/id` is the one attribute that carries a domain of its
own. It is the identity a claim uses to pair a screen with that
screen's speakers, once the audio operator (milestone 59) publishes the
same value from the HDMI ELD. An attribute written with no domain
belongs to the driver that published it, so the shared name has to
carry a domain that neither driver owns.

Both drivers build the value the same way, because the scheduler
compares the two byte for byte and a disagreement of one character
parks the claim forever.

* The manufacturer's PNP id in lowercase, then the product code as
  four lowercase hexadecimal digits: `gsm-5b09`.
* Then, only when the monitor states a name, that name in lowercase
  with each run of spaces replaced by one dash:
  `gsm-5b09-lg-ultrawide`.
* A monitor that states no name keeps the two-part form, `boe-095f`,
  with no trailing dash. Those two parts are what the ELD carries as
  well, so a nameless panel still pairs with its own speakers.
* A manufacturer this operator cannot decode publishes no pairing
  attribute at all. A value with no manufacturer in it would match
  every other monitor that also states none.

Two monitors of one model produce one value, and a constraint across
the two drivers is then satisfied by either pairing.

The facts come from sysfs, not from the compositor:
`/sys/class/drm/<card>-<connector>/status` and the `edid` file beside
it. Weston reports the same things only over its private IPC, and
reading a file needs no protocol and no version agreement with the
compositor running beside it.

## Deploying it

    kubectl apply -k deploy/

Or reference `deploy/` from your own GitOps and patch it there. The
base assumes the namespace `liken-system` exists.

Nothing states which machine has the monitors. The operator's pod
claims the card's display device, only a machine with a graphics card
publishes one, and the scheduler puts the pod where the hardware is. To
serve more than one card, raise `replicas` on the Deployment to the
number of cards: each replica's claim allocates a distinct one, and a
replica past that number parks Pending and costs nothing.

Three DeviceClasses come with the base. `display-gpu` and
`display-render` are the raw devices the operator claims from liken,
selected by the attributes liken puts on them:

    device.driver == "liken.sh" &&
    has(device.attributes["liken.sh"].displayNode)

    device.driver == "liken.sh" &&
    has(device.attributes["liken.sh"].renderNode)

The card node allocates once, which is what makes this pod the only
program setting a mode on the card. The render node is shareable, and
the compositor's GL renderer builds its EGL device from it, so holding
it takes nothing away from the transcoders on the same GPU.

`display-output` is what a consumer claims:

    device.driver == "display.liken.sh"

## Claiming an output

Name one screen by its connector:

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
                # A monitor that goes dark for a moment is not a loss.
                # This number belongs to the workload, not to the
                # operator: it says how long this pod may hold a screen
                # that shows nothing before the pod ends.
                - key: display.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

Or name the monitor rather than the socket it is plugged into, which is
the claim that survives somebody moving a cable. `model` comes from the
monitor and is absent on an empty connector, so the selector guards it
first: a selector that reads a missing attribute fails the whole
allocation.

    has(device.attributes["display.liken.sh"].model) &&
    device.attributes["display.liken.sh"].model == "LG HDR WQHD"

Or take any output that fits, which is the claim a video player makes.
`widthPixels` is also a monitor fact, so it takes the same guard:

    has(device.attributes["display.liken.sh"].widthPixels) &&
    device.attributes["display.liken.sh"].widthPixels >= 1920

Leave out the `selectors` block to claim any output at all.

Tolerate `display.liken.sh/disconnected` and nothing else. The second
taint, `display.liken.sh/no-output`, is `NoSchedule` and must stay
untolerated: a tolerated `NoExecute` taint still lets the scheduler
allocate the device, so a pod that tolerated both would allocate a dark
output, wait in `ContainerCreating`, get evicted when its
`tolerationSeconds` ran out, and have the same dark output allocated to
its replacement. Leaving the `NoSchedule` taint alone parks the pod as
`Unschedulable` instead, visibly, until a monitor comes back.

Then the pod names the claim, and the container that draws on the
screen names the pod's entry and passes the app-id to its own toolkit:

    apiVersion: v1
    kind: Pod
    metadata:
      name: kitchen-dashboard
      namespace: house
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

A mount and three environment variables. No device node at all: a
Wayland client draws through the compositor, and the compositor is what
holds the card.

| What | Value |
|---|---|
| mount | `/var/run/display.liken.sh`, the compositor's runtime directory |
| `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| `WAYLAND_DISPLAY` | `wayland-0` |
| `DISPLAY_APP_ID` | the allocated output's app-id, such as `hdmi-a-1` |

The client has to pass the app-id to its own toolkit, because no
Wayland client reads a variable that chromium and mpv do not define.
chromium takes `--class=$(DISPLAY_APP_ID)` and mpv takes
`--wayland-app-id=$(DISPLAY_APP_ID)`.

**The claim is the arbitration and the app-id is only the routing.**
The compositor refuses nothing. A client that presents an app-id
matching no output still gets a screen, on whichever output the
compositor enumerated first, and two clients that present one app-id
both get that output with the second covering the first. What prevents
both is that the second pod cannot allocate an output the first pod
holds. An app-id that a pod invents rather than reading from
`DISPLAY_APP_ID` is outside that guarantee.

One container drives one screen. A claim that allocates two outputs
into one container delivers two app-ids, and only one `DISPLAY_APP_ID`
survives, because CDI applies the edits in order and the last value
wins. A pod that drives two screens runs two containers, each naming
its own request.

## The privilege it takes

None. The pod declares no `hostNetwork`, adds no capability, drops
`ALL`, and runs unprivileged.

The Bluetooth operator needs `hostNetwork` and `NET_ADMIN` because the
Bluetooth stack's whole control surface is a socket family that exists
only in the host's network namespace, and the kernel tests for
`CAP_NET_ADMIN` on the management channel's privileged commands. A
compositor meets no such check. libseat's noop backend opens the card
node with a plain `open()`, and the kernel hands DRM master to the
first process to open it, so everything this pod does to the hardware
it does through its claim.

Two things beside that are worth stating.

* **`hostUsers: true`.** The kernel delivers uevents to the initial
  user namespace only. A pod in its own user namespace receives an
  empty stream with no error to read, and a monitor plugged in after
  the pod started would never appear. This is the default, and the pod
  spec states it because the failure is silent.
* **`/var/run/display.liken.sh` is a hostPath.** The Wayland socket has
  to be somewhere a consumer's container can mount, and the CDI spec
  names one path for both ends. The compositor creates the socket with
  a zero umask, so a client running under any uid can connect to it.

Beside those, the pod takes the two hostPath mounts every DRA driver
takes: the kubelet's plugin registry directory, so the kubelet finds
the driver, and `/var/run/cdi`, so prepared claims land where the
container runtime reads them. Its own plugin socket directory,
`/var/lib/kubelet/plugins/display.liken.sh`, is the third.

## Disconnects and restarts

**A monitor that goes dark is tainted, never deleted.** The device
stays in the slice with both taints on it, and the taint-eviction
controller ends the pod that holds the claim once the claim's
`tolerationSeconds` runs out. A monitor that comes back clears the
taints and the scheduler places the consumer again. Deleting the device
instead would strand the next consumer: the allocation still names the
device, `NodePrepareResources` retries against a device that is in no
slice, and nothing bounds that retry. KEP-5322 would have bounded
exactly this case, and it was closed as not-planned in March 2026.

**The slice outlives the pod.** The operator retracts nothing when it
stops, and it publishes nothing when its walk of sysfs comes back
empty, because an empty inventory is a delete of every device in it.
The Node owns the slice, so a node that leaves the cluster takes the
slice with it. To remove the operator for good, delete the workload and
then the slice by name:

    kubectl delete resourceslice <node>-display.liken.sh

**An operator restart ends every client's Wayland connection.** This is
the one place where this operator's restart is worse than the Bluetooth
operator's. There, the prepared CDI files carry device nodes, and a
consumer that is already running keeps its device across a restart of
the operator's pod. Here the delivery is a live connection to a process
that just died: the new pod starts a new compositor, and every client
on every screen has to reconnect, which for most clients means the pod
restarts. Deleting this operator's pod is a visible event on every
monitor, not a quiet one.

**A running pod's device set never changes.** CRI carries CDI devices
only at container creation, CDI has no re-apply operation, and NRI's
post-create updates reach cgroup settings and not device nodes. The pod
is one session. The taint is what ends the session so the scheduler can
start the next one.

**The compositor and the operator live and die together.** The
compositor holds the screens and the socket, so its death ends every
client at once, and an operator that outlived it would publish outputs
that no pod can draw on. The operator starts weston as its own child
and exits nonzero when weston exits. The container ends with the
operator, and the kubelet restarts both.

**A compositor that dies taints every output on its way out.** That
write is what ends the clients, and it has to happen while this
operator is still running: the pod the kubelet starts next publishes
the same devices again, and a slice that does not change raises no
scheduler event, so nothing would ever evict the pods that are drawing
into a socket that is gone.

**Startup taints each output for what the operator has read.** The two
taints answer two questions, and a startup that has not run the
compositor yet has an answer for only one of them.

Nothing routes to a screen until the compositor enumerates its heads,
so every output publishes with `display.liken.sh/no-output`,
`NoSchedule`. A previous pod's slice never outlives the compositor it
described, and a compositor that cannot start at all still leaves an
honest answer behind.

Whether a connector is dark is the other question, and sysfs and the
EDID answer it with no compositor running. A dark connector publishes
with `display.liken.sh/disconnected`, `NoExecute`, the same as on any
other pass, so its holder is evicted even if the compositor never
starts. A connector with a monitor on it publishes without that taint:
it ends the pod holding the screen, and a restart of this operator is
no reason to end a client whose monitor never moved. The first
reconcile after the socket appears clears the `NoSchedule` taint from
the outputs that can serve a client.

## Not here yet

* **Minted app-ids.** Version 0 writes one fixed app-id for each
  output, the device's own name, into `weston.ini` at startup. It is
  simple, and it lets a client that guesses the string take the screen
  without a claim. Minting one for each allocation would make the
  app-id a capability, and it costs the compositor a config change on
  every allocation, which today means restarting the compositor and
  ending every client on every screen. Milestone 57 leaves the choice
  open, and this is the answer for now.
* **Hotplug of a new output.** The operator enumerates the connectors
  once, at startup, and writes an `[output]` section for each one that
  has a monitor on it. Unplugging and replugging a monitor that was
  there at startup works with no restart: the taints go on and come
  off, and the claim's `tolerationSeconds` covers a short absence.

  A monitor plugged into a connector that was **empty** at startup is
  different. The compositor has no section for it, so no app-id
  reaches it, and a client sent there would land on the first output
  instead, covering whatever was already on that screen. The operator
  refuses to make that happen: the device publishes with the monitor's
  own facts and keeps the `no-output` `NoSchedule` taint, so a claim
  on it parks as `Unschedulable` and `NodePrepareResources` refuses it
  by name. Restarting the operator writes the section and clears the
  taint, at the cost of ending every client on every screen:

      kubectl rollout restart -n liken-system deploy/display-operator
* **HDMI audio.** Each output carries an HDMI PCM on the machine's
  audio controller, and this operator does not touch it. That belongs
  to the audio operator, milestone 59, which publishes each PCM as its
  own device. One claim holds a request against each driver with a
  `matchAttribute` constraint on `monitor.liken.sh/id`, which is why
  both drivers publish that attribute byte for byte the same way.
* **The drill.** Milestone 57 states what a drill against a real card
  and two real monitors must show. None of it has run.
* **Metrics.** The operator prints what it does to stderr and reports
  output state through the taints. It exposes no metrics endpoint.

## The images

Two, from one `Dockerfile`.

* `ghcr.io/liken-sh/weston` is the compositor and every library it
  loads, on nothing else. No shell, no package manager, no libc
  userland.
* `ghcr.io/liken-sh/display-operator` is that image plus the operator's
  binary, which is static and is the whole of what this repository
  adds. It is the image the Deployment names.

The second is built **from** the first rather than beside it, so the
compositor that the release starts and reads the log of is the same set
of bytes the pod runs. The two share every layer, so a node that has
one pulls the other for the size of one binary.

The pod runs **one** container. The operator writes `weston.ini` from
the outputs it enumerated and then runs weston as its own child, and a
weston that exits has to end the operator. A pod spec has no way to
bind one container's life to another's: two containers restart
separately, and rebuilding that binding across a shared volume would
put two restart loops where there is now one exit status.

The compositor's own image exists because it is the artifact the
release proves. Debian ships every libweston backend in one package, so
installing `weston` also installs FreeRDP, neatvnc, GStreamer,
PipeWire, libavcodec and a speech synthesiser, none of which this
operator loads. `weston-closure.sh` takes the four modules the operator
uses, resolves what the loader needs for each, and copies that. It
names the loads that `ldd` cannot see, which is every module weston
opens by file name, glvnd's EGL vendor, mesa's gbm backend, and the DRI
drivers.

## Building it

    go build ./...
    go test ./...
    docker build --target weston -t weston .
    docker build -t display-operator .

The Kubernetes libraries and the Go version are pinned to what liken
builds against, because the two drivers serve the same kubelet on the
same node. The Debian suite is pinned as well, because the closure
script names weston 14 and a suite that carries weston 15 has to fail
the build rather than ship a set of modules nobody read.

To start the compositor with no graphics card, which is what the
release does:

    printf '[core]\nshell=kiosk\nrenderer=gl\nrequire-input=false\n' > weston.ini
    docker run --rm --tmpfs /run -e XDG_RUNTIME_DIR=/run \
        -v "$PWD/weston.ini:/weston.ini:ro" weston \
        --backend=headless --config=/weston.ini --socket=smoke

The log names each module as it opens it. `Using GL renderer` is the
line that says mesa resolved a driver, and the `kiosk-shell.so` line is
the last load.

The EDID fixtures in `testdata` are whole EDIDs read off real monitors
with `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`. To add one,
save the hex on one line and give the parser a case in `edid_test.go`.

## License

MIT. See [LICENSE](LICENSE).
