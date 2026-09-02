A `Keymap` is one controller model's table from its odd controls to
the kernel key names they should report, written once per model and
shared by every [`Remote`](/docs/reference/remotes/) of that model. A
model needs one only where the base table gets it wrong. The base
passes every `KEY_*` code as itself, turns the hat axes into the
arrows, and reads a gamepad's south and east buttons as enter and
back, so a `Remote` with no `Keymap` already works.

It is cluster-scoped, the way a `DeviceClass` and a `StorageClass` are,
because one model's table is the same in every namespace. A `Remote` in
any namespace names it without a namespace qualifier.

Both sides of the table use evdev's names, because every Linux
controller driver reports the south face button as `BTN_SOUTH`,
whatever is printed on the button, and every consumer binds
`KEY_VOLUMEUP` the same way. A `Keymap` renames a control and nothing
more. The right side is a kernel key name, or `none` to drop the
control, and each consumer holds its own table from key names to what
they mean there.

    apiVersion: media.liken.sh/v1alpha1
    kind: Keymap
    metadata:
      name: dualsense
    spec:
      buttons:
        - press: BTN_NORTH
          key: KEY_VOLUMEUP
          repeat:
            delay: 400ms
            interval: 150ms
        - press: BTN_TR
          key: KEY_FASTFORWARD
          repeat:
            delay: 400ms
            interval: 250ms
        - press: BTN_THUMBR
          key: none
      axes:
        - axis: ABS_HAT0X
          value: 1
          key: KEY_RIGHT

Buttons and axes are separate lists because they bind differently: a
button is a press, and an axis entry names a direction as well. A
`Keymap` must bind at least one entry across the two lists.
