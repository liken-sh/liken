---
name: install
description: "Install equipment-operator on a liken cluster, declare a Receiver, and put it under a Player. Use when an AV receiver must report its power, input, and volume to the cluster."
---

This skill is the guide at https://equipment.liken.sh/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

This guide installs `equipment-operator` on a
[`liken`](https://liken.sh/docs/) cluster and declares a `Receiver`.
At the end, the operator runs in `liken-system` and reports the
receiver's power, input, and volume.

You need:

* A `liken` cluster with the
  [`media-operator`](https://media.liken.sh) installed. The operator
  reads a session's level from the media bus that `media-operator`
  runs.
* A receiver that supports the Denon and Marantz control protocol on
  the network, with network control enabled in its own menu.
* `kubectl` with cluster-admin access, because the install creates a
  CRD and a `ClusterRole`.

## Apply the manifests

This site serves the repository's
[`deploy/`](https://equipment.liken.sh/deploy/kustomization.yaml) directory as raw YAML, so
the install needs no clone:

    kubectl apply -n liken-system \
      -f https://equipment.liken.sh/deploy/receivers-crd.yaml \
      -f https://equipment.liken.sh/deploy/cecbuses-crd.yaml \
      -f https://equipment.liken.sh/deploy/deviceclasses.yaml \
      -f https://equipment.liken.sh/deploy/rbac.yaml \
      -f https://equipment.liken.sh/deploy/operator.yaml \
      -f https://equipment.liken.sh/deploy/cec.yaml

`cec.yaml` runs the CEC node workload, the `equipment-operator-cec`
`DaemonSet`. Its pod claims a USB CEC adapter through the
`cec-adapter` `DeviceClass`, so on a node with no adapter the pod
stays `Pending`, and the `DaemonSet` never reports all its pods
ready. The [`CECBus`](https://equipment.liken.sh/docs/reference/cecbuses/)
reference describes what the pod reports.

For GitOps, point a `Kustomization` at the base and pin `<ref>` to a
release tag:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - https://github.com/liken-sh/equipment-operator//deploy?ref=<ref>

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
one the [`display-operator`](https://display.liken.sh) publishes for
that cable. The volume block is in the receiver's own scale. `max` is
the loudest level a press may set the room to, and a Denon requires
it, because the limit a Denon reports moves with the volume. `step`
is how far one press moves the volume, and half steps are allowed.
`kubectl get receivers` shows what the receiver last reported:

    NAME          POWER   INPUT   VOLUME   REACHABLE   AGE
    living-room   on      MPLAY   50.0     True        2m

## Put it under a Player

You declare no session by hand. The `media-operator` resolves each
`Player` screen to a machine and monitor ID, then finds the input that
matches both values. It applies `spec.session` while the `Player` has
that screen, including the idle screen. The session reads the level
from the `Player` volume topic for that whole period. The room remote
therefore changes the receiver's level while a film plays and while
the screen is idle. The topic and payload belong to `media-operator`.
Its [players page](https://media.liken.sh/docs/reference/players/)
defines them. This operator's reads and writes on that topic are
described on [the receiver on the bus](https://equipment.liken.sh/docs/reference/bus/). When a
`Play` starts, the session powers the receiver on and selects the input
once. Waking the screen also triggers those commands through
`spec.session.awake`, even with no `Play`. Starting an idle screen after
a reboot does not by itself power the receiver on.

A person at the receiver's own remote can change the receiver without
the cluster changing it back immediately. If the person selects
another input, the status records that input. The operator selects the
configured input again only when `active` or `awake` changes from false
to true. If the person turns the volume knob, the operator writes the
new level to the volume topic, so the next press starts from that level.
