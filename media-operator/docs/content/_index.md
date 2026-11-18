---
title: media-operator
---

# `media-operator`

`media-operator` plays films, series, and music on the screens and
speakers of a [`liken`](https://liken.sh/docs/) cluster, and lets a
remote control them. You declare each unit of equipment, such as a
TV with its speakers, as a `Player`, and you start playback by
creating a `Play` on it. A `Remote` connects a controller to the
players it drives.

Things you can run this way:

* a film on a TV with a surround pair, the picture on the TV and the
  sound on the speakers,
* an album on a lone speaker,
* a season of episodes, played in order to the end,
* any `https://` stream, `nfs://` export, `claim://` volume in the
  `Play`'s namespace, or a `pattern://` test pattern built into the
  player image.

Start here:

* [Install the operator](/docs/guides/install/). The install applies
  the manifests that this site serves at
  [`/deploy/`](/deploy/kustomization.yaml), so you don't need a clone.
* [Declare a `Player`](/docs/guides/declare-a-player/) for a screen
  and its speakers, and prove that it plays.
* [Map a new controller](/docs/guides/mapping-a-controller/) when its
  buttons do the wrong thing.
* [Reference](/docs/reference/): each resource, its fields, and its
  topics on the message bus.

## How it works

The operator defines these resources:

* A `Player` is one unit of equipment in one place: a lone speaker, a
  TV with its own speakers, or a TV with a receiver. It selects its
  screen, speakers, and GPU with the same
  [CEL](https://kubernetes.io/docs/reference/using-api/cel/) selectors
  that a hand-written `ResourceClaim` uses.
* A `Play` is one run of media on a player, like a `Job`: a film, an
  album, or a season. Create it to start, delete it to stop, and
  `kubectl get plays` lists what's playing now.
* A `Remote` is one physical controller, bound to the players it
  drives.
* A `Keymap` maps one controller model's buttons to media actions.
* `MediaPreferences` holds the cluster's default audio and subtitle
  languages.

For each `Play`, the operator starts one pod that runs
[`mpv`](https://mpv.io/), with claims on the `Player`'s devices. The
claims on the speakers and the other devices exist only while the
`Play` runs, so other workloads can use those devices between plays.
Between plays, each `Player` runs an idle pod on its screen. The idle
pod shows a clock and the player's name, and fades them after a few
minutes with no activity. When the screen is a `display-operator`
`Display`, the operator can also turn the panel off after a longer
time.

Each `Remote` has its own pod, which publishes button presses to the
cluster's [message bus](/docs/reference/bus/), and the playback pod
reads them from there. Your own programs can use the same bus. The
operator's capabilities agent publishes what each GPU can decode,
encode, and scale as `media.liken.sh` devices, so a pod can claim,
for example, a GPU that decodes 10-bit HEVC.

`media-operator` is one of the extension operators for
[running a home theater](https://liken.sh/docs/concepts/running-a-home-theater/).
It plays the media that a `Play` points to, and
[`library-operator`](https://liken.sh/library/) adds a catalog of
your films and series, and a browser on the screen to choose from
it.

* [The source](https://github.com/liken-sh/liken/tree/main/media-operator)
* [The `liken` manual](https://liken.sh/docs/)
