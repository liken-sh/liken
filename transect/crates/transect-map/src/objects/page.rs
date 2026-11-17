//! The page of any object of any kind: its board, with its own
//! conditions and facts in the middle and what it names and what names
//! it around them, and its card.  The spec of a kind that transect has
//! no knowledge of may contain a credential, and the page has no way to
//! tell, so the page never renders a raw spec.

use askama::Template;
use serde_json::Value;

use super::{ObjectBoard, ObjectCard};
use transect_frame::frame::crumbs::Crumb;
use transect_graph::untrusted::capped;
use transect_observe::health::{Deciding, Level};

/// The longest, in characters, that each text field of a condition runs
/// on the page.
const MESSAGE_CHARS: usize = 300;

#[derive(Template)]
#[template(path = "objects/object.html", blocks = ["main"])]
pub struct ObjectPage {
    pub kind: String,
    pub name: String,
    /// Its namespace and the layer the namespace is in, for a namespaced
    /// kind.
    pub place: Option<(String, String)>,
    /// The kind's plural, "Certificates", and its list in the namespace
    /// when it has one.
    pub label: String,
    pub kind_href: Option<String>,
    /// The page's own path, which its event stream's URL includes.
    pub path: String,
    pub board: ObjectBoard,
    pub card: ObjectCard,
}

/// One of the object's conditions.
#[derive(Clone, Debug, PartialEq)]
pub struct Condition {
    pub kind: String,
    pub status: String,
    pub reason: Option<String>,
    pub message: Option<String>,
    /// The level its dot draws, or none for gray: only the condition
    /// that decides the object's health takes a color.
    pub level: Option<Level>,
}

/// Every condition in `data`'s `status.conditions`, in the order the
/// controller writes them, each with the level that `deciding` gives it.
pub fn conditions(data: &Value, deciding: &Deciding) -> Vec<Condition> {
    let found = data.pointer("/status/conditions").and_then(Value::as_array);
    found
        .into_iter()
        .flatten()
        .filter_map(|condition| {
            let text = |key: &str| {
                let value = condition.get(key)?.as_str()?;
                capped(value, MESSAGE_CHARS)
            };
            let kind = text("type")?;
            let status = text("status").unwrap_or_else(|| "Unknown".into());
            let reason = text("reason");
            Some(Condition {
                level: deciding.level(&kind, &status, reason.as_deref()),
                kind,
                status,
                reason,
                message: text("message"),
            })
        })
        .collect()
}

impl ObjectPage {
    /// The crumb: the cluster, then the layer and the namespace for a
    /// namespaced object, then its kind and its name.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let mut crumbs = vec![Crumb::home()];
        if let Some((namespace, layer)) = &self.place {
            let select = format!("/?select={namespace}");
            crumbs.push(Crumb::link(layer, format!("{select}#{layer}")));
            crumbs.push(Crumb::link_name(
                namespace,
                transect_graph::links::namespace(namespace),
            ));
        }
        crumbs.push(match &self.kind_href {
            Some(href) => Crumb::link(&self.label, href.clone()),
            None => Crumb::current(&self.label),
        });
        crumbs.push(Crumb::current_name(&self.name));
        crumbs
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    fn reads_every_condition_in_order() {
        let data = serde_json::json!({"status": {"conditions": [
            {"type": "Ready", "status": "False", "reason": "Idle", "message": "nothing\nto do"},
            {"type": "Synced", "status": "True"},
            {"status": "True"},
        ]}});
        let found: Vec<(String, String, Option<String>, Option<String>)> =
            conditions(&data, &Deciding::default())
                .into_iter()
                .map(|c| (c.kind, c.status, c.reason, c.message))
                .collect();
        assert_eq!(
            found,
            vec![
                (
                    "Ready".into(),
                    "False".into(),
                    Some("Idle".into()),
                    Some("nothing to do".into())
                ),
                ("Synced".into(), "True".into(), None, None),
            ]
        );
    }
}
