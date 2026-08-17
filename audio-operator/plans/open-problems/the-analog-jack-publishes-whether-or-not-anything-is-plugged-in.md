# The analog jack publishes whether or not anything is plugged in

Open problem. Every HDA controller has an analog output, and most
machines have nothing in the socket. The operator publishes that
output as a device either way, so a slice carries something no claim
will ever take.

## What the code does

`alsa.go` sorts a playback PCM device by whether it has an ELD
element. An output that has one drives HDMI or DisplayPort. An output
that has none is the analog jack, and `connector` returns the literal
string `analog` for it. Nothing reads the jack detection state, and
nothing keeps the device out of the slice when the socket is empty.

An HDMI output that nobody plugged a monitor into is a different case
and it is already handled: the port is real hardware that is present
and idle, so it publishes with `audio.liken.sh/disconnected` and
`audio.liken.sh/no-monitor` and a claim parks instead of getting
silence. The analog jack has no equivalent signal in the slice.

## What is unmeasured

On liken-1 the pinned PCI controller published four outputs on
2026-08-17, `card0-pcm3`, `card0-pcm7`, `card0-pcm8`, and
`card0-pcm9`, and every one of them had an ELD element. No analog
device appeared at all. The machine's other card is USB and the
controller claim excludes it, which is
[The claim takes any sound card, and a node serves only one](the-claim-takes-any-sound-card-and-a-node-serves-only-one.md).

So the cost this question describes has not been seen on the one
machine that runs the operator. What it would cost on a machine with
an analog codec is still a guess.

## The fix that was named, and what it costs

The HDA jack detection nodes report whether something is plugged in,
so the operator could publish the analog output only when the socket
is occupied. That trades a device nobody claims for a device that
appears and disappears with a cable, and a device that disappears is
the case the whole taint design exists to avoid: a claimed device must
never leave a slice, so an unplugged jack would have to taint rather
than vanish, exactly as a dark HDMI connector does now.

Following that reasoning to its end, the answer may be that the analog
jack should publish always and carry the same two taints when nothing
is plugged in, which makes this a missing taint rather than an extra
device. That reading has not been checked against what the jack
detection nodes actually report on a machine with an analog codec,
and there is no such machine in the fleet to check it on.
