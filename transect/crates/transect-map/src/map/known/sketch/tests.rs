use askama::Template;
use rstest::rstest;

use super::super::{Board, KnownPage};
use super::*;
use crate::objects::{Center, ObjectCard, Section, Side};
use transect_graph::edges::{ClusterScoped, Endpoint, Plurals};
use transect_observe::health::Level;
use transect_resources::knowledge::{self, KindKnowledge, fixtures::knowledge};

const REFERENCES: &str = "references: [
    {path: status.screen.monitor, label: shows on},
    {path: status.amp.name, kind: Amp, group: amps.example.com, label: amplified by},
    {path: 'spec.handsets[].name', kind: Handset, group: widgets.example.com, label: controlled by},
    {path: status.lamp.name, kind: Lamp, group: widgets.example.com, label: lit by},
    {path: status.bus.address, address: true, label: talks over},
    {path: status.bell.name, kind: Bell, group: widgets.example.com, label: rings}]";

const ELEVATION: &str = "elevations: [{name: the kiosk, question: What is this kiosk made of?,
    sketch: \"face face amp\\nhand lamp bell\\nbus . .\",
    roles: {
      face: {reference: status.screen.monitor, shape: screen, expected: true},
      amp: {reference: status.amp.name, expected: true, reads: [Model, Power]},
      hand: {reference: 'spec.handsets[].name', shape: handheld, expected: true},
      lamp: {reference: status.lamp.name, shape: speaker, expected: true},
      bus: {reference: status.bus.address, expected: true},
      bell: {reference: status.bell.name}}}]";

const KIOSK: &str = "
spec:
  handsets: [{name: left}, {name: right}]
status:
  screen: {monitor: tv-1}
  amp: {name: amp-1}
";

fn kiosk() -> Object {
    Object::new("widgets.example.com", "Kiosk", Some("shop"), "front")
}

fn plurals() -> Plurals {
    Plurals::from([(
        ("widgets.example.com".to_string(), "Handset".to_string()),
        "handsets".to_string(),
    )])
}

/// A fake of what each relation resolves to: a named object to itself,
/// and anything else to nothing.
fn reach(relation: &Relation) -> Reached {
    let ends = match &relation.names {
        Names::Object(object) => vec![Endpoint::Object(object.clone())],
        _ => vec![],
    };
    Reached {
        ends,
        guessed: false,
    }
}

/// The printer columns of the kiosk itself that have a value. `Sale` is
/// declared and empty, so it has no value. `Note` has separators and
/// characters that HTML escapes.
const OWN: &[(&str, &str)] = &[
    ("Mode", "attract"),
    ("Stock", "full"),
    ("Note", "FL,FR&<LFE"),
];

/// The page of the kiosk above, with the knowledge that `spec` writes.
/// Every object it names stands as `standing`, and each of its columns
/// reads "on". The kiosk's own columns read as `OWN` says. Handsets have
/// pages.
fn page_of(spec: &str, standing: Standing) -> KnownPage {
    let known = knowledge("kiosks.widgets.example.com", spec);
    let data = serde_yaml::from_str(KIOSK).unwrap();
    let relations = knowledge::relations(
        &known,
        &kiosk(),
        &data,
        &ClusterScoped::default(),
        &knowledge::Matches::default(),
    );
    let plurals = plurals();
    let page = |object: &Object| {
        (object.kind == "Handset")
            .then(|| transect_graph::links::page(object, &plurals))
            .flatten()
    };
    let readings = |object: &Object, reads: &[String]| {
        let value = |name: &String| match *object == kiosk() {
            true => OWN.iter().find(|(own, _)| own == name).map(|(_, v)| *v),
            false => Some("on"),
        };
        let read = reads.iter().filter_map(|name| {
            value(name).map(|value| Reading {
                name: name.clone(),
                value: value.into(),
            })
        });
        read.collect()
    };
    let board = Board {
        object: &kiosk(),
        knowledge: &known,
        layer: "apps".into(),
        path: "/namespaces/shop/kiosks.widgets.example.com/front".into(),
        reached: relations.iter().map(|r| (r, reach(r))).collect(),
        column: Vec::new(),
        counts: Vec::new(),
        chips: Vec::new(),
        level: Level::Healthy,
        resting: None,
        plurals: &plurals,
        page: &page,
        ground: &|_| None,
        device: &|device| device.short(),
        standing: &|_| standing.clone(),
        readings: &readings,
    };
    KnownPage::build(board, None, card(&known))
}

