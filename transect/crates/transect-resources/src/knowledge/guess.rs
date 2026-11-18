//! Finds the fields in a custom resource's `spec` and `status` that may
//! refer to another object. This is for a kind that has no
//! `KindKnowledge` to declare its references. `addresses::guess` in
//! `transect-network` resolves each clue against the cluster.
//!
//! - A `*Ref` object gives a `name` and a `kind`. When it has no `kind`,
//!   the field name's prefix gives the kind, so a `secretRef` refers to
//!   a `Secret`.
//! - A `*Name` string gives a name, and its prefix gives the kind, such
//!   as the `Service` of a `serviceName`.
//! - Any other string may be an address when it has the form of one.
//!
//! Field names are only a convention, so every clue is a guess. A name
//! counts only when it is a valid object name, so a `displayName` with
//! a space in it refers to nothing.

use serde_json::{Map, Value};

/// The top-level fields that the walk reads.
const WALKED: [&str; 2] = ["spec", "status"];

/// A field whose name or value suggests that it refers to something.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Clue {
    /// The path of the field, with `[]` for each list level, as kind
    /// knowledge writes paths.
    pub path: String,
    pub value: String,
    pub hint: Hint,
}

/// What a clue's field suggests that it refers to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Hint {
    /// A `*Ref` that has its own `kind`, and a group when its
    /// `apiVersion` or `apiGroup` gives one.
    Kind {
        group: Option<String>,
        kind: String,
        namespace: Option<String>,
    },
    /// A `*Ref` that has only a name, or a `*Name`. The `prefix` is the
    /// field name without its suffix, such as `service` for
    /// `serviceName`.
    Prefix {
        prefix: String,
        namespace: Option<String>,
    },
    /// A string that has the form of an address.
    Address,
}

/// Every clue in the `spec` and `status` of `data`.
pub fn clues(data: &Value) -> Vec<Clue> {
    let mut found = Vec::new();
    for top in WALKED {
        if let Some(value) = data.get(top) {
            walk(value, top, &mut found);
        }
    }
    found
}

/// Adds the clues under `value` to `found`. A field that is itself a
/// clue is not searched further.
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

/// The clue of the field `key` when its name ends in `Ref` or `Name`
/// and its value has the matching shape.
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

/// The clue of a `*Ref` object: its `name`, with its `kind` or, when it
/// has none, the kind that its prefix names.
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

/// The group of an `apiVersion`: `apps` for `apps/v1`, and the empty
/// core group for `v1`.
fn group_of(api_version: &str) -> &str {
    api_version
        .rsplit_once('/')
        .map(|(group, _)| group)
        .unwrap_or_default()
}

/// Whether `value` is a valid Kubernetes object name, which is a DNS
/// subdomain.
fn is_name(value: &str) -> bool {
    let fits = !value.is_empty() && value.len() <= 253;
    let chars = value
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '.');
    let ends = |c: Option<char>| c.is_some_and(|c| c.is_ascii_alphanumeric());
    fits && chars && ends(value.chars().next()) && ends(value.chars().last())
}

/// Whether the field at `path` may hold an address. An image reference
/// or a path can look like a host name followed by a path, but it does
/// not name a service that the object connects to. So a field named
/// `image` or `path`, or ending in `Image` or `Path`, does not count.
fn may_hold_address(path: &str) -> bool {
    let field = path.rsplit('.').next().unwrap_or(path);
    let field = field.trim_end_matches("[]");
    let named = |word: &str| {
        let title = format!("{}{}", word[..1].to_uppercase(), &word[1..]);
        field == word || field.ends_with(&title)
    };
    !named("image") && !named("path")
}

/// Whether `text` could be an address. `transect-network` decides
/// whether it is one.
fn is_address(text: &str) -> bool {
    (text.contains('.') || text.contains(':')) && !text.contains(char::is_whitespace)
}

#[cfg(test)]
mod tests;
