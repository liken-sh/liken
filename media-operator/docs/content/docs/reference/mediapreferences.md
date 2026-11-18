---
title: MediaPreferences
weight: 50
toc: true
---

<!-- Generated from deploy/mediapreferences-crd.yaml by crdref. Do not edit. -->

# `MediaPreferences`

A `MediaPreferences` sets the cluster's defaults: the preferred audio
and subtitle languages, the time zone of the idle clock, and when an
idle display fades and turns off. For each setting, the operator uses
the value from the [`Play`](/docs/reference/plays/)'s spec, then from
the [`Player`](/docs/reference/players/)'s spec, then from this
object. A `Play` has no idle settings, and the time zone is set only
here.

A cluster has at most one `MediaPreferences`, named `default`. The CRD
rejects any other name, so a misnamed object fails when you apply it.
A cluster with no `MediaPreferences` works, and the operator uses only
the `Play` and the `Player`.

    apiVersion: media.liken.sh/v1alpha1
    kind: MediaPreferences
    metadata:
      name: default
    spec:
      audioLanguages: [ja, en]
      subtitleLanguages: [en]
      subtitles: auto
      sdh: prefer
      timeZone: America/New_York
      idle:
        fadeAfterSeconds: 600
        offAfterSeconds: 1200
        offMode: backlight

The cluster's defaults: the preferred audio and subtitle languages, the time zone of the display clock, and what an idle screen does. A Player or a Play can override each language field. Nothing overrides the time zone.

## spec

