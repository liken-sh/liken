//! The text of the matrix: each cell's title, the headers, and the
//! legend. All other text in the matrix comes from the cluster.

use std::collections::BTreeMap;

use super::{Cell, Matrix, Skip};

/// The legend under the matrix. Each entry is the CSS class, the glyph,
/// and the label for one kind of cell, so that the legend's sample of
/// each looks like the cells it describes.
pub const LEGEND: [(&str, &str, &str); 5] = [
    ("cell cell-ready", "", "ready"),
    ("cell cell-restarting", "↻", "restarted within the hour"),
    ("cell cell-not-ready", "", "not ready"),
    (
        "cell cell-missing",
        "?",
        "no pod, and nothing excludes this machine",
    ),
    ("cell cell-skipped", "–", "not scheduled here, by design"),
];

impl Cell {
    pub fn class(&self) -> &'static str {
        match self {
            Self::Ready => "cell cell-ready",
            Self::Restarting { .. } => "cell cell-restarting",
            Self::NotReady => "cell cell-not-ready",
            Self::Missing => "cell cell-missing",
            Self::Skipped(_) => "cell cell-skipped",
        }
    }

    pub fn title(&self) -> String {
        match self {
            Self::Ready => "ready".into(),
            Self::Restarting { restarts } => {
                format!("restarted within the hour, {}", count(*restarts, "restart"))
            }
            Self::NotReady => "not ready".into(),
            Self::Missing => "no pod here, and nothing excludes this machine".into(),
            Self::Skipped(Skip::NodeSelector) => "not scheduled here by its node selector".into(),
            Self::Skipped(Skip::Affinity) => "not scheduled here by its node affinity".into(),
            Self::Skipped(Skip::Taint(key)) => format!("not scheduled here: taint {key}"),
        }
    }

    /// The glyph inside the cell. A skipped cell has the same dash for
    /// every reason, and its title states the reason.
    pub fn glyph(&self) -> &'static str {
        match self {
            Self::Ready | Self::NotReady => "",
            Self::Restarting { .. } => "↻",
            Self::Missing => "?",
            Self::Skipped(_) => "–",
        }
    }
}

impl Matrix {
    /// The heading above the matrix, such as "10 DaemonSets · 88 pods".
    pub fn header(&self) -> String {
        format!(
            "{} · {}",
            count(self.rows.len(), "DaemonSet"),
            count(self.pods, "pod")
        )
    }

    /// The heading above the names of the other top owners, with a count
    /// for each kind, such as "2 Deployments · 1 StatefulSet".
    pub fn others_header(&self) -> String {
        let mut kinds: BTreeMap<&str, usize> = BTreeMap::new();
        for other in &self.others {
            *kinds.entry(other.kind.as_str()).or_default() += 1;
        }
        let counts: Vec<String> = kinds
            .into_iter()
            .map(|(kind, number)| count(number, kind))
            .collect();
        counts.join(" · ")
    }
}

/// The label over the matrix.
pub const DAEMON_SETS: &str = "On each machine";
/// The label over the names of the other top owners.
pub const OTHERS: &str = "Also here";

fn count(number: impl std::fmt::Display, noun: &str) -> String {
    let number = number.to_string();
    format!("{number} {noun}{}", if number == "1" { "" } else { "s" })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::Other;
    use super::*;

    #[rstest]
    #[case::ready(Cell::Ready, "ready")]
    #[case::restarting(Cell::Restarting { restarts: 3 }, "restarted within the hour, 3 restarts")]
    #[case::restarted_once(Cell::Restarting { restarts: 1 }, "restarted within the hour, 1 restart")]
    #[case::missing(Cell::Missing, "no pod here, and nothing excludes this machine")]
    #[case::selector(
        Cell::Skipped(Skip::NodeSelector),
        "not scheduled here by its node selector"
    )]
    #[case::affinity(
        Cell::Skipped(Skip::Affinity),
        "not scheduled here by its node affinity"
    )]
    #[case::taint(Cell::Skipped(Skip::Taint("dedicated".into())), "not scheduled here: taint dedicated")]
    fn a_cell_says_what_it_shows(#[case] cell: Cell, #[case] title: &str) {
        assert_eq!(cell.title(), title);
    }

    fn other(kind: &str, name: &str) -> Other {
        Other {
            kind: kind.into(),
            name: name.into(),
            href: None,
            warm: false,
        }
    }

    #[rstest]
    fn the_others_count_by_kind() {
        let matrix = Matrix {
            machines: Vec::new(),
            rows: Vec::new(),
            pods: 1,
            others: vec![
                other("Deployment", "api"),
                other("StatefulSet", "db"),
                other("Deployment", "web"),
            ],
        };
        assert_eq!(matrix.header(), "0 DaemonSets · 1 pod");
        assert_eq!(matrix.others_header(), "2 Deployments · 1 StatefulSet");
    }
}
