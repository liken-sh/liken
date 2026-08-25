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
