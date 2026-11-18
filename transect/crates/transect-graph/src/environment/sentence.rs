//! The heading and the counts of a pod's environment block.

use super::{Environment, Keys, Kind, Source};

/// The note under the block that tells the reader why it lists no values.
pub const NAMES_ONLY: &str =
    "Names only, never values: transect reads the pod spec, not the Secrets or ConfigMaps.";

impl Environment {
    /// "Environment · 29 variables", or "Environment" when the spec sets
    /// no variables.
    pub fn heading(&self) -> String {
        match self.variables {
            0 => "Environment".into(),
            1 => "Environment · 1 variable".into(),
            count => format!("Environment · {count} variables"),
        }
    }

    /// "21 plain values", or `None` when there are none.
    pub fn plain_text(&self) -> Option<String> {
        match self.plain {
            0 => None,
            1 => Some("1 plain value".into()),
            count => Some(format!("{count} plain values")),
        }
    }

    /// "2 from the pod's own fields", or `None` when there are none.
    pub fn fields_text(&self) -> Option<String> {
        (self.fields > 0).then(|| format!("{} from the pod's own fields", self.fields))
    }
}

impl Source {
    pub fn kind_text(&self) -> &'static str {
        match self.kind {
            Kind::Secret => "Secret",
            Kind::ConfigMap => "ConfigMap",
        }
    }

    /// "4 keys", "every key", "1 key · mounted".
    pub fn keys_text(&self) -> String {
        let keys = match &self.keys {
            Keys::Every => "every key".to_string(),
            Keys::Named(keys) if keys.len() == 1 => "1 key".into(),
            Keys::Named(keys) => format!("{} keys", keys.len()),
        };
        if self.mounted {
            format!("{keys} · mounted")
        } else {
            keys
        }
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;

    use rstest::rstest;

    use super::*;

    fn environment(variables: usize, plain: usize, fields: usize) -> Environment {
        Environment {
            sources: vec![],
            plain,
            fields,
            variables,
        }
    }

    #[rstest]
    #[case::none(environment(0, 0, 0), "Environment", None, None)]
    #[case::one(
        environment(1, 1, 0),
        "Environment · 1 variable",
        Some("1 plain value"),
        None
    )]
    #[case::several(
        environment(29, 21, 2),
        "Environment · 29 variables",
        Some("21 plain values"),
        Some("2 from the pod's own fields")
    )]
    fn writes_counts_as_text(
        #[case] environment: Environment,
        #[case] heading: &str,
        #[case] plain: Option<&str>,
        #[case] fields: Option<&str>,
    ) {
        assert_eq!(
            (
                environment.heading().as_str(),
                environment.plain_text().as_deref(),
                environment.fields_text().as_deref()
            ),
            (heading, plain, fields)
        );
    }

    fn source(keys: Keys, mounted: bool) -> Source {
        Source {
            kind: Kind::Secret,
            name: "database".into(),
            keys,
            mounted,
        }
    }

    fn named(count: usize) -> Keys {
        Keys::Named((0..count).map(|n| n.to_string()).collect::<BTreeSet<_>>())
    }

    #[rstest]
    #[case::several(source(named(4), false), "4 keys")]
    #[case::one_mounted(source(named(1), true), "1 key · mounted")]
    #[case::every(source(Keys::Every, false), "every key")]
    fn writes_the_keys_of_a_source(#[case] source: Source, #[case] text: &str) {
        assert_eq!(source.keys_text(), text);
    }
}
