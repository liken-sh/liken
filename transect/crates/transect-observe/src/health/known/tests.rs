use rstest::rstest;

use super::*;
use transect_resources::knowledge::fixtures::{knowledge, plurals};

/// Where the reference of a widget leads, whatever its path: a pad that
/// the cluster stores, or one of the ends with no stored object.
enum Lead {
    Found(Box<DynamicObject>),
    Nothing,
    Ambiguous,
    Missing,
    Unwatched,
}

fn pad() -> Object {
    Object::new("pads.example.com", "Pad", None, "aa-bb")
}

impl ReferenceEnds for Lead {
    fn ends(&self, _: &Object, _: &str) -> Vec<Followed<'_>> {
        match self {
            Self::Found(found) => vec![Followed::Found(pad(), found)],
            Self::Nothing => vec![],
            Self::Ambiguous => vec![Followed::Ambiguous(2)],
            Self::Missing => vec![Followed::Missing(pad())],
            Self::Unwatched => vec![Followed::Unwatched(pad())],
        }
    }
}

fn widget() -> Object {
    Object::new("widgets.example.com", "Widget", Some("shop"), "front-room")
}

fn resource(conditions: &str) -> DynamicObject {
    serde_yaml_ng::from_str(&format!(
        "apiVersion: widgets.example.com/v1\nkind: Widget\nmetadata: {{namespace: shop, name: front-room}}\nstatus: {{conditions: {conditions}}}\n"
    ))
    .unwrap()
}

fn kinds(spec: &str) -> Kinds {
    let known = [knowledge("widgets.widgets.example.com", spec)];
    Kinds::resolve(&known, &plurals(), &[])
}

fn read(spec: Option<&str>, conditions: &str) -> Option<(String, Level)> {
    let kinds = spec.map(kinds).unwrap_or_default();
    let reason = condition(&kinds, &Lead::Nothing, &widget(), &resource(conditions))?;
    Some((reason.to_string(), reason.level()))
}

const SPINNING: &str = "{healthy: {condition: Spinning}}";
const RESTING: &str = "{healthy: {condition: Spinning, calm: [Parked]}}";

#[rstest]
#[case::true_is_healthy(Some(SPINNING), "[{type: Spinning, status: 'True'}]", None)]
#[case::false_with_a_message(
    Some(SPINNING),
    "[{type: Spinning, status: 'False', reason: Jammed, message: the axle is stuck}]",
    Some(("front-room: Spinning is False (Jammed): the axle is stuck", Level::Failing))
)]
#[case::unknown(
    Some(SPINNING),
    "[{type: Spinning, status: Unknown}]",
    Some(("front-room: Spinning is Unknown", Level::Warning))
)]
#[case::missing(
    Some(SPINNING),
    "[{type: Ready, status: 'True'}]",
    Some(("front-room: reports no Spinning condition", Level::Warning))
)]
#[case::ready_without_knowledge(
    None,
    "[{type: Ready, status: 'False', reason: NoQuorum}]",
    Some(("front-room: Ready is False (NoQuorum)", Level::Failing))
)]
#[case::ready_when_knowledge_names_none(
    Some("{summary: Turns.}"),
    "[{type: Ready, status: 'True'}]",
    None
)]
#[case::a_calm_reason(
    Some(RESTING),
    "[{type: Spinning, status: 'False', reason: Parked, message: nobody asked}]",
    None
)]
#[case::a_false_that_is_not_calm(
    Some(RESTING),
    "[{type: Spinning, status: 'False', reason: Jammed}]",
    Some(("front-room: Spinning is False (Jammed)", Level::Failing))
)]
#[case::neither_knowledge_nor_ready(None, "[{type: Synced, status: 'False'}]", None)]
#[case::no_conditions_at_all(None, "[]", None)]
fn reads_the_condition_that_means_it_works(
    #[case] spec: Option<&str>,
    #[case] conditions: &str,
    #[case] expected: Option<(&str, Level)>,
) {
    let expected = expected.map(|(text, level)| (text.to_string(), level));
    assert_eq!(read(spec, conditions), expected);
}

