# An unknown HDMI setting word keeps the old value

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test calls `applyHDMILine` with `VSAUDIO AMP` and then with
`VSAUDIO OTHER`, and wants the audio out to be anything but `avr`. It
failed with:

```
after VSAUDIO OTHER the audio out reads avr, the value of the line before, want a value that is not avr
```

The test is not in the tree, because a failing test cannot land on
`main`.

Clearing the field to unknown would not by itself send the declared
value again. `Settings.Pending` in `denon/settings_pending.go` skips a
field that the receiver does not report when the value that the
operator applied before already equals the declared value.

## What a fix has to decide

When the receiver reports an HDMI setting word that the operator does
not know, the status still shows the last known value, and a change
made at the receiver is not reverted.

- Does the status show the setting as unknown, or keep the last known
  value?
- When it shows unknown, does the operator send your declared value
  again? That needs a change to `Settings.Pending` as well.
- Does the same rule apply to every settings family?
