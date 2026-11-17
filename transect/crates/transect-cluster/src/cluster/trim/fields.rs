//! The fields kind knowledge reads, copied out of an object a trimmed
//! watch cuts, so a kind whose knowledge reads a handful of fields, such
//! as a GitOps tool's source and revision, is held as those fields and
//! not whole.  A whole object of such a kind can hold a list of every
//! object it applied and a history that grows on each reconcile, which
//! a page never draws and which would count as a change each time.

use serde_json::{Map, Value};

/// Copies each field `paths` names from `from` into `into`, at the same
/// path.  A path is written as kind knowledge writes it, with `[]` for
/// each item of a list, and copies the value it ends at whatever its
/// shape, so a path to an object keeps the whole object.
pub fn copy(from: &Value, into: &mut Map<String, Value>, paths: &[String]) {
    for path in paths {
        let segments: Vec<&str> = path.split('.').collect();
        if let Some(found) = project(from, &segments)
            && let Value::Object(found) = found
        {
            merge(into, found);
        }
    }
}

/// `value` with only what `segments` reach, or none when they reach
/// nothing.
fn project(value: &Value, segments: &[&str]) -> Option<Value> {
    let Some((segment, rest)) = segments.split_first() else {
        return Some(value.clone());
    };
    let (name, each) = match segment.strip_suffix("[]") {
        Some(name) => (name, true),
        None => (*segment, false),
    };
    let field = value.get(name)?;
    let kept = match each {
        false => project(field, rest)?,
        true => {
            let items = field.as_array()?.iter();
            // An item without the field keeps its place as an empty
            // object, so the lists two paths project stay the same
            // length and merge item by item.
            let empty = || Value::Object(Map::new());
            let kept: Vec<Value> = items
                .map(|item| project(item, rest).unwrap_or_else(empty))
                .collect();
            Value::Array(kept)
        }
    };
    Some(Value::Object(Map::from_iter([(name.to_string(), kept)])))
}

/// Merges `from` into `into`, joining objects at every depth.  Two
/// paths into one list each keep a projection of every item, so the
/// lists merge item by item.
fn merge(into: &mut Map<String, Value>, from: Map<String, Value>) {
    for (key, value) in from {
        match (into.get_mut(&key), value) {
            (Some(Value::Object(held)), Value::Object(value)) => merge(held, value),
            (Some(Value::Array(held)), Value::Array(value)) if held.len() == value.len() => {
                for (held, value) in held.iter_mut().zip(value) {
                    if let (Value::Object(held), Value::Object(value)) = (held, value) {
                        merge(held, value);
                    }
                }
            }
            (_, value) => {
                into.insert(key, value);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;
    use serde_json::json;

    use super::*;

    fn copied(paths: &[&str]) -> Value {
        let from = json!({
            "spec": {"path": "./shop", "sourceRef": {"kind": "Repo", "name": "fleet"}, "secret": "hunter2"},
            "status": {
                "conditions": [{"type": "Ready"}],
                "history": [{"version": "2", "digest": "a"}, {"version": "1", "digest": "b"}],
                "inventory": {"entries": [{"id": "x"}]}
            }
        });
        let mut into = Map::new();
        into.insert("status".into(), json!({"conditions": [{"type": "Ready"}]}));
        let paths: Vec<String> = paths.iter().map(|path| path.to_string()).collect();
        copy(&from, &mut into, &paths);
        Value::Object(into)
    }

    #[rstest]
    #[case::a_field(&["spec.path"], json!({"spec": {"path": "./shop"}, "status": {"conditions": [{"type": "Ready"}]}}))]
    #[case::an_object(
        &["spec.sourceRef"],
        json!({"spec": {"sourceRef": {"kind": "Repo", "name": "fleet"}}, "status": {"conditions": [{"type": "Ready"}]}})
    )]
    #[case::a_field_of_each_item(
        &["status.history[].version"],
        json!({"status": {"conditions": [{"type": "Ready"}], "history": [{"version": "2"}, {"version": "1"}]}})
    )]
    #[case::two_fields_of_each_item(
        &["status.history[].version", "status.history[].digest"],
        json!({"status": {"conditions": [{"type": "Ready"}], "history": [{"version": "2", "digest": "a"}, {"version": "1", "digest": "b"}]}})
    )]
    #[case::a_missing_field(&["spec.chart.name"], json!({"status": {"conditions": [{"type": "Ready"}]}}))]
    #[case::a_field_some_items_lack(
        &["status.history[].version", "status.history[].digest", "status.history[].note"],
        json!({"status": {"conditions": [{"type": "Ready"}], "history": [{"version": "2", "digest": "a"}, {"version": "1", "digest": "b"}]}})
    )]
    fn copies_only_the_fields_named(#[case] paths: &[&str], #[case] expected: Value) {
        assert_eq!(copied(paths), expected);
    }
}
