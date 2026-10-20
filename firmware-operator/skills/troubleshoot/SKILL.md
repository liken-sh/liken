---
name: troubleshoot
description: "Find why a Firmware's upload failed or never started: a Failed entry and its message, a Conflict between two rules, a selector that does not compile or matches nothing, an upload pod that stays Pending, and a claimed device that stays Pending because its firmware is not loaded. Use when kubectl get firmware shows Failed or Conflict, when a bare chip gets no upload, or when a pod that claims a camera does not start."
---

This skill is the guide at https://liken.sh/firmware/docs/guides/troubleshoot/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Troubleshoot an upload

Start from the `Firmware`. `kubectl get firmware` shows whether the
rule is valid and counts the result of each port where it matched a
device. `kubectl describe firmware <name>` shows the entries in
`status.devices` and the `Event`s of the last hour. Find the symptom
below.

## The entry is Failed

The entry's `message` says why. The operator does not retry a failed
upload. A new attempt starts when the device appears again, after a
power cycle or a replug, or when you edit the `Firmware`'s spec.

| Message | Cause and fix |
|---|---|
| `the loader exited with code <n>: <line>` | The loader failed. The last line of its log follows the code. Check the command, the path of the firmware file in the image, and the loader type, such as `-t fx2` or `-t fx3`. A loader that runs only as root fails here, because the upload pod runs as user 1000. |
| `the loader exited 0 and the device stayed in the slice for 1m0s after it` | The loader reported success, and the bare chip did not reset. The firmware file is usually wrong for the chip, or the chip needs a second stage, such as `fxload -s` for some older QHY cameras. |
| `the upload pod did not finish within 5m0s of its creation` | The pod never ran, or ran for five minutes. See [The upload pod stays Pending](#the-upload-pod-stays-pending). |
| `the upload pod failed: DeadlineExceeded: ...` | The loader ran longer than five minutes, and the kubelet stopped it. |
| `the upload pod is gone; ...` | Someone deleted the upload pod before the operator read its result. |

To try again without a replug, edit the spec, for example with an
argument that the loader accepts, or delete the `Firmware` and apply
it again.

## The entry is Conflict

Two or more `Firmware` objects match the same device, so the operator
runs none of them on it. The message names the other rules:

```sh
kubectl get firmware -o custom-columns=NAME:.metadata.name,SELECTOR:.spec.selector
```

Narrow one selector, for example with the product ID, or delete the
duplicate. The rule that still matches then uploads to the device
that is in the slice.

## Valid is False

The selector or `spec.machines` does not compile, and the rule runs
nowhere. The condition's message gives the compiler's error:

```sh
kubectl get firmware <name> -o jsonpath='{.status.conditions[?(@.type=="Valid")].message}'
```

A selector reads attributes as `device.attributes["liken.sh"].<name>`.
An attribute that a device does not have makes the selector fail on
that device, and the device counts as not matched. Guard such an
attribute with `has()`, for example
`has(device.attributes["liken.sh"].serial)`.

## No entry appears

The rule matched no device. Check the device in the machine's slice:

```sh
kubectl get resourceslice <node>-liken.sh -o yaml
```

- **The device is not in the slice.** The OS publishes a USB device
  only when a driver binds it or it has a usbfs node. `liken`'s
  [hardware modules guide](https://liken.sh/docs/guides/hardware-modules/)
  covers a device that the machine does not publish.
- **The IDs differ.** Compare the `vendor` and `product` attributes
  with the selector. Each is four lowercase hexadecimal digits, as a
  string.
- **The device has no `busNumber` or `deviceNumber`.** The operator
  uploads only to a device with both, because it builds the usbfs path
  from them. An older `liken` release does not publish them; upgrade
  the machine.
- **`spec.machines` leaves the machine out.** Compare the selector
  with the labels of the `Machine`:
  `kubectl get machine <node> --show-labels`.

## The upload pod stays Pending

The entry is `Running`, and the pod in `liken-system` does not start:

```sh
kubectl -n liken-system get pods -l firmware.liken.sh/upload=true
kubectl -n liken-system describe pod <pod>
```

- **The claim is not allocated.** The claim has the pod's name, and
  selects the device by its port path and device number. When the
  device left or enumerated again before the scheduler allocated the
  claim, the claim matches nothing. The operator writes `Failed` five
  minutes after it created the pod, and the new appearance gets its
  own upload. Check the class with
  `kubectl get deviceclass firmware-upload`: the base ships it.
- **The image does not pull.** The pod's `Event`s name the error.
  Check the tag in `spec.upload.image`. The network policy that
  denies the pod's traffic does not apply to the pull, because the
  node's container runtime pulls the image, not the pod.

## A pod that claims the loaded device stays Pending

A pod that claims the camera by its loaded IDs, such as `0921`, stays
`Pending` until the device with those IDs is in the slice. That is
the expected wait while the upload runs. When the upload is `Failed`
or `Conflict`, fix that first. When the camera has no `Firmware` at
all, nothing loads it, and the pod waits for good.
