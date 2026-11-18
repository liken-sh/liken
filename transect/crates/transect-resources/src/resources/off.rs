//! The line a page shows in place of a section that needs a resource.
//! When no resource of that kind exists, the line says which resource
//! fills the section.  When one exists but did not load, the line names
//! it and links to `/resources`, where its condition gives the reason,
//! so a reader who already wrote the resource is not told to write it.

use super::{Directory, Status};

/// The line shown in place of an empty section.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OffLine {
    pub text: String,
    /// Whether the line ends by sending the reader to `/resources`.
    pub see_resources: bool,
}

impl OffLine {
    /// A line of `text` with no link to `/resources`.
    pub fn plain(text: impl Into<String>) -> Self {
        Self {
            text: text.into(),
            see_resources: false,
        }
    }
}

impl Directory {
    /// The name of the first document of `kind` that did not load.  A
    /// document left out as a duplicate does not count.
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

    /// The line for a section that needs a resource of `kind`: `text`
    /// when no document of `kind` failed to load, and otherwise
    /// `prefix` followed by "the <kind> <name> did not load".
    pub fn off(&self, kind: &str, prefix: &str, text: &str) -> OffLine {
        match self.unloaded(kind) {
            Some(name) => OffLine {
                text: format!("{prefix}the {kind} {} did not load", shown(name)),
                see_resources: true,
            },
            None => OffLine::plain(text),
        }
    }
}

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

    /// A directory of one file with one `MetricsSource` named `metrics`
    /// for each condition in `conditions`.
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
    #[case::none_there(vec![], OffLine::plain("a MetricsSource would show it"))]
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
