# Preferred languages and subtitles

Plan 08. It adds `MediaPreferences`, a cluster resource that states the
languages a viewer wants and whether subtitles show. When this slice lands, a
`Play` starts on the right audio track and the right subtitles with no
hand-picking. One resource holds the household default, and a `Player` or a
`Play` overrides it.

## The problem

The player takes whatever `mpv` picks. `mpv` reads the first audio track in the
file and shows no subtitles. A viewer who wants English audio, or the original
audio with English subtitles, sets it by hand on the control strip every time a
`Play` runs. Nothing remembers the choice, and nothing states it for a room.

## The resource

`MediaPreferences` is a cluster-scoped resource, and one instance holds the
household default. Its name is `default`. The default belongs to the whole
cluster, not to one namespace, the same reason a `Keymap` is cluster-scoped. The
name is fixed because a second default is a contradiction. A `MediaPreferences`
under any other name is rejected at apply, so the mistake shows at once instead
of adding a silent second object.

## Three tiers resolve each field

A field resolves from the most specific place that sets it: the `Play` first,
then the `Player`, then the `default` `MediaPreferences`. A field that no tier
sets passes nothing to `mpv`, so `mpv` keeps its own default and the feature
adds no behavior until someone states a preference. Each field resolves on its
own, so a `Play` sets `subtitles` to `on` and still takes the room's languages.

This is the field-by-field resolution the presentation block already uses in
plan 07-c, one tier deeper.

## The fields

* `audioLanguages`. An ordered list of language codes, most wanted first, such
  as `[en, ja]`. `mpv` takes the first audio track that matches.
* `subtitleLanguages`. An ordered list for subtitles, separate from the audio
  list. The two are separate because a viewer takes Japanese audio with English
  subtitles, so one list cannot state both.
* `subtitles`. One of `on`, `off`, or `auto`. `on` shows subtitles always.
  `off` shows none. `auto` shows them only when the audio that played is not the
  first-choice language, so a foreign film gets subtitles and a native one does
  not.

The codes are ISO 639 language codes, `en`, `ja`, `es`. A code that matches no
track in a file is not an error. `mpv` falls to its own default, and the status
reports what played.

## How the preference reaches mpv

The operator resolves the fields when it builds the `Play` pod and passes them
as `mpv` options, the same path the trickplay interval takes. There is no logic
in the display.

* `audioLanguages` becomes `--alang`.
* `subtitleLanguages` becomes `--slang`.
* `subtitles: on` sets `--sub-visibility=yes` and `--subs-with-matching-audio=yes`,
  so subtitles show even when the audio already matches.
* `subtitles: off` sets `--sid=no`, so no subtitle track loads.
* `subtitles: auto` sets `--subs-with-matching-audio=no`, so `mpv` shows
  subtitles only when the audio language differs from the subtitle language.

The operator also sets `--subs-match-os-language=no`. Without it `mpv` reads the
pod's locale and can pick a subtitle that the stated `subtitleLanguages` did not
ask for.

## The preference applies at the start of a Play

A change to any tier takes effect on the next `Play`, not the one already
running. The audio track and the subtitle track are `mpv` start options, and
switching them mid-film would pull the sound out from under the viewer. So a
room that changes its default does not disturb a film in progress.

## The status reports what resolved and what played

The `Play` status reports the resolved languages and the subtitle setting, and
the track `mpv` chose. So a code that matched no track shows plainly. The viewer
asked for `de` audio, the status shows the audio fell to the first track, and
the cause is visible instead of guessed. This follows the console-parity rule,
that what the boot resolves also reaches the status.

## Set aside for this slice

* A person model. `MediaPreferences` holds one household default and a per-room
  override, not a profile per viewer. A viewer model can layer on later.
* System-wide preferences. The clock format, the units, and the locale are not
  here. `MediaPreferences` is media playback, and it lives in the media-operator.
  A cluster-wide preferences object that other operators also read is a separate,
  larger design.
* Forced and SDH subtitles. A viewer who wants only the forced lines of a
  foreign passage, or the hearing-impaired track, is a later slice.
* Live re-selection. Changing the audio or subtitle track of a running film is
  set aside above, by choice.

## How it will be proved

On `liken-1`, with a film that carries more than one audio track and more than
one subtitle track.

The drill checks each claim:

* The `default` `MediaPreferences` sets `audioLanguages: [en]`,
  `subtitleLanguages: [en]`, and `subtitles: auto`. A `Play` of a
  foreign-audio film shows English subtitles, and a `Play` of an English-audio
  film shows none.
* A `Player` override to `audioLanguages: [ja]` takes the Japanese track, and
  the `Play` under that `Player` follows it.
* A `Play` override to `subtitles: on` shows subtitles over matching audio.
* A `MediaPreferences` under a name other than `default` is rejected at apply.
* The `Play` status shows the resolved fields and the chosen tracks.

Before the hardware drill, the name pin is drilled with a scratch CRD on the dev
cluster, so the rejection is proved without a release. The same path runs on a
workstation through `media-preview`, so the resolved options are seen before the
release.
