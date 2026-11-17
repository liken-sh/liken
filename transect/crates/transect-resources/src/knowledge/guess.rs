//! Guessed references, for a custom kind with no knowledge: its spec
//! and status walked for fields whose names suggest they name
//! something.  A `*Ref` names an object by its `kind` and `name`, or by
//! the kind its prefix names when it has only a name, as a `secretRef`
//! does.  A `*Name` names an object of the kind its prefix names, such
//! as a `serviceName`, or, for a prefix that names no kind, the one
//! object of that name in its namespace.  Any other text that has the
//! form of one address is resolved as a configured address is.
//!
//! Field names are a convention, not a contract, so every match is a
//! guess and only a name that is a valid object name counts: a
//! `displayName` of "Living Room" names nothing.  Kind knowledge turns
//! the walk off for its kind, because its references come from the
//! operator.

use serde_json::{Map, Value};

/// The top-level fields walked: what the object asks for and what its
/// controller reports.
const WALKED: [&str; 2] = ["spec", "status"];

/// One field whose name or value suggests it names something.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Clue {
    /// Where it was read, with `[]` for each item of a list, as kind
    /// knowledge writes paths.
    pub path: String,
    pub value: String,
    pub hint: Hint,
}

/// What a clue's field suggests it names.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Hint {
    /// A `*Ref` with its own `kind`, and its group when its `apiVersion`
    /// or `apiGroup` gives one.
    Kind {
        group: Option<String>,
        kind: String,
        namespace: Option<String>,
    },
    /// A `*Ref` with only a name, or a `*Name`, by the field's prefix,
    /// such as `service` of `serviceName`.
    Prefix {
        prefix: String,
        namespace: Option<String>,
    },
    /// Any other text that has the form of an address.
    Address,
}

/// Every clue in `data`'s spec and status.
pub fn clues(data: &Value) -> Vec<Clue> {
    let mut found = Vec::new();
    for top in WALKED {
        if let Some(value) = data.get(top) {
            walk(value, top, &mut found);
        }
    }
    found
}

fn walk(value: &Value, path: &str, found: &mut Vec<Clue>) {
    match value {
        Value::Object(fields) => {
            for (key, field) in fields {
                let at = format!("{path}.{key}");
                match clue(key, field, &at) {
                    Some(clue) => found.push(clue),
                    None => walk(field, &at, found),
                }
            }
        }
        Value::Array(items) => {
            let at = format!("{path}[]");
            for item in items {
                walk(item, &at, found);
            }
        }
        Value::String(text) if is_address(text) && may_hold_address(path) => found.push(Clue {
            path: path.into(),
            value: text.clone(),
            hint: Hint::Address,
        }),
        _ => {}
    }
}

/// The clue `key` gives when its name suggests it names something.
fn clue(key: &str, field: &Value, path: &str) -> Option<Clue> {
    if let Some(prefix) = key.strip_suffix("Ref").filter(|p| !p.is_empty()) {
        let fields = field.as_object()?;
        return reference(prefix, fields, path);
    }
    let prefix = key.strip_suffix("Name").filter(|p| !p.is_empty())?;
    let value = field.as_str().filter(|value| is_name(value))?;
    Some(Clue {
        path: path.into(),
        value: value.into(),
        hint: Hint::Prefix {
            prefix: prefix.into(),
            namespace: None,
        },
    })
}

/// The clue of a `*Ref`'s fields: its `name`, and its `kind` or else
/// the kind of its prefix.
fn reference(prefix: &str, fields: &Map<String, Value>, path: &str) -> Option<Clue> {
    let text = |key: &str| fields.get(key).and_then(Value::as_str);
    let name = text("name").filter(|name| is_name(name))?;
    let namespace = text("namespace").map(String::from);
    let hint = match text("kind") {
        Some(kind) => Hint::Kind {
            group: text("apiGroup")
                .or_else(|| text("apiVersion").map(group_of))
                .map(String::from),
            kind: kind.into(),
            namespace,
        },
        None => Hint::Prefix {
            prefix: prefix.into(),
            namespace,
        },
    };
    Some(Clue {
        path: path.into(),
        value: name.into(),
        hint,
    })
}

/// The group of an `apiVersion`: `apps` of `apps/v1`, and the core
/// group, empty, of `v1`.
fn group_of(api_version: &str) -> &str {
    api_version
        .rsplit_once('/')
        .map(|(group, _)| group)
        .unwrap_or_default()
}

/// Whether `value` is a valid object name: a DNS subdomain of lowercase
/// letters, digits, `-`, and `.`, at most 253 long.
fn is_name(value: &str) -> bool {
    let fits = !value.is_empty() && value.len() <= 253;
    let chars = value
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '.');
    let ends = |c: Option<char>| c.is_some_and(|c| c.is_ascii_alphanumeric());
    fits && chars && ends(value.chars().next()) && ends(value.chars().last())
}

/// Whether the field at `path` may hold an address.  An image reference
/// and a file path both read like a host and a path, and neither is a
/// place the object calls: an image names the registry the kubelet
/// pulls from, and a path names a directory.
fn may_hold_address(path: &str) -> bool {
    let field = path.rsplit('.').next().unwrap_or(path);
    let field = field.trim_end_matches("[]");
    let named = |word: &str| {
        let title = format!("{}{}", word[..1].to_uppercase(), &word[1..]);
        field == word || field.ends_with(&title)
    };
    !named("image") && !named("path")
}

/// Whether `text` could be an address: it has a dot or a colon and no
/// space, so a sentence or a single word is left alone.  Address
/// resolution decides whether it is one.
fn is_address(text: &str) -> bool {
    (text.contains('.') || text.contains(':')) && !text.contains(char::is_whitespace)
}

#[cfg(test)]
mod tests;
