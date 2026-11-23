//! How to read the values of a log store's level field. The
//! `LogSource` declares them in `levels`: each list holds prefixes of
//! the values that mean an error, a warning, or a plain line, matched
//! without regard to case. A store that uses OpenTelemetry's
//! `severity_text` writes values such as `ERROR`, `Warn`, and `INFO`,
//! and a store that uses syslog severities writes `err` and `crit`.
//! When no list matches a value, such as `Unspecified`, transect
//! guesses the line's level from its text.

use serde::Deserialize;

use super::level::Level;

#[derive(Clone, Debug, Default, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Levels {
    #[serde(default)]
    pub error: Vec<String>,
    #[serde(default)]
    pub warning: Vec<String>,
    #[serde(default)]
    pub plain: Vec<String>,
}

impl Levels {
    /// The levels, or the reason they are invalid. An empty prefix
    /// matches every value, so it would match every line.
    pub fn checked(self) -> Result<Self, String> {
        let lists = [
            ("error", &self.error),
            ("warning", &self.warning),
            ("plain", &self.plain),
        ];
        for (name, list) in lists {
            if let Some(at) = list.iter().position(|start| start.trim().is_empty()) {
                return Err(format!(
                    "levels.{name}[{at}] is empty, and would match every line"
                ));
            }
        }
        Ok(self)
    }

    /// The level whose list has a prefix of `value`, or `None` when no
    /// list does.
    pub fn named(&self, value: &str) -> Option<Level> {
        let value = value.trim().to_lowercase();
        let starts = |starts: &[String]| {
            starts
                .iter()
                .any(|start| value.starts_with(&start.to_lowercase()))
        };
        let lists = [
            (Level::Error, &self.error),
            (Level::Warning, &self.warning),
            (Level::Plain, &self.plain),
        ];
        lists
            .into_iter()
            .find(|(_, list)| starts(list))
            .map(|(level, _)| level)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn levels() -> Levels {
        serde_yaml_ng::from_str("{error: [err, fatal], warning: [warn], plain: [info, debug]}")
            .unwrap()
    }

    #[rstest]
    #[case::error("ERROR", Some(Level::Error))]
    #[case::fatal("Fatal", Some(Level::Error))]
    #[case::warn(" WARN ", Some(Level::Warning))]
    #[case::info("INFO", Some(Level::Plain))]
    #[case::debug("debug", Some(Level::Plain))]
    #[case::unspecified("Unspecified", None)]
    #[case::empty("", None)]
    fn a_value_matches_a_level_or_none(#[case] value: &str, #[case] expected: Option<Level>) {
        assert_eq!(levels().named(value), expected);
    }

    #[rstest]
    #[case::an_empty_error("{error: ['']}", "levels.error[0] is empty")]
    #[case::a_blank_plain("{error: [err], plain: [info, ' ']}", "levels.plain[1] is empty")]
    fn rejects_an_empty_prefix(#[case] levels: &str, #[case] message: &str) {
        let levels: Levels = serde_yaml_ng::from_str(levels).unwrap();
        let error = levels.checked().unwrap_err();
        assert!(
            error.contains(message),
            "the error is {error:?}, want it to contain {message:?}"
        );
    }

    #[rstest]
    fn empty_lists_match_nothing() {
        assert_eq!(Levels::default().named("ERROR"), None);
    }
}
