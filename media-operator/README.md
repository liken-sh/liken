# media-operator

`media-operator` plays films, music, and series on the screens and
speakers of a [`liken`](https://github.com/liken-sh/liken) cluster, and
you control it with Kubernetes resources. The
[`display-operator`](https://liken.sh/display/),
[`audio-operator`](https://liken.sh/audio/), and
[`bluetooth-operator`](https://liken.sh/bluetooth/) publish each
display, speaker, and controller as a device that a pod can claim.
`media-operator` groups those devices into units and runs playback on
them.

The API has five resources:

- A `Player` is one unit of equipment in one place, such as a lone
  speaker, a TV with its surround pair, or a gaming TV with its
  controllers.
- A `Play` is one run of media on a `Player`, such as a film, an album,
  or a season of episodes in order. It works like a `Job`: create it to
  start playback and delete it to stop. `kubectl get plays` lists what
  is playing.
- A `Remote` is one physical controller, bound to the `Player`s that it
  controls.
- A `Keymap` corrects the key names of one controller model that
  reports the wrong ones. A model that reports the right names needs
  none.
- `MediaPreferences` sets the cluster-wide default languages for audio
  and subtitles.

For each `Play`, the operator creates one playback pod on the machine
that has the hardware, with the device claims, tolerations, and
sockets that the `Player` spec calls for. The pod runs `mpv`, a sidecar
that controls `mpv` and connects to the message bus, and a sidecar that
draws the on-screen display. Each `Remote` has its own pod, which
publishes each button press to the message bus under the kernel's name
for the key. Each program that reads the presses maps those names to
its own actions.

A `Play` gives its media by URI. An `https://` URI streams the media.
An `nfs://` URI mounts an NFS export. A
`claim://<namespace>/<claim>/<path>` URI mounts a
`PersistentVolumeClaim`, which must be in the `Play`'s own namespace.
A `pattern://` URI plays a test pattern from the player image.
`media-operator` does not manage media libraries.

The manual is at [liken.sh/media](https://liken.sh/media/). It has the
install guide, a reference for each resource, and the message bus that
the operator's pods and your own programs share.
[`plans/00-design.md`](plans/00-design.md) is the design: the
resources, the alternatives that were rejected, and the questions that
are still open. [`plans/`](plans/) holds the plans that built the
operator and the plans that are still open.
