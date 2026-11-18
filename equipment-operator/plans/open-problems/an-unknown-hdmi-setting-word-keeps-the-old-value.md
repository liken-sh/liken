# An unknown HDMI setting word keeps the old value

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`applyHDMILine` in `denon/settings_hdmi.go` stores the value of one
`VSAUDIO` or `SSHOS` line. When the word in the line is not in the
table for its key, `foldHDMIWord` returns false and the field keeps
the value of an earlier line. The comment says such a word changes no
field because the operator could not send it back.

Suppose that the receiver reported `ON` for a field, and someone
changes the field at the receiver to a value whose word the table
does not hold. The reported settings still say `ON`. If the declared
value is `ON`, `Settings.Pending` finds no difference, and the
operator does not send `ON` again. The status then reports a value
the receiver no longer has.

## Evidence

- `foldHDMIWord` writes the field only when a word matches.
- The same pattern may apply to the word readers of the other
  settings families in `denon/state.go`. Nobody checked them.

## What would confirm it

A test of `applyHDMILine` that applies a known line and then a line
with the same key and an unknown word, and reads the field.

## What a fix has to decide

- Whether an unknown word clears the field to unknown, so `Pending`
  sends the declared value again, or keeps the last known value.
- Whether the same rule applies to every settings family.
