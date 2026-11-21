//! A role is one place in the elevations of a kind, and the reference
//! whose values fill it. `spec.roles` states each role once, and each
//! elevation places the roles it needs in its sketch, so every elevation
//! draws the same role the same way. A role that no elevation places is
//! still knowledge that an agent can read.

use std::sync::Arc;

use serde::Deserialize;

use super::Reference;
use super::references::target_of;
use super::through::Through;
use crate::shapes::{self, library};

/// One role, such as the screen of a kiosk.
#[derive(Clone, Debug, PartialEq)]
pub struct Role {
    /// The role's name, which a sketch writes in its cells.
    pub name: String,
    /// The `path` of the entry of `references` whose values are the
    /// role's parts.
    pub reference: String,
    /// The shape's name as the role writes it. A role names a shape
    /// only to draw its parts otherwise than the knowledge of their kind
    /// states, or when its reference names no kind.
    pub shape: Option<String>,
    /// The definition that the page draws the role's parts in, which
    /// `KindKnowledge::draw_with` finds. Until then it is the library's
    /// default.
    pub drawn: Arc<shapes::Shape>,
    /// Whether the page draws the role when its reference reads no
    /// value.
    pub expected: bool,
    /// The printer columns that each part shows: of the part's object,
    /// or of the object at the end of a reference of the part's kind.
    pub reads: Vec<Column>,
    /// The printer columns of the page's own object, or of the object at
    /// the end of one of its references, that each part of the role
    /// shows, such as what a kiosk plays on the part that is its
    /// screen.
    pub shows: Vec<Show>,
}

/// One printer column, as `kubectl get` prints it, of an object or of
/// the object at the end of one of its references.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Column {
    /// The column's name as its CRD declares it.
    pub name: String,
    pub through: Option<Through>,
}

/// One entry of a role's `shows`: columns in order of preference. The
/// part draws the first of them that has a value, so an author can
/// write "the play when one plays, and the idle screen when none does".
/// The parser guarantees that the list is not empty.
#[derive(Clone, Debug, PartialEq)]
pub struct Show {
    pub columns: Vec<Column>,
}

/// One entry of `roles`, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Written {
    reference: String,
    shape: Option<String>,
    #[serde(default)]
    expected: bool,
    #[serde(default)]
    reads: Vec<WrittenColumn>,
    #[serde(default)]
    shows: Vec<WrittenShow>,
}

/// A column as written: its name, or a map with its name and the
/// reference to follow first. The map is read as a map, because serde
/// would also read a struct from a list of two names.
#[derive(Deserialize)]
#[serde(untagged)]
enum WrittenColumn {
    Name(String),
    Through(serde_yaml::Mapping),
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenThrough {
    column: String,
    through: String,
}

/// One entry of a role's `shows`, as written: one column, or a list of
/// columns of which the first with a value is drawn.
#[derive(Deserialize)]
#[serde(untagged)]
enum WrittenShow {
    First(Vec<WrittenColumn>),
    One(WrittenColumn),
}

/// The column that `written` names.
fn column(written: WrittenColumn) -> Result<Column, String> {
    Ok(match written {
        WrittenColumn::Name(name) => Column {
            name,
            through: None,
        },
        WrittenColumn::Through(map) => {
            let written: WrittenThrough = serde_yaml::from_value(map.into())
                .map_err(|error| format!("a column with through: {error}"))?;
            Column {
                name: written.column,
                through: Some(Through::new(written.through)),
            }
        }
    })
}

/// The columns that `written` names.
fn columns(written: Vec<WrittenColumn>) -> Result<Vec<Column>, String> {
    written.into_iter().map(column).collect()
}

/// The roles of `written`, the document's `spec.roles`, in the order
/// the document writes them, checked against the kind's `references`.
/// The error names the first fault and the role it is in.
pub fn read(
    written: Option<serde_yaml::Mapping>,
    references: &[Reference],
) -> Result<Vec<Role>, String> {
    let mut roles = Vec::new();
    for (name, written) in written.unwrap_or_default() {
        let Some(name) = name.as_str().map(String::from) else {
            return Err(format!("roles has the key {name:?}, which is not a name"));
        };
        if !valid_name(&name) {
            return Err(format!(
                "roles has the name {name:?}, which is not a lower-case letter followed by lower-case letters, digits, and hyphens"
            ));
        }
        let written: Written =
            serde_yaml::from_value(written).map_err(|error| format!("roles.{name}: {error}"))?;
        let role =
            role(name.clone(), written, references).map_err(|e| format!("roles.{name}: {e}"))?;
        roles.push(role);
    }
    Ok(roles)
}

fn role(name: String, written: Written, references: &[Reference]) -> Result<Role, String> {
    if !references.iter().any(|r| r.path == written.reference) {
        return Err(format!(
            "reference {} is the path of no entry of references",
            written.reference
        ));
    }
    let reads = columns(written.reads)?;
    let follows = reads.iter().find(|column| column.through.is_some());
    if let Some(column) = follows
        && target_of(references, &written.reference).is_none()
    {
        return Err(format!(
            "reads {} through a reference of the part's kind, and the reference {} names no kind",
            column.name, written.reference
        ));
    }
    let shows = written.shows.into_iter().enumerate().map(|(at, show)| {
        let columns = match show {
            WrittenShow::One(written) => vec![column(written)?],
            WrittenShow::First(written) => columns(written)?,
        };
        if columns.is_empty() {
            return Err(format!(
                "shows[{at}] is an empty list, and an entry of shows is a column or a list of one or more columns"
            ));
        }
        let through = columns.iter().filter_map(|column| column.through.as_ref());
        if let Some(through) = through.into_iter().find(|t| target_of(references, &t.path).is_none()) {
            return Err(format!(
                "shows[{at}] follows {}, which is the path of no entry of references that names a kind",
                through.path
            ));
        }
        Ok(Show { columns })
    });
    Ok(Role {
        name,
        reference: written.reference,
        shape: written.shape,
        drawn: library::default(),
        expected: written.expected,
        reads,
        shows: shows.collect::<Result<_, _>>()?,
    })
}

/// Whether `name` can name a role. The rule keeps a name a valid CSS
/// identifier with no quote, because the page writes it into
/// `grid-template-areas` and `grid-area`.
pub fn valid_name(name: &str) -> bool {
    let mut chars = name.chars();
    let first = chars.next().is_some_and(|c| c.is_ascii_lowercase());
    first && chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-')
}

#[cfg(test)]
mod tests;
