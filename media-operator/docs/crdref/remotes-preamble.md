A `Remote` is one physical controller: the device it is and the
[`Keymap`](/docs/reference/keymaps/) for its model. It names no
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
