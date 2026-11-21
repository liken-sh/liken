//! The head of a sketch: a tab for each elevation of the kind, in the
//! author's order, with the current one marked, and the author who
//! states them. Each elevation has a URL of its own under the object's,
//! and the object's own URL draws the first. An elevation that the URL
//! names and the kind does not have draws the first, with a line that
//! says so.

use transect_graph::links::Reference;
use transect_resources::knowledge::Elevation;

/// The head of the sketch of one elevation.
#[derive(Debug, PartialEq)]
pub struct Head {
    /// A tab for each elevation, or none when the kind has one, because
    /// a lone tab leads nowhere.
    pub tabs: Vec<ElevationTab>,
    /// Who states the elevations: the controller that the knowledge
    /// names, or else the `KindKnowledge`, as the card names it.
    pub author: Reference,
    /// The line that says which elevation the URL named, when the kind
    /// has no elevation of that slug.
    pub missing: Option<String>,
}

/// One elevation's tab.
#[derive(Debug, PartialEq, Eq)]
pub struct ElevationTab {
    pub name: String,
    pub href: String,
    pub current: bool,
}

/// The URL of the elevation with `slug` of the object at `path`.
pub fn href(path: &str, slug: &str) -> String {
    format!("{path}/elevations/{slug}")
}

/// The elevation of `elevations` that the URL names by `slug`, or the
/// first when it names none or one the kind does not have, with the
/// head of its sketch. It is none when the kind has no elevation.
pub fn choose<'a>(
    elevations: &'a [Elevation],
    slug: Option<&str>,
    path: &str,
    kind: &str,
    author: Reference,
) -> Option<(&'a Elevation, Head)> {
    let named = slug.and_then(|slug| elevations.iter().find(|e| e.slug == slug));
    let elevation = named.or(elevations.first())?;
    let missing = slug.filter(|_| named.is_none()).map(|slug| {
        format!(
            "The knowledge of {kind} states no elevation {slug}, so the page draws {}.",
            elevation.name
        )
    });
    let tabs = elevations.iter().map(|e| ElevationTab {
        name: e.name.clone(),
        href: href(path, &e.slug),
        current: e.slug == elevation.slug,
    });
    let tabs = match elevations.len() {
        1 => Vec::new(),
        _ => tabs.collect(),
    };
    let head = Head {
        tabs,
        author,
        missing,
    };
    Some((elevation, head))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn elevation(name: &str) -> Elevation {
        Elevation {
            name: name.into(),
            slug: transect_resources::knowledge::elevation::slug(name),
            question: "What is it?".into(),
            grid: transect_resources::knowledge::Grid { rows: Vec::new() },
            wires: Vec::new(),
        }
    }

    fn author() -> Reference {
        Reference {
            text: "kiosk-operator".into(),
            href: Some("/namespaces/ops/deployments/kiosk-operator".into()),
        }
    }

    type Chosen = (&'static str, Vec<(String, bool)>, Option<&'static str>);

    #[rstest]
    #[case::the_first_at_the_objects_url(
        &["the kiosk", "the wiring"],
        None,
        ("the kiosk", vec![("/k/elevations/the-kiosk".into(), true), ("/k/elevations/the-wiring".into(), false)], None)
    )]
    #[case::the_one_the_url_names(
        &["the kiosk", "the wiring"],
        Some("the-wiring"),
        ("the wiring", vec![("/k/elevations/the-kiosk".into(), false), ("/k/elevations/the-wiring".into(), true)], None)
    )]
    #[case::the_first_for_a_slug_the_kind_does_not_have(
        &["the kiosk", "the wiring"],
        Some("the-cellar"),
        (
            "the kiosk",
            vec![("/k/elevations/the-kiosk".into(), true), ("/k/elevations/the-wiring".into(), false)],
            Some("The knowledge of Kiosk states no elevation the-cellar, so the page draws the kiosk.")
        )
    )]
    #[case::no_tabs_for_one_elevation(&["the kiosk"], None, ("the kiosk", vec![], None))]
    fn chooses_the_elevation_that_the_url_names(
        #[case] names: &[&str],
        #[case] slug: Option<&str>,
        #[case] expected: Chosen,
    ) {
        let elevations: Vec<Elevation> = names.iter().map(|name| elevation(name)).collect();
        let (chosen, head) = choose(&elevations, slug, "/k", "Kiosk", author()).unwrap();
        let tabs = head
            .tabs
            .into_iter()
            .map(|tab| (tab.href, tab.current))
            .collect();
        let (name, expected_tabs, missing) = expected;
        assert_eq!(
            (chosen.name.as_str(), tabs, head.missing.as_deref()),
            (name, expected_tabs, missing)
        );
    }

    #[rstest]
    fn chooses_nothing_for_a_kind_with_no_elevation() {
        assert_eq!(
            choose(&[], Some("the-kiosk"), "/k", "Kiosk", author()),
            None
        );
    }
}
