//! Sorting the documents of every file into the resources they are.
//! The sort is pure, so each way a document can go wrong is a table
//! test, beside the binary's `Sorts`.

use serde::Deserialize;

use std::sync::Arc;

use super::{Condition, DocId, Document, File, GROUP, Overlay, Sorts, VERSION};

/// One file as read from the directory: its path under it, and its text
/// or why it could not be read.
pub struct Read {
    pub path: String,
    pub text: Result<String, String>,
}

/// Every resource in `files`, in their order, with a condition for each
/// document.
pub fn sort<S: Sorts>(files: Vec<Read>) -> S {
    let mut sorted = S::default();
    for (at, read) in files.into_iter().enumerate() {
        let mut file = File {
            path: read.path,
            problem: None,
            documents: Vec::new(),
        };
        match read.text.and_then(|text| documents(&text)) {
            Err(problem) => file.problem = Some(problem),
            Ok(values) => {
                let values = values.into_iter().filter(|value| !value.is_null());
                for (document, value) in values.enumerate() {
                    let doc = DocId { file: at, document };
                    let (kind, name) = identify(&value);
                    let condition = take(&mut sorted, doc, &file.path, value);
                    file.documents.push(Document {
                        kind,
                        name,
                        conditions: vec![condition],
                    });
                }
            }
        }
        sorted.directory().files.push(file);
    }
    sorted
}

/// The YAML documents in `text`, or why it is not YAML.
fn documents(text: &str) -> Result<Vec<serde_yaml::Value>, String> {
    serde_yaml::Deserializer::from_str(text)
        .map(serde_yaml::Value::deserialize)
        .collect::<Result<_, _>>()
        .map_err(|error| format!("not YAML: {error}"))
}

/// A document's kind and name, as the page lists it, or blanks for a
/// document that does not give them.
fn identify(value: &serde_yaml::Value) -> (String, String) {
    let text = |value: Option<&serde_yaml::Value>| {
        value
            .and_then(|value| value.as_str())
            .unwrap_or_default()
            .to_string()
    };
    let metadata = value.get("metadata");
    let name = text(metadata.and_then(|metadata| metadata.get("name")));
    let name = match text(metadata.and_then(|metadata| metadata.get("namespace"))) {
        namespace if namespace.is_empty() => name,
        namespace => format!("{namespace}/{name}"),
    };
    (text(value.get("kind")), name)
}

/// Keeps `value` as the resource its kind names, or gives the reason it
/// does not.  A document of `GROUP` goes to `sorted`; any other is an
/// overlay.
fn take<S: Sorts>(sorted: &mut S, doc: DocId, path: &str, value: serde_yaml::Value) -> Condition {
    let api_version = value.get("apiVersion").and_then(|v| v.as_str());
    let kind = value
        .get("kind")
        .and_then(|v| v.as_str())
        .unwrap_or_default()
        .to_string();
    let ours = api_version.is_some_and(|v| v == format!("{GROUP}/{VERSION}"));
    let our_group = api_version.is_some_and(|v| v.starts_with(&format!("{GROUP}/")));
    match (ours, kind) {
        (true, kind) => sorted.take(doc, &kind, value).unwrap_or_else(|| {
            Condition::left_out("UnknownKind", format!("{GROUP} has no kind {kind:?}"))
        }),
        (false, _) if our_group => Condition::left_out(
            "UnknownVersion",
            format!("transect reads {GROUP}/{VERSION}"),
        ),
        (false, _) => match Overlay::parse(doc, path, value) {
            Ok(overlay) => {
                Arc::make_mut(&mut sorted.directory().overlays).push(overlay);
                Condition::loaded()
            }
            Err(message) => Condition::left_out("Invalid", message),
        },
    }
}
