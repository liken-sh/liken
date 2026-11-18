A `Remote` is one physical controller. It selects the controller's
device and, when the model needs one, a
[`Keymap`](/docs/reference/keymaps/). Without a `Keymap`, the base
table maps the arrows, OK, back, and every `KEY_*` code the device
emits. Add a `Keymap` to correct a button that the base table maps
wrongly. A `Remote` does not name a `Player`. Each `Player` lists the
`Remote`s that control it in `spec.remotes`, so one controller can
control several `Player`s.

A `Remote` name has at most 32 characters, because `media-operator`
builds the names of the objects it creates for the `Remote` from it,
and Kubernetes limits some of those names to 63 characters.

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

`spec.device.parameters` is configuration for the driver that
prepares the controller, and `media-operator` copies it unchanged
into the controller's device claim. For example, the Bluetooth
operator's `inputs` parameter chooses which input classes, such as
`joystick`, reach the container that reads the controller. The
Bluetooth operator's manual lists the classes.

    spec:
      device:
        class: gamepad
        selector: device.attributes["bluetooth.liken.sh"].address == "7C:66:EF:22:E7:80"
        parameters:
          driver: bluetooth.liken.sh
          values:
            inputs: [joystick]
