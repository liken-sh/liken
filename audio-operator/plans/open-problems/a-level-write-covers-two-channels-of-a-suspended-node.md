# A level write covers two channels of a suspended node

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
The code was checked on 2026-11-19: `channelCount` at
`audio-operator/volume.go:146` returns 2 for an empty
`channelVolumes`. No unit test can show the defect, because it
depends on what PipeWire prints for a real suspended node with more
than 2 channels, and no fixture of one exists. A hand-made graph would
test only the fallback that the comment on `stereoChannels` already
states.

`channelCount` in `audio-operator/volume.go` returns the number of
channels that a level write sets. It returns the length of the node's
`channelVolumes`, and it returns `stereoChannels`, which is 2, when
that list is empty. Its comment says that a suspended node can print
an empty `channelVolumes`.

An ALSA node with more channels than 2, such as an HDMI output with a
5.1 layout, then gets a write for 2 channels when it is suspended. The
comment on `stereoChannels` states the result: the other channels keep
their old level. So a declared level, a mute, or a volume request that
arrives while the node is suspended can set the front pair and leave
the other channels where they were.

## What would confirm it

The `pw-dump` output of a suspended ALSA node with a layout of more
than 2 channels, to show whether `channelVolumes` is empty there. If
it is, a level write to that node followed by a second `pw-dump` after
the node runs again shows which channels changed.

## What a fix has to decide

Where the channel count comes from when `channelVolumes` is empty. The
operator declares each node's layout in the PipeWire drop-in, so the
count is known without the graph.
