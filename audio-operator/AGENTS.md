# Working on audio-operator

`audio-operator` is a Go Kubernetes operator and a DRA driver with the
driver name `audio.liken.sh`. `README.md` says what it is for and
describes its kinds.

## How it runs

One `audio-operator` pod runs on each node that has a sound card. Its
claim takes every device whose attribute `sound.liken.sh/supportsSound`
is true: each sound card that `liken` publishes, and the media bus
that `bluetooth-operator` publishes for a Bluetooth radio. The media
bus is a D-Bus connection to the radio's `bluetoothd`. The comments in
`deploy/operator.yaml` give the order in which the containers start,
and the reason for it.

PipeWire owns the PCM devices of the cards. Every process that plays
or records, in the pod and in a consumer pod, connects to PipeWire's
socket. PipeWire reads its node declarations only when it loads its
configuration, so the `declare` init container writes them from the
claimed cards before PipeWire starts. WirePlumber owns the connection
to `bluetoothd` over the media bus, and registers the A2DP endpoint
there.

The `operator` container reads the PipeWire graph with `pw-dump -m`,
the control devices of the cards, and the paired devices from BlueZ.
It applies each `spec` to PipeWire and to the controls of the card.
It writes the `ResourceSlice` `<node>-audio.liken.sh`, the `status`
of each `Sink` and `Source` on its node, and the CDI spec that mounts
PipeWire's socket into a consumer pod. A consumer then plays through
PipeWire directly.

A capture request goes to the `audio-api` `Deployment`. `audio-api`
checks authentication and authorization, reads `status.node` of the
`Sink` or `Source`, and forwards the request to the `capture`
container on that node. `capture` records from PipeWire's socket with
`pw-record`, encodes the sound, and streams it back. `audio-api` posts
a `Captured` `Event` on the `Sink` or `Source` for each capture
request. Neither `audio-api` nor `capture` stores any
sound.

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

## Finding things

- The reference page of the routes is generated from the OpenAPI
  document, with text from `docs/apiref/`.
- `make openapi` writes `docs/static/v1/audio/openapi.json` from the
  route table. A test fails when the committed file differs from it.
- `smoke/audio-operator.sh` checks that the image contains every file
  that PipeWire and WirePlumber load by name. `ldd` doesn't list these
  files.
- The ELD fixtures in `testdata/` follow the layout that the kernel's
  `hda_eld.c` writes. They exist because a machine with no monitor
  attached reports no ELD that you could capture.
