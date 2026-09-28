---
title: Install
weight: 10
description: "Install equipment-operator on a liken cluster, declare a Receiver, and put it under a Player. Use when an AV receiver must report its power, input, and volume to the cluster."
---

This guide installs `equipment-operator` on a
[`liken`](https://liken.sh/docs/) cluster and declares a `Receiver`.
At the end, the operator runs in `liken-system` and reports the
receiver's power, input, and volume.

You need:

* A `liken` cluster with the
  [`media-operator`](https://liken.sh/media/) installed. The operator
  reads a session's level from the media bus that `media-operator`
  runs.
* A receiver that supports the Denon and Marantz control protocol on
  the network, with network control enabled in its own menu.
* `kubectl` with cluster-admin access, because the install creates a
  CRD and a `ClusterRole`.

## Apply the manifests

This site serves the repository's
[`deploy/`](/deploy/kustomization.yaml) directory as raw YAML, so
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
`DaemonSet`. Its pod claims a USB CEC adapter through the
`cec-adapter` `DeviceClass`, so on a node with no adapter the pod
stays `Pending`, and the `DaemonSet` never reports all its pods
ready. A node labeled `equipment.liken.sh/cec: none` gets no pod, as
[Keep the pods off nodes with no CEC adapter](#keep-the-pods-off-nodes-with-no-cec-adapter)
describes. The [`CECBus`](/docs/reference/cecbuses/)
reference describes what the pod reports, and the
[`Television`](/docs/reference/televisions/) reference describes the
TV that a `CECBus` in `Control` finds.

For GitOps, point a `Kustomization` at the base and pin `<ref>` to a
release tag:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - https://github.com/liken-sh/equipment-operator//deploy?ref=<ref>

## Keep the pods off nodes with no CEC adapter

The `DaemonSet` makes a pod on every node. On a node with no
CEC adapter, the claim matches no device, and the pod stays `Pending`. To make no
pod on such a node, label the node `equipment.liken.sh/cec: none`.

The `DaemonSet` in the base carries this node affinity, so no patch is
needed:

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

`NotIn` matches a node whose label has a different value, and also a
node that has no such label. The Kubernetes page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
gives this rule. So with no label the `DaemonSet` makes a pod on every
node, and a node labeled `none` gets no pod. When a label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches, and
adds one to a node that matches again.

Declare the label in the node's `Machine`, so the label is part of
the record of the machine:

    spec:
      nodeLabels:
        equipment.liken.sh/cec: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on, and an older release refuses it, so on an older
release use the `kubectl label` way below.

The `liken` machine operator applies the label to the running node. A
machine that is demoted or installed again registers a new Node, and
the Node has the label from registration. To run the pod on that
node again, remove the key from `spec.nodeLabels`, and `liken`
removes the label from the node. `liken` also removes a label that
you set with `kubectl` before the `Machine` declared it, when the key
leaves the spec.

You can also label the node with `kubectl`:

    kubectl label node node-1 equipment.liken.sh/cec=none

The label stays on the node across reboots. `liken` leaves it in
place, because `liken` removes only the labels that a `Machine`
declared. The label goes with the Node object: a machine that is
demoted or installed again registers a new Node, and you label it
again. To run the pod on that node again, remove the label:

    kubectl label node node-1 equipment.liken.sh/cec-

A patch of your own that sets a node affinity on this `DaemonSet`
replaces the list of terms in the base, and the `none` term with it.
Copy the `none` requirement into each term of your patch.

## Turn network discovery off

By default, the operator searches the LAN for WiiM amps with mDNS and
SSDP. Each search takes 4 seconds, and the next one starts 30 seconds
after it ends. It creates a `Receiver` for each amp that no
`Receiver` names, and then it reads and drives that amp. Turn the
search off when the cluster shares its LAN with amps that it must not
drive, for example a test cluster on the same network as a home's own
equipment. The `EQUIPMENT_NETWORK_DISCOVERY` variable on the
`Deployment` takes `on` or `off`. Set it to `off` with a patch in your
`Kustomization`:

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
`off` as the boolean false. Any value other than `on` or `off` stops
the operator at start with an error that names the value, so a wrong
value never leaves the search on.

With the search off, the operator writes one line to its log at start
that says so. It sends no mDNS or SSDP search and creates no
`Receiver`. A `Receiver` that you declare works as before. A WiiM
`Receiver` must state `spec.wiim.address`, because nothing else finds
the amp's address. A WiiM `Receiver` with no address sends nothing and
reports the amp unreachable.

The operator does not delete the `Receiver` objects that the search
created before you turned it off, because only a search deletes one.
So a discovered `Receiver` also stays when you declare a `Receiver` for
the same amp, and the two objects then name one amp. Each discovered
`Receiver` has the `equipment.liken.sh/discovered` label. List them,
and delete the ones you do not want:

    kubectl get receivers -l equipment.liken.sh/discovered

The setting covers the network search only. The CEC node workload
still creates a `CECBus` in `Listen` for an adapter that no `CECBus`
names, and a `Listen` adapter sends nothing on the HDMI wire. A
`Television` is created only for a `CECBus` that a person sets to
`Control`.

## Declare the receiver

A `Receiver` names the protocol, the address, and the wiring. The
wiring is the fact nothing can discover: which machine's HDMI output
connects to which input. A receiver forwards one EDID on every
input, so every entry names the machine as well as the monitor id.

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

The input name is the receiver's own spelling. The monitor id is the
one the [`display-operator`](https://liken.sh/display/) publishes for
that cable. The volume block is in the receiver's own scale. `max` is
the loudest level a press may set the room to, and a Denon requires
it, because the limit a Denon reports moves with the volume. `step`
is how far one press moves the volume, and half steps are allowed.
`kubectl get receivers` shows what the receiver last reported, and
the `Player` whose session holds it:

    NAME          POWER   INPUT   VOLUME   PLAYER              REACHABLE   AGE
    living-room   On      MPLAY   50.0     house/living-room   True        2m

`kubectl get receivers -o wide` adds the driver, the address, whether
a `Play` stands, and the sound mode.

## Put it under a Player

You declare no session by hand. The `media-operator` resolves each
`Player` screen to a machine and monitor ID, then finds the input that
matches both values. It applies `status.session` while the `Player`
has that screen, including the idle screen. A `media-operator` that
does not write `status.session` applies `spec.session` instead, and the
operator reads that block while `status.session` is absent. The
session reads the level
from the `Player` volume topic for that whole period. The room remote
therefore changes the receiver's level while a film plays and while
the screen is idle. The topic and payload belong to `media-operator`.
Its [players page](https://liken.sh/media/docs/reference/players/)
defines them. This operator's reads and writes on that topic are
described on [the receiver on the bus](/docs/reference/bus/). When a
`Play` starts, the session powers the receiver on and selects the input
once. It sends each command only when the receiver reports another
value. Waking the screen also triggers those commands through
`status.session.awake`, even with no `Play`. Starting an idle screen after
a reboot does not by itself power the receiver on, and an operator
restart sends nothing for the sessions it finds.

The remote's power button turns the whole room on or off. The
`media-operator` publishes a toggle on the session's power topic. When
the session's input names a `Display` that a `Television` lists, the
TV's power decides what the press does: a TV that is on means the press
turns the room off, and a TV in standby means the press turns the room
on. The CEC node workload asks the TV for its power at each press,
because no timer asks the TV between presses, and a TV that a person
turned off with its own remote may say nothing on the wire. The press
waits up to 3 seconds for that answer, and it decides from the
`Television`'s `status.power` when none arrives. With no `Television`,
the receiver's power decides. A
press that turns the room off asks the TV for standby over CEC and puts
the receiver in standby. A WiiM has no standby command, so it stays on,
and its log line says so. A press that turns the room on wakes the TV,
shows the machine's input, and turns the receiver on. Only the power
button turns the TV off. A `Play` that ends, a screen that goes idle,
and an operator restart leave the TV as it is, because the TV can show
another input, such as a streaming player, while the room's player is
idle.

A person at the receiver's own remote can change the receiver without
the cluster changing it back immediately. If the person selects
another input, the status records that input. The operator selects the
configured input again only when `active` or `awake` changes from false
to true. If the person turns the volume knob, the operator writes the
new level to the volume topic, so the next press starts from that level.
