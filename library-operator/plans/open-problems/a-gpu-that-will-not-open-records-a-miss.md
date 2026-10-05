# A GPU that will not open records a miss

The trickplay worker passes `-hwaccel vaapi` and the render node to
`ffmpeg` (`ffmpegSheets` in `trickplaysheets.go`). When ffmpeg cannot
open the render node, it exits with code 251 before it reads a frame:

    [AVHWDeviceContext @ 0x5e5d4ac6ea40] Failed to initialise VAAPI connection: -1 (unknown libva error).
    Device creation failed: -5.
    [vist#0:0/hevc @ 0x5e5d4ac63640] [dec:hevc @ 0x5e5d4ac80f00] No device available for decoder: device type vaapi needed for codec hevc.

`ffmpegRefused` reads any exit code as the file's own state, so
`stageTrickplay` in `trickplay.go` records `attemptNothing`. A miss
lasts thirty days. The same failure on every video makes the worker
record a miss for each one in seconds, and a walk does not reopen any
of them until the thirty days pass.

This happened on a cluster whose `trickplay` claim template asked for
any render node with `display-render`. A retried pod landed on a node
with an AMD GPU, and the Debian iHD driver in the image cannot open it.
In about forty seconds, the pod recorded 43 misses and one error across
five season folders. The fix on that cluster was a claim that pairs the
render node with a `media.liken.sh` device, as the `media` guide
"Claim a GPU by what it decodes" shows. The ledgers kept the misses,
and a person had to remove the entries by hand.

## Why it matters

The miss window assumes that a refused decode is a fact about the file.
A render node that the image cannot open is a fact about the pod, and
it makes every file fail the same way. The docs say that a codec the
GPU refuses falls back to software decoding. That fallback does not
apply here, because ffmpeg fails while it creates the device, before
it chooses a decoder.

## What a fix could look like

- **Open the device once before the list.** The worker can run one
  short ffmpeg or `vainfo` call on the render node when it starts. If
  the call fails, the worker logs the driver's error, decodes in
  software, or exits with an error and records nothing. The check
  costs one process for each pod.
- **Read the device failure as an error.** `stageTrickplay` can match
  the output of a failed run for ffmpeg's device-creation lines and
  record `attemptError`, so the one-day window applies. This depends
  on ffmpeg's message text, which can change between releases.
- **Stop after a run of identical failures.** The worker can exit
  after several consecutive videos fail with the same reason, so a
  bad placement costs a few attempts and not a list. The pod's failure
  then shows on the `Job`.
- **Fall back to software on a device error.** A second ffmpeg run
  without `-hwaccel` makes the tiles on any node, at the cost of CPU
  time.

## What is not known

- Whether other device failures, such as a render node that the pod
  holds but a driver that hangs, exit with a code or with a signal.
  A signal records `attemptError` now.
- Whether the appearances worker has the same gap. Its claim template
  asks for `decode-10bit`, so a render node that its image cannot open
  is less likely, but the code path was not checked.