The operator uses a language or subtitle field here only when neither the Play nor the Player sets it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | Audio languages in order of preference, as IETF language tags such as `en` or `pt-BR`. mpv plays the first audio track that matches, and treats `en` and `eng` as the same language. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | Subtitle languages in order of preference, so a viewer can hear the original audio with subtitles in their own language. When it is empty, `audioLanguages` is used. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | When subtitles show. With `on`, the playback pod shows the first full or SDH track in the order of `subtitleLanguages`. When there is none, it shows the forced track of the audio's language, if that language is in `audioLanguages`. With `auto`, audio in the first language of `audioLanguages` shows only its forced track, or nothing, and other audio chooses as `on` does. With `off`, no subtitles show. One of: `on`, `off`, `auto`. |
| <span id="spec--sdh"></span>`sdh` | string | no | Whether an SDH track ranks ahead of a full track in the same language (`prefer`) or behind it (`avoid`). An SDH track also describes sounds, for a viewer who is deaf or hard of hearing. When omitted, the two rank the same, and the playback pod takes the track with the default flag, or else the first in the file. One of: `prefer`, `avoid`. |
| <span id="spec--timezone"></span>`timeZone` | string | no | The time zone, as an IANA name such as `America/New_York`. The playback pod gets it as `TZ`, so the display clock shows local time and not UTC. |
| <span id="spec--idle"></span>`idle` | [object](#specidle) | no | What a screen does while nothing plays. Each field applies to a Player whose own `idle` block leaves it unset. |

### spec.idle

What a screen does while nothing plays. Each field applies to a Player whose own `idle` block leaves it unset.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidle--controller"></span>`controller` | string | no | The operator that draws the idle screen, as a domain-qualified name. With the default, `media.liken.sh/idle-screen`, media-operator draws its own idle screen. With `media.liken.sh/none`, nothing draws one and the operator creates no idle claim. Any other name leaves the screen to the operator with that name, which reads the Player's `status.idle` for the claim to use, its request names, the fade and off windows, and the MQTT broker and topics. Pattern: `^[a-z0-9.-]+/[a-z0-9-]+$`. |
| <span id="specidle--image"></span>`image` | string | no | The container image that draws the idle screen. The image runs its own entrypoint and reads the Player's state from the MQTT bus. It implements the fade and off windows, the dimming of the screen, the volume indicator, and the requests to turn the panel on or off. It acts on a remote's press only while that remote's focus is on this Player. When unset, the screen runs the idle client that media-operator ships. |
| <span id="specidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds with no activity before an idle screen fades to black. The default is 600, and 0 turns the fade off. |
| <span id="specidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds with no activity before an idle panel turns off. Unset or 0 leaves the panel on. The shipped idle client treats a value below `fadeAfterSeconds` as `fadeAfterSeconds`. A panel turns off only when display-operator runs in the cluster and publishes a `Display` for the screen. |
| <span id="specidle--offmode"></span>`offMode` | string | no | How the panel turns off after `offAfterSeconds`. With `backlight`, the default, the panel stays on at brightness zero and still responds to DDC. With `power`, the panel powers off, and some panels then stop responding to DDC. Use `power` only for a panel that you have seen wake from it. One of: `backlight`, `power`. |

## How a Play resolves it

The operator resolves each field separately. It takes the value from
the `Play` if the `Play` sets the field, otherwise from the `Player`,
otherwise from the default. A field that none of the three sets has no
value. For `audioLanguages` and `subtitleLanguages`, an empty list is a
value. A `Play` with `audioLanguages: []` overrides the `Player` and
this object, and its pod gets no audio language preference.

The operator resolves the tiers when it creates a `Play`'s pod. The
resolved languages become mpv's `--alang` and `--slang` arguments. With
`subtitles: on` or `auto`, the pod's command sidecar also receives the
resolved fields and chooses the subtitle track itself, as the next
section describes. The operator also writes the resolved values to the
`Play`'s `status.audioLanguages`, `status.subtitleLanguages`,
`status.subtitles`, and `status.sdh`, so `kubectl get play <name> -o
yaml` shows what the three tiers produced.

When you edit the `MediaPreferences`, the operator writes the new
resolved values to the status of each running `Play`. The running pod
keeps the arguments it started with, so until the next `Play` starts,
the status can differ from what the pod uses.

## How the subtitle track is chosen

Many files flag their subtitle tracks wrongly. A forced track has
subtitles only for speech in a language other than the audio's, and it
often has the default flag. Some forced tracks have no flag or title
that marks them. mpv ranks a track with the default flag above a full
track, so if mpv chose, `subtitles: on` would often show a forced
track in place of the full one. So with `on` and `auto`, the command
sidecar chooses the track itself after mpv loads each item and selects
its audio, and sets mpv's `sid` property.

The sidecar gives each subtitle track one role. It reads the track's
flags, title, and file name from mpv, and its event count from the
item's `presentation.subtitles`:

| Role | A track gets it when |
|---|---|
| `empty` | it has 0 events |
| `forced` | it has the forced flag, a title that contains "forc", a `forced` segment in its file name, or fewer events than a fifth of the largest track in the same language |
| `commentary` | its title contains "commentary" |
| `sdh` | it has the hearing-impaired flag, or a title word or file name segment that reads SDH, CC, or HI |
| `full` | none of the above |

The event count is compared only when both tracks have a known count.
When a `Play` has no counts, as with one that you create with
`kubectl`, the roles come from the flags, titles, and file names alone.
For an external subtitle file such as `Movie.eng.sdh.1.srt`, the
sidecar reads the language and the markers from the file name itself,
because mpv reads neither from a name that ends in a number. The
sidecar treats `en`, `eng`, and `en-US` as the same language.

The sidecar reads the languages in the order of `subtitleLanguages`. When
no tier sets `subtitleLanguages`, or a tier sets an empty list, it uses
the order of `audioLanguages`. When both are empty, the sidecar sets
nothing, mpv's own choice stays, and the sidecar logs the reason.

With `subtitles: on`, the sidecar chooses in this order:

1. The first `full` or `sdh` track, going through the languages in order.
   Within one language, `sdh: prefer` ranks `sdh` tracks first and
   `sdh: avoid` ranks them last. With no `sdh` setting, the two roles
   rank the same. After that, a track with the default flag ranks first,
   then the track that comes first in the file.
2. A `forced` track in the language of the audio that plays, if that
   language is in `audioLanguages`.
3. A `forced` track in one of these languages, when
   the audio is in another language. Such a track is often a full
   translation with a wrong flag.
4. A `full` or `sdh` track that has no language, such as `Movie.srt`
   beside `Movie.mkv`. Such a track is usually in the language of the
   release.
5. A `commentary` track in one of these languages.

The sidecar never chooses an `empty` track. When nothing matches, no
subtitles show.

With `subtitles: auto`, audio in the first language of
`audioLanguages` shows only its `forced` track, or no subtitles. Audio
in any other language shows a track as `on` does. With `subtitles: off`,
no subtitles show and the sidecar chooses nothing.

The sidecar chooses once for each item, when mpv loads it, so a track
that someone selects with a remote stays until the next item. Until the
sidecar sets `sid`, and when it fails, mpv's own choice shows. The
operator passes mpv options that bring that choice closer to the
sidecar's. With `on`, `--subs-fallback-forced=no` stops mpv from
ranking a forced track above a full track in the same language. With
`auto`, `--subs-with-matching-audio=forced` lets mpv choose a forced
track when the audio is in a subtitle language. With both modes, the
operator also passes `--reset-on-next-file=sid`. Without it, mpv would
apply the `sid` that the sidecar set to the next file with the same
track layout, and if the sidecar failed on that file, it would show the
earlier file's choice. The sidecar logs one line for each choice, with
the track, its role, its event count, and the reason.

## No status

Nothing reports on a `MediaPreferences`, so it has no status
subresource. `kubectl get mediapreferences` shows the subtitle
mode and the age.

## Not on the bus

`MediaPreferences` has no topics on the [bus](/docs/reference/bus/).
The operator reads it when it builds a pod, and passes the resolved
values to the pod as arguments and environment variables.
