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

* [19, A claim as a media reference](19-a-claim-as-a-media-reference.md).
  A third URI scheme, `claim://`, names a `PersistentVolumeClaim` in
  the `Play`'s namespace and a path inside it, so a `Play` plays from
  any volume the cluster can mount and names no server. The library
  layer depends on it.
* [20, The idle screen is its own
  image](20-the-idle-screen-is-its-own-image.md). The idle screen
  leaves `mpv` and becomes `media-operator-idle`, a Rust client on the
  Iced toolkit that reads the bus. The sidecar publishes its four
  decisions on a screen topic and loses its socket, the seven Lua
  modules that drew the screen are deleted, and the playback overlay
  stays. The library layer depends on it.
* [21, A remote that teaches its
  keymap](21-a-remote-that-teaches-its-keymap.md). Built, and
  awaiting its drill. `spec.discovery` on the `Remote` keeps every
  node and logs each event with a paste-ready `Keymap` entry, the
  button vocabulary widens to the kernel's whole `EV_KEY` space, and
  `status.unbound` reports the declared codes the `Keymap` does not
  bind.

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
* [09, The idle screen](completed/09-the-idle-screen.md). Built
  across plans 12 to 17 and drilled on `liken-1` through release
  2026.08.24-010: the standing per-`Player` pod, the bus-fed surface,
  the fade, and the panel power, on the display-operator's
  [plan 07](https://github.com/liken-sh/display-operator/blob/main/plans/completed/07-sharing-the-screen.md)
  draw device. Two surface items, the household zone and the
  now-playing from another room, were set aside when the plan closed.
* [10, The player owns the volume](completed/10-the-player-owns-the-volume.md).
  Built, and drilled on `liken-1` on 2026-08-24 in release
  2026.08.24-011. The listening level and the muted flag became
  `Player` state, one retained message per unit: a press publishes
  the next state, every pod applies it off the subscription, the
  operator seeds unity and writes a `Play`'s declared start through,
  and the display draws its own indicator, shown only by the
  sidecar's `volume-changed` signal. The idle screen sets the room's
  level before any media plays, and the local harnesses gained
  volume keys.
* [11, The music experience](completed/11-the-music-experience.md).
  Built, and drilled on `liken-1` on 2026-08-26 in release
  2026.08.26-003. An album plays as one `mpv` EDL timeline: the
  player shim expands a directory item marked as an album, the
  tracks become chapters on the film's own scrubber, the cover
  centers on the blanked frame, and a standalone track's header
  reads its own tags. The drill's findings shipped in the same day's
  releases: the idle line names the record, the display survives the
  pod's startup order, and the canvas takes each screen's own ratio,
  proven beside a film on the second screen.
* [12, The idle screen reads the bus](completed/12-the-idle-screen-reads-the-bus.md).
  Built, and drilled on `liken-1` on 2026-08-24 in release
  2026.08.24-006, with the reply-await fix in 2026.08.24-008. The idle
  screen became a live client of the bus: a retained status per
  `Player`, controller presence from the standing remote pod, and the
  `liken` mark in motion per the brand's `motion.md`. The full cycle
  was read off the broker in order, and a `displayName` edit showed
  with no restart.
* [13, Standing pods follow the template](completed/13-standing-pods-follow-the-template.md).
  Built, and drilled on `liken-1` on 2026-08-24 by its own rollouts.
  The 2026.08.24-007 apply rolled every unstamped standing pod and
  claim once, and the 2026.08.24-008 apply rolled only the pods and
  kept the claims, the two-tier repair the template hash decides.
* [14, The sidecar reports the ending](completed/14-the-sidecar-reports-the-ending.md).
  Built, and drilled on `liken-1` on 2026-08-24 in release
  2026.08.24-007. The bus carried `ended` one second after the exit
  press, and the idle screen took over with no black gap. The arrival
  animation needed the reply-await fix in 2026.08.24-008, because the
  revealed message was lost when the sidecar closed its socket before
  mpv's replies.
* [15, Finished plays clean up](completed/15-finished-plays-clean-up.md).
  Built, and drilled on `liken-1` on 2026-08-24 in release
  2026.08.24-009. A `Finished` Play's pod and claim go on the first
  pass that reads the terminal phase, and the Play follows after
  `ttlSecondsAfterFinished`, a spec field defaulting to 300 seconds,
  so a library app times its continue-watching window with the same
  knob. The upgrade's first pass swept two lingering finished pods at
  once, and both Plays deleted on their five-minute stamps.
* [16, The idle screen goes dark](completed/16-the-idle-screen-goes-dark.md).
  Built, and drilled on `liken-1` on 2026-08-24 in release
  2026.08.24-010. After a quiet stretch the idle screen fades to
  black, and a press on the unit's remotes brings it back; `back`
  toggles it by hand. The policy is `idle.fadeAfterSeconds` on the
  `Player`, defaulted by `MediaPreferences`. Only press edges act,
  because the reader publishes releases too, and a release that
  counted would wake the screen its own press just slept.
* [17, The idle screen powers the panel](completed/17-the-idle-screen-powers-the-panel.md).
  Built, and drilled on `liken-1` on 2026-08-24 in release
  2026.08.24-010. The idle pod holds the panel's `-control` device
  and its sidecar writes DDC/CI itself: the backlight to 0 at
  `idle.offAfterSeconds`, and the actual state folded into
  `PlayerStatus.Panel`. The BOE read 0 over DDC after the windows, a
  press restored it, a `Play` against the dark panel lit it, and the
  first press after a controller's Bluetooth reconnect woke it,
  eyewitnessed. `offMode: power` stays gated on the metal drill in
  the plan.
* [18, Blanking moves to the Display](completed/18-blanking-moves-to-the-display.md).
  Built, and drilled on `liken-1` on 2026-08-27 in release
  2026.08.27-002. The idle sidecar states a panel desire on its
  retained topic and writes no hardware; the operator turns the
  desire into `spec.override` on the screen's `Display`, and
  `PlayerStatus.Panel` folds what the `Display` observed. The
  `-control` claim request, the DDC client, and `Player.spec.control`
  retire with the wire. The drill blanked the BOE 50 seconds after
  the shortened quiet window with the capture committed first, and an
  idle pod deleted while the panel was dark relit it in 5 seconds,
  the failure plan 17's process memory could not survive. The lift
  needed the display-operator's 2026.08.27-003 (`spec` defaults to
  `{}`, because server-side apply prunes an empty spec).

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
