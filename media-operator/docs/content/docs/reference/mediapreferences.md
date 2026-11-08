---
title: MediaPreferences
weight: 50
toc: true
---

<!-- Generated from deploy/mediapreferences-crd.yaml by crdref. Do not edit. -->

# `MediaPreferences`

A `MediaPreferences` holds the cluster's defaults: the preferred
audio and subtitle languages, the time zone its displays show, and
when an idle display fades and goes dark. It is the lowest of the three tiers a
[`Play`](/docs/reference/plays/) resolves: the `Play`'s own spec
first, then the [`Player`](/docs/reference/players/)'s, then this
default.

There is exactly one, and its name is `default`. The CRD pins the
name with a CEL rule, so a `MediaPreferences` under any other name
is rejected at apply, and the mistake shows at once instead of
adding a silent second object. A cluster without one is fine: a
missing default is not an error, and resolution skips the tier.

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

The cluster's defaults: the preferred audio and subtitle languages, the time zone its displays show, and what an idle display does. A Player or a Play overrides the language fields field by field; the time zone has no override.

## spec

The default language and subtitle fields. The operator uses each one only when the Player and the Play leave it unset.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | Ordered audio language codes, most wanted first; mpv takes the first audio track that matches. Codes are IETF language tags, and mpv treats the ISO 639-1 form en and the ISO 639-2 form eng as the same language. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | Ordered subtitle language codes, separate from the audio list, so one viewer takes foreign audio with native subtitles. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | When subtitles show. With on, the command sidecar shows the first full or SDH track in the order of subtitleLanguages, or of audioLanguages when subtitleLanguages is empty, and when none exists, the forced track in the language of the audio, if that language is in audioLanguages. With auto, audio in the first of audioLanguages shows only its forced track, or nothing, and other audio shows a track as on does. With off, no subtitles show. One of: `on`, `off`, `auto`. |
| <span id="spec--sdh"></span>`sdh` | string | no | Whether an SDH track, one with the descriptions of sounds for a viewer who is deaf or hard of hearing, ranks ahead of a full track in the same language (prefer) or behind it (avoid). Omit it, and the two rank the same, so the track with the default flag wins, and then the first in the file. One of: `prefer`, `avoid`. |
| <span id="spec--timezone"></span>`timeZone` | string | no | The time zone, as an IANA name like America/New_York. The player pod reads it as TZ, so the display clock shows local time instead of UTC. One per cluster: no Play or Player overrides it. |
| <span id="spec--idle"></span>`idle` | [object](#specidle) | no | The cluster's default for what a display does while nothing plays, read for each field a Player's own block leaves unset. |

### spec.idle

The cluster's default for what a display does while nothing plays, read for each field a Player's own block leaves unset.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidle--controller"></span>`controller` | string | no | The operator that draws every idle screen whose Player does not name its own, as a domain-qualified name. Two names belong to the media operator: media.liken.sh/idle-screen, the default, which draws the idle screen this operator ships, and media.liken.sh/none, under which nothing draws an idle screen and no claim exists. Any other name hands each screen to the operator that handles it, which reads a Player's status.idle for the claim to reference, the requests it carries, the two windows, and the bus it joins. Pattern: `^[a-z0-9.-]+/[a-z0-9-]+$`. |
| <span id="specidle--image"></span>`image` | string | no | The container image that draws every idle screen. The image starts with its own entrypoint and reads each unit's state from the bus. It implements the fade and off windows, the focus gate, the shade, the volume indicator, and the panel desire in its own process. Unset, every screen whose Player names no image runs the idle client that the media operator ships. |
| <span id="specidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds with no activity before an idle screen fades to black. Zero disables the automatic fade; unset, every screen fades after 600. |
| <span id="specidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds with no activity before an idle panel goes dark, at least fadeAfterSeconds. Zero or unset means panels never go dark on their own. A panel goes dark only where the cluster runs a display-operator that publishes a Display for the screen. |
| <span id="specidle--offmode"></span>`offMode` | string | no | Which override the off window applies to the screen's Display. The default, backlight, holds the panel at brightness zero, which still answers DDC. Power off stops some panels from answering DDC at all; use it only for a panel that you have seen wake from it. One of: `backlight`, `power`. |

## How a Play resolves it

Each field settles on its own, `Play` then `Player` then the
default. The first tier that states a field wins it, and a field no
tier states resolves to nothing. For the two lists, omitting the
field means the tier states nothing, and an empty list is a
statement: a
`Play` with `audioLanguages: []` states no preference and overrides
the tiers below it.

The operator resolves the tiers when it creates a `Play`'s pod, and
the resolved languages become mpv's `--alang` and `--slang`
arguments. In `on` and `auto`, the pod's command sidecar also
receives the resolved fields and chooses the subtitle track itself,
as the next section describes. It also reports the resolved values on every `Play`'s
status, so `kubectl get play` shows what the three tiers settled
on. The operator watches `MediaPreferences`, so an edit refreshes
that resolved record on a running `Play`'s status within one pass;
the running player keeps the arguments it started with, and the
edit reaches the next `Play`'s pod.

## How the subtitle track is chosen

The flags and titles of subtitle tracks are not reliable. A forced
track, which translates only the parts of the audio that are not in
the audio's own language, often carries the default flag, and some
forced tracks carry no flag or title at all. mpv ranks the default
flag above a full track, so with mpv's own rule, `subtitles: on`
shows a forced track in place of the full one. So in `on` and
`auto`, the command sidecar chooses the track when mpv has loaded
each item and selected its audio, and sets mpv's `sid`.

The sidecar gives each subtitle track one role, from its flags, its
title, the name of its file, and the event count that the item's
`presentation.subtitles` carries:

| Role | A track takes it when |
|---|---|
| `empty` | it holds 0 events |
| `forced` | it has the forced flag, a title with "forc" in it, a `forced` segment in its file name, or fewer events than a fifth of the largest track in the same language |
| `commentary` | its title has "commentary" in it |
| `sdh` | it has the hearing-impaired flag, or a title word or file name segment that reads SDH, CC, or HI |
| `full` | none of the above |

The test by count applies only when the counts of both tracks are
known. A `Play` with no counts, such as one that `kubectl` creates,
gets the same rule from the flags, the titles, and the file names
alone. The sidecar reads the language and the markers of an external
file from its name, such as `Movie.eng.sdh.1.srt`, because mpv reads
neither from a name that ends in a number. The languages `en`, `eng`,
and `en-US` are the same language to the rule.

The rule reads in the order of `subtitleLanguages`. When no tier
states `subtitleLanguages`, or a tier states an empty list, the rule
reads in the order of `audioLanguages`. When both are empty, the
sidecar sets nothing, mpv's own choice stands, and the sidecar's log
says why.

With `subtitles: on`, the sidecar takes the first `full` or `sdh`
track in the order of the reading languages. Within one language, `sdh:
prefer` puts `sdh` tracks first, `sdh: avoid` puts them last, and
with no `sdh` the two roles rank the same. Then the default flag
ranks first, and then the order of the tracks in the file. When no
such track exists, the sidecar takes the `forced` track in the
language of the audio that plays, if that language is in
`audioLanguages`. After that, it takes a `forced` track in a reading
language under audio in another language, because such a track is
often a full translation with a wrong flag. Then it takes a `full` or
`sdh` track that names no language, such as `Movie.srt` beside
`Movie.mkv`, because such a track is usually in the language of the
release. A `commentary` track in a reading language is the last
choice, and an `empty` track is never a choice. When nothing matches,
no subtitles show.

With `subtitles: auto`, audio in the first language of
`audioLanguages` shows only its `forced` track, or nothing. Audio in
any other language shows a track as `on` does. With `subtitles: off`,
no subtitles show, and the sidecar chooses nothing.

The sidecar chooses once for each item, when mpv loads it, so a
track that a person selects with a remote stays until the next item.
Until the sidecar acts, and when it cannot act, mpv shows its own
choice. For that choice, the operator passes mpv
`--subs-fallback-forced=no` with `on`, and
`--subs-with-matching-audio=forced` with `auto`. In both modes, it
also passes `--reset-on-next-file=sid`, because mpv keeps the `sid`
that the sidecar set into the next file with the same track layout.
Without the reset, a sidecar that fails on a later item would leave
the earlier item's choice in place of mpv's own. The sidecar writes one
line to its log for each choice, with the track, its role, its
count, and the reason.

## No status

A `MediaPreferences` is a table a person writes and nothing reports
on, so there is no status subresource, and `kubectl get
mediapreferences` shows the subtitle mode and the age.

## Not on the bus

`MediaPreferences` is the one resource of this operator with no
topics on the [bus](/docs/reference/bus/). Nothing at run time
subscribes to a preference: the operator resolves the three tiers
when it builds a pod, and the settled values travel into the pod as
arguments and environment, and onto the `Play`'s status as the
record of what resolved.
