# media-operator

The routing and control of media playback in a cluster, expressed
as Kubernetes resources. It runs on a
[`liken`](https://github.com/liken-sh/liken) cluster above the
hardware operators: the
[`display-operator`](https://display.liken.sh),
[`audio-operator`](https://audio.liken.sh), and
[`bluetooth-operator`](https://bluetooth.liken.sh) publish each
display, speaker, and controller as a claimable device, and this
operator declares what those devices form together.

The API is five resources. A `Player` is one unit of
equipment in one place: a lone speaker, a TV with its surround
pair, a gaming TV with its controllers. A `Play` is one run of
media on a player, with a lifecycle analogous to a `Job`: a film,
an album, or a season of episodes, played in order and run to
completion.
Create it to start, delete it to stop, and `kubectl get plays`
lists what plays right now. A
`Remote` is one physical controller, bound to the players it
drives. A `Keymap` maps one controller model's buttons to named
media actions. A fifth resource, `MediaPreferences`, holds one
cluster-wide default for audio and subtitle languages.

The operator reconciles a `Play` into one playback pod beside the
hardware, running `mpv` under a thin supervisor, with every device
claim, toleration, and socket built from the `Player` spec. Each
`Remote` runs as a standing pod that publishes button events to a
message bus, and the playback pod applies the bindings. Media
arrives by URI: `https://` streams, and `nfs://` mounts. Media
library management is a separate concern, and this project does
none of it.

The manual is at [media.liken.sh](https://media.liken.sh/): the
install guide, the reference for each resource, and the message bus
every pod and any of your own programs share.
[`plans/00-design.md`](plans/00-design.md) is the design: the
resources, what was considered and set aside, and what the design
still owes an answer to. [`plans/README.md`](plans/README.md)
indexes the plans that built it and the ones still open.
