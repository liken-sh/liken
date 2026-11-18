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
