//! Where the parts of a role come from. A role reads a reference of the
//! page's own object, such as the screen that a kiosk names, or the
//! objects of another kind that name the page's object, such as the
//! shelves that name their room. Either first step can follow one more
//! reference, so a role can draw the shelf that a bin names.
//!
//! The objects that name a page's object are read from the same
//! relations as every reference: `namedBy` names a reference that the
//! knowledge of the other kind states, so its rules of matching apply,
//! and that knowledge makes `transect` keep the other kind's spec in its
//! watch.

use serde::Deserialize;

use super::super::Reference;
use super::super::references::{Names, Relation, target_of};
use super::super::through::Through;
use transect_graph::edges::Object;

/// Where a role's parts come from: a first step, and one more reference
/// to follow from each object that it reads.
#[derive(Clone, Debug, PartialEq)]
pub struct Fill {
    pub first: First,
    /// The reference to follow from each object of the first step. Its
    /// path is a reference of the first step's kind, so only
    /// `Kinds::resolve` can confirm it, and it finds the kind of the
    /// parts there.
    pub through: Option<Through>,
}

/// The first step of a role's parts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum First {
    /// The values of the reference at this path of the page's object.
    Reference(String),
    /// The objects of another kind whose field names the page's object.
    NamedBy(NamedBy),
}

/// The objects of `kind` whose field at `path` names the page's object.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NamedBy {
    pub kind: String,
    /// The kind's group, which is the group of the page's kind unless
    /// the role states another.
    pub group: String,
    pub path: String,
}

/// `namedBy`, as written. Each field is optional here so that the
/// parser can name the one that is missing.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WrittenNamedBy {
    kind: Option<String>,
    group: Option<String>,
    path: Option<String>,
}

impl Fill {
    /// The fill of a role that writes `reference` or `named_by`, and
    /// `through`, checked against `references`, the document's own. A
    /// `namedBy` with no group takes `group`, the group of the
    /// document's kind.
    pub fn read(
        reference: Option<String>,
        named_by: Option<WrittenNamedBy>,
        through: Option<String>,
        references: &[Reference],
        group: &str,
    ) -> Result<Self, String> {
        let first = match (reference, named_by) {
            (Some(_), Some(_)) => {
                return Err(
                    "the role has both reference and namedBy, and its parts come from one of them"
                        .into(),
                );
            }
            (None, None) => {
                return Err(
                    "the role has neither reference nor namedBy, so nothing gives it parts".into(),
                );
            }
            (Some(path), None) => {
                if !references.iter().any(|r| r.path == path) {
                    return Err(format!(
                        "reference {path} is the path of no entry of references"
                    ));
                }
                First::Reference(path)
            }
            (None, Some(written)) => First::NamedBy(NamedBy::read(written, group)?),
        };
        let through = match through {
            Some(path) if path.trim().is_empty() => return Err("through is empty".into()),
            Some(path) => Some(Through::new(path)),
            None => None,
        };
        let fill = Self { first, through };
        if let (Some(through), None) = (&fill.through, fill.first_kind(references)) {
            return Err(format!(
                "the role follows {} from {}, which names no kind",
                through.path,
                fill.first.fields()
            ));
        }
        Ok(fill)
    }

    /// The group and the kind of the objects of the first step, or none
    /// when its reference reads values that name no kind.
    pub fn first_kind<'a>(&'a self, references: &'a [Reference]) -> Option<(&'a str, &'a str)> {
        match &self.first {
            First::Reference(path) => target_of(references, path),
            First::NamedBy(named) => Some((&named.group, &named.kind)),
        }
    }

    /// The group and the kind of the role's parts: the kind at the end
    /// of `through`, once `Kinds::resolve` has found it, or the kind of
    /// the first step.
    pub fn parts_kind<'a>(&'a self, references: &'a [Reference]) -> Option<(&'a str, &'a str)> {
        match &self.through {
            Some(through) => through
                .target
                .as_ref()
                .map(|(group, kind)| (group.as_str(), kind.as_str())),
            None => self.first_kind(references),
        }
    }

    /// The path of the page's own reference that the role reads, when
    /// its first step is one.
    pub fn reference(&self) -> Option<&str> {
        match &self.first {
            First::Reference(path) => Some(path),
            First::NamedBy(_) => None,
        }
    }

    /// The fields that the parts are read from, such as
    /// "Shelf spec.room, then spec.label".
    pub fn fields(&self) -> String {
        let first = self.first.fields();
        match &self.through {
            Some(through) => format!("{first}, then {}", through.path),
            None => first,
        }
    }
}

impl First {
    /// The field of the first step: the page's own path, or the kind and
    /// the path of the objects that name the page's object.
    pub fn fields(&self) -> String {
        match self {
            Self::Reference(path) => path.clone(),
            Self::NamedBy(named) => format!("{} {}", named.kind, named.path),
        }
    }
}

impl NamedBy {
    fn read(written: WrittenNamedBy, group: &str) -> Result<Self, String> {
        let present = |field: Option<String>, name: &str| {
            field
                .filter(|value| !value.trim().is_empty())
                .ok_or_else(|| format!("namedBy has no {name}"))
        };
        Ok(Self {
            kind: present(written.kind, "kind")?,
            path: present(written.path, "path")?,
            group: written.group.unwrap_or_else(|| group.to_string()),
        })
    }

    /// Whether `referrer`, whose relations are `relations`, is an object
    /// of the kind that names `named` at the path.
    pub fn names(&self, referrer: &Object, relations: &[Relation], named: &Object) -> bool {
        let kind = (referrer.group.as_str(), referrer.kind.as_str());
        kind == (self.group.as_str(), self.kind.as_str())
            && relations.iter().any(|relation| {
                relation.path == self.path && relation.names == Names::Object(named.clone())
            })
    }
}

#[cfg(test)]
mod tests;
