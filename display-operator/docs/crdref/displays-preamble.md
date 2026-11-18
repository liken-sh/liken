A `Display` is one monitor, as a Kubernetes resource. You use it to
read the panel's controls, to set its brightness, input, and other
controls, and to turn the panel off for a while, all without a claim. The operator creates a `Display` for each monitor it
finds. A `Display` is cluster-scoped, like a `Node`, and its name is
the monitor id that the devices publish as `monitor.liken.sh/id`. You
never create or delete one.

The operator writes all of `status`: the controls the panel reports,
the values it last read from the panel, and the values it saved
before an override. You write the settings you want the panel to keep
in `spec`. A program that changes settings for a while, such as one
that darkens idle screens, adds `spec.override` and removes it when
it is done.

A list or a watch can select the monitors of one machine with
`status.node`, which the API server accepts as a field selector:

```sh
kubectl get displays --field-selector status.node=node-1
```

```yaml
apiVersion: display.liken.sh/v1alpha1
kind: Display
metadata:
  name: boe-1080-display
spec:
  brightness: 80
status:
  node: node-1
  connector: HDMI-A-2
  capabilities:
    brightness:
      max: 100
    input:
      values: [VGA-1, DVI-1, DVI-2, DP-1, DP-2, HDMI-1, HDMI-2]
    power:
      values: [On, Off, HardOff]
  observed:
    brightness: 80
    power: On
  conditions:
    - type: Connected
      status: "True"
    - type: Responsive
      status: "True"
    - type: CompositorServing
      status: "True"
      reason: Serving
```

The [devices reference](/docs/reference/devices/) describes two other
ways to control a panel. A claim can carry parameters that the
operator applies once, when it prepares the claim. A long-running pod
can claim the control device, which gives it the panel's DDC/CI
connection directly. On a `Display`, you write
the settings the panel should have, and the operator writes them
again whenever the panel reports a different value.
