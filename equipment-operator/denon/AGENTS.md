# Working on denon

This package is the driver for the Denon and Marantz control
protocol. It implements `equipment.Driver`.

## The Denon protocol

A Denon or Marantz receiver listens for ASCII text on TCP port 23.
Each command and each event ends with a carriage return. The commands
that this operator sends, and the lines it reads back, come from
these sources:

- Denon's published documents under
  [assets.denon.com](https://assets.denon.com). The header of
  `protocol.go` cites
  [Ver.8.6.0 for the AVR-1713/AVR-1613](https://assets.denon.com/documentmaster/uk/avr1713_avr1613_protocol_v860.pdf),
  which has the base command set. The
  [AVR-X4000 document, Ver.03](<http://assets.denon.com/DocumentMaster/us/AVRX4000_PROTOCOL(10%203%200)_V03.pdf>)
  is newer. It adds the `ZM`, `Z2`, and `Z3` commands, which the
  older document does not have.
- [ol-iver/denonavr](https://github.com/ol-iver/denonavr), the Python
  library that Home Assistant uses. Its
  [const.py](https://raw.githubusercontent.com/ol-iver/denonavr/main/denonavr/const.py)
  lists the commands and the event prefixes for current models,
  including the zones, the tuner, and the network player.
- Home Assistant's
  [Denon AVR integration](https://www.home-assistant.io/integrations/denonavr/),
  which lists the models that each behavior has been tested on.

`MVMAX` is not in Denon's documents. An AVR-X1700H sends it next to
every `MV` line. The driver reports it as the zone's `volumeMax`. The
operator does not act on it, because the limit changes with the
volume.

The driver reports volume in half steps, two for each unit on the
receiver's display.

The driver also reports the address that it used to reach the
receiver. That is the remote address of the open connection. Until a
dial connects, it is the declared address, resolved before the dial.
The controller writes it to
`status.address`.

## The state and the snapshot

The driver reads the receiver into its own state. The state has one
entry per zone, the settings for the whole unit, the tone and
Audyssey settings, the audio settings, the HDMI setup, and the
channel volumes. `equipment.State` contains the reachability and the
zones, which every driver reports. `ProtocolStatus` contains the
rest. The controller writes it to `status.denon`, in this shape (the
values are examples):

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

The tone and channel values are in the units the display shows, so
the wire value 50 appears as 0. The LFE level is in negative
decibels. The power is `On` or `Standby`. Every power value that this
operator reads and writes uses these same PascalCase words.

The main zone's power comes from `ZM` when a model reports it, and
from `PW` otherwise. A model with a second zone reports it with `Z2`
lines. The driver reports that zone only after the receiver sends
one of those lines.

`TestTheAVRX1700HTranscriptFoldsIntoItsState` reads the lines of a
transcript from a real receiver, `testdata/avr-x1700h.txt`, and
checks the state that they produce.

## The HDMI setup

The settings of the receiver's Video > HDMI Setup menu are available
on port 23, in two command families. `VSAUDIO` is HDMI Audio Out, and
Denon's documents list it. The `SSHOS` family covers the rest of the
menu, and no Denon document lists it. An AVR-X1700H replies to
`SSHOS ?` with one line for each key, and then a closing `SSHOS END`
line. The driver sends `VSAUDIO ?` and `SSHOS ?` each time it
connects.

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
that the receiver reports. The receiver accepted `SSHOSCONSTS LAS` and
sent no line back. A later `SSHOS ?` reported `LAS`. The value was
already `LAS`, so this test does not show whether the receiver echoes
a change. After the driver sends a command in either family, it sends
`SSHOS ?` or `VSAUDIO ?`, so the status shows the value that the
receiver reports in either case.

Where each word comes from:

- Measured on an AVR-X1700H on a home cluster on 2026-09-26: every
  key above, read with `VSAUDIO ?` and `SSHOS ?`, and the words `AMP`,
  `LAS`, `POS`, `ALL`, `ON`, and `OFF`. The only set command sent to
  the receiver was `SSHOSCONSTS LAS`, which set it to the value it
  already had.
- Denon's documents: `VSAUDIO AMP` and `VSAUDIO TV`, in the AVR-X4000
  document and the FY21 and FY23 protocol spreadsheets.
- The receiver's web setup page, in its `VideoSettings.js`: the sets
  of values. Pass Through Source is Last, HDMI1 to HDMI7, or Front. RC
  Source Select is `POS` or `SSO`, the same words as the port-23 line.
  Power Off Control is All, Video, or Off.
- A forum post that quotes a Marantz receiver's `SSHOSCONSTS HD3`
  line: the `HD` words for the HDMI jacks.
- Not measured and not documented: `SSO`, `VID`, and `HD1` to `HD7`.
  `VID` follows the three-letter form of every other word in the
  family. Front is left out, because no source shows its word.

On the measured receiver, the web setup page and port 23 disagree
about ARC. The page shows ARC as on and does not let a person change
it. `SSHOSCONARC` reports `OFF`.

## The model

No command on port 23 returns the model. So the driver reads the
model from the AIOS UPnP description at
`http://<address>:60006/upnp/desc/aios_device/aios_device.xml`. It
reads it once per connection, and tries again until one read
succeeds. It writes the `modelName` to `status.model` and the
`manufacturer` to `status.manufacturer`. denonavr's `const.py` lists
that port and path for the AVR-X 2016 generation. For older
receivers it lists `/description.xml` on port 8080, which the driver
does not read. Not measured on the AVR-X1700H: whether its
`modelName` starts with a prefix such as `Denon`.

## What is not read

The parser ignores these parts of the protocol:

- the tuner (`TF`, `TM`, `TP`)
- the network player (`NS`, `NSA`, `NSE`)
- the video controls (`VSASP`, `VSMONI`)
- the trigger outputs (`TR`)
- the speaker presets, except the preset number
- the `OPINF` capability bitmaps

The measured AVR-X1700H does not reply over port 23 to any of the
tuner, network player, video, and trigger queries. So no real
transcript can prove a parser for them.

Plan 15 covers the speaker setup. Plan 17 covers the inputs, the
zones, the video controls, the triggers, and the receiver's HTTP and
HTTPS setup interfaces.
