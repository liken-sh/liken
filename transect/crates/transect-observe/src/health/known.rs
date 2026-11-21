//! A custom resource's health, read from one of its conditions.  When
//! kind knowledge names a condition, that condition decides: `True` is
//! healthy, `False` is failing, and `Unknown` or a missing condition is
//! a warning.  A `False` with a reason that the knowledge lists in
//! `calm`, such as `Idle`, means that the object is at rest, so it is not
//! a failure and the page draws it gray.  When no kind knowledge names a
//! condition, `Ready` decides in the same way, except that a resource
//! with no `Ready` condition adds no reason, and its health is the
//! roll-up of the workloads under it.
//!
//! A kind whose knowledge says that it is healthy `through` a reference
//! has no condition of its own.  Its object is as healthy as the object
//! at the end of the reference, by that object's own knowledge, so it is
//! at rest while that object is.  A reference that names nothing, or
//! names an object that the cluster does not have, is a warning.

use kube::core::DynamicObject;
use serde_json::Value;

use super::{Level, Reason};
use transect_graph::edges::Object;
use transect_graph::untrusted::capped;
use transect_resources::knowledge::{Followed, Healthy, Kinds, ReferenceEnds};

/// The condition that decides a resource's health when no kind knowledge
/// names one.
const READY: &str = "Ready";

/// The maximum length, in characters, of a condition's reason or message
/// on a card.
const MESSAGE_CHARS: usize = 200;

/// The object whose own conditions decide another object's health.
enum Judged<'a> {
    /// The object, itself or at the end of a reference, and the data
    /// that the cluster stores for it.
    Own(Object, &'a DynamicObject),
    /// No object decides, and the warning says why.
    Unfollowed(String),
    /// The reference names an object of a kind that `transect` does not
    /// watch, so nothing can be said.
    Unknown,
}

/// The object whose conditions decide the health of `object`: `object`
/// itself, or the object at the first end of the reference that its
/// knowledge follows. Only one reference is followed, because a kind
/// whose health is through a reference has no condition of its own.
fn judged<'a>(
    kinds: &Kinds,
    ends: &'a dyn ReferenceEnds,
    object: &Object,
    resource: &'a DynamicObject,
) -> Judged<'a> {
    let healthy = kinds.of_object(object).and_then(|k| k.healthy.as_ref());
    let Some(Healthy::Through(through)) = healthy else {
        return Judged::Own(object.clone(), resource);
    };
    if let Some(unresolved) = &through.unresolved {
        return Judged::Unfollowed(unresolved.clone());
    }
    let path = &through.path;
    match ends.ends(object, path).into_iter().next() {
        Some(Followed::Found(other, found)) => {
            match kinds.of_object(&other).and_then(|k| k.condition()) {
                Some(_) => Judged::Own(other, found),
                None => Judged::Unknown,
            }
        }
        None => Judged::Unfollowed(format!("{path} names nothing")),
        Some(Followed::Ambiguous(count)) => {
            Judged::Unfollowed(format!("{path} matches {count} objects, so it names none"))
        }
        Some(Followed::Missing(other)) => {
            Judged::Unfollowed(format!("the cluster has no such {}", other.kind))
        }
        Some(Followed::Unwatched(_)) => Judged::Unknown,
    }
}

/// The reason that `resource` adds to its own health.  It is `None` when
/// the deciding condition is `True` or at rest, and when no kind
/// knowledge names a condition and the resource has no `Ready`.
pub fn condition(
    kinds: &Kinds,
    ends: &dyn ReferenceEnds,
    object: &Object,
    resource: &DynamicObject,
) -> Option<Reason> {
    reason(kinds, object, &judged(kinds, ends, object, resource))
}

/// The reason that `judged` gives `object`.  A reason read through a
/// reference is still about `object`, because the namespace page counts
/// it for `object`.
fn reason(kinds: &Kinds, object: &Object, judged: &Judged) -> Option<Reason> {
    match judged {
        Judged::Own(judged, resource) => {
            let mut reason = own_condition(kinds, judged, resource)?;
            if let Reason::Condition { workload, .. } = &mut reason {
                *workload = object.clone();
            }
            Some(reason)
        }
        Judged::Unfollowed(why) => Some(Reason::Unfollowed {
            workload: object.clone(),
            why: why.clone(),
        }),
        Judged::Unknown => None,
    }
}

