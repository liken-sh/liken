//! A custom resource's health, read from its own condition.  With kind
//! knowledge, the condition that the knowledge names decides: `True` is
//! healthy, `False` fails, and `Unknown` or a missing condition is a
//! warning.  A `False` whose reason the knowledge lists as calm, such as
//! `Idle`, means the object is at rest, so it is not a failure and the
//! page draws it gray.  Without knowledge, a `Ready` condition decides
//! the same way, by the convention of the standard `status.conditions`.
//! When the resource has neither, it adds no reason, and its health is
//! the roll-up of the workloads under it.

use kube::core::DynamicObject;
use serde_json::Value;

use super::{Level, Reason};
use transect_graph::edges::Object;
use transect_graph::untrusted::capped;
use transect_resources::knowledge::Kinds;

/// The condition that decides the health of a resource with no kind
/// knowledge.
const READY: &str = "Ready";

/// The maximum length, in characters, of a condition's reason or message
/// on a card.
const MESSAGE_CHARS: usize = 200;

/// The reason `resource`, the custom resource `object`, adds to its
/// health, or none when its condition is `True`, is `False` with a calm
/// reason, or is missing with no kind knowledge to name it.
pub fn condition(kinds: &Kinds, object: &Object, resource: &DynamicObject) -> Option<Reason> {
    let named = kinds.of_object(object).and_then(|k| k.healthy.as_deref());
    let found = named.or(Some(READY)).and_then(|name| find(resource, name));
    let condition = match (named, found) {
        (_, Some(found)) => found,
        (Some(name), None) => {
            return Some(Reason::Condition {
                workload: object.clone(),
                condition: name.into(),
                status: None,
                reason: None,
                message: None,
            });
        }
        (None, None) => return None,
    };
    let text = |key: &str| {
        let value = condition.get(key)?.as_str()?;
        capped(value, MESSAGE_CHARS)
    };
    let status = text("status").unwrap_or_else(|| "Unknown".into());
    if calm(kinds, object, condition) {
        return None;
    }
    (status != "True").then(|| Reason::Condition {
        workload: object.clone(),
        condition: text("type").unwrap_or_default(),
        status: Some(status),
        reason: text("reason"),
        message: text("message"),
    })
}

/// The level that `resource`'s own condition gives it, for its dot.
/// The level is the one that `condition` finds, or healthy when that
/// condition is `True` or is `False` with a calm reason.  The function
/// returns `None` when the resource
/// reports no condition to read, so the page draws no dot for a kind
/// with no `Ready` condition, rather than a healthy one.
pub fn reported(kinds: &Kinds, object: &Object, resource: &DynamicObject) -> Option<Level> {
    if let Some(reason) = condition(kinds, object, resource) {
        return Some(reason.level());
    }
    let named = kinds.of_object(object).and_then(|k| k.healthy.as_deref());
    find(resource, named.unwrap_or(READY)).map(|_| Level::Healthy)
}

/// Why `resource` is at rest, when its kind knowledge's condition is
/// `False` with a calm reason: the condition's message, or its reason
/// when it gives none.  The page shows it as a gray line.
pub fn resting(kinds: &Kinds, object: &Object, resource: &DynamicObject) -> Option<String> {
    let named = kinds.of_object(object)?.healthy.as_deref()?;
    let condition = find(resource, named)?;
    if !calm(kinds, object, condition) {
        return None;
    }
    let text = |key: &str| capped(condition.get(key)?.as_str()?, MESSAGE_CHARS);
    text("message").or_else(|| text("reason"))
}

/// Whether `condition`, the one `object`'s kind knowledge names, is a
/// `False` with one of the knowledge's calm reasons.
fn calm(kinds: &Kinds, object: &Object, condition: &Value) -> bool {
    let Some(knowledge) = kinds.of_object(object) else {
        return false;
    };
    let field = |key: &str| condition.get(key).and_then(Value::as_str);
    let named = field("type") == knowledge.healthy.as_deref();
    let reason = field("reason").is_some_and(|reason| knowledge.calm.iter().any(|c| c == reason));
    named && field("status") == Some("False") && reason
}

/// The condition that decides an object's health, and the reasons that
/// mean the object is at rest, for drawing its conditions.  The
/// condition is the one that its kind knowledge names, or `Ready` when
/// the knowledge names none.  Only that condition gets a color.  For any
/// other condition, such as `AttentionNeeded`, transect cannot determine
/// whether `True` is good, so it colors neither of that condition's
/// values.
#[derive(Clone, Debug, PartialEq)]
pub struct Deciding {
    pub condition: String,
    pub calm: Vec<String>,
}

/// `Ready` decides, with no calm reasons, as for a kind with no
/// knowledge.
impl Default for Deciding {
    fn default() -> Self {
        Self {
            condition: READY.into(),
            calm: Vec::new(),
        }
    }
}

impl Deciding {
    pub fn of(kinds: &Kinds, object: &Object) -> Self {
        let knowledge = kinds.of_object(object);
        Self {
            condition: knowledge
                .and_then(|known| known.healthy.clone())
                .unwrap_or_else(|| READY.into()),
            calm: knowledge
                .map(|known| known.calm.clone())
                .unwrap_or_default(),
        }
    }

    /// The level of the condition `kind` with `status` and `reason`.
    /// The function returns `None`, which the page draws gray, for any
    /// condition but the deciding one, and for the deciding one at rest.
    pub fn level(&self, kind: &str, status: &str, reason: Option<&str>) -> Option<Level> {
        if kind != self.condition {
            return None;
        }
        match (status, reason) {
            ("True", _) => Some(Level::Healthy),
            ("False", Some(reason)) if self.calm.iter().any(|c| c == reason) => None,
            ("False", _) => Some(Level::Failing),
            _ => Some(Level::Warning),
        }
    }
}

/// The condition of type `name` in `resource`'s `status.conditions`.
fn find<'a>(resource: &'a DynamicObject, name: &str) -> Option<&'a Value> {
    let conditions = resource.data.get("status")?.get("conditions")?.as_array()?;
    conditions
        .iter()
        .find(|condition| condition.get("type").and_then(Value::as_str) == Some(name))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_resources::knowledge::fixtures::{knowledge, plurals};

    fn widget() -> Object {
        Object::new("widgets.example.com", "Widget", Some("shop"), "front-room")
    }

    fn resource(conditions: &str) -> DynamicObject {
        serde_yaml::from_str(&format!(
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
        let reason = condition(&kinds, &widget(), &resource(conditions))?;
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
        assert_eq!(reported(&kinds, &widget(), &resource(conditions)), expected);
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
        let found = resting(&kinds, &widget(), &resource(conditions));
        assert_eq!(found.as_deref(), expected);
    }

    #[rstest]
    #[case::the_deciding_condition_working("Spinning", "True", None, Some(Level::Healthy))]
    #[case::the_deciding_condition_failing(
        "Spinning",
        "False",
        Some("Jammed"),
        Some(Level::Failing)
    )]
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
        let conditions =
            format!("[{{type: Ready, status: 'False', message: \"one\\ntwo {long}\"}}]");
        let (text, _) = read(None, &conditions).unwrap();
        assert!(
            text.starts_with("front-room: Ready is False: one two x"),
            "{text}"
        );
        assert!(text.chars().count() < 250, "{text}");
    }
}
