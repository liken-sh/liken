//! The head of a sketch: a tab for each elevation of the kind, in the
//! author's order, with the current one marked, and the author who
//! states the current one. An elevation of a pattern of the catalog is
//! stated by `transect`, and its head also names the pattern. Each elevation has a URL of its own under the object's,
//! and the object's own URL draws the first. An elevation that the URL
//! names and the kind does not have draws the first, with a line that
//! says so.

use transect_graph::links::Reference;
use transect_resources::knowledge::Stated;

/// The author of an elevation of a pattern of the catalog.
const TRANSECT: &str = "transect";

/// The head of the sketch of one elevation.
#[derive(Debug, PartialEq)]
pub struct Head {
    /// A tab for each elevation, or none when the kind has one, because
    /// a lone tab leads nowhere.
    pub tabs: Vec<ElevationTab>,
    /// Who states the current elevation: for the kind's own, the
    /// controller that the knowledge names, or else the
    /// `KindKnowledge`, as the card names it, and `transect` for an
    /// elevation of the catalog.
    pub author: Reference,
    /// The title of the pattern of the catalog that states the current
    /// elevation, such as "primary with replicas".
    pub pattern: Option<String>,
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

/// The elevation of `elevations`, each by its slug, that the URL names
/// by `slug`, or the first when it names none or one the kind does not
/// have, with the head of its sketch. `author` states the kind's own
/// elevations. It is none when the kind has no elevation.
pub fn choose<'a, 'k>(
    elevations: &'a [(String, Stated<'k>)],
    slug: Option<&str>,
    path: &str,
    kind: &str,
    author: Reference,
) -> Option<(&'a (String, Stated<'k>), Head)> {
    let named = slug.and_then(|slug| elevations.iter().find(|(s, _)| s == slug));
    let chosen = named.or(elevations.first())?;
    let (current, stated) = chosen;
    let missing = slug.filter(|_| named.is_none()).map(|slug| {
        format!(
            "The knowledge of {kind} states no elevation {slug}, so the page draws {}.",
            stated.elevation.name
        )
    });
    let tabs = elevations.iter().map(|(slug, other)| ElevationTab {
        name: other.elevation.name.clone(),
        href: href(path, slug),
        current: slug == current,
    });
    let tabs = match elevations.len() {
        1 => Vec::new(),
        _ => tabs.collect(),
    };
    let author = match stated.pattern {
        Some(_) => Reference {
            text: TRANSECT.into(),
            href: None,
        },
        None => author,
    };
    let head = Head {
        tabs,
        author,
        missing,
        pattern: stated.pattern.map(|pattern| pattern.title.clone()),
    };
    Some((chosen, head))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    use transect_resources::knowledge::Elevation;

    fn elevation(name: &str) -> Elevation {
        Elevation {
            name: name.into(),
            slug: transect_resources::knowledge::elevation::slug(name),
            question: "What is it?".into(),
            form: transect_resources::knowledge::elevation::Form::Sketch(
                transect_resources::knowledge::elevation::Sketch {
                    grid: transect_resources::knowledge::Grid { rows: Vec::new() },
                    wires: Vec::new(),
                    align: Default::default(),
                },
            ),
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
        let stated = stated(&elevations);
        let ((_, chosen), head) = choose(&stated, slug, "/k", "Kiosk", author()).unwrap();
        let tabs = head
            .tabs
            .into_iter()
            .map(|tab| (tab.href, tab.current))
            .collect();
        let (name, expected_tabs, missing) = expected;
        assert_eq!(
            (
                chosen.elevation.name.as_str(),
                tabs,
                head.missing.as_deref()
            ),
            (name, expected_tabs, missing)
        );
    }

    /// Each of `elevations` as the kind's own, by its slug.
    fn stated(elevations: &[Elevation]) -> Vec<(String, Stated<'_>)> {
        let own = elevations.iter().map(|elevation| Stated {
            elevation,
            roles: &[],
            pattern: None,
        });
        own.map(|stated| (stated.elevation.slug.clone(), stated))
            .collect()
    }

    #[rstest]
    fn chooses_nothing_for_a_kind_with_no_elevation() {
        assert_eq!(
            choose(&[], Some("the-kiosk"), "/k", "Kiosk", author()),
            None
        );
    }
}
