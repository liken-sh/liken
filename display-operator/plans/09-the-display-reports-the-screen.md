# The Display reports the screen

The `Display` answers what a panel carries and what its controls
hold, but not what the screen is doing: the mode it runs, the modes
it accepts, and the monitor's own identity live only on the
`ResourceSlice`. This plan adds them to `status` in the format each
deserves, and it adds `spec.mode`, the resting mode the screen
returns to between claims. The slice keeps every attribute it has.

## The problem

Three facts about a screen are hard to reach or missing:

- `currentMode` and the monitor's identity are slice attributes. A
  person reading a `Display` has to cross-reference the slice to
  learn what the resource's own panel runs.
- The slice's `modes` attribute is cut to whole names under the
  API's 64-character attribute limit, so the published list is a
  truncation of what the kernel offers. No surface carries the full
  list.
- A claim's `mode` parameter sets the mode for the claim's lifetime,
  and nothing states what the screen returns to. When the claim
  ends, the screen keeps the film's mode until the next compositor
  start, because no resting declaration exists.

## The design

### Status carries the screen

`status` gains fields the slice pass already knows, written by the
same operator from the same reads, so the two surfaces cannot
drift:

- `currentMode`: the mode the output drives now, as `1920x1080@60`,
  from the same DRM read the slice's attribute uses. Absent while
  the output drives nothing.
- `modes`: the kernel's full mode list, deduplicated, with refresh,
  in the same string form. Status has no 64-character limit, so
  this list is complete where the slice's is cut. The slice's
  truncated attribute stays as it is, for selectors.
- `manufacturer`, `model`, `serial`: the EDID identity the slice
  publishes, so `kubectl describe display` names the monitor.
- `widthMillimeters`, `heightMillimeters`: the panel's physical
  size.

### The resting mode

`spec.mode` declares the mode the screen rests at, as one string in
the `modes` form, validated against `status.modes`. The precedence
follows the brightness pattern:

- A prepared claim's `mode` parameter wins for the claim's
  lifetime, exactly as today.
- While no output claim is prepared on the connector, the operator
  reconciles the screen to the declared resting mode.
- No declaration means today's behavior: the screen keeps whatever
  mode the last actuation left.

A mode lands through the compositor, and a mode change restarts it.
So the operator applies a resting mode only while the output is
free, and a `spec.mode` edit during a claim waits for the claim to
end. When a claim's unprepare frees the output and the screen's
mode differs from a declared resting mode, the operator restores
the resting mode, which restarts the compositor once. Every
standing draw client rides that restart the way it rides a mode
prepare: the kubelet supervises the compositor, and the media
layer's idle clients exit and reconnect on their own.

`spec.mode` is a resting declaration, not an override. A temporary
mode already has a home, the claim that needs it, so the override
block does not grow a mode.

## What was considered and set aside

**A `mode` field on `spec.override`.** Set aside because a
temporary mode with a lifetime is what a claim's `mode` parameter
already is, and a second temporary channel would be two writers on
one knob.

**Truncating `status.modes` to match the slice.** Set aside because
the truncation exists only for the attribute value limit, and
repeating a workaround where the constraint does not apply would
make the better surface worse.

## How the work is proved

On `liken-1`:

- Both `Display` resources report `currentMode`, the full mode
  list, identity, and physical size, and the mode list is longer
  than the slice's truncated attribute for at least one panel.
- Declaring `spec.mode` on a free screen restarts the compositor
  once and `currentMode` follows the declaration.
- Starting a `Play` whose claim states a different mode wins the
  screen for the film; ending it returns the screen to the resting
  declaration.
- Editing `spec.mode` while the claim holds the screen changes
  nothing until the claim ends.
