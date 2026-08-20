# The player image is still Debian

The operator image is one static binary on `scratch`. The player
image is not: it is `debian:stable-slim` plus `mpv`,
`intel-media-va-driver`, `libgl1-mesa-dri`, and `pipewire-bin`,
because `mpv` opens a wide dynamic closure at run time: the GL and
EGL stack, the VA-API driver, and the PipeWire client libraries,
which refuse to build a client context without
`/usr/share/pipewire/client.conf`.

The cost is the usual cost of a distribution base: hundreds of files
nothing here reads, a CVE feed for all of them, and an image nobody
has measured. The playback pod is also the least trusted process in
this system, which makes its image the one that most deserves a
floor this low.

The audio operator already walked the path out. Its plan 02, "A
closure on scratch", replaced the Debian base with a named file set
measured from the running daemons' memory maps, with a release gate
that starts the daemons and fails on any mapped file the image
lacks. The player image can take the same treatment, with one
complication the audio image did not have: `mpv` loads its VA-API
and GL drivers only when real media hits real hardware, so a
measurement on a GPU-less runner misses exactly the files the living
room needs. The measurement has to run on hardware, or those files
have to be asserted by name the way the audio gate asserts
`libspa-alsa.so`.

Nobody has decided what work this becomes, so it has no number.
