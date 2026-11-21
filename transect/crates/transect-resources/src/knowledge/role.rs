//! A role is one place in the elevations of a kind, and where its parts
//! come from. `spec.roles` states each role once, and each
//! elevation places the roles it needs in its sketch, so every elevation
//! draws the same role the same way. A role that no elevation places is
//! still knowledge that an agent can read.

use std::sync::Arc;

use serde::Deserialize;

use super::Reference;
use super::through::Through;
use crate::shapes::{self, library};
use fill::WrittenNamedBy;
pub use fill::{Fill, First, NamedBy};
pub use look::Look;

mod fill;
pub mod look;

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
    /// Whether the page draws the parts of the role that are alike as
    /// one part, with each name and each reading's values. A part that
    /// its health or an unmet expectation marks stands apart from the
    /// stack, so the reader sees which part deviates.
    pub stack: bool,
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

/// The fields of a role of a `KindKnowledge` that say where its parts
/// come from. `look::read` reads the rest of the role.
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct WrittenFill {
    reference: Option<String>,
    named_by: Option<WrittenNamedBy>,
    through: Option<String>,
    elevation: Option<String>,
}

/// The keys of `WrittenFill`, as a document writes them.
const FILL_KEYS: &[&str] = &["reference", "namedBy", "through", "elevation"];

/// The keys of a role of a pattern of the catalog, which says that the
/// kind's `pattern` fills it or that the pattern derives its parts. A
/// `KindKnowledge` states neither.
const CATALOG_KEYS: &[&str] = &["fill", "derive"];

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
    for (name, mut written) in entries(written)? {
        let role = (|| {
            if let Some(key) = CATALOG_KEYS.iter().find(|key| written.contains_key(**key)) {
                return Err(format!(
                    "{key} is a field of a role of transect's catalog, and a KindKnowledge fills the roles of its pattern in pattern.roles"
                ));
            }
            let fill: WrittenFill = serde_yaml::from_value(take(&mut written, FILL_KEYS).into())
                .map_err(|error| error.to_string())?;
            let look = look::read(written, references)?;
            let elevation = fill.elevation;
            let fill = Fill::read(
                fill.reference,
                fill.named_by,
                fill.through,
                references,
                group,
            )?;
            Role::new(name.clone(), fill, elevation, look, references)
        })()
        .map_err(|e| format!("roles.{name}: {e}"))?;
        roles.push(role);
    }
    Ok(roles)
}

/// Each role of `written` by its name, in the order of the document,
/// with its fields as a map. A name is checked here, because the page
/// writes it into the sketch's grid.
pub fn entries(
    written: Option<serde_yaml::Mapping>,
) -> Result<Vec<(String, serde_yaml::Mapping)>, String> {
    let mut entries = Vec::new();
    for (name, written) in written.unwrap_or_default() {
        let Some(name) = name.as_str().map(String::from) else {
            return Err(format!("roles has the key {name:?}, which is not a name"));
        };
        if !valid_name(&name) {
            return Err(format!(
                "roles has the name {name:?}, which is not a lower-case letter followed by lower-case letters, digits, and hyphens"
            ));
        }
        let serde_yaml::Value::Mapping(written) = written else {
            return Err(format!("roles.{name} is not a map of fields"));
        };
        entries.push((name, written));
    }
    Ok(entries)
}

/// The fields of `written` whose keys are in `keys`, which it removes
/// from `written`, so the fields that are left are those that
/// `look::read` reads.
pub fn take(written: &mut serde_yaml::Mapping, keys: &[&str]) -> serde_yaml::Mapping {
    let mut taken = serde_yaml::Mapping::new();
    for key in keys {
        if let Some(value) = written.remove(*key) {
            taken.insert((*key).into(), value);
        }
    }
    taken
}

impl Role {
    /// The role `name`, whose parts come from `fill`, drawn as `look`
    /// says, and as the elevation `elevation` of their kind when it is
    /// some. The checks here need both the fill and the look:
    /// `references` are the document's own.
    pub fn new(
        name: String,
        fill: Fill,
        elevation: Option<String>,
        look: Look,
        references: &[Reference],
    ) -> Result<Self, String> {
        if elevation
            .as_deref()
            .is_some_and(|value| value.trim().is_empty())
        {
            return Err("elevation is empty".into());
        }
        let first_kind = fill.first_kind(references);
        if let (Some(elevation), None, None) = (&elevation, &fill.through, first_kind) {
            return Err(format!(
                "the role draws each part as {elevation:?}, and the reference {} names no kind, so its parts are values with no elevation",
                fill.first.fields()
            ));
        }
        if let (Some(elevation), true) = (&elevation, look.stack) {
            return Err(format!(
                "the role draws each part in full as {elevation:?}, so its parts cannot stack"
            ));
        }
        let follows = look.reads.iter().find(|column| column.through.is_some());
        if let (Some(column), None, None) = (follows, &fill.through, first_kind) {
            return Err(format!(
                "reads {} through a reference of the part's kind, and the reference {} names no kind",
                column.name,
                fill.first.fields()
            ));
        }
        Ok(Self {
            name,
            fill,
            label: look.label,
            elevation,
            shape: look.shape,
            drawn: library::default(),
            expected: look.expected,
            reads: look.reads,
            shows: look.shows,
            stack: look.stack,
            unresolved: None,
        })
    }

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
            First::Derived(_) | First::Unfilled => String::new(),
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
