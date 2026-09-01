# A remote that teaches its keymap

## The problem

Writing a `Keymap` for hardware the project has not seen is
guesswork. A new controller emits key codes the vendor documents
poorly or not at all, and the raw numbers already reach the events
topic, so a person with `mosquitto_sub` can watch them. What is
missing is everything around that: the names a `Keymap` binds, an
answer in `kubectl`, reach to the nodes the reader rejects, and a
documented flow. Worse, the old button vocabulary held fifteen
gamepad names, so a media remote's `KEY_*` codes could not be bound
at all, and the old node selection kept only nodes that declared
one of those fifteen, so such a remote read as absent: connected,
emitting, and reported as away.

## The design

The whole `EV_KEY` name space becomes bindable. A generated table,
`keycodes.go`, holds every `BTN_*` and `KEY_*` name from the
kernel's `input-event-codes.h`, written by `make codes` and
committed. The `Keymap` schema replaces its fifteen-name list with a
pattern, and the compile stays the real gate. The axes stay the two
hats: no action takes an analog value, and a resting stick reports
constantly. The node selection widens in the same release, to any
node that declares a key code or a hat axis, because a `Keymap` that
may bind `KEY_PLAYPAUSE` needs the node that carries it. The two
changes are one change: a vocabulary the selection rule cannot
deliver is a promise the translator cannot keep.

`spec.discovery`, a boolean on the `Remote`, is the teaching mode.
The reader pod receives it as one environment variable, so the
toggle replaces the pod and the claim survives, the pod-only tier of
plan 13. In discovery the reader keeps every node the claim
delivered, because the point of discovery is to see what the rule
rejects, and it logs each event with the code's evdev name, the
value as a press, a release, or a repeat, and a paste-ready `Keymap`
entry. Publishing is unchanged: the translator already drops
unbound events, and a reader that went silent would stop waking a
faded idle screen. So a person maps a controller while the room
plays.

In both modes the reader logs one verdict line per node at each
open, with the keep or reject decision and the counts behind it, so
a controller that reaches nothing shows why. It logs the picture
once and repeats nothing while a sleeping controller keeps the
two-second scan busy.

The reader also publishes what the controller declares. A node's
capability bitmaps state every code it can report, complete with no
button pressed, so at each node open the pod publishes the union
over its kept nodes to a retained `codes` topic, shaped like the
presence topic beside it: republished on reconnect, cleared with an
empty payload when the nodes vanish. The operator folds it into a
desk and reports the gap on the `Remote`: `status.unbound`, every
declared code the `Keymap` does not bind. The gap is derived on
every pass and never accumulated, so it needs no reset, empties as
the `Keymap` grows, and stands afterward as a completeness check.
The list is a map keyed on code and type together, because one
number can name both a key and an axis, and it carries no cap: the
evdev ABI bounds a node at 768 key codes, and a real remote can
declare a full keyboard.

`spec.keymap` becomes optional, because a person mapping unknown
hardware has no `Keymap` yet. A `Player` that lists a `Remote` with
no `Keymap` gets a translator whose table topic never receives, so
that controller translates nothing and nothing fails.

## Considered and set aside

- The operator turns discovery on when no `Keymap` matches. Nothing
  compares a `Keymap` to a device, and the common failure, a table
  that compiles but binds the wrong codes, would never trigger it.
- A retained bus setting as the toggle. The reader has no inbound
  handler, the mode would be invisible to `kubectl`, and the broker
  holds no volume, so a broker restart would drop it.
- A one-shot request object, the `PairingRequest` shape. The
  standing pod is built from the `Remote` alone; a second object
  feeding it would make the template depend on two resources.
- A status map of every code ever observed. It stores between
  passes, needs a bound and a reset, and the bitmaps already give
  the complete set in the first second.
- A full census in status. `liken` rules that status reports the
  gap; a controller whose `Keymap` binds everything reports nothing.
- Capturing the device silently during discovery. Unbound events
  are dropped anyway, and silence would break the idle screen's
  wake path.
- The reader pod writing status itself. The pod holds no API
  credentials by design, and a second writer would race the
  operator's per-pass write.
- The full name list as a CRD enum. Six hundred names would bury
  the generated reference page; the pattern catches a typo's shape
  and the compile catches the rest.
- Raw numeric codes on the `Keymap`'s left side. The names are the
  API and the numbers are the wire.
- Infrared and CEC remotes. No hardware operator publishes an input
  device for them, so there is nothing for a `Remote` to select.
  That is a hardware-operator problem, not a mapping problem.

## The proof

Built in release 2026.09.01-001 and drilled on the testbed on
2026-09-01, on a DualSense and on an X6 mini keyboard remote, the
`KEY_*`-only device the plan exists for. The three checks the build
assumed all passed:

1. The claim's CDI spec narrows `/dev/input` to the one controller.
   Read from the node's CDI directory: the DualSense's claim
   delivers exactly its own three event nodes, so discovery's
   keep-everything bypass sees only the claimed controller.
2. The declared bitmap matches reality in both directions. Every
   one of the DualSense's 17 declared key codes was produced by a
   real control, and no press produced an undeclared code.
3. The full flow of the
   [mapping guide](../../docs/content/docs/guides/mapping-a-controller.md)
   ran end to end on the X6: a `Remote` with no `Keymap`, discovery
   on, every front-cluster button pressed, and a ten-entry `Keymap`
   lifted from the log. `status.unbound` fell from 275 to 265, the
   keyboard on the shell's back that stays deliberately unbound.

The drill also surfaced what the mode exists to surface. The
verdict lines showed the X6's OK button is a mouse click and its
house-glyph button emits `KEY_BACK`, facts no vendor document
states. And the first `KEY_*` device on the testbed flushed out two
OS gaps that `liken` fixed in its own releases the same day: a BLE
HID stack needs `/dev/uhid` delivered with the adapter's claim, and
the Bluetooth vendors' runtime-named patch firmware never shipped
from a modinfo-derived set.
