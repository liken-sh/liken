//! The Runs box on a workload's page: one row per image that the
//! workload's pods run.  Each fact has its own labeled cell, so the
//! image, its source repository, its version, its commit, and the time
//! of the last rollout show as separate facts, not as one run of lines.

use super::origin::Origin;
use crate::forge::Commit;

/// The rows of a workload's Runs box.
#[derive(Debug, Default, PartialEq)]
pub struct Runs {
    pub rows: Vec<Row>,
}

/// One image a workload runs, once per digest its pods run.
#[derive(Debug, PartialEq)]
pub struct Row {
    pub image: String,
    /// The repository its source label names, without the scheme.
    pub repository: Option<String>,
    /// Why the repository and commit cells are empty, when they are.
    pub note: Option<String>,
    pub version: Option<String>,
    pub commit: Option<Cell>,
    /// When the workload last rolled out, which is the same in every row
    /// of the workload.
    pub rolled: Option<String>,
    /// The description from the image's labels, unless the card already
    /// opens with that description.
    pub description: Option<String>,
    pub documentation: Option<String>,
}

/// A cell's text, and the page that the text links to, if any.
#[derive(Debug, PartialEq)]
pub struct Cell {
    pub text: String,
    pub href: Option<String>,
}

impl Runs {
    /// The rows for `images`, in order.  Each image gets one row for each
    /// of its origins in `origins`, or one row with no origin when
    /// transect has not looked up its labels.  `describes` returns
    /// whether a row shows its image's description.  A row leaves the
    /// description out when the card already opens with it.
    pub fn of(
        images: &[String],
        origins: &[(String, Origin)],
        rolled: Option<String>,
        describes: impl Fn(&str) -> bool,
    ) -> Self {
        let rows = images.iter().flat_map(|image| {
            let found: Vec<&Origin> = origins
                .iter()
                .filter(|(name, _)| name == image)
                .map(|(_, origin)| origin)
                .collect();
            let row = |origin: Option<&Origin>| Row::of(image, origin, &rolled, describes(image));
            match found.is_empty() {
                true => vec![row(None)],
                false => found.into_iter().map(|origin| row(Some(origin))).collect(),
            }
        });
        Self {
            rows: rows.collect(),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.rows.is_empty()
    }
}

impl Row {
    fn of(image: &str, origin: Option<&Origin>, rolled: &Option<String>, describe: bool) -> Self {
        let built = match origin {
            Some(Origin::Read(built)) => Some(built),
            _ => None,
        };
        let commit = built.and_then(|built| built.commit.as_ref()).map(cell);
        Self {
            image: image.into(),
            repository: built
                .and_then(|built| built.source.as_deref())
                .map(without_scheme),
            note: origin.and_then(Origin::note),
            version: built.and_then(|built| built.version.clone()),
            commit: commit.flatten(),
            rolled: rolled.clone(),
            description: describe
                .then(|| built.and_then(|built| built.description.clone()))
                .flatten(),
            documentation: built.and_then(|built| built.documentation.clone()),
        }
    }
}

/// The commit cell: the short revision, with a link when transect can
/// build one for the forge, or `None` for a source label with no
/// revision.
fn cell(commit: &Commit) -> Option<Cell> {
    match commit {
        Commit::Link { href, text } => Some(Cell {
            text: text.rsplit('@').next().unwrap_or(text).into(),
            href: Some(href.clone()),
        }),
        Commit::Text { revision, .. } if revision.is_empty() => None,
        Commit::Text { revision, .. } => Some(Cell {
            text: revision.chars().take(12).collect(),
            href: None,
        }),
    }
}

fn without_scheme(source: &str) -> String {
    let source = source.trim_end_matches('/').trim_end_matches(".git");
    let source = source.split_once("://").map_or(source, |(_, rest)| rest);
    source.into()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::Failure;
    use super::super::origin::Built;
    use super::*;

    fn built(commit: Option<Commit>, source: Option<&str>) -> Origin {
        Origin::Read(Built {
            commit,
            source: source.map(Into::into),
            version: Some("1.4.2".into()),
            description: Some("A web shop.".into()),
            documentation: None,
        })
    }

    fn link() -> Option<Commit> {
        Some(Commit::Link {
            href: "https://forge.example.test/acme/shop/commit/0a1b2c3d".into(),
            text: "shop@0a1b2c3".into(),
        })
    }

    /// A row's repository, note, version, commit, and description.
    type Cells = (
        Option<String>,
        Option<String>,
        Option<String>,
        Option<String>,
        Option<String>,
    );

    /// The cells of the one row of `shop:1.4.2` with `origin`.
    fn cells(origin: Option<Origin>, describes: bool) -> Cells {
        let images = vec!["shop:1.4.2".to_string()];
        let origins: Vec<(String, Origin)> =
            origin.into_iter().map(|o| (images[0].clone(), o)).collect();
        let runs = Runs::of(&images, &origins, Some("rolled 3d ago".into()), |_| {
            describes
        });
        let [row] = &runs.rows[..] else {
            panic!("one row, not {}", runs.rows.len())
        };
        let commit = row.commit.as_ref().map(|cell| cell.text.clone());
        (
            row.repository.clone(),
            row.note.clone(),
            row.version.clone(),
            commit,
            row.description.clone(),
        )
    }

    fn some(text: &str) -> Option<String> {
        Some(text.into())
    }

    #[rstest]
    #[case::a_linked_commit(Some(built(link(), Some("https://forge.example.test/acme/shop.git"))), true, (some("forge.example.test/acme/shop"), None, some("1.4.2"), some("0a1b2c3"), some("A web shop.")))]
    #[case::a_revision_as_text(Some(built(Some(Commit::Text { source: "https://code.example.test/shop".into(), revision: "0a1b2c3d4e5f6a7b".into() }), Some("https://code.example.test/shop"))), true, (some("code.example.test/shop"), None, some("1.4.2"), some("0a1b2c3d4e5f"), some("A web shop.")))]
    #[case::no_source_label(Some(built(None, None)), true, (None, some("no source label"), some("1.4.2"), None, some("A web shop.")))]
    #[case::described_by_the_card(Some(built(link(), Some("https://forge.example.test/acme/shop"))), false, (some("forge.example.test/acme/shop"), None, some("1.4.2"), some("0a1b2c3"), None))]
    #[case::unreadable(Some(Origin::Unreadable(Failure::NeedsCredentials)), true, (None, some("labels unreadable: the registry requires credentials that transect does not have"), None, None, None))]
    #[case::not_looked_up(None, true, (None, None, None, None, None))]
    fn fills_each_cell(
        #[case] origin: Option<Origin>,
        #[case] describes: bool,
        #[case] expected: Cells,
    ) {
        assert_eq!(cells(origin, describes), expected);
    }
}
