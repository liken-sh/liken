A `Remote` is one physical controller: the device it is and, where
its model needs one, the [`Keymap`](/docs/reference/keymaps/) for its
model. The base table already gives a `Remote` with no `Keymap` the
arrows, OK, back, and every `KEY_*` code the device emits, and a
`Keymap` corrects a device the base gets wrong. It names no
player. A `Player` names the `Remote`s it owns through
`spec.remotes`, so the unit that owns a controller is the one that
lists it, and one controller can drive several units.

    apiVersion: media.liken.sh/v1alpha1
    kind: Remote
    metadata:
      name: den-pad
      namespace: den
    spec:
      device:
        class: gamepad
        selector: device.attributes["bluetooth.liken.sh"].address == "04:4A:5B:11:22:33"
      keymap: dualsense

`spec.device.parameters` is opaque configuration for the driver that
prepares the controller. A `Remote` uses it to name the classes of
input it needs, in the driver's own words. The bluetooth operator's
manual documents its `inputs` parameter and the classes it accepts.

    spec:
      device:
        class: gamepad
        selector: device.attributes["bluetooth.liken.sh"].address == "7C:66:EF:22:E7:80"
        parameters:
          driver: bluetooth.liken.sh
          values:
            inputs: [joystick]
