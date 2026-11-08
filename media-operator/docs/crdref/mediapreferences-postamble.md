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
`audioLanguages`. A `commentary` track is the last choice, and an
`empty` track is never a choice. When nothing matches, no subtitles
show.

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
