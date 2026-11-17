//! `transect`'s own resources, read from the YAML files under
//! `--resources`: the `Layers`, kind knowledge, device knowledge,
//! Intents, Outsides, registry mirrors, the log
//! source and its patterns, the metrics source, and overlays.
//!
//! One loader reads every document once and sorts it by kind.  A
//! document that does not parse or validate never stops `transect`: it
//! is left out with a condition that gives the reason, in the shape of
//! a CRD's `status.conditions`, which the `/resources` page lists.  Some
//! conditions need the live cluster, such as an overlay that names no
//! object, so the page adds those from each model of the cluster.
//!
//! This module reads the directory, keeps the overlays, and passes each
//! document of `GROUP` to a `Sorts`.  Each domain owns the parser for
//! its own kind, and the binary's `Sorts`, which depends on every
//! domain, sends each kind to its parser.  A domain reads its own field
//! of the loaded resources through `Field`.

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
/// annotation `transect` reads, as in `transect.liken.sh/why`.  This is the
/// only place the name is written.
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

/// The resources in `text`, read as the loader reads a file at `path`
/// under the directory, for the fixture cluster.
#[cfg(any(test, debug_assertions))]
pub fn from_text<S: Sorts>(path: &str, text: &str) -> S {
    parse::sort(vec![parse::Read {
        path: path.into(),
        text: Ok(text.into()),
    }])
}

/// Where one document is: its file's place among the loaded files, and
/// its place in that file.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct DocId {
    pub file: usize,
    pub document: usize,
}

/// What every document of `GROUP` is sorted into: the directory read,
/// and each domain's resources.  Only the binary depends on every
/// domain.
pub trait Sorts: Default {
    fn directory(&mut self) -> &mut Directory;

    /// Keeps `value`, a document of `GROUP` and `VERSION` of `kind`, as
    /// the resource it is, with the condition that states the result,
    /// or none when `GROUP` has no such kind.
    fn take(&mut self, doc: DocId, kind: &str, value: serde_yaml::Value) -> Option<Condition>;
}

/// What the directory held: each file, and the overlays, which
/// `transect` merges onto every domain's objects.
#[derive(Debug, Default)]
pub struct Directory {
    /// The directory read, or none when `transect` has none.
    pub dir: Option<String>,
    pub files: Vec<File>,
    /// Shared with each reading of the cluster that merges them onto
    /// its objects.
    pub overlays: Arc<Vec<Overlay>>,
}

/// A directory sorted with no kind of `GROUP`, for the tests of what
/// this module reads itself.
#[cfg(test)]
impl Sorts for Directory {
    fn directory(&mut self) -> &mut Directory {
        self
    }

    fn take(&mut self, _: DocId, _: &str, _: serde_yaml::Value) -> Option<Condition> {
        None
    }
}

/// One file, by its path under the directory.
#[derive(Debug, PartialEq)]
pub struct File {
    pub path: String,
    /// Why nothing in it was read, such as YAML that does not parse.
    pub problem: Option<String>,
    pub documents: Vec<Document>,
}

/// One document of a file, and the conditions loading it gave.
#[derive(Debug, PartialEq)]
pub struct Document {
    pub kind: String,
    pub name: String,
    pub conditions: Vec<Condition>,
}
