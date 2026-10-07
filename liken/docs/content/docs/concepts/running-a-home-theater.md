---
title: Running a home theater
weight: 50
---

# Running a home theater

The home theater operators turn a machine that is connected to a
television into a media player that a household controls with a
remote. You declare each room's screen, speakers, and remote as
Kubernetes resources. The operators run the player, keep a catalog of
the household's films and series, and drive the receiver and
the television in the room.

For example, a room has a machine connected by HDMI to a receiver and
a television, a USB-CEC adapter plugged into a spare input of the
receiver, and a Bluetooth remote. With the television in standby, one press of the
remote's power button wakes the television over CEC, switches the
television and the receiver to the machine's input, and turns the
receiver on. The media browser shows the namespace's catalog on the
screen, and a press of a button starts a film. When the `Catalog`
names a Jellyfin server, each person's progress in each title stays
the same in Jellyfin and on the television, in both directions.

## The operators

[`media-operator`](https://liken.sh/media/) declares a `Player` for
each screen and its speakers, a `Play` for each title that plays, a
`Remote` for each controller, and a `Keymap` that maps the buttons of
a controller. It runs each `Play` as one pod with mpv, runs an idle
screen while nothing plays, and runs the message bus that connects
the remotes, the players, and your own programs. It also measures what
each GPU can decode and encode, and publishes the result as devices
of its own driver, `media.liken.sh`.

[`library-operator`](https://liken.sh/library/) declares a `Library`
for each directory of films or series, and scans each one into
a `Catalog`, a SQLite database that Corrosion copies to every screen
in the namespace. Its media browser takes the place of a `Player`'s
idle screen and starts a `Play` on that `Player`. A
`MetadataProvider` adds titles, plots, and art, which the operator
writes as `.nfo` files and art files beside the media.

[`people-operator`](https://liken.sh/people/) defines the `Person`
resource: one person in the household, with a name and a picture.
The media browser asks who is watching. `library-operator` records
those people on each `Play`, and keeps the progress in each title for
them.

[`equipment-operator`](https://liken.sh/equipment/) controls the
A/V equipment that a machine plays through. A `Receiver` turns an A/V
receiver on, selects its input, and sets its volume over the network.
A `CECBus` and a `Television` wake a TV and put it in standby over
HDMI-CEC, through a USB-CEC adapter.

## What they depend on

* A `Player` selects a monitor output from `display-operator`, an
  audio output from `audio-operator`, and a GPU render node from the
  operating system. A `Remote` selects a paired controller from
  `bluetooth-operator`. You write the device classes they name, as
  each device operator's manual shows.
* The capabilities agent of `media-operator` claims every GPU render
  node, through the class `media-render` that it ships.
* `equipment-operator` claims each USB-CEC adapter that the operating
  system publishes, through the class `cec-adapter` that it ships. It
  reads each `Display` of `display-operator`, and a `Television`
  reports the `Display` objects whose picture reaches that TV. It does
  not connect to the message bus.
* `library-operator` connects to the message bus of `media-operator`,
  so install `media-operator` first. It reads `Player` and `Person`
  objects, and creates each `Play`.

## Extension points

* The idle screen. A `Player`'s `spec.idle.controller` hands the
  screen to another program while nothing plays. The media browser of
  `library-operator` uses this contract, and a program of your own can
  use it too, as [Hand the idle screen to another
  controller](https://liken.sh/media/docs/guides/handing-the-idle-screen-to-another-controller/)
  shows.
* The message bus. The bus is MQTT. A program of your own, such as a
  phone app or a Home Assistant instance, connects as a plain MQTT
  client with no Kubernetes credentials. It can send commands to a
  `Play`, and ask a `Player` for a volume step or a mute. The
  [bus reference](https://liken.sh/media/docs/reference/bus/) lists
  each topic and who can write it.
* Play requests. `library-operator` answers play requests on the bus
  from any client, so a browser of another make gets the same
  service.
* The catalog. The catalog is SQLite, and the
  [catalog guide](https://liken.sh/library/docs/guides/catalog/) shows
  how to query it.
* Webhooks. Each `Library` serves a webhook, so an import in Radarr,
  Sonarr, or Jellyfin rescans one folder at once.
* The files on the volume. The `.nfo` files and the art are in the
  forms that Kodi and Jellyfin read, so those programs can read the
  same library.
* Jellyfin. `spec.jellyfin` on a `Catalog` keeps playback progress the
  same between `library-operator` and a Jellyfin server.