fn card(known: &KindKnowledge) -> ObjectCard {
    ObjectCard {
        kind: "Kiosk".into(),
        name: "front".into(),
        level: Some(Level::Healthy),
        resting: false,
        why: None,
        controllers: Vec::new(),
        code: Vec::new(),
        known: Some(knowledge::KnownThrough::of(known, None)),
        facts: Vec::new(),
    }
}

fn sketch(standing: Standing) -> Sketch {
    let spec = format!("{{{REFERENCES}, {ELEVATION}}}");
    page_of(&spec, standing).sketch.unwrap()
}

type Drawn<'a> = (&'a str, &'a str, Vec<(&'a str, Option<&'a str>)>);

#[rstest]
fn draws_each_role_with_a_part_for_each_value_and_leaves_an_unexpected_empty_role_out() {
    let sketch = sketch(Standing::Plain);
    let roles: Vec<Drawn> = sketch
        .roles
        .iter()
        .map(|role| {
            let parts = role.parts.iter().map(|part| {
                let reference = &part.end.reference;
                (reference.text.as_str(), reference.href.as_deref())
            });
            (role.area.as_str(), role.label.as_str(), parts.collect())
        })
        .collect();
    let handsets = "/namespaces/shop/handsets.widgets.example.com/";
    assert_eq!(
        roles,
        vec![
            ("face", "shows on", vec![("tv-1", None)]),
            ("amp", "amplified by", vec![("Amp amp-1", None)]),
            (
                "hand",
                "controlled by",
                vec![
                    ("Handset left", Some(format!("{handsets}left").as_str())),
                    ("Handset right", Some(format!("{handsets}right").as_str())),
                ]
            ),
            ("lamp", "lit by", vec![]),
            ("bus", "talks over", vec![]),
        ]
    );
}

#[rstest]
#[case::a_value("face", None)]
#[case::an_object("amp", None)]
#[case::a_kind_named(
    "lamp",
    Some(("no Lamp", "status.lamp.name names nothing now"))
)]
#[case::no_kind_named(
    "bus",
    Some(("nothing", "status.bus.address names nothing now"))
)]
fn draws_an_expected_role_with_no_part_as_nothing(
    #[case] area: &str,
    #[case] nothing: Option<(&str, &str)>,
) {
    let sketch = sketch(Standing::Plain);
    let role = sketch.roles.iter().find(|role| role.area == area).unwrap();
    let found = role.nothing.as_ref();
    let found = found.map(|n| (n.text.as_str(), n.note.as_str()));
    assert_eq!(found, nothing);
}

fn why() -> Why {
    Why {
        reason: "Unreachable".into(),
        message: Some("the amp does not answer".into()),
    }
}

#[rstest]
#[case::plain(Standing::Plain, State::Plain, None)]
#[case::a_warning(Standing::Warning(why()), State::Warning, Some(why()))]
#[case::failing(Standing::Failing(why()), State::Failing, Some(why()))]
#[case::at_rest(Standing::Resting(why()), State::Resting, Some(why()))]
#[case::missing(Standing::Missing, State::NoObject, None)]
fn takes_a_parts_state_and_its_reason_from_the_object_it_names(
    #[case] standing: Standing,
    #[case] state: State,
    #[case] reason: Option<Why>,
) {
    let sketch = sketch(standing);
    let states: Vec<(&str, State, Option<Why>)> = sketch
        .roles
        .iter()
        .flat_map(|role| {
            role.parts
                .iter()
                .map(|part| (role.area.as_str(), part.state, part.why.clone()))
        })
        .collect();
    assert_eq!(
        states,
        vec![
            ("face", State::Value, None),
            ("amp", state, reason.clone()),
            ("hand", state, reason.clone()),
            ("hand", state, reason),
        ]
    );
}

#[rstest]
#[case::plain(State::Plain, None)]
#[case::failing(State::Failing, Some("failing"))]
#[case::a_warning(State::Warning, Some("warning"))]
#[case::at_rest(State::Resting, Some("at rest"))]
#[case::a_value(State::Value, Some("not an object"))]
#[case::no_such_object(State::NoObject, Some("no such object"))]
fn names_each_state_for_a_screen_reader(#[case] state: State, #[case] spoken: Option<&str>) {
    assert_eq!(state.spoken(), spoken);
}

