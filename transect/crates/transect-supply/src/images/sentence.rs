//! The text that an `Origin` shows for an image: its commit, version,
//! description, and documentation from the image's labels, and a note
//! when there is no commit to show.

use super::origin::Origin;
use crate::forge::Commit;
use transect_graph::edges::Provenance;

impl Origin {
    /// The URL of the commit's page, when transect can build one.
    pub fn href(&self) -> Option<&str> {
        match self.commit()? {
            Commit::Link { href, .. } => Some(href),
            Commit::Text { .. } => None,
        }
    }

    /// The commit as text: "app@0a1b2c3" for a link, otherwise the source
    /// and the revision.
    pub fn code_text(&self) -> Option<String> {
        Some(match self.commit()? {
            Commit::Link { text, .. } => text.clone(),
            Commit::Text { source, revision } if revision.is_empty() => source.clone(),
            Commit::Text { source, revision } => format!("{source} at {revision}"),
        })
    }

    /// The reason there is no commit to show, when there is none.
    pub fn note(&self) -> Option<String> {
        match self {
            Self::Reading => Some("reading the image's labels".into()),
            Self::Unreadable(failure) => Some(format!("labels unreadable: {}", failure.note())),
            Self::Read(built) if built.commit.is_none() => Some("no source label".into()),
            Self::Read(_) => None,
        }
    }

    pub fn version_text(&self) -> Option<String> {
        match self {
            Self::Read(built) => built
                .version
                .as_ref()
                .map(|version| format!("version {version}")),
            _ => None,
        }
    }

    pub fn description(&self) -> Option<&str> {
        match self {
            Self::Read(built) => built.description.as_deref(),
            _ => None,
        }
    }

    /// The description comes from the image's own labels.
    pub fn description_provenance(&self) -> Provenance {
        Provenance::Image(None)
    }

    pub fn documentation(&self) -> Option<&str> {
        match self {
            Self::Read(built) => built.documentation.as_deref(),
            _ => None,
        }
    }

    fn commit(&self) -> Option<&Commit> {
        match self {
            Self::Read(built) => built.commit.as_ref(),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::Failure;
    use super::super::origin::Built;
    use super::*;

    fn read(commit: Option<Commit>) -> Origin {
        Origin::Read(Built {
            commit,
            source: None,
            version: None,
            description: None,
            documentation: None,
        })
    }

    fn text(source: &str, revision: &str) -> Option<Commit> {
        Some(Commit::Text {
            source: source.into(),
            revision: revision.into(),
        })
    }

    #[rstest]
    #[case::a_link(
        read(Some(Commit::Link { href: "https://github.com/acme/app/commit/abc".into(), text: "app@abc".into() })),
        Some("app@abc"),
        None
    )]
    #[case::text_with_a_revision(
        read(text("https://code.example.com/team/app", "abc")),
        Some("https://code.example.com/team/app at abc"),
        None
    )]
    #[case::text_without_one(
        read(text("https://code.example.com/team/app", "")),
        Some("https://code.example.com/team/app"),
        None
    )]
    #[case::no_source(read(None), None, Some("no source label"))]
    #[case::reading(Origin::Reading, None, Some("reading the image's labels"))]
    #[case::unreadable(
        Origin::Unreadable(Failure::NeedsCredentials),
        None,
        Some("labels unreadable: the registry requires credentials that transect does not have")
    )]
    fn writes_the_code_text_and_the_note(
        #[case] origin: Origin,
        #[case] code: Option<&str>,
        #[case] note: Option<&str>,
    ) {
        assert_eq!(
            (origin.code_text().as_deref(), origin.note().as_deref()),
            (code, note)
        );
    }
}
