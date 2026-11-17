//! The line a page shows where a resource of `transect`'s would fill a
//! section: what would turn it on, or, when a resource of that kind is
//! there but did not load, that it did not, with a link to
//! `/resources`.  The link sends a reader who wrote the resource to the
//! condition that states what is wrong with it, instead of a line that
//! tells them to write it.

use super::{Directory, Status};

/// One line in place of what a page cannot show.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OffLine {
    pub text: String,
    /// Whether the line ends by sending the reader to `/resources`.
    pub see_resources: bool,
}

impl OffLine {
    /// `text`, as the page shows it.
    pub fn said(text: impl Into<String>) -> Self {
        Self {
            text: text.into(),
            see_resources: false,
        }
    }
}

impl Directory {
    /// The name of the first document of `kind` that is there but did
    /// not load, other than one left out as a second copy of a resource
    /// that did.
    pub fn unloaded(&self, kind: &str) -> Option<&str> {
        let documents = self.files.iter().flat_map(|file| &file.documents);
        let mut found = documents.filter(|document| document.kind == kind);
        let failed = found.find(|document| {
            document.conditions.iter().any(|condition| {
                condition.kind == "Loaded"
                    && condition.status == Status::False
                    && condition.reason != "Duplicate"
            })
        })?;
        Some(failed.name.as_str())
    }

    /// `said` for a section that a resource of `kind` would fill, or,
    /// when one is there but did not load, `prefix` and a line that
    /// states that.
    pub fn off(&self, kind: &str, prefix: &str, said: &str) -> OffLine {
        match self.unloaded(kind) {
            Some(name) => OffLine {
                text: format!("{prefix}the {kind} {} did not load", shown(name)),
                see_resources: true,
            },
            None => OffLine::said(said),
        }
    }
}

/// A document's name as a line shows it, or "with no name".
fn shown(name: &str) -> &str {
    match name {
        "" => "with no name",
        name => name,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::{Condition, Document, File};
    use super::*;

    /// A directory of one file, with a `MetricsSource` named `metrics` for
    /// each of `conditions`, the one condition loading it gave.
    fn directory(conditions: &[Condition]) -> Directory {
        let documents = conditions.iter().map(|condition| Document {
            kind: "MetricsSource".into(),
            name: "metrics".into(),
            conditions: vec![condition.clone()],
        });
        Directory {
            files: vec![File {
                path: "a.yaml".into(),
                problem: None,
                documents: documents.collect(),
            }],
            ..Directory::default()
        }
    }

    fn invalid() -> Condition {
        Condition::left_out("Invalid", "spec.nodelabels is not a field")
    }

    #[rstest]
    #[case::none_there(vec![], None)]
    #[case::one_that_loaded(vec![Condition::loaded()], None)]
    #[case::one_that_did_not(vec![invalid()], Some("metrics"))]
    #[case::a_second_copy(
        vec![Condition::loaded(), Condition::left_out("Duplicate", "the one read first is used")],
        None
    )]
    fn finds_a_resource_that_did_not_load(
        #[case] conditions: Vec<Condition>,
        #[case] expected: Option<&str>,
    ) {
        assert_eq!(directory(&conditions).unloaded("MetricsSource"), expected);
    }

    #[rstest]
    #[case::none_there(vec![], OffLine::said("a MetricsSource would show it"))]
    #[case::one_that_did_not(
        vec![invalid()],
        OffLine {
            text: "no usage: the MetricsSource metrics did not load".into(),
            see_resources: true,
        }
    )]
    fn says_what_would_show_it_or_what_did_not_load(
        #[case] conditions: Vec<Condition>,
        #[case] expected: OffLine,
    ) {
        let line = directory(&conditions).off(
            "MetricsSource",
            "no usage: ",
            "a MetricsSource would show it",
        );
        assert_eq!(line, expected);
    }
}
