//! Where `transect` reads the intent for each `Secret` from: the object
//! that writes the `Secret`, and the `Secret`'s own annotations only
//! with `--secret-annotations`.  A pod page shows this beside each
//! `Secret` the pod reads, so a reader who sees no intent for a `Secret`
//! can tell why.

use std::collections::HashMap;

use transect_graph::edges::Object;

/// The sources of intent for `Secret` objects.
#[derive(Debug, Default)]
pub struct SecretIntent {
    reads_annotations: bool,
    /// The object that writes each `Secret`, keyed by the `Secret`.
    writers: HashMap<Object, Object>,
}

impl SecretIntent {
    /// `written` maps each object that writes a `Secret` to that
    /// `Secret`.
    pub fn new(reads_annotations: bool, written: &HashMap<Object, Object>) -> Self {
        let writers = written
            .iter()
            .map(|(writer, secret)| (secret.clone(), writer.clone()))
            .collect();
        Self {
            reads_annotations,
            writers,
        }
    }

    /// The sentence that a pod page shows for the `Secret` `name` in
    /// `namespace`, saying where its intent is read from.
    pub fn title(&self, namespace: &str, name: &str) -> String {
        let secret = Object::new("", "Secret", Some(namespace), name);
        match (self.writers.get(&secret), self.reads_annotations) {
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
        let writer = Object::new("vault.example.com", "VaultItem", Some("shop"), "db");
        let secret = Object::new("", "Secret", Some("shop"), "db-login");
        HashMap::from([(writer, secret)])
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
        #[case] reads_annotations: bool,
        #[case] title: &str,
    ) {
        let secrets = SecretIntent::new(reads_annotations, &written());
        assert_eq!(secrets.title("shop", name), title);
    }
}
