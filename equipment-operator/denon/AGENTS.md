# Working on denon

This directory holds the Denon protocol driver.

## The Denon protocol

A Denon or Marantz receiver listens for ASCII on TCP port 23. Each
command and each event ends with a carriage return. The commands this
operator sends, and the lines it reads back, come from these sources.

- Denon's published documents under
  [assets.denon.com](https://assets.denon.com). The header of
  `protocol.go` cites
  [Ver.8.6.0 for the AVR-1713/AVR-1613](https://assets.denon.com/documentmaster/uk/avr1713_avr1613_protocol_v860.pdf),
  which has the base command set. The
  [AVR-X4000 document, Ver.03](<http://assets.denon.com/DocumentMaster/us/AVRX4000_PROTOCOL(10%203%200)_V03.pdf>)
  is newer and adds the `ZM`, `Z2`, and `Z3` commands that the older
  document does not carry.
- [ol-iver/denonavr](https://github.com/ol-iver/denonavr) is the Python
  library Home Assistant uses. Its
  [const.py](https://raw.githubusercontent.com/ol-iver/denonavr/main/denonavr/const.py)
  lists the commands and the event prefixes for current models, including
  the zones, the tuner, and the network player.
- Home Assistant's
  [Denon AVR integration](https://www.home-assistant.io/integrations/denonavr/)
  names the models each behavior is proved on.

`MVMAX` is not in Denon's documents. A live AVR-X1700H sends it beside
every `MV` response, so the parser reads it and nothing acts on it.

The driver implements `equipment.Driver` and reports volume in half
steps, two per display unit.

## The state and the snapshot

The driver reads the receiver into its own state: one entry per zone,
the unit-wide settings, the tone and Audyssey settings, the audio
settings, and the channel volumes. `equipment.State` carries the
reachability verdict and the zones, which every driver reports.
`ProtocolStatus` carries the rest, and the controller writes it under
`status.denon`.

The snapshot shape, which the driver's transcript test pins:

```json
{
  "system": {"power": "on", "eco": "auto", "dimmer": "bright", "autoStandby": "off", "speakerPreset": 1, "audioInputMode": "hdmi", "videoSelect": "off", "bluetoothTransmitter": "off", "bluetoothOutput": "speakers"},
  "tone": {"control": false, "bass": 0, "treble": 0},
  "audyssey": {"multeq": "reference", "dynamicEq": true, "referenceLevelOffset": 0, "dynamicVolume": "off", "loudnessManagement": true},
  "audio": {"drc": "off", "lfe": 0, "effect": 0, "delay": 0, "audioDelay": 0, "subwoofer": true, "restorer": "off", "graphicEq": "off", "headphoneEq": "off", "speakerVirtualizer": true, "dialogEnhancer": "off"},
  "channelVolumes": {"FL": 0, "FR": 0, "C": 0, "SW": 0}
}
```

The tone and channel values are in display units, so the wire's 50
reads as 0. The LFE level is negative decibels.

Main-zone power comes from `ZM` when a model reports it, and from `PW`
otherwise. A model that has a second zone names it with `Z2` lines, and
the driver reports that zone only once the receiver has named it.

## What is not read

The parser ignores the tuner (`TF`, `TM`, `TP`), the network player
(`NS`, `NSA`, `NSE`), the video controls (`VSASP`, `VSMONI`), the
trigger outputs (`TR`), the speaker presets beyond the number, and the
`OPINF` capability bitmaps. The house's AVR-X1700H answers none of the
first four over port 23, so there is no way to prove a parser for them
here.

Plan 04 holds the four families as future work. Plan 07 records the
receiver's second interface, where the video controls answer.
