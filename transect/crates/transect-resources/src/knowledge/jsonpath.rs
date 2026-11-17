//! The part of JSONPath a CRD's printer columns use: dotted fields,
//! `[n]`, `[*]`, and a filter of one equality, `[?(@.field=="value")]`,
//! as in `.status.conditions[?(@.type=="Ready")].status`.  A path that
//! uses anything else is refused with the part it could not read, so a
//! page can show which column it left out and why.

use serde_json::Value;

/// One step of a path.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Step {
    Field(String),
    Index(usize),
    /// Every item of a list.
    Each,
    /// The items of a list whose `field` is the text `value`.
    Filter {
        field: String,
        value: String,
    },
}

/// The steps of `path`, or the part of it outside the subset.
pub fn parse(path: &str) -> Result<Vec<Step>, String> {
    let path = path.trim();
    let path = path
        .strip_prefix('{')
        .and_then(|inner| inner.strip_suffix('}'))
        .unwrap_or(path);
    let mut steps = Vec::new();
    let mut rest = path.strip_prefix('$').unwrap_or(path);
    while !rest.is_empty() {
        if let Some(after) = rest.strip_prefix('.') {
            let end = after.find(['.', '[']).unwrap_or(after.len());
            let name = &after[..end];
            if name.is_empty() || !name.chars().all(field_char) {
                return Err(format!(".{name}"));
            }
            steps.push(Step::Field(name.into()));
            rest = &after[end..];
        } else if let Some(after) = rest.strip_prefix('[') {
            let end = after.find(']').ok_or_else(|| format!("[{after}"))?;
            steps.push(bracket(&after[..end])?);
            rest = &after[end + 1..];
        } else {
            return Err(rest.into());
        }
    }
    Ok(steps)
}

fn field_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || c == '_' || c == '-'
}

/// The step inside one pair of brackets.
fn bracket(inner: &str) -> Result<Step, String> {
    let refused = || format!("[{inner}]");
    if inner == "*" {
        return Ok(Step::Each);
    }
    if let Ok(index) = inner.parse::<usize>() {
        return Ok(Step::Index(index));
    }
    let filter = inner
        .strip_prefix("?(@.")
        .and_then(|filter| filter.strip_suffix(')'))
        .ok_or_else(refused)?;
    let (field, value) = filter.split_once("==").ok_or_else(refused)?;
    let quoted = |text: &str| {
        let text = text.trim();
        let unquoted = text
            .strip_prefix('"')
            .and_then(|t| t.strip_suffix('"'))
            .or_else(|| text.strip_prefix('\'').and_then(|t| t.strip_suffix('\'')));
        unquoted
            .filter(|inner| !inner.contains(['"', '\'']))
            .map(String::from)
    };
    let field = field.trim();
    if field.is_empty() || !field.chars().all(field_char) {
        return Err(refused());
    }
    let value = quoted(value).ok_or_else(refused)?;
    Ok(Step::Filter {
        field: field.into(),
        value,
    })
}

/// Every value `steps` reach in `value`, in order.  A missing field
/// reaches nothing.
pub fn values<'a>(value: &'a Value, steps: &[Step]) -> Vec<&'a Value> {
    let mut found = vec![value];
    for step in steps {
        found = found
            .into_iter()
            .flat_map(|value| -> Vec<&Value> {
                match step {
                    Step::Field(name) => value.get(name).into_iter().collect(),
                    Step::Index(index) => value.get(*index).into_iter().collect(),
                    Step::Each => value.as_array().into_iter().flatten().collect(),
                    Step::Filter {
                        field,
                        value: wanted,
                    } => {
                        let items = value.as_array().into_iter().flatten();
                        let matches = |item: &&Value| {
                            item.get(field).and_then(Value::as_str) == Some(wanted.as_str())
                        };
                        items.filter(matches).collect()
                    }
                }
            })
            .collect();
    }
    found
}

/// A value as a column shows it: text as it is, a number or a boolean
/// as JSON writes it, and a list of them joined by commas.  An object
/// shows as a count of its fields, "{3 fields}", and a list that holds
/// an object or a list as a count of its items, "[2 items]", never as
/// JSON, so a CRD's column on `.spec` cannot print the spec.
pub fn text(value: &Value) -> String {
    match value {
        Value::Array(items) if items.iter().all(is_scalar) => {
            items.iter().map(scalar).collect::<Vec<_>>().join(",")
        }
        Value::Array(items) => counted(items.len(), "item", "[]"),
        Value::Object(fields) => counted(fields.len(), "field", "{}"),
        other => scalar(other),
    }
}

