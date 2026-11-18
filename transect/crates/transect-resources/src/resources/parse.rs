//! Sorts the documents of every file into resources.  Sorting does no
//! I/O, so a test can give it YAML text directly.

use serde::Deserialize;

use std::sync::Arc;

use super::{Condition, DocId, Document, File, GROUP, Overlay, Sorts, VERSION};

/// One file from the directory: its path relative to the directory, and
/// its text or the error from reading it.
pub struct Read {
    pub path: String,
    pub text: Result<String, String>,
}

/// Sorts every document in `files` into `S`, in file order, and records
/// a condition for each document.  Empty documents are skipped.
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

/// The YAML documents in `text`, or an error message if `text` is not
/// valid YAML.
fn documents(text: &str) -> Result<Vec<serde_yaml::Value>, String> {
    serde_yaml::Deserializer::from_str(text)
        .map(serde_yaml::Value::deserialize)
        .collect::<Result<_, _>>()
        .map_err(|error| format!("not YAML: {error}"))
}

/// A document's kind and its name, as the page lists them.  The name is
/// `namespace/name` when the document has a namespace.  Either is empty
/// when the document does not set it.
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

/// Stores `value` in `sorted` and returns the document's condition.  A
/// document of `GROUP/VERSION` goes to `Sorts::take`, a document of
/// another version of `GROUP` is left out as `UnknownVersion`, and any
/// other document is parsed as an overlay.
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
