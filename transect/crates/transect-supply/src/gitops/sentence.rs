//! The text that a card's code section shows, apart from names, URLs,
//! and the GitOps tool's own messages.

use super::Off;
use super::Ready;
use super::code::{Declared, Release};
use super::forge::Place;
use transect_graph::edges::Object;
use transect_resources::resources::OffLine;

impl Off {
    /// The reason that the card shows no code.
    pub fn note(&self) -> OffLine {
        match self {
            Self::NoKnowledge => OffLine::plain(
                "reconciles from is off: no kind knowledge declares applies for a kind this cluster serves",
            ),
            Self::Listing(kinds) => OffLine::plain(format!(
                "reconciles from is off for now: transect is still listing {kinds}"
            )),
            Self::Refused(kinds) => OffLine::plain(format!(
                "reconciles from is off: transect may not list {kinds}"
            )),
            Self::Unloaded(name) => OffLine {
                text: format!(
                    "reconciles from is off: no kind knowledge declares applies, and the KindKnowledge {name} did not load"
                ),
                see_resources: true,
            },
        }
    }
}

impl Place {
    /// The URL of the directory's page, when transect can link to the
    /// directory.
    pub fn href(&self) -> Option<&str> {
        match self {
            Self::Link { href, .. } => Some(href),
            Self::Text { .. } => None,
        }
    }

    /// "fleet/shop" for a link, or the repository's URL and the path.
    pub fn text(&self) -> String {
        match self {
            Self::Link { text, .. } => text.clone(),
            Self::Text { repository, path } => format!("{repository} {path}"),
        }
    }
}

impl Declared {
    /// The applier's kind and name, as "Tree shop", or "Tree ops/shop"
    /// when its namespace is not `here`.
    pub fn name_text(&self, here: Option<&str>) -> String {
        named(&self.applier, here)
    }

    /// "ready", or the reason that the applier is not ready.
    pub fn state_text(&self) -> String {
        match &self.ready {
            Ready::True => "ready".into(),
            Ready::False(message) => format!("not ready: {message}"),
            Ready::Unknown(message) => format!("reconciling: {message}"),
            Ready::Missing => "not reconciled yet".into(),
        }
    }

    /// What the card shows beside the directory when the source label of
    /// a running image names the same repository.
    pub fn built_here_text(&self) -> &'static str {
        "a running image is built from this repository too"
    }

    /// Whether the card shows its applier as a problem.
    pub fn failing(&self) -> bool {
        matches!(self.ready, Ready::False(_))
    }
}

impl Release {
    /// The release's kind and name, as "Release cache", with its
    /// namespace when it is not `here`.
    pub fn name_text(&self, here: Option<&str>) -> String {
        named(&self.object, here)
    }

    /// ", chart cache from https://charts.example.com", or only the
    /// parts that the release sets.
    pub fn chart_text(&self) -> String {
        match (&self.chart, &self.repository) {
            (Some(chart), Some(repository)) => format!(", chart {chart} from {repository}"),
            (Some(chart), None) => format!(", chart {chart}"),
            (None, Some(repository)) => format!(", from {repository}"),
            (None, None) => String::new(),
        }
    }
}

fn named(object: &Object, here: Option<&str>) -> String {
    transect_graph::links::kind_and_name_in(object, here)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::object;
    use super::*;

    fn declared(ready: Ready) -> Declared {
        Declared {
            applier: object("Tree", "ops", "shop"),
            href: None,
            ready,
            place: None,
            repository: None,
            built_here: false,
        }
    }

    #[rstest]
    #[case::ready(Ready::True, Some("ops"), "Tree shop ready")]
    #[case::in_another_namespace(Ready::True, Some("shop"), "Tree ops/shop ready")]
    #[case::not_ready(
        Ready::False("build failed".into()),
        Some("ops"),
        "Tree shop not ready: build failed"
    )]
    #[case::reconciling(
        Ready::Unknown("Reconciliation in progress".into()),
        Some("ops"),
        "Tree shop reconciling: Reconciliation in progress"
    )]
    #[case::not_yet(Ready::Missing, Some("ops"), "Tree shop not reconciled yet")]
    fn words_an_appliers_readiness(
        #[case] ready: Ready,
        #[case] here: Option<&str>,
        #[case] text: &str,
    ) {
        let declared = declared(ready);
        let words = format!("{} {}", declared.name_text(here), declared.state_text());
        assert_eq!(words, text);
    }

    #[rstest]
    #[case::a_chart_from_a_repository(
        Some("cache"),
        Some("https://charts.example.com"),
        "Release cache, chart cache from https://charts.example.com"
    )]
    #[case::a_chart(Some("cache"), None, "Release cache, chart cache")]
    #[case::a_chart_by_reference(
        None,
        Some("oci://registry.example.com/cache"),
        "Release cache, from oci://registry.example.com/cache"
    )]
    #[case::nothing_more(None, None, "Release cache")]
    fn words_a_release(
        #[case] chart: Option<&str>,
        #[case] repository: Option<&str>,
        #[case] text: &str,
    ) {
        let release = Release {
            object: object("Release", "shop", "cache"),
            href: None,
            chart: chart.map(Into::into),
            repository: repository.map(Into::into),
        };
        let words = format!(
            "{}{}",
            release.name_text(Some("shop")),
            release.chart_text()
        );
        assert_eq!(words, text);
    }
}
