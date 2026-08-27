---
title: MediaPreferences
weight: 50
toc: true
---

<!-- Generated from deploy/mediapreferences-crd.yaml by crdref. Do not edit. -->

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
      timeZone: America/New_York
      idle:
        fadeAfterSeconds: 600
        offAfterSeconds: 1200
        offMode: backlight

The cluster's defaults: the preferred audio and subtitle languages, the time zone its displays show, and what an idle display does. A Player or a Play overrides the language fields field by field; the time zone has no override.

## spec

The default language and subtitle fields, each read only when no more specific tier states it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | Ordered audio language codes, most wanted first; mpv takes the first audio track that matches. Codes are IETF language tags, and mpv treats the ISO 639-1 form en and the ISO 639-2 form eng as the same language. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | Ordered subtitle language codes, separate from the audio list, so one viewer takes foreign audio with native subtitles. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | When subtitles show; on always, off never, auto only when the audio that played is not the first choice. One of: `on`, `off`, `auto`. |
| <span id="spec--timezone"></span>`timeZone` | string | no | The time zone, as an IANA name like America/New_York. The player pod reads it as TZ, so the display clock shows local time instead of UTC. One per cluster: no Play or Player overrides it. |
| <span id="spec--idle"></span>`idle` | [object](#specidle) | no | The cluster's default for what a display does while nothing plays, read for each field a Player's own block leaves unset. |

### spec.idle

The cluster's default for what a display does while nothing plays, read for each field a Player's own block leaves unset.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds of quiet before an idle screen fades to black. Zero disables the automatic fade; unset, every screen fades after 600. |
| <span id="specidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds of quiet before an idle panel goes dark, at least fadeAfterSeconds. Zero or unset means panels never go dark on their own. A panel goes dark only where the cluster runs a display-operator that publishes a Display for the screen. |
| <span id="specidle--offmode"></span>`offMode` | string | no | Which override the off window applies to the screen's Display: backlight, the default, or power, which is deeper. State power only for a panel that woke from it in a drill. One of: `backlight`, `power`. |

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
arguments. It also reports the resolved values on every `Play`'s
status, so `kubectl get play` shows what the three tiers settled
on. The operator watches `MediaPreferences`, so an edit refreshes
that resolved record on a running `Play`'s status within one pass;
the running player keeps the arguments it started with, and the
edit reaches the next `Play`'s pod.

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
