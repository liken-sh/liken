---
title: Install
weight: 10
description: "Install observatory-operator on a liken cluster, run the example observatory of INDI simulators, reserve its telescope, and connect KStars to it. Use when a cluster has no observatory.liken.sh resources yet, to try the operator with no hardware, or when removing the operator."
---

This guide installs `observatory-operator` on a
[`liken`](https://liken.sh/docs/) cluster, and then runs a whole
observatory of INDI simulators: a dome, a weather station, two
telescopes, cameras, a filter wheel, a focuser, and a guider. At the
end, one telescope is reserved, and KStars on your desktop drives it.
No hardware is involved, so you can see every part of the operator
work before you describe your own equipment.

You need:

* A `liken` cluster. The simulators need no device, so any node can
  run them.
* `kubectl` with cluster-admin access, because the install creates
  20 CRDs.
* KStars on your desktop, for the last part. Any INDI client works
  the same way.

## Apply the manifests

The operator has no release yet, so you install a development build.
Every push to `main` that changes the operator publishes one. Its
version is the most recent release tag of the repository, plus a
suffix: `2026.10.04-004-dev-124-93f85ef4` is 124 commits past
release `2026.10.04-004`, at commit `93f85ef4`. The summary of the CI
run for that commit gives the version, and the
[package page](https://github.com/liken-sh/liken/pkgs/container/observatory-operator)
lists every version that exists.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version. Write this
`kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/liken-sh/liken//observatory-operator/deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/observatory-operator
    newTag: <version>
```

A git fetch by sha needs all forty characters. The eight in the
version are not enough. Then apply it:

```sh
kubectl apply -k .
```

The base creates:

* the namespace `observatory`
* the CRDs of the `observatory.liken.sh` group
* a `ServiceAccount` and a `Role` that cover only that namespace
* the operator, a `Deployment` with one replica

The operator reads and writes resources in its own namespace only, so
every resource of your observatory goes in `observatory`. This site
also serves the [manifests](/deploy/kustomization.yaml) as raw YAML,
if you want to read them first or write your own.

Check that the operator runs:

```sh
kubectl -n observatory rollout status deployment/observatory-operator
kubectl -n observatory logs deployment/observatory-operator
```

## Run the example observatory

The example declares a site named `lab` with two telescopes, `east`
and `west`, and a device of every kind the operator supports. Every
driver is an INDI simulator. Apply it:

```sh
kubectl apply -n observatory -f https://liken.sh/observatory/examples/simulators.yaml
```

Every kind is in the category `astro`, so one command lists the whole
observatory:

```sh
kubectl get astro -n observatory
```

The devices of `east` and `west` are `Idle`. Describing equipment
starts nothing. The operator starts pods only for a telescope that a
`Reservation` holds. The spare focuser is `Inventory`, because it is
on the shelf: it names no optical train.

The last resource in the example is the `Reservation` `east-tonight`.
It has no start time, so it activates the `east` telescope at once.
Watch it work through its steps:

```sh
kubectl get rsv -n observatory -w
```

Each line shows the phase, the step that runs, and what that step
waits for. The first activation pulls the INDI images, so it takes
longer than the next one. The camera's activation cools the sensor to
-10 °C and waits for it, which takes a few minutes on the simulator.
To wait for the end in a script:

```sh
kubectl wait --for=condition=Ready reservation/east-tonight -n observatory --timeout=15m
```

When the reservation is `Ready`, the telescope's pods run, every
device is connected, the dome is open, the mount is unparked, and
PHD2 is connected to the guide camera and the mount:

```sh
kubectl get pods -n observatory
kubectl get mnt,cam,dome,guider -n observatory
```

[How a reservation runs](/docs/reference/how-a-reservation-runs/)
explains each step.

## Connect KStars

The reservation's status names the telescope's INDI server:

```sh
kubectl get rsv east-tonight -n observatory -o jsonpath='{.status.endpoint.host}:{.status.endpoint.port}'
```

That address, `east-telescope.observatory.svc:7624`, works only inside
the cluster. From your desktop, forward the port:

```sh
kubectl port-forward -n observatory svc/east-telescope 7624
```

In KStars, open Ekos, create a profile, and set its mode to remote
with host `localhost` and port `7624`. Leave the device list empty:
Ekos takes every device the server offers. Start the profile, and the
INDI control panel lists the simulators by their INDI names, such as
`Telescope Simulator` and `CCD Simulator`.

For guiding, forward PHD2's event server too, in a second terminal:

```sh
kubectl port-forward -n observatory svc/east-guider 4400
```

In the Ekos guide module, choose PHD2 as the guider, with host
`localhost` and port `4400`. [Guide with PHD2](/docs/guides/guide-with-phd2/)
covers the guider in full.

A port forward is the simplest path, and it is enough for one person
at one desktop. To reach the telescope another way, see
[Reach the telescope from outside the cluster](/docs/guides/reserve-a-telescope/#reach-the-telescope-from-outside-the-cluster).

## End the reservation

Delete the reservation to end the night:

```sh
kubectl delete reservation east-tonight -n observatory
```

The delete waits while the operator runs the deactivation steps: it
stops guiding, closes the dust cap, warms the camera, parks the mount,
parks the dome, and stops the pods. That takes a few minutes, mostly
for the camera's warm-up. When the command returns, the equipment is
safe to power off.

## Remove the operator

Remove your resources first, while the operator still runs:

```sh
kubectl delete -n observatory -f https://liken.sh/observatory/examples/simulators.yaml
```

A resource that the operator is still running something for carries
the finalizer `observatory.liken.sh/deactivate`, and its delete waits
for the operator. If the operator is gone first, those deletes wait
forever. [Deleting a running resource](/docs/reference/how-a-reservation-runs/#deleting-a-running-resource)
explains the finalizer and how to remove it by hand.

Then delete the base:

```sh
kubectl delete -k .
```

This deletes the CRDs, and with them every resource of the
`observatory.liken.sh` group that is left.
