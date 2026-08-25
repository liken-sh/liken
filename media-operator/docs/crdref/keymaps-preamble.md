A `Keymap` is one controller model's table from buttons and axes to
named actions, written once per model and shared by every
[`Remote`](/docs/reference/remotes/) of that model. It is
cluster-scoped, the way a `DeviceClass` and a `StorageClass` are,
because one model's table is the same in every namespace. A `Remote` in
any namespace names it without a namespace qualifier.

The left side of the table uses evdev's names, because every Linux
controller driver reports the south face button as `BTN_SOUTH`,
whatever is printed on the button. The right side names what a
person means, `pause` or `seek`, never an mpv command, so a
different player program can implement the same table later.

    apiVersion: media.liken.sh/v1alpha1
    kind: Keymap
    metadata:
      name: dualsense
    spec:
      buttons:
        - press: BTN_SOUTH
          action: pause
        - press: BTN_TR
          action: seek
          amount: 30
          repeat:
            delay: 400ms
            interval: 300ms
      axes:
        - axis: ABS_HAT0X
          value: 1
          action: right

Buttons and axes are separate lists because they bind differently: a
button is a press, and an axis entry names a direction as well. A
`Keymap` must bind at least one entry across the two lists.
