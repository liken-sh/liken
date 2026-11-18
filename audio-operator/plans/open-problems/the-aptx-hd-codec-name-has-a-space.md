# The aptX HD codec name has a space

Open bug, recorded on 2026-11-18. It is suspected and not reproduced,
and PipeWire's spelling of the codec was not checked.

`codecName` in `audio-operator/codecs.go` turns a codec's label from
the speaker device's `PropInfo` into a codec name for the `codecs`
device attribute. It lowercases the label, trims it, and replaces each
`-` with `_`. Its comment says that the result is the spelling of the
sink node's `api.bluez5.codec` property, which the `codec` attribute
publishes, so the two attributes use the same names.

The function does not replace a space. The label "aptX HD" becomes
`aptx hd`, and `TestCodecNameFollowsTheNodesSpelling` in
`codecs_test.go` asserts that result. Two faults follow if PipeWire
spells the codec `aptx_hd` in `api.bluez5.codec`:

- The `codec` attribute of a speaker that plays aptX HD reads
  `aptx_hd`, and no name in its `codecs` attribute matches it.
- `codecList` joins the names with spaces, so a CEL selector reads
  `aptx hd` in the `codecs` value as two names, `aptx` and `hd`. A
  claim that asks for `aptx` with `.contains()` also matches a speaker
  that offers only aptX HD.

## What would confirm it

PipeWire's bluez5 codec table, which gives each codec's name and its
description, or the `pw-dump` output of a speaker that offers aptX HD.
Either shows the value of `api.bluez5.codec` and the label in
`PropInfo`.

## What a fix has to decide

Whether `codecName` replaces spaces as it replaces dashes, or the
operator takes the names from a field that PipeWire publishes in the
same spelling as `api.bluez5.codec`, if `PropInfo` has one. The test
case changes with either fix.
