# The ELD carries no serial number

Open problem. An audio device says which model of monitor it plays
into. It cannot say which unit.

## Where the identity comes from

Monitor identity for an audio output comes from the ELD block, which
the graphics driver writes into the audio driver when a monitor
connects. The kernel prints the whole block at
`snd_hdmi_print_eld_info` in `sound/pci/hda/hda_eld.c`. The fields are
`monitor_present`, `eld_valid`, `codec_pin_nid`, `codec_dev_id`,
`codec_cvt_nid`, `monitor_name`, `connection_type`, `eld_version`,
`edid_version`, `manufacture_id`, `product_id`, `port_id`,
`support_hdcp`, `support_ai`, `audio_sync_delay`, `speakers`,
`sad_count`, and the audio descriptors. There is no EDID serial number
among them.

The operator builds `monitor.liken.sh/id` from three of those fields:
the manufacturer code, the product code, and the monitor name. All
three describe a model.

## What it costs

Two identical monitors on one machine carry the same
`monitor.liken.sh/id`. A claim that pairs a screen with that screen's
speakers uses a `matchAttribute` constraint over that attribute, and a
constraint over one value is satisfied by either pairing. So a claim
can get one screen and the other screen's speakers, and nothing in the
cluster reports the swap.

The two operators can also disagree about how specific an identity is.
The display operator reads the EDID directly, so it has the serial
number. This operator has no serial number to read. Both publish one
attribute name under one domain, and the values must be identical byte
for byte, so the identity stays as coarse as the coarser of the two
sides.

## The candidate

The ELD's `port_id` field is the candidate tiebreak. Nobody measured
it. Two questions are open. The first is whether `port_id` differs
between two outputs of one card. The second is whether it corresponds
to the DRM connector the display operator names, because a value that
only this operator can read pairs nothing. This document names the
candidate and does not pick it.

## What a claim can do today

A claim selects on the `card` and `pcm` attributes when the model name
is not enough:

    device.attributes["audio.liken.sh"].card == 0 &&
    device.attributes["audio.liken.sh"].pcm == 8

That names one output of one card. It does not survive a cable that
moves between the card's outputs, which is the case the pairing
attribute exists for.
