use rstest::rstest;

use super::super::super::Standing;
use super::page_of_known;
use crate::shapes::text::State;
use transect_graph::edges::Object;
use transect_resources::knowledge::catalog::{Derived, Judged, Marks};
use transect_resources::knowledge::fixtures::knowledge;
use transect_resources::shapes::Shapes;

/// A kiosk whose handsets are the instances of a pattern of the
/// catalog, and which states an elevation of its own that draws them.
const SPEC: &str = "{references: [
    {path: 'spec.handsets[].name', kind: Pod},
    {path: status.amp.name, kind: Pod}],
  roles: {hand: {reference: 'spec.handsets[].name'}, amp: {reference: status.amp.name}},
  elevations: [{name: the hands, question: Which handsets?, sketch: hand amp}],
  pattern: {name: primary-with-replicas, roles: {
    instances: {reference: 'spec.handsets[].name'},
    primary: {reference: status.amp.name}}}}";

/// The state of the part `amp-1`, the pattern's primary, in the
/// elevation `slug`, when a line of the pattern marks it.
fn primary(slug: Option<&str>) -> Option<State> {
    let mut known = knowledge("kiosks.widgets.example.com", SPEC);
    known.draw_with(&Shapes::default(), |_, _| None);
    let mut marks = Marks::default();
    marks.unmet(&Object::new("", "Pod", Some("shop"), "amp-1"), "not ready");
    let judged = Judged {
        marks,
        ..Judged::default()
    };
    let page = page_of_known(&known, Standing::Plain, slug, Some(&judged));
    let sketch = page.sketch.unwrap();
    let parts = sketch
        .plan()
        .unwrap()
        .roles
        .iter()
        .flat_map(|role| &role.drawn.parts);
    let mut named =
        parts.filter(|part| part.names().iter().any(|name| name.text.ends_with("amp-1")));
    named.next().map(|part| part.state)
}

/// The pattern's marks belong to its own elevation, where the lines of
/// what it expects are, so the kind's own elevation draws the part as
/// its health says.
#[rstest]
#[case::the_kinds_own_elevation(None, Some(State::Plain))]
#[case::the_patterns_elevation(Some("the-store"), Some(State::Warning))]
fn marks_a_part_only_in_the_patterns_elevation(
    #[case] slug: Option<&str>,
    #[case] state: Option<State>,
) {
    assert_eq!(primary(slug), state);
}

/// The place of a derived role with no part, as its text, its note,
/// and its state.
fn promoter(derived: Derived) -> Option<(String, String, State)> {
    let mut known = knowledge("kiosks.widgets.example.com", SPEC);
    known.draw_with(&Shapes::default(), |_, _| None);
    let judged = Judged {
        derived: [("promoter".to_string(), derived)].into_iter().collect(),
        ..Judged::default()
    };
    let page = page_of_known(&known, Standing::Plain, Some("the-store"), Some(&judged));
    let sketch = page.sketch.unwrap();
    let role = sketch
        .plan()
        .unwrap()
        .roles
        .iter()
        .find(|role| role.area == "promoter")?;
    let nothing = role.drawn.nothing.as_ref()?;
    Some((nothing.text.clone(), nothing.note.clone(), nothing.state))
}

/// A derived role with nothing to do is at rest, and one whose part the
/// knowledge names and the cluster lacks is missing it.
#[rstest]
#[case::nothing_to_do(
    Derived::Nothing("no replica to promote".into()),
    Some(("no operator".to_string(), "no replica to promote".to_string(), State::Elsewhere))
)]
#[case::a_named_part_that_is_gone(
    Derived::Missing("nothing that runs example/operator controls Kiosk front".into()),
    Some((
        "no operator".to_string(),
        "nothing that runs example/operator controls Kiosk front".to_string(),
        State::Empty
    ))
)]
fn draws_a_derived_role_with_no_part(
    #[case] derived: Derived,
    #[case] expected: Option<(String, String, State)>,
) {
    assert_eq!(promoter(derived), expected);
}
