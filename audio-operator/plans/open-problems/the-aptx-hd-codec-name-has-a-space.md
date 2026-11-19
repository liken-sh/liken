# The aptX HD codec name has a space

Open bug, recorded on 2026-11-18. PipeWire's source was checked, and
a unit test reproduced the bug on 2026-11-19. The bug is wider than
the space: for several codecs the label and the name differ in more
than spelling.

`codecName` in `audio-operator/codecs.go` turns a codec's label from
the speaker device's `PropInfo` into a codec name for the `codecs`
device attribute. It lowercases the label, trims it, and replaces each
`-` with `_`. Its comment says that the result is the spelling of the
sink node's `api.bluez5.codec` property, which the `codec` attribute
publishes, so the two attributes use the same names.

The function does not replace a space. The label "aptX HD" becomes
`aptx hd`, and `TestCodecNameFollowsTheNodesSpelling` in
`codecs_test.go` asserts that result. PipeWire spells the codec
`aptx_hd` in `api.bluez5.codec`, so two faults follow:

- The `codec` attribute of a speaker that plays aptX HD reads
  `aptx_hd`, and no name in its `codecs` attribute matches it.
- `codecList` joins the names with spaces, so a selector that splits
  the `codecs` value reads `aptx hd` as two names, `aptx` and `hd`. A
  claim that asks for `aptx` with `.contains()` also matches a speaker
  that offers only aptX HD. CEL's `.contains()` matches a substring,
  so that claim also matches `aptx_hd`, and the space does not cause
  this second match.

## What PipeWire's source shows

The source of PipeWire's bluez5 plugin on its `master` branch, read on
2026-11-19, gives each codec a `name` and a `description`:

- `bluez5-device.c` builds the `PropInfo` labels of the codec
  property from each codec's `description`, and sets
  `api.bluez5.codec` on the node from the codec's `name`, through
  `get_codec_name`.
- `a2dp-codec-aptx.c` names aptX HD `aptx_hd`, with the description
  "aptX HD".
- Several names are not the description with its case and separators
  changed: "FastStream duplex SBC" is `faststream_sbc`, "LC3-24kHz"
  is `lc3_a127`, and "Opus 05 Pro Audio" is `opus_05_pro`.

So the comment on `codecName`, which says that the operator needs no
table of names to keep in step with PipeWire, is wrong for those
codecs, and replacing a space with `_` corrects only aptX HD and
"aptX-LL mSBC".

## The reproduction

The test is the existing table of `codecName` cases, with the aptX HD
row corrected to `aptx_hd` and rows added for the other codecs above.
It failed with:

    codecName("aptX HD") = "aptx hd", want "aptx_hd"
    codecName("FastStream duplex SBC") = "faststream duplex sbc", want "faststream_sbc"
    codecName("LC3-24kHz") = "lc3_24khz", want "lc3_a127"
    codecName("Opus 05 Pro Audio") = "opus 05 pro audio", want "opus_05_pro"

The corrected rows are not in the tree, because a failing test cannot
land on `main`.

## What a fix has to decide

Where does the operator get the name of each codec that a speaker
offers, so that a claim can ask for a codec by the name that the
`codec` attribute shows? One option keeps a table from PipeWire's
codec ids or descriptions to its names, which must follow each
PipeWire release. Another reads the names from a field that PipeWire
publishes in the spelling of `api.bluez5.codec`, if one exists:
`PropInfo` holds only the codec ids and descriptions. Replacing spaces
as `codecName` replaces dashes corrects aptX HD and "aptX-LL mSBC"
only. The test case
changes with each fix.
