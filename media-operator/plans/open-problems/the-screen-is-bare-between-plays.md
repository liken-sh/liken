# The screen is bare between plays

The liken display lives inside the playback pod. It is an `mpv` script,
and it draws only while a `Play` runs. Between plays there is no pod and
no script, so the machine's screen shows whatever its compositor shows
with no client, a bare background. A `Player` with no `Play` is dark.

This is the gap between two states with nothing in the middle. A `Play`
starts, and the screen is the film with the liken display over it. The
`Play` ends, and the screen is nothing. A person who walks up to an idle
`Player` sees no clock, no room name, no now-playing from another room,
and no sign the `Player` is powered and ready.

An idle screen would fill the gap: a standing surface that draws status
while no `Play` runs, and gives up the screen the moment a playback pod
is ready to draw. What it shows is open, and the display already holds
the pieces. The clock, the household's zone, and the art bridge that
decodes a logo are in hand. A now-playing from another room, the time,
and the `Player`'s own name are the natural first contents.

The shape has precedent in this operator. The remote reader is a
standing pod, one per machine, that lives between plays and hands its
input to each playback pod in turn. An idle display is the same shape
for the screen: a standing per-`Player` renderer that owns the screen
when no `Play` does, and gives it up when a `Play`'s `mpv` starts. The
two could even be one standing pod, because both are per-machine and
per-`Player`.

The handoff is the hard part, and it is why this is a problem and not a
plan. Two Wayland clients want one screen: the idle renderer and the
playback pod's `mpv`. The idle one must give up the screen with no black
flash as the film's first frame arrives, and draw again cleanly when the
`Play` ends. Who owns the compositor, how the two order the handoff, and
whether the idle renderer is `mpv` again or a smaller surface are the
questions this owes an answer to.
