//! The meaning of the values of a log store's level field, which its
//! `LogSource` declares in `levels`.  Each list holds the starts of the
//! values that name an error, a warning, or a plain line, in any case.
//! A store that keeps OpenTelemetry's `severity_text` writes `ERROR`,
//! `Warn`, and `INFO`, and a store that keeps syslog's names writes
//! `err` and `crit`.  When no list names a value, such as
//! `Unspecified`, transect guesses the line's level from its text.

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
    /// The levels, or the reason they do not validate.  An empty start
    /// matches the start of every value, so it would name every line.
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

    /// The level that `value` names, matched by the start of the value,
    /// or `None` when no list names it.
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
        serde_yaml::from_str("{error: [err, fatal], warning: [warn], plain: [info, debug]}")
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
    fn a_value_names_a_level_or_none(#[case] value: &str, #[case] expected: Option<Level>) {
        assert_eq!(levels().named(value), expected);
    }

    #[rstest]
    #[case::an_empty_error("{error: ['']}", "levels.error[0] is empty")]
    #[case::a_blank_plain("{error: [err], plain: [info, ' ']}", "levels.plain[1] is empty")]
    fn refuses_an_empty_start(#[case] levels: &str, #[case] says: &str) {
        let levels: Levels = serde_yaml::from_str(levels).unwrap();
        let error = levels.checked().unwrap_err();
        assert!(error.contains(says), "{error:?} does not say {says:?}");
    }

    #[rstest]
    fn no_lists_name_nothing() {
        assert_eq!(Levels::default().named("ERROR"), None);
    }
}
