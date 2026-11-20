# media-operator

`media-operator` plays films, music, and series on the screens and
speakers of a [`liken`](https://github.com/liken-sh/liken) cluster, and
you control it with Kubernetes resources. The
[`display-operator`](https://liken.sh/display/),
[`audio-operator`](https://liken.sh/audio/), and
[`bluetooth-operator`](https://liken.sh/bluetooth/) publish each
display, speaker, and controller as a device that a pod can claim.
`media-operator` groups those devices into `Player`s and runs
playback on them.

A `Play` lists its media by URI. An `https://` URI streams the media.
An `nfs://` URI mounts an NFS export. A
`claim://<namespace>/<claim>/<path>` URI mounts a
`PersistentVolumeClaim`, which must be in the `Play`'s own namespace.
A `pattern://` URI plays a test pattern from the player image.
`media-operator` does not manage media libraries.

The operator is installed with its message bus, a Mosquitto broker
that the operator and every pod it creates connect to over MQTT. Your
own programs, such as a Home Assistant integration, can read and
publish the same topics. When a `Player`'s screen is connected to an
A/V receiver that [`equipment-operator`](https://liken.sh/equipment/)
controls, the operator writes the `Player`'s session into the
`Receiver`'s `status.session`, and `equipment-operator` sets the
receiver's power, input, and volume. You can create each `Play` by
hand, or let [`library-operator`](https://liken.sh/library/) draw a
media browser on a `Player`'s screen and create the `Play` that a
person picks.

The operator defines these resources:

- A `Player` is the screens, speakers, and controllers in one place
  that play together, such as a lone speaker, a TV with its surround
  pair, or a gaming TV with its controllers.
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
its own actions. A `DaemonSet` of the operator asks the media driver
of each GPU what it can decode, encode, and scale. It publishes those
facts as `media.liken.sh` devices, so a claim can select, for
example, a GPU that decodes 10-bit HEVC.

This `Play` plays a film from an NFS export on the `Player` named
`studio`, from ten minutes in:

```yaml
apiVersion: media.liken.sh/v1alpha1
kind: Play
metadata:
  name: nosferatu
  namespace: media
spec:
  players: [studio]
  items:
    - uri: nfs://nas/media/movies/Nosferatu (1922)/Nosferatu.mkv
  start: "0:10:00"
```

The manual is at [liken.sh/media](https://liken.sh/media/). It has the
install guide, a reference for each resource, and a reference for the
message bus that the operator's pods and your own programs share.
[`plans/00-design.md`](plans/00-design.md) is the design: the
resources, the alternatives that were rejected, and the questions that
are still open.

MIT. See [LICENSE](LICENSE).
