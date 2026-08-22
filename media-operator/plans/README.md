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

This plan is designed. It keeps its number and moves to
[`completed/`](completed/) when it is built and drilled. It draws the
display that the reworked `Remote` from plans 04 through 06 navigates,
and it is provable on hardware alone.

* [07, The player draws its own display](07-the-player-draws-its-own-display.md).
  `liken` writes its own on-screen display as one `mpv` script, drawn
  through `libass` and `overlay-add`. A `Play` gains a `presentation`
  block that states the media type, a hint, and the art beside the
  media, and the display renders that declaration and tunes the layout
  by type. The command bus from plans 04 through 06 navigates it, so a
  remote or a gamepad drives it. The design is large, so it builds in
  slices, each usable when it lands:
    * [07-a, The scrubber a remote summons](07-a-the-scrubber-a-remote-summons.md).
      The script over `mpv`, the fine scrubber from `mpv`'s properties,
      the summon, and the navigation actions on the bus.
    * [07-b, The stack and the choosers](07-b-the-stack-and-the-choosers.md).
      The vertical focus stack, the chapter scrubber, and the audio and
      subtitle choosers, all from `mpv`'s properties.
    * [07-c, The presentation contract](07-c-the-presentation-contract.md).
      `spec.items` with a `presentation` block, three-tier field
      resolution, per-item switching, and text tuning by type.
    * [07-d, Art on screen](07-d-art-on-screen.md). The bridge decodes
      art to `bgra`, and the display places the `logo`, `clearart`, and
      `backdrop`.
    * [07-e, Trickplay on the seekbar](07-e-trickplay-on-the-seekbar.md).
      The scrub cursor shows a thumbnail cropped from a Jellyfin sprite
      sheet.
    * [07-f, The music experience](07-f-the-music-experience.md). The
      music layout blanks the video and composes the album art, the
      track list, and the queue.

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