#[rstest]
#[case::ready(None, "[{type: Ready, status: 'True'}]", Some(Level::Healthy))]
#[case::not_ready(None, "[{type: Ready, status: 'False'}]", Some(Level::Failing))]
#[case::no_ready_condition(None, "[{type: Synced, status: 'True'}]", None)]
#[case::the_condition_knowledge_names(
    Some(SPINNING),
    "[{type: Spinning, status: 'True'}]",
    Some(Level::Healthy)
)]
#[case::missing_the_condition_knowledge_names(Some(SPINNING), "[]", Some(Level::Warning))]
#[case::at_rest(
    Some(RESTING),
    "[{type: Spinning, status: 'False', reason: Parked}]",
    Some(Level::Healthy)
)]
fn reads_the_level_of_a_dot(
    #[case] spec: Option<&str>,
    #[case] conditions: &str,
    #[case] expected: Option<Level>,
) {
    let kinds = spec.map(kinds).unwrap_or_default();
    assert_eq!(
        reported(&kinds, &Lead::Nothing, &widget(), &resource(conditions)),
        expected
    );
}

#[rstest]
#[case::its_message(
    Some(RESTING),
    "[{type: Spinning, status: 'False', reason: Parked, message: Not reserved}]",
    Some("Not reserved")
)]
#[case::its_reason_with_no_message(
    Some(RESTING),
    "[{type: Spinning, status: 'False', reason: Parked}]",
    Some("Parked")
)]
#[case::working(Some(RESTING), "[{type: Spinning, status: 'True'}]", None)]
#[case::failing(
    Some(RESTING),
    "[{type: Spinning, status: 'False', reason: Jammed}]",
    None
)]
#[case::another_condition_with_a_calm_reason(
    Some(RESTING),
    "[{type: Spinning, status: 'True'}, {type: Lit, status: 'False', reason: Parked}]",
    None
)]
fn says_why_a_resource_is_at_rest(
    #[case] spec: Option<&str>,
    #[case] conditions: &str,
    #[case] expected: Option<&str>,
) {
    let kinds = spec.map(kinds).unwrap_or_default();
    let found = resting(&kinds, &Lead::Nothing, &widget(), &resource(conditions));
    assert_eq!(found.as_deref(), expected);
}

#[rstest]
#[case::with_a_message(
    "[{type: Spinning, status: 'False', reason: Parked, message: Not reserved}]",
    Some(("Parked", Some("Not reserved")))
)]
#[case::with_no_message(
    "[{type: Spinning, status: 'False', reason: Parked}]",
    Some(("Parked", None))
)]
#[case::working("[{type: Spinning, status: 'True'}]", None)]
fn gives_the_reason_and_message_of_a_resource_at_rest(
    #[case] conditions: &str,
    #[case] expected: Option<(&str, Option<&str>)>,
) {
    let found = at_rest(
        &kinds(RESTING),
        &Lead::Nothing,
        &widget(),
        &resource(conditions),
    );
    let found = found.as_ref().map(|(r, m)| (r.as_str(), m.as_deref()));
    assert_eq!(found, expected);
}

#[rstest]
#[case::the_deciding_condition_working("Spinning", "True", None, Some(Level::Healthy))]
#[case::the_deciding_condition_failing("Spinning", "False", Some("Jammed"), Some(Level::Failing))]
#[case::the_deciding_condition_at_rest("Spinning", "False", Some("Parked"), None)]
#[case::the_deciding_condition_unknown("Spinning", "Unknown", None, Some(Level::Warning))]
#[case::another_condition_true("AttentionNeeded", "True", None, None)]
#[case::another_condition_false("Active", "False", Some("Jammed"), None)]
fn colors_only_the_deciding_condition(
    #[case] kind: &str,
    #[case] status: &str,
    #[case] reason: Option<&str>,
    #[case] expected: Option<Level>,
) {
    let deciding = Deciding::of(&kinds(RESTING), &widget());
    assert_eq!(deciding.level(kind, status, reason), expected);
}

#[rstest]
fn strips_what_an_operator_writes_to_one_short_line() {
    let long = "x".repeat(400);
    let conditions = format!("[{{type: Ready, status: 'False', message: \"one\\ntwo {long}\"}}]");
    let (text, _) = read(None, &conditions).unwrap();
    assert!(
        text.starts_with("front-room: Ready is False: one two x"),
        "{text}"
    );
    assert!(text.chars().count() < 250, "{text}");
}

