//! Which edge kinds a view shows.  A reader turns kinds off to see one
//! relation alone, such as only where things store, and the kinds that
//! are on are part of the view's URL, so that view is a link.
//!
//! Only the kinds a reader knows from the map can be turned off.  A
//! `Service`'s selects and an `Ingress`'s routes-to draw the workload
//! page's entry, and owner references decide what a workload is, so
//! they stay on.

use std::collections::BTreeSet;

use super::Kind;

/// The kinds a reader can turn off, in the order the row draws them.
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

/// The edge kinds turned off in one view.  Every kind is on unless the
/// URL turns it off, and a set is kept in one form, so two URLs that
/// turn on the same kinds are the same view.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub struct Shown {
    off: BTreeSet<Kind>,
}

impl Kind {
    /// The kind's name in a URL, or none for a kind that stays on.
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
    /// The kinds `edges` names, comma-separated by slug, turned on and
    /// every other off.  No `edges` turns every kind on, and an empty one
    /// turns every kind off; a name that matches no slug is skipped.
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

    /// The value of `edges` for this view, or none when every kind is on,
    /// so the plain URL stays plain.
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

    /// The view with `kind` flipped.  The kinds the row does not offer,
    /// such as one whose source the cluster lacks, have no edges, so
    /// they count as off once any kind is: a URL then names only kinds
    /// the reader can see.  Turning on the last of `offered` that was off
    /// turns every kind on, so the view returns to the plain URL.
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
