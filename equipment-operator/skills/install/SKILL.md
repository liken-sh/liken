---
name: install
description: "Install equipment-operator on a liken cluster, declare a Receiver, and put it under a Player. Use when an AV receiver must report its power, input, and volume to the cluster."
---

This skill is the guide at https://liken.sh/equipment/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Install the operator

This guide installs `equipment-operator` on a
[`liken`](https://liken.sh/docs/) cluster and declares a `Receiver`.
At the end, the operator runs in `liken-system` and reports the
receiver's power, input, and volume.

You need:

* A `liken` cluster with
  [`media-operator`](https://liken.sh/media/) installed. When a
  `Player` plays, `media-operator` writes what the receiver should do
  into the `Receiver`'s status, and this operator does it.
* A receiver on the network that speaks the Denon and Marantz control
  protocol, with network control enabled in its own menu, or a WiiM
  device.
* `kubectl` with cluster-admin access, because the install creates a
  CRD and a `ClusterRole`.

## Apply the manifests

This site serves the repository's
[`deploy/`](https://liken.sh/equipment/deploy/kustomization.yaml) directory as raw YAML, so
the install needs no clone:

    kubectl apply -n liken-system \
      -f https://liken.sh/equipment/deploy/receivers-crd.yaml \
      -f https://liken.sh/equipment/deploy/cecbuses-crd.yaml \
      -f https://liken.sh/equipment/deploy/televisions-crd.yaml \
      -f https://liken.sh/equipment/deploy/deviceclasses.yaml \
      -f https://liken.sh/equipment/deploy/rbac.yaml \
      -f https://liken.sh/equipment/deploy/operator.yaml \
      -f https://liken.sh/equipment/deploy/cec.yaml

`cec.yaml` runs the CEC node workload, the `equipment-operator-cec`
`DaemonSet`. Each of its pods claims a USB CEC adapter through the
`cec-adapter` `DeviceClass`. On a node with no adapter the pod stays
`Pending`, so
[keep the pods off nodes with no CEC adapter](#keep-the-pods-off-nodes-with-no-cec-adapter).
The [`CECBus`](https://liken.sh/equipment/docs/reference/cecbuses/) reference describes what the
pod reports, and the [`Television`](https://liken.sh/equipment/docs/reference/televisions/)
reference describes the TV that a `CECBus` in `Control` finds.

Each pod serves its readiness and liveness probes at `/healthz` on port
9200, and they succeed only while the node workload's main loop
responds. A pod whose loop stops responding becomes not `Ready` after
about 60 seconds, and the kubelet restarts its container after about
150 seconds. The operator's `Deployment` and the node workloads watch
these pods to decide whether an adapter's report is current, as
[Connect a CEC adapter](https://liken.sh/equipment/docs/guides/connect-a-cec-adapter/#give-the-adapter-control)
describes. The `equipment-operator-cec-pods` `Role` in `rbac.yaml` lets
them `get`, `list`, and `watch` pods in `liken-system`.

For GitOps, point a `Kustomization` at the base and set `<version>` to
the release tag that last published the operator. The
[GitHub release](https://github.com/liken-sh/liken/releases) of each
tag lists every component and its version. The base sets the namespace
`liken-system` on each object that has none, so leave out a top-level
`namespace:` field. That field overwrites the namespace of every
object, and it would move the monitoring component's dashboard out of
the monitoring namespace. The base names the image at `latest`, so set
the image to the same version:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    resources:
      - https://github.com/liken-sh/liken//equipment-operator/deploy?ref=<version>
    images:
      - name: ghcr.io/liken-sh/equipment-operator
        newTag: <version>

## Keep the pods off nodes with no CEC adapter

The `DaemonSet` creates a pod on every node. On a node with no CEC
adapter, the claim matches no device and the pod stays `Pending`. To
create no pod on such a node, label the node
`equipment.liken.sh/cec: none`.

The `DaemonSet` in the base has this node affinity, so you need no
patch:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: equipment.liken.sh/cec
              operator: NotIn
              values: ["none"]
```

`NotIn` also matches a node that has no such label, as the Kubernetes
page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
describes. So the `DaemonSet` creates a pod on every node without the
label, and on no node labeled `none`. When a label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches and
creates one on a node that matches again.

Declare the label in the node's `Machine`, so the machine's own record
keeps it:

    spec:
      nodeLabels:
        equipment.liken.sh/cec: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on. An older release refuses it, so on an older release
use the `kubectl label` method below.

The `liken` machine operator applies the label to the running node,
and a machine that is demoted or installed again registers its new
Node with the label. To run the pod on the node again, remove the key
from `spec.nodeLabels`, and `liken` removes the label from the node,
even when you had set it with `kubectl` first.

You can also label the node with `kubectl`:

    kubectl label node node-1 equipment.liken.sh/cec=none

The label stays on the node across reboots, because `liken` removes
only the labels that a `Machine` declared. But it is on the Node
object, so when the machine is demoted or installed again and
registers a new Node, you must label the new Node. To run the pod on
the node again, remove the label:

    kubectl label node node-1 equipment.liken.sh/cec-

If you patch a node affinity onto this `DaemonSet`, your patch
replaces the base's list of terms, including the `none` term, so copy
the `none` requirement into each term of your patch.

## Which devices the operator adopts

Other brands build devices on the LinkPlay platform that WiiM uses.
Their devices advertise the same `_linkplay._tcp.local.` mDNS service
and answer the same `getStatusEx` request, and the mDNS response does
not include the model. So the operator checks each device twice, and
controls it only when neither check finds another brand.

The first check is in discovery, before it creates a `Receiver`. Every
LinkPlay device serves a UPnP device description over plain HTTP at
`/description.xml`, on the port in its mDNS `SRV` record. The first
time a search finds a device, discovery reads that description with
one `GET`, and creates a `Receiver` only when the `modelName` begins
with `WiiM`, in any letter case. The check reads the model and not the
manufacturer, because LinkPlay also makes devices that other brands
sell:

| Device | `manufacturer` | `modelName` | Verdict |
| --- | --- | --- | --- |
| WiiM Amp | Linkplay Technology Inc. | WiiM Amp | a WiiM |
| Arylic amplifier | Rakoit Technology(SZ) Co., Ltd. | A50 | not a WiiM |

Because the description is on its own port over plain HTTP, this check
also works for a device that refuses the HTTPS control API. Discovery
keeps each result until the operator restarts. For a device that is
not a WiiM, discovery creates no `Receiver`, deletes the `Receiver`
that it created for the device earlier, sends the device nothing more,
and writes one line to its log:

    discovery skipped the LinkPlay device <uuid> at <address>: its UPnP description names the manufacturer Rakoit Technology(SZ) Co., Ltd. and the model A50, which is not a WiiM

When discovery cannot read or parse the description, it writes one
line to its log with the cause, creates the `Receiver` as it does for a
WiiM, and reads the description again on the next search that finds
the device. Until then the second check decides:

    discovery read no UPnP description from the LinkPlay device <uuid> at <address>, so its getStatusEx project decides whether it is a WiiM: <cause>

The second check is in the WiiM driver, on every poll. It reads the
`project` field of the `getStatusEx` response and controls the device
only when the field begins with `WiiM`, in any letter case. A WiiM Amp
reports `WiiM_Amp_4layer`, and an Arylic amplifier reports
`ARYLIC_A50TE`. This check also covers a `Receiver` that you declare.
The operator reads `getStatusEx` on every poll anyway, so the check
sends a WiiM no extra request. To a device whose `project` does not
begin with `WiiM`, the operator sends `getStatusEx` and nothing else:
no setting, no command, no event subscription, and no read of the
description. It writes one line to its log for each such device:

    discovery skipped the LinkPlay device <uuid> at <address>: its project is ARYLIC_A50TE, which is not a WiiM

When the `project` names another brand, the operator deletes the
`Receiver` that discovery created for the device, and discovery
creates no other for it until the operator restarts. Neither check
deletes a `Receiver` that you declared. That `Receiver` reports the
device unreachable, and the operator still sends the device only
`getStatusEx`. A device that does not answer `getStatusEx`, or whose
response has no `project` field, is not treated as another brand.

## Turn network discovery off

By default, the operator searches the LAN for WiiM amps with mDNS and
SSDP. Each search takes 4 seconds, and the next one starts 30 seconds
after the previous one ends. The operator creates a `Receiver` for each
amp that no `Receiver` names, and then reads and controls that amp.
Turn the search off when the cluster shares its LAN with amps that it
must not control, for example when a test cluster is on the same
network as a home's equipment. The `EQUIPMENT_NETWORK_DISCOVERY`
environment variable on the `Deployment` is `on` or `off`, and `on` by
default. To set it to `off`, add a patch to your `Kustomization`:

```yaml
patches:
  - patch: |-
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: equipment-operator
      spec:
        template:
          spec:
            containers:
              - name: operator
                env:
                  - name: EQUIPMENT_NETWORK_DISCOVERY
                    value: "off"
```

Keep the quotes. A YAML 1.1 reader, such as `kubectl`, reads a bare
`off` as the boolean false. Any value other than `on` or `off` stops the
operator at start with an error that names the value, so a wrong value
never leaves the search on.

With the search off, the operator logs one line at start that says so,
sends no mDNS or SSDP search, and creates no `Receiver`. A `Receiver`
that you declare works as before, but a WiiM `Receiver` must then set
`spec.wiim.address`, because nothing else finds the amp. A WiiM
`Receiver` with no address sends nothing and reports the amp
unreachable.

Turning the search off does not delete the `Receiver` objects that the
search created earlier. Only a search, or the driver when it finds a
device of another brand, deletes one. A discovered `Receiver` also
stays when you declare your own `Receiver` for the same amp, and then
two objects control one amp. Each discovered `Receiver` has the
`equipment.liken.sh/discovered` label. List them, and delete the ones
you do not want:

    kubectl get receivers -l equipment.liken.sh/discovered

The setting does not affect CEC. The CEC node workload still creates a
`CECBus` in `Listen` for an adapter that no `CECBus` names, and an
adapter in `Listen` sends nothing on the HDMI wire. The operator
creates a `Television` only for a `CECBus` that you set to `Control`.

## Declare the receiver

A `Receiver` sets the protocol, the address, and the wiring. The
operator cannot discover the wiring, that is, which machine's HDMI
output connects to which input. A receiver passes the same EDID to
every input, so each entry names the machine as well as the monitor
ID.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Receiver
metadata:
  name: living-room
spec:
  denon:
    address: receiver.example
  inputs:
    - name: MPLAY
      machine: node-1
      monitor: don-0070-denon-avr
  volume:
    max: 72
    step: 0.5
```

The input `name` is the spelling that the receiver uses. The monitor ID
is the one that [`display-operator`](https://liken.sh/display/)
publishes for that cable. The `volume` block uses the receiver's own
scale. `max` is the loudest level that one press may set the room to,
and a Denon requires it, because the limit that a Denon reports moves
with the volume. `step` is how far one press moves the volume, and half
steps are allowed. `indicator` is `Player` by default, so the
`Player`'s screens draw the volume bar. Set it to `Receiver` when the
receiver shows its own overlay on the TV. `media-operator` reads
`indicator`, and `equipment-operator` does not.

`kubectl get receivers` shows the receiver's model, the values that it
last reported, and the `Player` whose session uses it:

    NAME          MODEL        POWER   INPUT   VOLUME   PLAYER              REACHABLE   AGE
    living-room   AVR-X1700H   On      MPLAY   50.0     house/living-room   True        2m

The driver reads the model from the receiver's UPnP description into
`status.model`, and the manufacturer into `status.manufacturer`. A WiiM
serves its description on port 49152. A Denon or Marantz receiver of the
AVR-X 2016 generation or later serves it on port 60006. For a receiver
that serves no description, the `MODEL` column is empty.

`kubectl get receivers -o wide` adds the driver, the address, whether a
`Play` is active, and the sound mode.

## Put it under a Player

You do not declare a session by hand. `media-operator` resolves each
`Player` screen to a machine and a monitor ID, then finds the input that
matches both. While the `Player` has that screen, including its idle
screen, `media-operator` writes the session into `status.session`. An
older `media-operator` that does not write `status.session` writes
`spec.session` instead, and the operator reads `spec.session` while
`status.session` is absent.

There are no message bus topics to configure, because the operator
does not connect to a message bus. `media-operator` writes each press
of the room remote into the session. A volume key becomes a
`volumeAsk`, an absolute level in the receiver's own scale, at or below
`spec.volume.max`. The operator sends each new request to the receiver
once. When presses arrive faster than the receiver reports back the
last volume it was sent, the operator sends only the newest. The room
remote changes the volume both while a film plays and while the screen
is idle. The [`Receiver`](https://liken.sh/equipment/docs/reference/receivers/) reference
describes each request.

When a `Play` starts, the operator powers the receiver on and selects
the input, each only when the receiver reports a different value.
Waking the screen sets `status.session.awake` and sends the same
commands, even when no `Play` is running. An idle screen that starts
after a reboot does not power the receiver on, and after an operator
restart the operator sends nothing for the sessions it finds.

The remote's power button turns the whole room on or off.
`media-operator` sets the `Player`'s `spec.power`, and when the `Player`
turns off it writes a `powerAsk` of `off` into the session. When the
session's input is a `Display` that a `Television` lists, the TV's
power decides what the request does: if the TV is on, the operator
turns the room off, and if the TV is in standby, the room is already
off and the operator sends nothing. The CEC node workload asks the TV
for its power on each request, because a TV that a person turned off
with its own remote may send nothing on the wire, and nothing reads
the power between requests. The operator waits up to 3 seconds for
that reading, and when none arrives it uses the `Television`'s
`status.power`.
With no `Television`, the receiver's power decides.

A request that turns the room off sends the TV a standby command over
CEC and puts the receiver in standby. A WiiM has no standby command, so
it stays on, and the operator's log line says so. When the `Player`
turns on, its screen wakes and the session's `awake` flag becomes true,
so the operator wakes the TV, switches it to the machine's input, and
turns the receiver on. Only a `Player` that turns off turns the TV off.
A `Play` that ends, a screen that goes idle, and an operator restart
leave the TV as it is, because the TV can show another input, such as a
streaming player, while the room's player is idle.

A person can change the receiver with its own remote, and the operator
does not change it back at once. If the person selects another input,
the status shows that input, and the operator selects the configured
input again only when `active` or `awake` changes from false to true.
If the person turns the volume knob, the status shows the new volume,
and `media-operator` steps the next press from that level. The TV's
remote can change the volume too: while the receiver's System Audio
Mode is on, the TV sends the volume keys of its remote to the receiver
over HDMI-CEC. So `status.zones` can show volume changes that `liken`
did not make. The
[CEC guide](https://liken.sh/equipment/docs/guides/connect-a-cec-adapter/#let-the-tv-remote-set-the-receivers-volume)
says when the mode is on.

## Read what happened to a receiver

When a `Receiver`'s `Reachable`, `SettingsConfirmed`, or `InputSelected`
condition changes, the operator posts one Kubernetes `Event` with the
condition's reason and message. The `Event`s are:

* `Unreachable`, a Warning, when the receiver stops answering.
* `Connected` when it answers again.
* `NotConfirmed`, a Warning, when the receiver does not confirm a
  declared setting after 3 sends.
* `SessionInput`, `OtherInput`, or `NoInputReported`, all Normal, when
  the receiver's input starts or stops matching the session's input.
* `SystemAudioModeOn` or `SystemAudioModeOff`, both Normal, each time
  the System Audio Mode of a `Receiver` with a `status.cec` changes.

A `Receiver` is cluster-scoped, so its `Event`s are in the `default`
namespace, and `kubectl events` finds them only with `-n default` or
`-A`:

    kubectl describe receiver living-room
    kubectl events -n default --for receiver/living-room

The API server deletes an `Event` one hour after its last change. After
that, the conditions and the operator's log still show what happened.
