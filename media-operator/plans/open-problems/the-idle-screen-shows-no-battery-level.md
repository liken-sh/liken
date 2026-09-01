# The idle screen shows no battery level

Open problem. The idle screen lists the unit's parts by name, and shows
each controller as connected or not. A remote and a gamepad run on
batteries, and nothing on the screen says how much charge either has.
The `bluetooth-operator`'s open problem, battery levels are not
reported, covers the read: the operator reads no level today, and the
media bus is where one would arrive.

Once a level is on the bus, two things here change:

* `Remote.status` folds the level of the device it names, so
  `kubectl get remotes` answers the question without a screen. The
  standing remote pod already reads the device's presence off the bus,
  and the level travels beside it.
* The idle screen draws the level beside the part's name, for every
  part that reports one. A part that reports none draws as it does
  today. The retained `Player` status carries each part's `connected`
  flag to the screen now, and a level is one more field on the same
  component.

The threshold for a low-charge warning, and where such a warning
shows, are open. The audio side is `audio-operator`'s: a `Sink` on a
battery reports its level on the same terms.
