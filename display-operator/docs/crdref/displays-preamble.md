A `Display` is one monitor as a Kubernetes resource. The operator
creates one for every monitor it probes, cluster-scoped like a
`Node`, named by the same monitor id the devices publish as
`monitor.liken.sh/id`. You never create or delete one. The operator
writes the whole of `status`: the controls the panel declares, the
values it last saw, and the values it saved before an override. You
write the resting fields of `spec`, and a machine writer, such as a
media layer that darkens idle screens, sets and lifts
`spec.override`.

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
      values: ["on", "off", hardOff]
  observed:
    brightness: 80
    power: "on"
  conditions:
    - type: Connected
      status: "True"
    - type: Responsive
      status: "True"
```

The [devices reference](/docs/reference/devices/) describes the
other paths to a panel: the claim parameters a `Play`-style workload
states once at prepare, and the control device a standing pod claims
for the raw wire. The `Display` is the declarative path: state what
the panel should hold, and the operator keeps it there.
