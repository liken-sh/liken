# A remote that teaches its keymap

Plan 21, a stub. The shape is chosen; the details wait for a design
pass before the build.

## The problem

Writing a `Keymap` for hardware the project has not seen is guesswork.
A new controller emits key codes the vendor documents poorly or not at
all, and today the only way to learn them is to read kernel sources,
guess, or attach diagnostic tools to the node by hand. The `Keymap`
resource is easy to write once the codes are known; discovering the
codes is the hard part, and nothing in the operator helps.

## The shape

A discovery mode of the remote pod. The reader already opens the
device and sees every event; in discovery mode it logs each key code
as it arrives, named as a `Keymap` would name it, so a person presses
every button on the new remote in turn and reads the codes straight
out of the pod log. The output should be easy to lift into a `Keymap`
spec with no translation by hand.

## To design before the build

- How the mode turns on: a field on the `Remote`, or a mode the
  operator sets while no `Keymap` matches the device.
- What one log line carries: the code, the event type, press against
  release and repeat, and the name the `Keymap` schema uses for it.
- Whether discovery still forwards mapped keys, or captures the
  device silently while it runs.
- How the manual presents the flow: plug in unknown hardware, turn on
  discovery, press every button, paste the result into a `Keymap`.
