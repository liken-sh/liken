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