/// The sketch of the kiosk whose amp role has the `shows` that `shows`
/// writes in YAML, with every named object standing as `standing`.
fn showing(shows: &str, standing: Standing) -> Sketch {
    let elevation = ELEVATION.replace(
        "reads: [Model, Power]}",
        &format!("reads: [Model, Power], shows: {shows}}}"),
    );
    let spec = format!("{{{REFERENCES}, {elevation}}}");
    page_of(&spec, standing).sketch.unwrap()
}

fn shown(part: &Part) -> Vec<(&str, &str)> {
    let shows = part.shows.iter();
    shows.map(|s| (s.name.as_str(), s.value.as_str())).collect()
}

#[rstest]
#[case::one_column("[Mode]", vec![("Mode", "attract")])]
#[case::the_first_alternative_with_a_value("[[Mode, Stock]]", vec![("Mode", "attract")])]
#[case::the_next_when_the_first_is_empty("[[Sale, Stock]]", vec![("Stock", "full")])]
#[case::nothing_when_every_alternative_is_empty("[[Sale, Gone]]", vec![])]
#[case::each_entry_in_order("[Stock, [Sale, Mode]]", vec![("Stock", "full"), ("Mode", "attract")])]
fn shows_the_columns_of_the_pages_own_object(
    #[case] shows: &str,
    #[case] expected: Vec<(&str, &str)>,
) {
    let sketch = showing(shows, Standing::Plain);
    assert_eq!(shown(&sketch.roles[1].parts[0]), expected);
}

#[rstest]
#[case::one_column("[Mode]", vec![false])]
#[case::the_first_alternative("[[Mode, Stock]]", vec![false])]
#[case::a_later_alternative("[[Sale, Stock]]", vec![true])]
#[case::each_entry("[Stock, [Sale, Mode]]", vec![false, true])]
fn marks_a_column_that_is_not_the_first_of_its_entry_as_a_fallback(
    #[case] shows: &str,
    #[case] fallbacks: Vec<bool>,
) {
    let sketch = showing(shows, Standing::Plain);
    let shown = sketch.roles[1].parts[0].shows.iter();
    assert_eq!(shown.map(|s| s.fallback).collect::<Vec<_>>(), fallbacks);
}

#[rstest]
fn shows_nothing_of_the_pages_own_object_on_a_value() {
    let elevation = ELEVATION.replace(
        "shape: screen, expected: true}",
        "shape: screen, expected: true, shows: [Mode]}",
    );
    let spec = format!("{{{REFERENCES}, {elevation}}}");
    let sketch = page_of(&spec, Standing::Plain).sketch.unwrap();
    assert_eq!(shown(&sketch.roles[0].parts[0]), vec![]);
}

#[rstest]
fn shows_nothing_of_the_pages_own_object_on_a_name_with_no_object() {
    let sketch = showing("[Mode]", Standing::Missing);
    assert_eq!(shown(&sketch.roles[1].parts[0]), vec![]);
}

#[rstest]
#[case::a_value("face", Some("not an object in the cluster"), vec![])]
#[case::an_object_with_readings("amp", None, vec![("model", "on"), ("power", "on")])]
#[case::an_object_with_no_reads("hand", None, vec![])]
fn notes_and_reads_each_part(
    #[case] area: &str,
    #[case] under: Option<&str>,
    #[case] readings: Vec<(&str, &str)>,
) {
    let sketch = sketch(Standing::Plain);
    let role = sketch.roles.iter().find(|role| role.area == area).unwrap();
    let part = &role.parts[0];
    let read = part.readings.iter();
    let read: Vec<(&str, &str)> = read.map(|r| (r.name.as_str(), r.value.as_str())).collect();
    assert_eq!((part.end.under.as_deref(), read), (under, readings));
}

#[rstest]
#[case::a_value("face", true)]
#[case::an_object("amp", false)]
#[case::an_object_with_a_page("hand", false)]
fn spells_out_a_part_that_is_not_an_object(#[case] area: &str, #[case] literal: bool) {
    let sketch = sketch(Standing::Plain);
    let role = sketch.roles.iter().find(|role| role.area == area).unwrap();
    assert_eq!(role.parts[0].literal, literal);
}