/// A widget that is as healthy as its pad, and the knowledge of pads,
/// whose `Connected` condition decides and which rest while `Asleep`.
/// Without `pads`, `transect` has no knowledge of a pad.
fn through(pads: bool) -> Kinds {
    let mut known = vec![knowledge(
        "widgets.widgets.example.com",
        "{references: [{path: status.pad, kind: Pad, group: pads.example.com}], healthy: {through: status.pad}}",
    )];
    if pads {
        known.push(knowledge(
            "pads.pads.example.com",
            "{healthy: {condition: Connected, calm: [Asleep]}}",
        ));
    }
    let mut plurals = plurals();
    plurals.insert(("pads.example.com".into(), "Pad".into()), "pads".into());
    Kinds::resolve(&known, &plurals, &[])
}

/// A pad whose `Connected` condition has `status` and `reason`.
fn stored_pad(status: &str, reason: &str) -> Lead {
    Lead::Found(Box::new(
        serde_yaml_ng::from_str(&format!(
            "{{apiVersion: pads.example.com/v1, kind: Pad, metadata: {{name: aa-bb}}, status: {{conditions: [{{type: Connected, status: '{status}', reason: {reason}, message: asleep since noon}}]}}}}"
        ))
        .unwrap(),
    ))
}

/// What a widget's health through its pad gives: the reason's sentence
/// and its level, the dot's level, and why the widget is at rest. The
/// widget's own conditions say nothing.
type Through = (
    Option<(String, Level)>,
    Option<Level>,
    Option<(String, Option<String>)>,
);

#[rstest]
#[case::a_connected_pad(true, stored_pad("True", "LinkUp"), (None, Some(Level::Healthy), None))]
#[case::a_pad_asleep(
    true,
    stored_pad("False", "Asleep"),
    (None, Some(Level::Healthy), Some(("Asleep".into(), Some("asleep since noon".into()))))
)]
#[case::a_pad_that_fails(
    true,
    stored_pad("False", "Dropped"),
    (
        Some(("front-room: Connected is False (Dropped): asleep since noon".into(), Level::Failing)),
        Some(Level::Failing),
        None
    )
)]
#[case::a_reference_that_names_nothing(
    true,
    Lead::Nothing,
    (Some(("front-room: status.pad names nothing".into(), Level::Warning)), Some(Level::Warning), None)
)]
#[case::a_reference_that_matches_several_pads(
    true,
    Lead::Ambiguous,
    (Some(("front-room: status.pad matches 2 objects, so it names none".into(), Level::Warning)), Some(Level::Warning), None)
)]
#[case::a_pad_the_cluster_does_not_have(
    true,
    Lead::Missing,
    (Some(("front-room: the cluster has no such Pad".into(), Level::Warning)), Some(Level::Warning), None)
)]
#[case::a_pad_of_a_kind_transect_does_not_watch(true, Lead::Unwatched, (None, None, None))]
#[case::a_through_that_does_not_resolve(
    false,
    stored_pad("True", "LinkUp"),
    (
        Some(("front-room: healthy follows status.pad, and transect has no knowledge of Pad".into(), Level::Warning)),
        Some(Level::Warning),
        None
    )
)]
fn reads_a_health_through_a_reference(
    #[case] pads: bool,
    #[case] lead: Lead,
    #[case] expected: Through,
) {
    let (kinds, own) = (through(pads), resource("[]"));
    let reason = condition(&kinds, &lead, &widget(), &own);
    let found = (
        reason.map(|reason| (reason.to_string(), reason.level())),
        reported(&kinds, &lead, &widget(), &own),
        at_rest(&kinds, &lead, &widget(), &own),
    );
    assert_eq!(found, expected);
}

/// A kind whose health is through a reference has no condition of its
/// own, so each of its own conditions is drawn gray.
#[rstest]
fn colors_none_of_the_conditions_of_a_kind_whose_health_is_through_a_reference() {
    let deciding = Deciding::of(&through(true), &widget());
    assert_eq!(deciding.level("Ready", "False", None), None);
}
