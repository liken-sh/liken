//! The row of edge kinds in the main column. It has one checkbox for
//! each kind that this cluster has. Each checkbox links to the same view
//! with that kind switched on or off.

use transect_graph::edges::{Kind, Shown};

#[derive(Debug, Default, PartialEq)]
pub struct EdgeRow {
    pub toggles: Vec<Toggle>,
    /// What the kinds change on a page that draws no edges itself.
    pub affects: Option<&'static str>,
}

/// What the edge kinds change on the cluster map, which draws no lines.
pub const ON_THE_MAP: &str = "The map draws no lines. A selected namespace's relations, \
    the loop marks, and a loss's cascade follow the edges of the kinds that are on.";

#[derive(Debug, PartialEq)]
pub struct Toggle {
    pub label: &'static str,
    /// What an edge of this kind means.
    pub meaning: &'static str,
    pub on: bool,
    /// The link to this view with this kind switched.
    pub href: String,
}

impl EdgeRow {
    /// A toggle for each kind in `offered`. `here` is the view's path
    /// and selection without its `edges` parameter, and each toggle's
    /// link is built from it.
    pub fn build(offered: &[Kind], shown: &Shown, here: &str) -> Self {
        let toggles = offered.iter().map(|&kind| Toggle {
            label: kind.words(),
            meaning: kind.meaning(),
            on: shown.is_on(kind),
            href: with_edges(here, shown.toggled(kind, offered).param()),
        });
        Self {
            toggles: toggles.collect(),
            affects: None,
        }
    }

    /// Sets `affects`, for a page that draws no edges.
    pub fn affecting(self, affects: &'static str) -> Self {
        Self {
            affects: Some(affects),
            ..self
        }
    }
}

/// `here` with `edges` as a query parameter, or `here` unchanged when
/// `edges` is `None`, which means every kind is on.
pub fn with_edges(here: &str, edges: Option<String>) -> String {
    match edges {
        None => here.into(),
        Some(edges) if here.contains('?') => format!("{here}&edges={edges}"),
        Some(edges) => format!("{here}?edges={edges}"),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    const OFFERED: [Kind; 3] = [Kind::RunsOn, Kind::StoresOn, Kind::AllowedToCall];

    fn row(edges: Option<&str>, here: &str) -> Vec<(&'static str, bool, String)> {
        let row = EdgeRow::build(&OFFERED, &Shown::parse(edges), here);
        let toggles = row.toggles.into_iter();
        toggles.map(|t| (t.label, t.on, t.href)).collect()
    }

    #[rstest]
    #[case::all_on(None, "/", vec![
        ("runs on", true, "/?edges=stores-on,allowed-to-call".into()),
        ("stores on", true, "/?edges=runs-on,allowed-to-call".into()),
        ("allowed to call", true, "/?edges=runs-on,stores-on".into()),
    ])]
    #[case::one_off_with_a_selection(Some("runs-on,stores-on"), "/?select=shop", vec![
        ("runs on", true, "/?select=shop&edges=stores-on".into()),
        ("stores on", true, "/?select=shop&edges=runs-on".into()),
        ("allowed to call", false, "/?select=shop".into()),
    ])]
    fn links_each_kind_to_the_view_with_it_flipped(
        #[case] edges: Option<&str>,
        #[case] here: &str,
        #[case] expected: Vec<(&'static str, bool, String)>,
    ) {
        assert_eq!(row(edges, here), expected);
    }
}
