//! Which edge kinds a view shows. A reader can turn kinds off to see one
//! relation alone, and the `edges` URL parameter lists the kinds that
//! are on, so a link opens the same view.
//!
//! Only the kinds in `TOGGLED` can be turned off. The workload page
//! needs `Selects` and `RoutesTo` edges, and `ManagedBy` edges decide
//! what a workload is, so those kinds and `PartOf` always show.

use std::collections::BTreeSet;

use super::Kind;

/// The kinds a reader can turn off, in the order the row shows them.
pub const TOGGLED: [Kind; 8] = [
    Kind::RunsOn,
    Kind::DependsOn,
    Kind::AuthenticatesThrough,
    Kind::StoresOn,
    Kind::Holds,
    Kind::ReconcilesFrom,
    Kind::ControlledBy,
    Kind::AllowedToCall,
];

/// The edge kinds turned off in one view. They are a set, so two URLs
/// that list the same kinds in a different order give equal views.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Shown {
    off: BTreeSet<Kind>,
}

impl Kind {
    /// The kind's name in the URL, or none for a kind that cannot be
    /// turned off.
    pub fn slug(self) -> Option<&'static str> {
        Some(match self {
            Self::RunsOn => "runs-on",
            Self::DependsOn => "depends-on",
            Self::AuthenticatesThrough => "authenticates-through",
            Self::StoresOn => "stores-on",
            Self::Holds => "holds",
            Self::ReconcilesFrom => "reconciles-from",
            Self::ControlledBy => "controlled-by",
            Self::AllowedToCall => "allowed-to-call",
            Self::ManagedBy | Self::Selects | Self::RoutesTo | Self::PartOf => return None,
        })
    }
}

impl Shown {
    /// The view for the `edges` URL value, a comma-separated list of
    /// slugs. Listed kinds are on and every other toggled kind is off.
    /// A missing value turns every kind on, an empty value turns every
    /// kind off, and a name that is not a slug is ignored.
    pub fn parse(edges: Option<&str>) -> Self {
        let Some(edges) = edges else {
            return Self::default();
        };
        let on: Vec<&str> = edges.split(',').map(str::trim).collect();
        let off = TOGGLED
            .into_iter()
            .filter(|kind| !kind.slug().is_some_and(|slug| on.contains(&slug)))
            .collect();
        Self { off }
    }

    pub fn is_on(&self, kind: Kind) -> bool {
        !self.off.contains(&kind)
    }

    pub fn all_on(&self) -> bool {
        self.off.is_empty()
    }

    /// The `edges` URL value for this view, or none when every kind is
    /// on, so the default view has a URL with no parameter.
    pub fn param(&self) -> Option<String> {
        if self.all_on() {
            return None;
        }
        let on: Vec<&str> = TOGGLED
            .into_iter()
            .filter(|&kind| self.is_on(kind))
            .filter_map(Kind::slug)
            .collect();
        Some(on.join(","))
    }

    /// The view with `kind` switched, where `offered` is the kinds the
    /// row shows. A kind is not offered when it has no edges, such as
    /// one whose source the cluster lacks. Turning off the first kind
    /// also turns off every kind not offered, so the URL lists only kinds
    /// the reader can see. When no offered kind is off any more, every
    /// kind is turned on again and the URL has no parameter.
    pub fn toggled(&self, kind: Kind, offered: &[Kind]) -> Self {
        let mut off = self.off.clone();
        if self.all_on() {
            off.extend(TOGGLED.into_iter().filter(|kind| !offered.contains(kind)));
        }
        if !off.remove(&kind) {
            off.insert(kind);
        }
        if offered.iter().all(|kind| !off.contains(kind)) {
            off.clear();
        }
        Self { off }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::absent(None, None)]
    #[case::every_kind(
        Some(
            "runs-on,depends-on,authenticates-through,stores-on,holds,reconciles-from,controlled-by,allowed-to-call"
        ),
        None
    )]
    #[case::one_kind(Some("stores-on"), Some("stores-on"))]
    #[case::in_the_rows_order(Some("holds,runs-on"), Some("runs-on,holds"))]
    #[case::none(Some(""), Some(""))]
    #[case::an_unknown_name(Some("stores-on,teleports"), Some("stores-on"))]
    fn reads_the_kinds_turned_on(#[case] edges: Option<&str>, #[case] param: Option<&str>) {
        assert_eq!(Shown::parse(edges).param().as_deref(), param);
    }

    #[rstest]
    #[case::off_a_kind(None, Kind::StoresOn, Some("runs-on,depends-on"))]
    #[case::back_on(Some("stores-on"), Kind::RunsOn, Some("runs-on,stores-on"))]
    #[case::the_last_offered_back_on(Some("runs-on,stores-on"), Kind::DependsOn, None)]
    fn flips_one_kind(
        #[case] edges: Option<&str>,
        #[case] kind: Kind,
        #[case] param: Option<&str>,
    ) {
        let offered = [Kind::RunsOn, Kind::DependsOn, Kind::StoresOn];
        let shown = Shown::parse(edges).toggled(kind, &offered);
        assert_eq!(shown.param().as_deref(), param);
    }

    #[rstest]
    #[case::a_toggled_kind_off(Some("stores-on"), Kind::RunsOn, false)]
    #[case::a_toggled_kind_on(Some("stores-on"), Kind::StoresOn, true)]
    #[case::a_kind_that_stays_on(Some(""), Kind::Selects, true)]
    fn says_which_kinds_are_on(#[case] edges: Option<&str>, #[case] kind: Kind, #[case] on: bool) {
        assert_eq!(Shown::parse(edges).is_on(kind), on);
    }
}
