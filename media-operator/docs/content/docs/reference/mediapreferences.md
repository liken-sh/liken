---
title: MediaPreferences
weight: 50
toc: true
---

# MediaPreferences

A `MediaPreferences` is the cluster-scoped default for audio and
subtitle languages, the wall-clock zone, and the idle screen
policy. It is the lowest of the three tiers a
[`Play`](/docs/reference/plays/) resolves: the `Play`'s own spec
first, then the [`Player`](/docs/reference/players/)'s, then this
default.

There is exactly one, and its name is `default`. The CRD pins the
name with a CEL rule, so a `MediaPreferences` under any other name
is rejected at apply, and the mistake shows at once instead of
adding a silent second object. A cluster without one is fine: a
missing default is not an error, and resolution skips the tier.

## The spec

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

`audioLanguages` and `subtitleLanguages` are ordered language
codes, most wanted first; mpv takes the first track that matches.
The two lists are separate, so one viewer takes foreign audio with
native subtitles.

`subtitles` is when subtitles show: `on` always, `off` never, and
`auto` only when the audio that played is not the first choice.

`timeZone` is the cluster's wall-clock zone as an IANA name, like
`America/New_York`. The player pod reads it as `TZ`, so the display
clock shows local time instead of UTC. It is one per cluster: unlike
the language fields, no `Play` or `Player` overrides it.

`idle` is the cluster's default idle screen policy, read for each
field a `Player`'s own block leaves unset:

- `idle.fadeAfterSeconds` is the seconds of quiet before an idle
  screen fades to black. Zero disables the automatic fade. Unset,
  every screen fades after 600.
- `idle.offAfterSeconds` is the seconds of quiet before an idle
  panel goes dark, at least `fadeAfterSeconds`. Zero or unset means
  panels never go dark on their own. It acts only on a `Player` that
  states a control device.
- `idle.offMode` is what the off window writes: `backlight`, the
  default, writes the backlight to zero and always wakes over DDC;
  `power` writes DPM off, which is deeper. State `power` only for
  panels a drill proved wake.

## How a Play resolves it

Each field settles on its own, `Play` then `Player` then the
default. The first tier that states a field wins it, and a field no
tier states resolves to nothing. For the two lists, omitting the
field leaves the tier silent, and an empty list is a statement: a
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
