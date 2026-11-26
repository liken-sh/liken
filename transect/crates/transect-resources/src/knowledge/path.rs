//! Reads a field path from kind knowledge, such as
//! `status.screen.node` or `spec.remotes[].name`, out of an object.
//! Each `[]` reads every item of a list, so one path can return several
//! values. A last segment that ends in `[keys]` reads the keys of a map,
//! such as the names of the `Signal`s in a purview that maps each name
//! to its scopes. A path that reaches a missing field, or a value of another
//! type, returns nothing rather than an error, because an operator
//! writes its status over time, and a field it has not written yet is
//! absent.

use serde_json::Value;

use transect_graph::edges::Object;

/// The strings that `path` reads from `object`. `data` holds the
/// object's spec and status but not its metadata, so `metadata.name`
/// and `metadata.namespace` are read from `object`. Kind knowledge needs
/// `metadata.name` because an operator often gives the `Secret` it
/// writes the name of its own resource, and no spec field holds that
/// name.
pub fn read_of(object: &Object, data: &Value, path: &str) -> Vec<String> {
    match path {
        "metadata.name" => vec![object.name.clone()],
        "metadata.namespace" => object.namespace.iter().cloned().collect(),
        _ => read(data, path),
    }
}

/// The strings that `path` reads from `value`, in order. A number
/// becomes its decimal text, because a name or a port can be a number.
/// A value of any other type at the end of the path is skipped.
pub fn read(value: &Value, path: &str) -> Vec<String> {
    if let Some(map) = path.strip_suffix(KEYS) {
        let maps = walk(value, map).into_iter().filter_map(Value::as_object);
        return maps.flat_map(|map| map.keys().cloned()).collect();
    }
    walk(value, path).into_iter().filter_map(text).collect()
}

/// The suffix of a path's last segment that reads the keys of a map. A
/// bracket and not a brace, because a path never contains a brace, and
/// a reference tells a name template from a path by its braces.
const KEYS: &str = "[keys]";

/// Whether `path` reaches any value in `value` that is not null. Use
/// it for a field whose value is an object, such as a reference to a
/// source, which `read` skips.
pub fn holds(value: &Value, path: &str) -> bool {
    walk(value, path).into_iter().any(|value| !value.is_null())
}

/// Every value that `path` reaches in `value`, in order. For
/// `report.items[]` these are the items of the list.
pub fn values<'a>(value: &'a Value, path: &str) -> Vec<&'a Value> {
    walk(value, path)
}

fn walk<'a>(value: &'a Value, path: &str) -> Vec<&'a Value> {
    let mut found = vec![value];
    let last = path.split('.').count() - 1;
    for (at, segment) in path.split('.').enumerate() {
        // The keys of a map are names, not values that a later segment
        // can read into, so `[keys]` reads nothing before the end.
        let segment = match (segment.strip_suffix(KEYS), at == last) {
            (Some(map), true) => map,
            (Some(_), false) => return Vec::new(),
            (None, _) => segment,
        };
        let (name, each) = match segment.strip_suffix("[]") {
            Some(name) => (name, true),
            None => (segment, false),
        };
        let fields = found.into_iter().filter_map(|value| value.get(name));
        found = match each {
            true => fields
                .filter_map(Value::as_array)
                .flat_map(|items| items.iter())
                .collect(),
            false => fields.collect(),
        };
    }
    found
}

fn text(value: &Value) -> Option<String> {
    match value {
        Value::String(text) if !text.is_empty() => Some(text.clone()),
        Value::Number(number) => Some(number.to_string()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::an_object("spec.sourceRef", true)]
    #[case::a_list_item("spec.items[].name", true)]
    #[case::missing("spec.colour", false)]
    #[case::null("spec.unset", false)]
    #[case::a_map_by_its_keys("spec.sourceRef[keys]", true)]
    fn says_whether_a_path_reaches_a_value(#[case] path: &str, #[case] expected: bool) {
        let value = serde_json::json!({"spec": {"sourceRef": {"kind": "Repo"}, "items": [{"name": "a"}], "unset": null}});
        assert_eq!(holds(&value, path), expected);
    }

    const WIDGET: &str = "
spec:
  owner: shop
  port: 8080
  remotes: [{name: hall}, {name: den}, {label: none}]
  hosts: [a.example.com, b.example.com]
  empty: ''
  purview: {porch: [front], doorbell: []}
status:
  screen: {node: alpha, modes: [{size: 4k}]}
  conditions: [{type: Ready}]
";

    fn widget() -> Value {
        serde_yaml_ng::from_str(WIDGET).unwrap()
    }

    #[rstest]
    #[case::a_field("spec.owner", vec!["shop"])]
    #[case::a_nested_field("status.screen.node", vec!["alpha"])]
    #[case::a_number("spec.port", vec!["8080"])]
    #[case::a_field_of_each_item("spec.remotes[].name", vec!["hall", "den"])]
    #[case::each_item_of_a_list_of_text("spec.hosts[]", vec!["a.example.com", "b.example.com"])]
    #[case::a_list_inside_an_object("status.screen.modes[].size", vec!["4k"])]
    #[case::a_missing_field("status.receiver.name", vec![])]
    #[case::a_missing_list("spec.sinks[].name", vec![])]
    #[case::an_object_not_text("status.screen", vec![])]
    #[case::a_list_read_as_a_field("spec.hosts", vec![])]
    #[case::an_empty_value("spec.empty", vec![])]
    #[case::the_keys_of_a_map("spec.purview[keys]", vec!["doorbell", "porch"])]
    #[case::the_keys_of_no_map("spec.owner[keys]", vec![])]
    #[case::the_keys_of_a_missing_map("spec.actors[keys]", vec![])]
    #[case::keys_before_the_end("spec.purview[keys].name", vec![])]
    fn reads_each_value_a_path_names(#[case] path: &str, #[case] expected: Vec<&str>) {
        assert_eq!(read(&widget(), path), expected);
    }

    #[rstest]
    #[case::its_name("metadata.name", Some("shop"), vec!["front"])]
    #[case::its_namespace("metadata.namespace", Some("shop"), vec!["shop"])]
    #[case::no_namespace("metadata.namespace", None, vec![])]
    #[case::other_metadata("metadata.labels", Some("shop"), vec![])]
    #[case::its_spec("spec.owner", Some("shop"), vec!["shop"])]
    fn reads_an_objects_name_from_its_metadata(
        #[case] path: &str,
        #[case] namespace: Option<&str>,
        #[case] expected: Vec<&str>,
    ) {
        let object = Object::new("widgets.example.com", "Widget", namespace, "front");
        assert_eq!(read_of(&object, &widget(), path), expected);
    }
}