/// The page draws each reading's value and each shown value with a
/// `<wbr>` after each separator, and escapes the text between them.
#[rstest]
fn breaks_values_after_each_separator_and_escapes_them() {
    let elevation = ELEVATION.replace(
        "reads: [Model, Power]}",
        "reads: [Model, Power], shows: [Note]}",
    );
    let spec = format!("{{{REFERENCES}, {elevation}}}");
    let html = page_of(&spec, Standing::Plain).render().unwrap();
    let values = html.split("<span class=\"sketch-reading-value\">").skip(1);
    let values: Vec<&str> = values.filter_map(|v| v.split("</span>").next()).collect();
    let end =
        r#"<div class="known-end" title="tv-1"><span class="object-name">tv-<wbr>1</span></div>"#;
    assert_eq!(
        (values, html.contains(end)),
        (vec!["on", "on", "FL,<wbr>FR&#38;&#60;LFE"], true)
    );
}

#[rstest]
fn notes_a_name_the_cluster_has_no_object_for() {
    let sketch = sketch(Standing::Missing);
    let amp = &sketch.roles[1].parts[0];
    assert_eq!(
        (
            amp.end.under.as_deref(),
            amp.end.reaches.clone(),
            amp.readings.len()
        ),
        (Some("the cluster has no such Amp"), vec![], 0)
    );
}

#[rstest]
fn lays_the_roles_out_on_the_grid_of_the_sketch() {
    let sketch = sketch(Standing::Plain);
    let shapes: Vec<String> = sketch.roles.iter().map(SketchRole::shape_class).collect();
    let areas: Vec<&str> = sketch.roles.iter().map(|r| r.lean.area_class()).collect();
    assert_eq!(
        (
            sketch.name.as_str(),
            sketch.question.as_str(),
            sketch.columns,
            sketch.areas.as_str(),
            shapes,
            areas,
            sketch.roles[0].source.as_str(),
            sketch.caption(),
        ),
        (
            "the kiosk",
            "What is this kiosk made of?",
            3,
            r#""face face amp" "hand lamp bell" "bus . .""#,
            vec![
                "sketch-screen".to_string(),
                "sketch-box".into(),
                "sketch-handheld".into(),
                "sketch-speaker".into(),
                "sketch-box".into(),
            ],
            vec![
                "sketch-area",
                "sketch-area sketch-lean-start",
                "sketch-area sketch-lean-end",
                "sketch-area",
                "sketch-area sketch-lean-end",
            ],
            "from status.screen.monitor",
            "Hover a part for the field it was read from. The places and shapes are from the KindKnowledge kiosks.widgets.example.com.".to_string(),
        )
    );
}

#[rstest]
#[case::with_an_elevation(format!("{{{REFERENCES}, {ELEVATION}}}"), true, 0)]
#[case::without_one(format!("{{{REFERENCES}}}"), false, 6)]
fn draws_a_sketch_in_place_of_the_ring(
    #[case] spec: String,
    #[case] sketched: bool,
    #[case] boxes: usize,
) {
    let page = page_of(&spec, Standing::Plain);
    assert_eq!(
        (page.sketch.is_some(), page.boxes.len(), page.diagram()),
        (sketched, boxes, true)
    );
}

#[rstest]
fn leaves_out_of_the_board_each_value_the_sketch_shows() {
    let spec = format!("{{{REFERENCES}, {ELEVATION}}}");
    let page = page_of(&spec, Standing::Plain);
    let center = Center {
        kind: "Kiosk".into(),
        name: "front".into(),
        level: None,
        resting: None,
        note: None,
        chips: Vec::new(),
        messages: Vec::new(),
    };
    let section = Section::new("Refers to", Side::Out)
        .literals("shows on", vec!["tv-1".into()])
        .literals("sells", vec!["tea".into()]);
    let page = page.with_board(center, vec![section]);
    let board = &page.board;
    let facts = board.right.iter().flat_map(|section| &section.facts);
    let labels: Vec<&str> = facts.map(|fact| fact.label.as_str()).collect();
    assert_eq!((board.center.is_none(), labels), (true, vec!["sells"]));
}
