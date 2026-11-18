A `Keymap` changes the key name that a controller's buttons and hat
axes send. You write one for each controller model, and each
[`Remote`](/docs/reference/remotes/) of that model refers to it. You
need one only when the base table gets a control wrong. The base table
sends every `KEY_*` code as itself, turns the hat axes into the arrow
keys, and maps a gamepad's south and east buttons to `KEY_ENTER` and
`KEY_BACK`. So a `Remote` with no `Keymap` already works.

A `Keymap` is cluster-scoped, like a `DeviceClass`, because a model's
buttons are the same in every namespace. A `Remote` in any namespace
refers to it by name alone.

Every binding uses evdev names. `press` or `axis` is the control, and
`key` is the key name that the control sends. Linux controller drivers
report the south face button as `BTN_SOUTH`, whatever is printed on
the button. A key of `none` makes the control send nothing. A `Keymap`
only renames controls. The program that reads the key decides what it
does. For example, `media-operator` turns each `KEY_VOLUMEUP` into one
step up of the `Player`'s volume.

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

An axis binding also needs a direction, `value`, so axes have their own
list. A `Keymap` must have at least one entry in `buttons` or `axes`.
