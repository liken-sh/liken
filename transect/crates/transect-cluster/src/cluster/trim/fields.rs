//! Copies the fields that kind knowledge reads into the trimmed form of
//! an object. A kind whose knowledge reads only a few fields, such as a
//! GitOps tool's source and revision, can then be stored as those fields
//! instead of whole. A whole object of such a kind can include a list of
//! every object it applied and a history that grows on each reconcile.
//! No page draws either, and each growth would count as a change.

use serde_json::{Map, Value};

/// Copies the field at each of `paths` from `from` into `into`, at the
/// same path. A path uses the syntax of kind knowledge, with `[]` for
/// each item of a list. The value at the end of a path is copied
/// whatever its type, so a path that ends at an object copies the whole
/// object.
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

/// A copy of `value` containing only the field that `segments` select,
/// or none when `value` has no such field.
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
            // An item without the field becomes an empty object, so
            // the lists from two paths have the same length and `merge`
            // can combine them item by item.
            let empty = || Value::Object(Map::new());
            let kept: Vec<Value> = items
                .map(|item| project(item, rest).unwrap_or_else(empty))
                .collect();
            Value::Array(kept)
        }
    };
    Some(Value::Object(Map::from_iter([(name.to_string(), kept)])))
}

/// Merges `from` into `into`, combining objects at every depth and
/// lists of equal length item by item.
fn merge(into: &mut Map<String, Value>, from: Map<String, Value>) {
    for (key, value) in from {
        match (into.get_mut(&key), value) {
            (Some(Value::Object(existing)), Value::Object(value)) => merge(existing, value),
            (Some(Value::Array(existing)), Value::Array(value))
                if existing.len() == value.len() =>
            {
                for (existing, value) in existing.iter_mut().zip(value) {
                    if let (Value::Object(existing), Value::Object(value)) = (existing, value) {
                        merge(existing, value);
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
