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
