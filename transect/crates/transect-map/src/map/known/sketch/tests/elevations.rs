//! A kind with several elevations: the one that the URL names, the
//! tabs and the author in the head, the wires, the roles that an
//! elevation does not place, and a reading through a reference.

use askama::Template;
use rstest::rstest;

use super::super::super::KnownPage;
use super::{REFERENCES, ROLES, page_at};
use crate::map::known::Standing;
use transect_resources::shapes::Shapes;

/// The kiosk with two elevations: the kiosk, which places every role
/// that has a part, and the wiring, which runs power from the amp to
/// the screen through the bell, and places no handset.
const ELEVATIONS: &str = "elevations: [
    {name: the kiosk, question: What is this kiosk made of?, sketch: \"face amp\\nhand bus\"},
    {name: the wiring, question: What feeds what?, sketch: \"amp bell face\\n. lamp .\",
     wires: [{from: amp, to: face, via: bell, carries: power}, {from: lamp, to: amp, carries: light}]}]";

fn page(slug: Option<&str>) -> KnownPage {
    let spec = format!("{{{REFERENCES}, {ROLES}, {ELEVATIONS}}}");
    page_at(&spec, Standing::Plain, &Shapes::default(), slug)
}

const AT: &str = "/namespaces/shop/kiosks.widgets.example.com/front";

type Shown = (String, String, Vec<(String, bool)>, Option<String>);

/// The elevation that a page draws, its path, its tabs, and its line
/// for a slug the kind does not have.
fn shown(page: &KnownPage) -> Shown {
    let sketch = page.sketch.as_ref().unwrap();
    let tabs = sketch.head.tabs.iter();
    let tabs = tabs.map(|tab| (tab.name.clone(), tab.current));
    (
        sketch.name.clone(),
        page.path.clone(),
        tabs.collect(),
        sketch.head.missing.clone(),
    )
}

fn tabs(current: usize) -> Vec<(String, bool)> {
    let names = ["the kiosk", "the wiring"].into_iter().enumerate();
    names
        .map(|(at, name)| (name.to_string(), at == current))
        .collect()
}

#[rstest]
#[case::the_first_at_the_objects_url(None, ("the kiosk".into(), AT.into(), tabs(0), None))]
#[case::the_elevation_the_url_names(
    Some("the-wiring"),
    ("the wiring".into(), format!("{AT}/elevations/the-wiring"), tabs(1), None)
)]
#[case::the_first_for_a_slug_the_kind_does_not_have(
    Some("the-cellar"),
    (
        "the kiosk".into(),
        format!("{AT}/elevations/the-cellar"),
        tabs(0),
        Some("The knowledge of Kiosk states no elevation the-cellar, so the page draws the kiosk.".into())
    )
)]
fn draws_the_elevation_that_the_url_names(#[case] slug: Option<&str>, #[case] expected: Shown) {
    assert_eq!(shown(&page(slug)), expected);
}

/// The head names the author as the card does: the knowledge, when no
/// single controller is known. Each tab links to its elevation's URL,
/// and the current one is marked for a screen reader.
#[rstest]
fn names_the_author_and_links_each_tab() {
    let html = page(Some("the-wiring")).render().unwrap();
    let current = format!(
        r#"<a class="lens-tab lens-tab-current" href="{AT}/elevations/the-wiring" hx-get="{AT}/elevations/the-wiring" hx-target="body" hx-swap="innerHTML" hx-push-url="true" aria-current="page">the wiring</a>"#
    );
    let author = r#"<div class="sketch-author">described by <span class="object-name">kiosks.widgets.example.com</span></div>"#;
    assert_eq!(
        (html.contains(&current), html.contains(author)),
        (true, true)
    );
}

/// The wiring draws power through the bell when the bell has a part,
/// and the bell names nothing here and is not expected, so the power
/// runs straight past it. The lamp is expected and names nothing, so
/// its wire is dashed.
#[rstest]
fn draws_the_wires_of_the_elevation() {
    let page = page(Some("the-wiring"));
    let wires = page.sketch.unwrap().plan.wires;
    let words: Vec<(String, bool)> = wires.iter().map(|w| (w.text(), w.dashed())).collect();
    assert_eq!(
        words,
        vec![
            ("power from Amp amp-1 to tv-1".to_string(), false),
            ("light from no Lamp to Amp amp-1".to_string(), true)
        ]
    );
}

/// A role that a wire meets marks its ports, where `wires.js` measures,
/// and a role that no wire meets marks none.
#[rstest]
#[case::the_wiring(Some("the-wiring"), vec![("amp", true), ("face", true), ("lamp", true)])]
#[case::the_kiosk(None, vec![("face", false), ("amp", false), ("hand", false), ("bus", false)])]
fn marks_the_ports_of_each_role_a_wire_meets(
    #[case] slug: Option<&str>,
    #[case] expected: Vec<(&str, bool)>,
) {
    let sketch = page(slug).sketch.unwrap();
    let roles = sketch.plan.roles.iter();
    let marked = roles.map(|role| (role.area.as_str(), !role.drawn.drawing.ports.is_empty()));
    assert_eq!(marked.collect::<Vec<_>>(), expected);
}

/// The line names the parts of each role that the elevation does not
/// place, by the role's label, and links the elevations that draw them.
/// A role with no part is left out of it.
#[rstest]
#[case::only_roles_with_no_part(None, None)]
#[case::a_role_with_parts(
    Some("the-wiring"),
    Some(vec![
        ("Not in the wiring: ", None),
        ("controlled by ", None),
        ("Handset left", Some("/namespaces/shop/handsets.widgets.example.com/left")),
        (" and ", None),
        ("Handset right", Some("/namespaces/shop/handsets.widgets.example.com/right")),
        (".", None),
        (" ", None),
        ("The kiosk", Some("/namespaces/shop/kiosks.widgets.example.com/front/elevations/the-kiosk")),
        (" draws them.", None),
    ])
)]
fn names_the_parts_that_an_elevation_does_not_place(
    #[case] slug: Option<&str>,
    #[case] line: Option<Vec<(&str, Option<&str>)>>,
) {
    let sketch = page(slug).sketch.unwrap();
    let found = sketch.unplaced.as_ref().map(|line| {
        let pieces = line.iter();
        pieces
            .map(|p| (p.text.as_str(), p.href.as_deref()))
            .collect::<Vec<_>>()
    });
    assert_eq!(found, line);
}

/// A reading through a reference links to the object it was read from,
/// and its title says which object and which reference.
#[rstest]
fn links_a_reading_through_a_reference_to_the_object_it_was_read_from() {
    let roles = ROLES.replace(
        "reads: [Model, Power]}",
        "reads: [Model, {column: Charge, through: status.pack}]}",
    );
    let spec = format!("{{{REFERENCES}, {roles}, {ELEVATIONS}}}");
    let html = page_at(&spec, Standing::Plain, &Shapes::default(), None)
        .render()
        .unwrap();
    let reading = r#"<div class="shape-reading" title="Charge through status.pack"><span class="shape-reading-name">charge</span> <span class="shape-reading-value"><a class="object-link" href="/far""#;
    assert!(html.contains(reading), "{html}");
}