fn is_scalar(value: &Value) -> bool {
    !matches!(value, Value::Array(_) | Value::Object(_))
}

fn scalar(value: &Value) -> String {
    match value {
        Value::String(text) => text.clone(),
        Value::Null => String::new(),
        other => other.to_string(),
    }
}

/// "{3 fields}" or "[1 item]".
fn counted(count: usize, noun: &str, brackets: &str) -> String {
    let (open, close) = brackets.split_at(1);
    let plural = if count == 1 { "" } else { "s" };
    format!("{open}{count} {noun}{plural}{close}")
}

/// Whether `text` is a count `text` writes for an object or a list,
/// which a page draws gray, because it replaces a value the page does
/// not show.  The trimmed store keeps a column's value as text, so this
/// function recognizes the count by its form.
pub fn counted_structure(text: &str) -> bool {
    let inner = match (text.strip_prefix('{'), text.strip_prefix('[')) {
        (Some(rest), _) => rest.strip_suffix('}').map(|inner| (inner, "field")),
        (_, Some(rest)) => rest.strip_suffix(']').map(|inner| (inner, "item")),
        _ => None,
    };
    let Some((inner, noun)) = inner else {
        return false;
    };
    let Some((count, word)) = inner.split_once(' ') else {
        return false;
    };
    let plural = format!("{noun}s");
    count.parse::<usize>().is_ok() && (word == noun || word == plural)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn object() -> Value {
        serde_json::json!({
            "metadata": {"name": "front"},
            "spec": {"replicas": 3, "hosts": ["a.test", "b.test"], "paused": false},
            "status": {"conditions": [
                {"type": "Synced", "status": "True"},
                {"type": "Ready", "status": "False"}
            ]}
        })
    }

    fn read(path: &str) -> Result<Vec<String>, String> {
        let object = object();
        let steps = parse(path)?;
        Ok(values(&object, &steps).into_iter().map(text).collect())
    }

    #[rstest]
    #[case::a_field(".metadata.name", Ok(vec!["front"]))]
    #[case::a_number(".spec.replicas", Ok(vec!["3"]))]
    #[case::a_boolean(".spec.paused", Ok(vec!["false"]))]
    #[case::an_index(".spec.hosts[1]", Ok(vec!["b.test"]))]
    #[case::every_item(".spec.hosts[*]", Ok(vec!["a.test", "b.test"]))]
    #[case::a_list(".spec.hosts", Ok(vec!["a.test,b.test"]))]
    #[case::an_object(".spec", Ok(vec!["{3 fields}"]))]
    #[case::an_object_of_one_field(".metadata", Ok(vec!["{1 field}"]))]
    #[case::a_list_of_objects(".status.conditions", Ok(vec!["[2 items]"]))]
    #[case::a_filter(r#".status.conditions[?(@.type=="Ready")].status"#, Ok(vec!["False"]))]
    #[case::a_filter_in_single_quotes(".status.conditions[?(@.type=='Synced')].status", Ok(vec!["True"]))]
    #[case::braces("{.metadata.name}", Ok(vec!["front"]))]
    #[case::a_missing_field(".status.phase", Ok(vec![]))]
    #[case::an_index_past_the_end(".spec.hosts[5]", Ok(vec![]))]
    #[case::a_slice(".spec.hosts[0:1]", Err("[0:1]".into()))]
    #[case::a_recursive_descent("..name", Err(".".into()))]
    #[case::a_filter_with_and(r#".status.conditions[?(@.type=="Ready" && @.status=="True")]"#, Err(r#"[?(@.type=="Ready" && @.status=="True")]"#.into()))]
    fn reads_the_subset_of_jsonpath_columns_use(
        #[case] path: &str,
        #[case] expected: Result<Vec<&str>, String>,
    ) {
        let expected = expected.map(|found| found.into_iter().map(String::from).collect());
        assert_eq!(read(path), expected);
    }

    #[rstest]
    #[case::fields("{3 fields}", true)]
    #[case::one_field("{1 field}", true)]
    #[case::items("[2 items]", true)]
    #[case::text("FL,FR", false)]
    #[case::braces_around_words("{not a count}", false)]
    fn tells_a_count_of_a_structure_from_a_value(#[case] text: &str, #[case] counted: bool) {
        assert_eq!(counted_structure(text), counted);
    }
}
