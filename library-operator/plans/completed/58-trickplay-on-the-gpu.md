# 58, Trickplay on the GPU

Built on 2026-09-11. The trickplay fact runs as a Job of its own,
claims the node's GPU through a `ResourceClaimTemplate` the Library
names, and decodes through VA-API on an image built from
display-operator's ffmpeg base. The operator image drops its static
ffmpeg.

## The problem

The trickplay fact ran as one init container in the enrich Job, in
software, at half a core: about twenty minutes for a two-hour film. A
media server that shares the volume extracts the same sheets on the
GPU, so it made every tile directory first, and the fact tiled almost
nothing. The fact also held the enrich Job for the length of each
decode, so a title's other facts waited behind it.

A static ffmpeg cannot load a VA-API driver, so the decode could not
move onto the GPU inside the operator's scratch image.

## The design

**A Job of its own.** The trickplay Job runs as worker `trickplay`,
named `<library>-trickplay-<walk>` from the walk it answers, with a
catalog agent and a catalog claim of its own, because the enricher's
claim is ReadWriteOnce and two Jobs cannot share it. It schedules when
the fact is on, the trickplay gap is open, a walk has finished, and no
trickplay Job of the Library is unfinished. The enricher's own gap
count leaves the trickplay gap out, and neither Job waits on the
other. A webhook's chain gains a trickplay stage beside its enrich
stage, and the rescan waits for both, so a new title's tiles start the
moment its folder is scanned. A chain parked on a long decode holds
only itself, never the Library's standing enricher.

**The render claim.** `spec.trickplay.render` names a `class` and an
optional CEL `selector`, the shape a Player's render device has. Set,
the operator keeps one `ResourceClaimTemplate` per Library, owned by
it, with one request for exactly one device of the class, and the
Job's pod names the template so the kubelet mints a claim per pod. A
template's spec is immutable, so a changed block is a delete and a
create. Unset, the Job carries no claim and decodes in software. Set on
a cluster with no such device, the pod stays Pending and the events
say why; the operator does not probe for hardware.

**The decode.** With a render node under `/dev/dri`, ffmpeg takes
`-hwaccel vaapi -hwaccel_device <node>` and nothing else changes: the
frames download to memory, the fps, scale, and tile filters stay in
software, the JPEG encode stays in software, and a codec the GPU
refuses falls back to software decoding on its own. With a claim the
container sees only the allocated node, so the first node is the right
one.

**The images.** The two facts that open a media file, probe and
trickplay, run on a fourth companion image, `library-operator-ffmpeg`:
the operator binary on `ghcr.io/liken-sh/ffmpeg`, display-operator's
[plan 19](https://github.com/liken-sh/display-operator/blob/main/plans/completed/19-the-vaapi-and-ffmpeg-images.md).
The operator image is the binary and the CA bundle alone, 21 MB where
it was 155 MB with the static ffmpeg. The image derives from the
operator's tag like the other three, and `FFMPEG_IMAGE` overrides it.

**What is left open.** A chain whose scan opened no other gap creates
no trickplay stage, so a title that needs only tiles waits for the
standing Job. The standing Job's only guard against a re-run is its
name, so a Library whose walk is slower than the Job's TTL can re-run
a filled gap, which costs a pod and no decode. The image pins the
ffmpeg base by tag and needs a bump when display-operator moves it.

## How the work is proved

Tests cover the Job's shape, the template for a class alone and with a
selector, the pod's claim and its absence, the delete-and-create on a
changed block, the schedule beside the enricher, the chain's stage and
the rescan's wait, and the ffmpeg arguments with and without a render
node. On a workstation with an Intel GPU the real command line decoded
a 1080p clip through the iHD driver, capped at half a core: 47 s
against 98 s in software.

The drill on `liken-1` is owed: roll the release, name the
`display-render` class on a Library, remove the tile directory from a
few titles, and time the rebuild against the twenty-minute software
figure.
