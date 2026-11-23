//! `transect`'s own resources, read from the YAML files under
//! `--resources`: the `Layers`, kind knowledge, device knowledge,
//! Intents, Outsides, registry mirrors, the log source and its
//! patterns, the metrics source, and overlays.
//!
//! A document that fails to parse or validate does not stop
//! `transect`. The loader leaves it out and records a condition with
//! the reason, in the shape of a CRD's `status.conditions`, and the
//! `/resources` page lists the conditions. A condition that needs the
//! live cluster, such as an overlay that matches no object, is added
//! by the page from each reading of the cluster.
//!
//! This module reads the directory, keeps the overlays, and passes each
//! document of `GROUP` to a `Sorts`. Each domain crate parses its own
//! kinds. The binary implements `Sorts`, because it is the only crate
//! that depends on every domain, and sends each kind to its parser. A
//! domain reads its own resources from the loaded set through `Field`.

mod condition;
mod field;
mod load;
mod off;
pub mod overlay;
mod page;
mod parse;
mod reload;

use std::collections::BTreeMap;
use std::sync::Arc;

pub use condition::{Condition, Status};
pub use field::Field;
#[cfg(any(test, feature = "fixtures"))]
pub use load::load;
pub use off::OffLine;
pub use overlay::{Overlaid, Overlay};
pub use page::ResourcesPage;
#[cfg(any(test, feature = "fixtures"))]
pub use parse::{Read, sort};
pub use reload::watch;

/// The API group of `transect`'s resources, and the prefix of every
/// annotation that `transect` reads, as in `transect.liken.sh/why`.
pub const GROUP: &str = "transect.liken.sh";

/// The version every resource of `GROUP` has.
pub const VERSION: &str = "v1alpha1";

/// The value of the annotation `<GROUP>/<name>` in `annotations`.
pub fn annotation<'a>(
    annotations: Option<&'a BTreeMap<String, String>>,
    name: &str,
) -> Option<&'a str> {
    annotations?
        .iter()
        .find(|(key, _)| field(key) == Some(name))
        .map(|(_, value)| value.as_str())
}

/// The name after `<GROUP>/` in an annotation's key, or none for a key
/// with another prefix.
pub fn field(key: &str) -> Option<&str> {
    key.strip_prefix(GROUP)?.strip_prefix('/')
}

/// Loads `text` as the loader loads a file at `path` in the resources
/// directory. The fixture cluster uses it.
#[cfg(any(test, debug_assertions))]
pub fn from_text<S: Sorts>(path: &str, text: &str) -> S {
    parse::sort(vec![parse::Read {
        path: path.into(),
        text: Ok(text.into()),
    }])
}

/// Identifies one document by the index of its file among the loaded
/// files and its index in that file.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct DocId {
    pub file: usize,
    pub document: usize,
}

/// The loaded set that the loader sorts every document of `GROUP`
/// into: the `Directory` and the resources of each domain.
pub trait Sorts: Default {
    fn directory(&mut self) -> &mut Directory;

    /// Parses `value`, a document of `kind` in `GROUP` and `VERSION`,
    /// and stores it as the resource of that kind. It returns the
    /// condition that describes the result, or none when `GROUP` has no
    /// such kind.
    fn take(&mut self, doc: DocId, kind: &str, value: serde_yaml_ng::Value) -> Option<Condition>;
}

/// The contents of the resources directory: each file, and the
/// overlays, which `transect` merges onto the objects of every domain.
#[derive(Debug, Default)]
pub struct Directory {
    /// The resources directory, or none when `transect` has none.
    pub dir: Option<String>,
    pub files: Vec<File>,
    /// Shared with each reading of the cluster.
    pub overlays: Arc<Vec<Overlay>>,
}

/// A `Sorts` with no kind of `GROUP`, for tests of what this module
/// reads itself.
#[cfg(test)]
impl Sorts for Directory {
    fn directory(&mut self) -> &mut Directory {
        self
    }

    fn take(&mut self, _: DocId, _: &str, _: serde_yaml_ng::Value) -> Option<Condition> {
        None
    }
}

/// One file, with its path in the resources directory.
#[derive(Debug, PartialEq)]
pub struct File {
    pub path: String,
    /// Why nothing in it was read, such as YAML that does not parse.
    pub problem: Option<String>,
    pub documents: Vec<Document>,
}

/// One document of a file, and the conditions that loading it produced.
#[derive(Debug, PartialEq)]
pub struct Document {
    pub kind: String,
    pub name: String,
    pub conditions: Vec<Condition>,
}
