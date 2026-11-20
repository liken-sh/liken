# Working on audio-operator

`audio-operator` is a Go Kubernetes operator and a DRA driver with the
driver name `audio.liken.sh`. `README.md` says what it is for and
describes its kinds.

## How it runs

The `audio-operator` `DaemonSet` runs one pod on each node, except a
node with the label `audio.liken.sh/sound-card: none`. The pod claims
its devices through the `ResourceClaimTemplate` `every-sound-card`,
which uses the `DeviceClass` `sound-card`. That class matches every
device on the node whose attribute `sound.liken.sh/supportsSound` is
true. Those devices are each sound card that `liken` publishes, and
the media bus that `bluetooth-operator` publishes for the Bluetooth
radio. Every container of the pod runs the same image.

- `declare` is an init container. It writes PipeWire's node
  declarations for the claimed cards. When the claim includes a media
  bus, it also writes the WirePlumber fragment that turns on the
  Bluetooth monitor.
- `pipewire` and `wireplumber` are native sidecars. PipeWire opens the
  PCM devices of the cards. WirePlumber registers the A2DP endpoint
  with `bluetoothd` over the media bus.
- `operator` reads the PipeWire graph with `pw-dump -m`, the control
  devices of the cards, and the list of paired devices from BlueZ. It
  serves DRA to the kubelet. It writes the `ResourceSlice`
  `<node>-audio.liken.sh`, the CDI spec that gives a consumer pod the
  PipeWire socket, and the `status` of each `Sink` and `Source` on its
  node. It applies each `spec` to PipeWire and to the card's controls.
- `capture` runs `pw-record` on the pod's PipeWire socket for each
  tap, which is one capture request. It encodes the sound and streams
  it.

The `audio-api` `Deployment` serves the capture API for the whole
cluster. For each request, it checks authentication and
authorization, reads `status.node` of the `Sink` or `Source` to find
its node, and forwards the request to the `capture` container on that
node. It posts a `Captured` `Event` on the `Sink` or `Source` for each
tap. Neither `audio-api` nor `capture` stores any sound.

## Design

The operator publishes what the hardware can do, and it leaves each
choice to a claim or to a `spec`. An endpoint that can't play stays
published, with a taint that says why. If the operator removed the
device instead, the kubelet would retry each claim on it with no
limit. For the same reason, a failed read from PipeWire or BlueZ
changes taints, not the list of devices. The operator writes only the
settings that a `Sink` or a `Source` declares.

The operator talks to `liken`, `display-operator`, and
`bluetooth-operator` only through claims and device attributes. When
it needs a new fact about a card or a radio, the driver that owns the
device publishes that fact as an attribute. `media-operator` owns
`status.session` of a `Sink`, and this operator writes the rest of
`status`.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- The reference pages `docs/content/docs/reference/sinks.md`,
  `sources.md`, and `routes.md` are generated from `deploy/crds.yaml`,
  the text in `docs/crdref/` and `docs/apiref/`, and the OpenAPI
  document. `make -C docs generate` regenerates them, and
  `make -C docs skills` regenerates `skills/` from the guides.
  `make test` fails when either is out of date.
- `make openapi` writes `docs/static/v1/audio/openapi.json` from the
  route table. A test fails when the committed file differs from it.
- `smoke/audio-operator.sh` checks that the image contains every file
  that PipeWire and WirePlumber load by name. `ldd` doesn't list these
  files.
- The ELD fixtures in `testdata/` follow the layout that the kernel's
  `hda_eld.c` writes. They exist because a machine with no monitor
  attached reports no ELD that you could capture.
