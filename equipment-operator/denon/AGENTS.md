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
steps, two per display unit. It reports the address it reached the
receiver on, which is the peer a connection answered from, or the
declared address resolved to one when no connection stands. The
controller writes it at `status.address`.

## The state and the snapshot

The driver reads the receiver into its own state: one entry per zone,
the unit-wide settings, the tone and Audyssey settings, the audio
settings, the HDMI setup, and the channel volumes. `equipment.State`
carries the reachability verdict and the zones, which every driver
reports.
`ProtocolStatus` carries the rest, and the controller writes it under
`status.denon`.

The snapshot shape, which the driver's transcript test pins:

```json
{
  "system": {"power": "On", "eco": "auto", "dimmer": "bright", "autoStandby": "off", "speakerPreset": 1, "audioInputMode": "hdmi", "videoSelect": "off", "bluetoothTransmitter": "off", "bluetoothOutput": "speakers"},
  "tone": {"control": false, "bass": 0, "treble": 0},
  "audyssey": {"multeq": "reference", "dynamicEq": true, "referenceLevelOffset": 0, "dynamicVolume": "off", "loudnessManagement": true},
  "audio": {"drc": "off", "lfe": 0, "effect": 0, "delay": 0, "audioDelay": 0, "subwoofer": true, "restorer": "off", "graphicEq": "off", "headphoneEq": "off", "speakerVirtualizer": true, "dialogEnhancer": "off"},
  "hdmi": {"audioOut": "avr", "passThrough": true, "passThroughSource": "last", "rcSourceSelect": "powerOnAndSource", "control": true, "arc": false, "tvAudioSwitching": false, "powerOffControl": "all", "powerSaving": false},
  "channelVolumes": {"FL": 0, "FR": 0, "C": 0, "SW": 0}
}
```

The tone and channel values are in display units, so the wire's 50
reads as 0. The LFE level is negative decibels. The power is `On` or
`Standby`, the same PascalCase words as every power this operator
reads and writes.

Main-zone power comes from `ZM` when a model reports it, and from `PW`
otherwise. A model that has a second zone names it with `Z2` lines, and
the driver reports that zone only once the receiver has named it.

## The HDMI setup

The receiver's Video > HDMI Setup menu is on port 23 in two command
families. `VSAUDIO` is HDMI Audio Out, and Denon's documents list it.
The `SSHOS` family holds the rest, and no Denon document lists it.
The house's AVR-X1700H answers `SSHOS ?` with one line per key and a
closing `SSHOS END`. The driver sends `VSAUDIO ?` and `SSHOS ?` on
every connect.

| Field | Key | Words |
|---|---|---|
| `audioOut` | `VSAUDIO` | `AMP` is `avr`, `TV` is `tv` |
| `passThrough` | `SSHOSPAS` | `ON`, `OFF` |
| `passThroughSource` | `SSHOSCONSTS` | `LAS` is `last`, `HD1` to `HD7` are `hdmi1` to `hdmi7` |
| `rcSourceSelect` | `SSHOSRSS` | `POS` is `powerOnAndSource`, `SSO` is `sourceSelectOnly` |
| `control` | `SSHOSCON` | `ON`, `OFF` |
| `arc` | `SSHOSCONARC` | `ON`, `OFF` |
| `tvAudioSwitching` | `SSHOSTAS` | `ON`, `OFF` |
| `powerOffControl` | `SSHOSCONPOF` | `ALL` is `all`, `VID` is `video`, `OFF` is `off` |
| `powerSaving` | `SSHOSCONPSV` | `ON`, `OFF` |

A set command is the key, a space, and the word, the same as the line
the receiver reports. The receiver took `SSHOSCONSTS LAS` and sent no
line, and a later `SSHOS ?` reported `LAS`. The value did not change,
so the test does not show whether the receiver echoes a change. The
driver sends `SSHOS ?` or `VSAUDIO ?` after it sends a command in that
family, so the status reads the value back in either case.

The sources for each word:

- Measured on the house's AVR-X1700H on 2026-09-26: every key above,
  read with `VSAUDIO ?` and `SSHOS ?`, and the words `AMP`, `LAS`,
  `POS`, `ALL`, `ON`, and `OFF`. The only set command sent to the
  receiver was `SSHOSCONSTS LAS`, to its value at the time.
- Denon's documents: `VSAUDIO AMP` and `VSAUDIO TV`, in the AVR-X4000
  document and the FY21 and FY23 protocol spreadsheets.
- The receiver's web setup page, in its `VideoSettings.js`: the value
  sets. Pass Through Source is Last, HDMI1 to HDMI7, or Front. RC
  Source Select is `POS` or `SSO`, the same words as the port-23 line.
  Power Off Control is All, Video, or Off.
- A forum post that quotes a Marantz receiver's `SSHOSCONSTS HD3`
  line: the `HD` jack words.
- Not measured and not documented: `SSO`, `VID`, and `HD1` to `HD7`.
  `VID` follows the three-letter form of every other word in the
  family. Front is left out, because no source gives its word.

The web setup page and port 23 disagree on ARC on the house's
receiver. The page reports ARC as on and does not let a person change
it, and `SSHOSCONARC` reports `OFF`.

## What is not read

The parser ignores the tuner (`TF`, `TM`, `TP`), the network player
(`NS`, `NSA`, `NSE`), the video controls (`VSASP`, `VSMONI`), the
trigger outputs (`TR`), the speaker presets beyond the number, and the
`OPINF` capability bitmaps. The house's AVR-X1700H answers none of the
first four over port 23, so there is no way to prove a parser for them
here.

Plan 04 holds the four families as future work. Plan 07 records the
receiver's second interface, where the video controls answer.
