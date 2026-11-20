# display-operator

`display-operator` lets a pod use a screen. The pod's manifest claims
a monitor output by its connector, by the monitor's model, or by its
size, and the windows that the pod opens show on that screen. You set
a monitor's brightness and input with `kubectl`, and you can power it
down for a while, whether or not a pod draws on it.

The operator runs the Weston compositor on each machine's graphics
card. It publishes each connector as a device that a pod can claim,
and it gives each claim its own Wayland socket. A claim can also ask
for a mode, such as a refresh rate that matches a film. The operator
creates a `Display` for each monitor, and it sends the settings that
you set on the `Display` to the panel over DDC/CI. A `Layout` divides
one screen into regions, so pods from several namespaces can share
it. The capture API returns a screenshot, a clip, or a live stream of
what a `Display` shows, to anyone whom RBAC allows to capture. The
operator doesn't start programs on a screen, because a `Deployment` or
another operator does that. It also doesn't choose which screen a
workload gets, because the claim decides that.

`display-operator` is one of `liken`'s
[extension operators for claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
It claims the graphics card that `liken` publishes, through the same
public interfaces that any driver uses, and a cluster can run without
it. The compositor is the [`weston`](../weston/) image, with a `liken`
module that places each window. A claim that names a monitor can get
the monitor's speakers from `audio-operator` along with its screen.
`media-operator` plays films and draws its idle screen on these
screens. `equipment-operator` reads the HDMI-CEC physical address that
each `Display` reports.

## The kinds

- A `Display` is one monitor, named by the monitor's id. Its `status`
  reports the panel's controls and their values, the modes that the
  monitor accepts, the mode that it is running, and the windows on the
  screen. Its `spec` sets the brightness, the input, the mode, and the
  other controls, and names the `Layout` of the screen. A temporary
  `override` takes precedence over `spec`, for example to turn the
  backlight off or power the panel down while the screen is idle.
  When you remove the `override`, the operator restores the values
  from `spec` and the values that it saved.
- A `Layout` is a list of regions. Each region is a rectangle, given
  in fractions of the screen, with a label selector that chooses the
  pod whose windows the region shows. Any number of `Display`s can use
  the same `Layout`.

This `Layout` puts a notice board on the left 70% of a screen and a
camera feed in the upper right:

```yaml
apiVersion: display.liken.sh/v1alpha1
kind: Layout
metadata:
  name: front-desk
spec:
  regions:
    - name: notices
      rect: {left: 0, top: 0, width: 0.7, height: 1}
      selector:
        matchLabels: {panel: notices}
    - name: camera
      rect: {left: 0.7, top: 0, width: 0.3, height: 0.6}
      selector:
        matchLabels: {panel: camera}
```

The manual is at [liken.sh/display](https://liken.sh/display/). It
has the install guide, the guides, and a reference for each kind.
The design documents and the open problems are in [`plans/`](plans/).

MIT. See [LICENSE](LICENSE).
