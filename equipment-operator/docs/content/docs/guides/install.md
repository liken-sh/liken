---
title: Install
weight: 10
---

This guide installs `equipment-operator` on a
[`liken`](https://liken.sh/docs/) cluster and declares a `Receiver`.
At the end, the operator runs in `liken-system` and reports the
receiver's power, input, and volume.

You need:

* A `liken` cluster with the
  [`media-operator`](https://media.liken.sh) installed. The operator
  reads a session's level from the media bus that `media-operator`
  runs.
* A receiver that speaks the Denon and Marantz control protocol on
  the network, with network control enabled in its own menu.
* `kubectl` with cluster-admin access, because the install creates a
  CRD and a `ClusterRole`.

## Apply the manifests

This site serves the repository's
[`deploy/`](/deploy/kustomization.yaml) directory as raw YAML, so
the install needs no clone:

    kubectl apply -n liken-system \
      -f https://equipment.liken.sh/deploy/receivers-crd.yaml \
      -f https://equipment.liken.sh/deploy/rbac.yaml \
      -f https://equipment.liken.sh/deploy/operator.yaml

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
lands on which input. A receiver forwards one EDID on every input,
so every entry names the machine as well as the monitor id.

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
```

The input name is the receiver's own spelling. The monitor id is the
one the [`display-operator`](https://display.liken.sh) publishes for
that cable. `kubectl get receivers` shows what the receiver last
said:

    NAME          POWER   INPUT   VOLUME   REACHABLE   AGE
    living-room   on      MPLAY   50.0     True        2m

## Put it under a Player

Nothing more is declared. The `media-operator` resolves each
`Player`'s screen to a machine and a monitor id, finds the input
that matches both, and applies `spec.session` while a `Play` stands.
The session powers the receiver on, selects the input once, and
takes the level from the `Player`'s volume topic. The remote's volume
keys then turn the receiver, and the playback pod runs at unity.

A person at the receiver's own remote outranks the cluster. If they
select another input, the status records it and nothing switches
back until the next `Play` starts. If they turn the knob, the new
level is written back to the bus, so the next press steps from
there.