fn own_condition(kinds: &Kinds, object: &Object, resource: &DynamicObject) -> Option<Reason> {
    let named = kinds.of_object(object).and_then(|k| k.condition());
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

/// The level of `resource`'s dot on the page: the level of the reason
/// from `condition`, or `Healthy` when the deciding condition is `True`
/// or at rest.  It is `None` when `condition` returns no reason because
/// the resource has no `Ready`, so such a kind gets no dot in place of a
/// healthy one.
pub fn reported(
    kinds: &Kinds,
    ends: &dyn ReferenceEnds,
    object: &Object,
    resource: &DynamicObject,
) -> Option<Level> {
    let judged = judged(kinds, ends, object, resource);
    if let Some(reason) = reason(kinds, object, &judged) {
        return Some(reason.level());
    }
    let Judged::Own(object, resource) = judged else {
        return None;
    };
    let named = kinds.of_object(&object).and_then(|k| k.condition());
    find(resource, named.unwrap_or(READY)).map(|_| Level::Healthy)
}

/// Why `resource` is at rest, when the condition that its kind knowledge
/// names is `False` with a calm reason: the condition's message, or its
/// reason when it has no message.  The page shows it as a gray line.
pub fn resting(
    kinds: &Kinds,
    ends: &dyn ReferenceEnds,
    object: &Object,
    resource: &DynamicObject,
) -> Option<String> {
    let (reason, message) = at_rest(kinds, ends, object, resource)?;
    Some(message.unwrap_or(reason))
}

/// The reason and the message of the condition that decides `object`'s
/// health, when it is `False` with a calm reason.  A sketch shows the
/// reason on the part and the message when the reader points at it.
pub fn at_rest(
    kinds: &Kinds,
    ends: &dyn ReferenceEnds,
    object: &Object,
    resource: &DynamicObject,
) -> Option<(String, Option<String>)> {
    let Judged::Own(object, resource) = judged(kinds, ends, object, resource) else {
        return None;
    };
    let named = kinds.of_object(&object)?.condition()?;
    let condition = find(resource, named)?;
    if !calm(kinds, &object, condition) {
        return None;
    }
    let text = |key: &str| capped(condition.get(key)?.as_str()?, MESSAGE_CHARS);
    Some((text("reason")?, text("message")))
}

/// Whether `condition` is the condition that `object`'s kind knowledge
/// names, with status `False` and one of the knowledge's calm reasons.
fn calm(kinds: &Kinds, object: &Object, condition: &Value) -> bool {
    let Some(knowledge) = kinds.of_object(object) else {
        return false;
    };
    let field = |key: &str| condition.get(key).and_then(Value::as_str);
    let named = field("type") == knowledge.condition();
    let reason = field("reason").is_some_and(|reason| knowledge.calm().iter().any(|c| c == reason));
    named && field("status") == Some("False") && reason
}

/// Which condition decides an object's health, and which `False`
/// reasons mean it is at rest, for drawing the object's conditions.  The
/// deciding condition is the one that the kind knowledge names, or
/// `Ready` when the knowledge names none.  Only that condition gets a
/// color, because for any other condition, such as `AttentionNeeded`,
/// `True` can be good or bad.  A kind whose health is through a
/// reference has no deciding condition of its own, so each of its own
/// conditions is drawn gray.
#[derive(Clone, Debug, PartialEq)]
pub struct Deciding {
    pub condition: Option<String>,
    pub calm: Vec<String>,
}

/// `Ready` decides, with no calm reasons, as for a kind with no
/// knowledge.
impl Default for Deciding {
    fn default() -> Self {
        Self {
            condition: Some(READY.into()),
            calm: Vec::new(),
        }
    }
}

impl Deciding {
    pub fn of(kinds: &Kinds, object: &Object) -> Self {
        let Some(knowledge) = kinds.of_object(object) else {
            return Self::default();
        };
        let condition = match &knowledge.healthy {
            Some(Healthy::Through(_)) => None,
            Some(Healthy::Condition { condition, .. }) => Some(condition.clone()),
            None => Some(READY.into()),
        };
        Self {
            condition,
            calm: knowledge.calm().to_vec(),
        }
    }

    /// The level of the condition `kind` with `status` and `reason`.  It
    /// is `None`, which the page draws gray, for any condition but the
    /// deciding one, and for the deciding one at rest.
    pub fn level(&self, kind: &str, status: &str, reason: Option<&str>) -> Option<Level> {
        if Some(kind) != self.condition.as_deref() {
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
mod tests;
