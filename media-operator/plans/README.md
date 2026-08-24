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
[`completed/`](completed/) holds the plans that are built and drilled.

## The design

* [00, The media-operator design](00-design.md). The founding
  design: the `Player`, `Play`, `Remote`, and `Keymap` resources,
  the playback pod, the input bus, and the carriage layer that
  comes later.

## Planned

These plans are designed. Each keeps its number and moves to
[`completed/`](completed/) when it is built and drilled.

* [09, The idle screen](09-the-idle-screen.md). A standing per-`Player`
  pod draws status while no `Play` runs, the clock, the zone, and the
  now-playing from another room, and yields the screen when a `Play`'s
  `mpv` draws on top. It holds a shared draw device so each `Play` keeps
  its own mode and power, and it sleeps the panel through a power request
  the display-operator counts. It needs the display-operator's
  [plan 07](https://github.com/liken-sh/display-operator/blob/main/plans/07-sharing-the-screen.md).
* [10, A native volume indicator](10-a-native-volume-indicator.md).
  `volume` and `mute` move off `mpv`'s built-in text onto a
  `liken`-drawn readout that fades with the rest of the OSD.
* [11, The music experience](11-the-music-experience.md). A music
  `Play` draws a layout `liken` owns, the album art centered with the
  track list and the queue, in place of the scrubber a film shows. It
  builds on the display of plan 07.

## Completed

* [01, A play becomes a pod](completed/01-a-play-becomes-a-pod.md). Built, and
  drilled on `liken-1` on 2026-08-20 in release 2026.08.20-001. The
  first slice: `Player` and `Play`, the operator, the player image,
  and the repository scaffolding. A film played from a `kubectl
  create`, its position advanced in `kubectl get plays`, and the
  delete tore everything down through the owner references.
* [02, A remote drives the play](completed/02-a-remote-drives-the-play.md).
  Built, and drilled on `liken-1` on 2026-08-20 in release
  2026.08.20-004. `Remote` and `Keymap`, with the reader as a
  sidecar in the playback pod: the smallest proof of input, taken
  knowingly ahead of the bus. A DualSense paused a film and
  `status.paused` followed within one report.
* [03, The bus carries input and reports](completed/03-the-bus-carries-input-and-reports.md).
  Built, and drilled on `liken-1` on 2026-08-20 in release
  2026.08.20-010. Mosquitto as the message bus, the remote reader moved
  to its own standing pod, and the playback pod rebuilt as `mpv` with
  one bus-bridge sidecar and no supervisor. The report moved off plain
  HTTP and onto the bus. It retired plan 02's sidecar and the plan-01
  supervisor, and closed the plain-HTTP open problem. A film played,
  a DualSense paused and scrubbed it over the bus, and the position
  kept advancing across an operator restart.
* [04, The player owns its remotes](completed/04-the-player-owns-its-remotes.md).
  Built, and drilled on `liken-1` on 2026-08-21 in release
  2026.08.21-005. The binding moved from the `Remote` onto the
  `Player`, so a `Player` is the whole description of a unit,
  controllers included, and a `Player` edit recreates a running
  `Play`'s pod at the film's position. Every reference stays
  same-namespace.
* [05, Keymaps onto the bus](completed/05-keymaps-onto-the-bus.md).
  Built, and drilled on `liken-1` on 2026-08-21 in release
  2026.08.21-005. The playback pod's one bridge split into a command
  sidecar and a translator sidecar per controller, and the keymap moved
  onto the bus as retained state, so a `Keymap` edit reaches a running
  film with no restart. The `Keymap` went cluster-scoped. A film paused
  from a `play-pause` published by hand, with no controller in the loop.
* [06, Focus and many remotes](completed/06-focus-and-many-remotes.md).
  Built, and drilled on `liken-1` on 2026-08-21 in release
  2026.08.21-005. One `Player` names several controllers and one
  `Remote` drives several units, and a retained focus mark decides which
  film a press reaches. Two films ran on two monitors from one
  DualSense: the cross paused only the film that held focus, and the
  source button cycled the focus to the other.
* [07, The player draws its own display](completed/07-the-player-draws-its-own-display.md).
  Built across slices 07-a through 07-e, 07-g, and 07-h, and proven on
  the workstation through `media-preview`. The on-hardware drill on
  `liken-1` runs with the next release. `liken` draws its own on-screen
  display as one `mpv` script through `libass` and `overlay-add`: a
  summoned scrubber, a focus stack with chapter and track choosers, a
  `presentation` block resolved per item, art decoded to `bgra`, a
  trickplay seekbar, a blurred scrim, and a grouped control strip with a
  clock. The composed music experience moved to plan 11.
* [08, Preferred languages and subtitles](completed/08-preferred-languages.md).
  Built. The on-hardware drill on `liken-1` runs with the next release.
  `MediaPreferences` states the audio and subtitle languages a viewer
  wants and whether subtitles show. One cluster resource holds the
  household default, and a `Player` or a `Play` overrides it. The
  operator resolves the fields at `Play` start and passes them to `mpv`.

## Open problems

[`open-problems/`](open-problems/) holds the questions this operator
owes an answer to. Those documents have no number, because nobody
has decided yet what work they become.

* [How the idle screen asks for power](open-problems/how-the-idle-screen-asks-for-power.md).
  Plan 09's idle pod holds a standing draw claim, but it must raise and
  drop its panel-power request on a slower clock as a room goes quiet. A
  small power claim it takes and releases reuses the display-operator's
  count but churns objects; a desired-power field the display-operator
  reads stays quiet but couples the two operators. Deferred to its own
  design.
* [The player image is still Debian](open-problems/the-player-image-is-still-debian.md).
  The operator image is one binary on `scratch`; the player image is
  a distribution base, because `mpv`'s runtime closure is wide. The
  audio operator's closure-on-scratch treatment applies, with the
  complication that `mpv` loads its GPU drivers only on real
  hardware.
* [The broker is not configurable](open-problems/the-broker-is-not-configurable.md).
  Plan 03 stands up its own MQTT broker and fixes the topic base. A
  home that already runs a broker, for Home Assistant or zigbee2mqtt,
  should be able to point the operator at it, and two clusters sharing
  that broker need a cluster's name in the base to stay apart.
* [The player is not a Home Assistant entity](open-problems/the-player-is-not-a-home-assistant-entity.md).
  MQTT was chosen for Home Assistant, but nothing publishes the
  discovery configs that make a `Player` a `media_player`. It also
  gives each `Player` its own retained status.
* [Two operators can run at once](open-problems/two-operators-can-run-at-once.md).
  The operator is a cluster singleton, but `replicas: 1` does not
  enforce one instance across a rollout or a partition. A `Lease` in
  `coordination.k8s.io` makes it a true singleton, and opens a
  quasi-HA path.
* [The bus authorizes nothing](open-problems/the-bus-authorizes-nothing.md).
  Any client that reaches the broker can publish or subscribe to any
  topic, so the trust boundary is the whole cluster. Acceptable for one
  home the owner controls, and it owes broker ACLs once a cluster runs
  a workload the owner does not trust.

## Rejected

[`rejected/`](rejected/) holds the designs the project considered and
chose not to build, with the reason. A rejected document is the record
that saves the next person from proposing the same thing again.

* [A pre-flight co-location condition on a Player](rejected/pre-flight-co-location-condition.md).
  The design asked for a `Player` condition that reports before a
  `Play` whether one machine can hold every claimed device. The
  scheduler already reports it one step later, through the `Play` that
  parks `Pending`, and answering early would make the operator
  re-derive an allocation the scheduler owns. Not worth the watch, the
  permission, and the drift.
