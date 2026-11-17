//! How `transect` reads the intent written for each `Secret`.  A pod
//! page shows this on each `Secret` the pod reads, so a reader who
//! finds a dependency missing learns why.  `transect` reads a
//! `Secret`'s intent from the object that writes it, and from the
//! `Secret`'s own annotations only with `--secret-annotations`.

use std::collections::HashMap;

use transect_graph::edges::Object;

/// Where intent for each `Secret` is read from.
#[derive(Debug, Default)]
pub struct SecretIntent {
    /// Whether `transect` watches the annotations of Secrets.
    watched: bool,
    /// The object that writes each `Secret`, by the `Secret`.
    writers: HashMap<Object, Object>,
}

impl SecretIntent {
    /// `written` names the `Secret` each producer writes.
    pub fn new(watched: bool, written: &HashMap<Object, Object>) -> Self {
        let writers = written
            .iter()
            .map(|(producer, secret)| (secret.clone(), producer.clone()))
            .collect();
        Self { watched, writers }
    }

    /// How the intent for the `Secret` `name` in `namespace` is read, for
    /// its title on a pod page.
    pub fn title(&self, namespace: &str, name: &str) -> String {
        let secret = Object::new("", "Secret", Some(namespace), name);
        match (self.writers.get(&secret), self.watched) {
            (Some(writer), true) => format!(
                "transect reads this Secret's annotations, and the intent on {} {}, which writes it",
                writer.kind, writer.name
            ),
            (Some(writer), false) => format!(
                "transect reads this Secret's intent from {} {}, which writes it",
                writer.kind, writer.name
            ),
            (None, true) => "transect reads this Secret's annotations, never its data".into(),
            (None, false) => "transect does not read this Secret's annotations".into(),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn written() -> HashMap<Object, Object> {
        let producer = Object::new("vault.example.com", "VaultItem", Some("shop"), "db");
        let secret = Object::new("", "Secret", Some("shop"), "db-login");
        HashMap::from([(producer, secret)])
    }

    #[rstest]
    #[case::written_and_watched(
        "db-login",
        true,
        "transect reads this Secret's annotations, and the intent on VaultItem db, which writes it"
    )]
    #[case::written(
        "db-login",
        false,
        "transect reads this Secret's intent from VaultItem db, which writes it"
    )]
    #[case::watched(
        "api-key",
        true,
        "transect reads this Secret's annotations, never its data"
    )]
    #[case::neither("api-key", false, "transect does not read this Secret's annotations")]
    fn says_how_a_secrets_intent_is_read(
        #[case] name: &str,
        #[case] watched: bool,
        #[case] title: &str,
    ) {
        let secrets = SecretIntent::new(watched, &written());
        assert_eq!(secrets.title("shop", name), title);
    }
}
