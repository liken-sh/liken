//! A role is one place in the elevations of a kind, and where its parts
//! come from. `spec.roles` states each role once, and each
//! elevation places the roles it needs in its sketch, so every elevation
//! draws the same role the same way. A role that no elevation places is
//! still knowledge that an agent can read.

use std::sync::Arc;

use serde::Deserialize;

use super::Reference;
use super::references::target_of;
use super::through::Through;
use crate::shapes::{self, library};
use fill::WrittenNamedBy;
pub use fill::{Fill, First, NamedBy};

mod fill;

/// One role, such as the screen of a kiosk.
#[derive(Clone, Debug, PartialEq)]
pub struct Role {
    /// The role's name, which a sketch writes in its cells.
    pub name: String,
    /// Where the role's parts come from.
    pub fill: Fill,
    /// What a part of the role is called, in place of the label of its
    /// reference or the name of its kind.
    pub label: Option<String>,
    /// The name of an elevation of the parts' kind. A sketch draws each
    /// part as that elevation, in the role's area, so a role can draw a
    /// whole arrangement, such as each shelf of a stock room.
    pub elevation: Option<String>,
    /// The shape's name as the role writes it. A role names a shape
    /// only to draw its parts otherwise than the knowledge of their kind
    /// states, or when its reference names no kind.
    pub shape: Option<String>,
    /// The definition that the page draws the role's parts in, which
    /// `KindKnowledge::draw_with` finds. Until then it is the library's
    /// default.
    pub drawn: Arc<shapes::Shape>,
    /// When the page draws the role with no part.
    pub expected: Expected,
    /// The printer columns that each part shows: of the part's object,
    /// or of the object at the end of a reference of the part's kind.
    pub reads: Vec<Column>,
    /// The printer columns of the page's own object, or of the object at
    /// the end of one of its references, that each part of the role
    /// shows, such as what a kiosk plays on the part that is its
    /// screen.
    pub shows: Vec<Show>,
    /// Why `Kinds::resolve` could not confirm the role's `namedBy` or
    /// its `elevation` in the knowledge of another kind. The page shows
    /// it in the role's place. A `through` keeps its own reason.
    pub unresolved: Option<String>,
}

/// When the page draws a role that has no part. A role that is drawn
/// with no part says that a part is missing, so a role that the object
/// needs only in some state is expected only then.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub enum Expected {
    /// The page leaves the role's area blank.
    #[default]
    Never,
    /// The page draws the role's empty place.
    Always,
    /// The role is expected while the page's object has this condition
    /// with the status `True`. At other times its empty place is drawn
    /// at rest, because it is not missing a part.
    While(String),
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
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Written {
    reference: Option<String>,
    named_by: Option<WrittenNamedBy>,
    through: Option<String>,
    label: Option<String>,
    elevation: Option<String>,
    shape: Option<String>,
    expected: Option<serde_yaml::Value>,
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
/// `group` is the group of the document's kind. The error names the
/// first fault and the role it is in.
pub fn read(
    written: Option<serde_yaml::Mapping>,
    references: &[Reference],
    group: &str,
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
        let role = role(name.clone(), written, references, group)
            .map_err(|e| format!("roles.{name}: {e}"))?;
        roles.push(role);
    }
    Ok(roles)
}

fn role(
    name: String,
    written: Written,
    references: &[Reference],
    group: &str,
) -> Result<Role, String> {
    let fill = Fill::read(
        written.reference,
        written.named_by,
        written.through,
        references,
        group,
    )?;
    for (field, value) in [("label", &written.label), ("elevation", &written.elevation)] {
        if value
            .as_deref()
            .is_some_and(|value| value.trim().is_empty())
        {
            return Err(format!("{field} is empty"));
        }
    }
    if let (Some(elevation), None, None) = (
        &written.elevation,
        &fill.through,
        fill.first_kind(references),
    ) {
        return Err(format!(
            "the role draws each part as {elevation:?}, and the reference {} names no kind, so its parts are values with no elevation",
            fill.first.fields()
        ));
    }
    let expected = written.expected.map(expected).transpose()?;
    let reads = columns(written.reads)?;
    let follows = reads.iter().find(|column| column.through.is_some());
    if let (Some(column), None, None) = (follows, &fill.through, fill.first_kind(references)) {
        return Err(format!(
            "reads {} through a reference of the part's kind, and the reference {} names no kind",
            column.name,
            fill.first.fields()
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
        fill,
        label: written.label,
        elevation: written.elevation,
        shape: written.shape,
        drawn: library::default(),
        expected: expected.unwrap_or_default(),
        reads,
        shows: shows.collect::<Result<_, _>>()?,
        unresolved: None,
    })
}

/// `expected`, as written: `true`, `false`, or `{while: <condition
/// type>}`.
fn expected(written: serde_yaml::Value) -> Result<Expected, String> {
    let fault = || {
        format!(
            "expected is {}, and it is true, false, or {{while: <condition type>}}",
            serde_yaml::to_string(&written).unwrap_or_default().trim()
        )
    };
    match &written {
        serde_yaml::Value::Bool(true) => Ok(Expected::Always),
        serde_yaml::Value::Bool(false) => Ok(Expected::Never),
        serde_yaml::Value::Mapping(map) if map.len() == 1 => {
            let condition = map.get("while").and_then(serde_yaml::Value::as_str);
            let condition = condition.filter(|condition| !condition.trim().is_empty());
            condition
                .map(|c| Expected::While(c.into()))
                .ok_or_else(fault)
        }
        _ => Err(fault()),
    }
}

impl Role {
    /// What a part of the role is called: the role's `label`, the label
    /// of its reference, or for the objects that name the page's object,
    /// their kind in lower-case words, so a `ShelfLabel` is a "shelf
    /// label".
    pub fn label(&self, references: &[Reference]) -> String {
        if let Some(label) = &self.label {
            return label.clone();
        }
        match &self.fill.first {
            First::Reference(path) => {
                let reference = references.iter().find(|r| r.path == *path);
                let label = reference.map(|r| r.label.as_deref().unwrap_or(r.edge.words()));
                label.unwrap_or_default().to_string()
            }
            First::NamedBy(named) => words(&named.kind),
        }
    }

    /// Why the role's parts or its elevation could not be confirmed, when
    /// `Kinds::resolve` could not.
    pub fn unresolved(&self) -> Option<&str> {
        let through = self.fill.through.as_ref();
        let through = through.and_then(|through| through.unresolved.as_deref());
        self.unresolved.as_deref().or(through)
    }
}

/// `kind` in lower-case words: `ShelfLabel` is "shelf label", and
/// `UPS` is "ups".
fn words(kind: &str) -> String {
    let mut words = String::new();
    let mut previous: Option<char> = None;
    for c in kind.chars() {
        if c.is_uppercase() && previous.is_some_and(char::is_lowercase) {
            words.push(' ');
        }
        words.extend(c.to_lowercase());
        previous = Some(c);
    }
    words
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
