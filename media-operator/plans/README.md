# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows liken's own `plans/`. A document states a problem,
states the design that answers it, and states what was considered and
set aside. It also states how the work was proved, and a proof runs on
hardware. The pattern is documented in liken's repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

The README states what the operator is. These documents state why it
is built the way it is, and what it still owes an answer to.

## Designs

* [00, The media-operator design](00-design.md). The founding
  design: the `Player`, `Play`, `Remote`, and `Keymap` resources,
  the playback pod, the input bus, and the carriage layer that
  comes later.
* [01, A play becomes a pod](01-a-play-becomes-a-pod.md). Built, and
  drilled on `liken-1` on 2026-08-20 in release 2026.08.20-001. The
  first slice: `Player` and `Play`, the operator, the player image,
  and the repository scaffolding. A film played from a `kubectl
  create`, its position advanced in `kubectl get plays`, and the
  delete tore everything down through the owner references.
* [02, A remote drives the play](02-a-remote-drives-the-play.md).
  Built, and drilled on `liken-1` on 2026-08-20 in release
  2026.08.20-004. `Remote` and `Keymap`, with the reader as a
  sidecar in the playback pod: the smallest proof of input, taken
  knowingly ahead of the bus. A DualSense paused a film and
  `status.paused` followed within one report.
* [03, The bus carries input and reports](03-the-bus-carries-input-and-reports.md).
  Designed, not built. Mosquitto as the message bus, the remote
  reader moved to its own standing pod, and the playback pod rebuilt
  as `mpv` with one bus-bridge sidecar and no supervisor. The report
  moves off plain HTTP and onto the bus. It retires plan 02's sidecar
  and the plan-01 supervisor, and closes the plain-HTTP open problem
  when it drills.

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody
has decided yet what work they become.

* [The player image is still Debian](open-problems/the-player-image-is-still-debian.md).
  The operator image is one binary on `scratch`; the player image is
  a distribution base, because `mpv`'s runtime closure is wide. The
  audio operator's closure-on-scratch treatment applies, with the
  complication that `mpv` loads its GPU drivers only on real
  hardware.
* [The playback pod reports over plain HTTP](open-problems/the-playback-pod-reports-over-plain-http.md).
  The supervisor POSTs its status to an HTTP endpoint the operator
  serves, proven by a token held in memory. The trust boundary that
  keeps the pod credential-free should stay; the transport should
  move onto the input plane's message bus when that plan lands. Plan
  03 closes this when it drills.
* [The broker is always in-cluster](open-problems/the-broker-is-always-in-cluster.md).
  Plan 03 stands up its own MQTT broker. A home that already runs one,
  for Home Assistant or zigbee2mqtt, should be able to point the
  operator at it instead.
* [One broker for many clusters](open-problems/one-broker-for-many-clusters.md).
  The topic base is one string, `liken/media`. Two clusters sharing
  one broker collide until the base carries a cluster's name.
* [The player is not a Home Assistant entity](open-problems/the-player-is-not-a-home-assistant-entity.md).
  MQTT was chosen for Home Assistant, but nothing publishes the
  discovery configs that make a `Player` a `media_player`. It also
  gives each `Player` its own retained status.
* [Two operators can run at once](open-problems/two-operators-can-run-at-once.md).
  The operator is a cluster singleton, but `replicas: 1` does not
  enforce one instance across a rollout or a partition. A `Lease` in
  `coordination.k8s.io` makes it a true singleton, and opens a
  quasi-HA path.
