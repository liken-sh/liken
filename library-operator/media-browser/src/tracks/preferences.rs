// The `Player`'s language preferences, as library-operator resolves them
// for the browser. The operator resolves each field from the `Player` and
// then from the household's `MediaPreferences`, the way media-operator
// does for a `Play`, and writes the result as JSON into the browser's
// environment. A `Play` can state its own preferences too, but no `Play`
// exists while a person reads a page, so a play that overrides them can
// take other tracks than the page marks.

use serde_json::Value;

/// The variable library-operator writes the preferences into.
pub const VARIABLE: &str = "LIBRARY_MEDIA_PREFERENCES";

/// The two subtitle modes in which media-operator's command sidecar
/// chooses the track. In `off` the play shows no subtitles, and with no
/// mode the sidecar leaves mpv's own choice, so the page marks no track in
/// either case.
pub const ON: &str = "on";
pub const AUTO: &str = "auto";

/// The two values of the `sdh` preference. An empty value is no
/// preference, and full and SDH tracks then rank the same.
pub const PREFER: &str = "prefer";
pub const AVOID: &str = "avoid";

/// The preferences in media-operator's own field names.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Preferences {
    /// `audioLanguages`, most preferred first.
    pub audio_languages: Vec<String>,
    /// `subtitleLanguages`, most preferred first.
    pub subtitle_languages: Vec<String>,
    /// `subtitles`: `on`, `auto`, `off`, or empty where no tier states it.
    pub subtitles: String,
    /// `sdh`: `prefer`, `avoid`, or empty.
    pub sdh: String,
}

impl Preferences {
    /// The preferences the JSON states. A value that does not parse states
    /// none, so the page marks no subtitle track and marks the audio track
    /// that mpv takes with no language to match.
    pub fn parse(json: &str) -> Self {
        let Ok(value) = serde_json::from_str::<Value>(json) else {
            return Self::default();
        };
        let list = |key: &str| -> Vec<String> {
            value[key]
                .as_array()
                .map(|items| {
                    items
                        .iter()
                        .filter_map(|item| item.as_str().map(str::to_string))
                        .collect()
                })
                .unwrap_or_default()
        };
        let word = |key: &str| value[key].as_str().unwrap_or_default().to_string();
        Self {
            audio_languages: list("audioLanguages"),
            subtitle_languages: list("subtitleLanguages"),
            subtitles: word("subtitles"),
            sdh: word("sdh"),
        }
    }

    /// Whether a play under these preferences has its subtitle track
    /// chosen by the rule in `subtitle`.
    pub fn chooses_subtitles(&self) -> bool {
        self.subtitles == ON || self.subtitles == AUTO
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_operator_s_json_reads_field_by_field() {
        let preferences = Preferences::parse(
            r#"{"audioLanguages":["en","ja"],"subtitleLanguages":["en"],"subtitles":"auto","sdh":"prefer"}"#,
        );
        assert_eq!(
            preferences,
            Preferences {
                audio_languages: vec!["en".into(), "ja".into()],
                subtitle_languages: vec!["en".into()],
                subtitles: AUTO.into(),
                sdh: PREFER.into(),
            }
        );
        assert!(preferences.chooses_subtitles());
    }

    #[test]
    fn a_value_that_does_not_parse_states_nothing() {
        assert_eq!(Preferences::parse("on"), Preferences::default());
        assert_eq!(Preferences::parse(""), Preferences::default());
    }

    #[test]
    fn only_on_and_auto_choose_a_subtitle_track() {
        let mode = |subtitles: &str| Preferences {
            subtitles: subtitles.into(),
            ..Preferences::default()
        };
        assert!(mode(ON).chooses_subtitles());
        assert!(!mode("off").chooses_subtitles());
        assert!(!mode("").chooses_subtitles());
    }
}
