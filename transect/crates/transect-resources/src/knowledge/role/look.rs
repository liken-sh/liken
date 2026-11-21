//! How the parts of a role look and what they read: the fields that a
//! role of a `KindKnowledge` and a role of a pattern of `transect`'s
//! catalog write the same way. One function reads them for both, so a
//! field means the same in each.

use serde::Deserialize;

use super::super::Reference;
use super::super::references::target_of;
use super::super::through::Through;
use super::{Column, Expected, Show};

/// The fields of a role that say how its parts are drawn.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Look {
    pub label: Option<String>,
    pub shape: Option<String>,
    pub expected: Expected,
    pub reads: Vec<Column>,
    pub shows: Vec<Show>,
    pub stack: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Written {
    label: Option<String>,
    shape: Option<String>,
    expected: Option<serde_yaml::Value>,
    #[serde(default)]
    reads: Vec<WrittenColumn>,
    #[serde(default)]
    shows: Vec<WrittenShow>,
    #[serde(default)]
    stack: bool,
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

/// The look of a role whose other fields `written` has, checked against
/// `references`, the references of the page's own object that `shows`
/// can follow.
pub fn read(written: serde_yaml::Mapping, references: &[Reference]) -> Result<Look, String> {
    let written: Written =
        serde_yaml::from_value(written.into()).map_err(|error| error.to_string())?;
    if written
        .label
        .as_deref()
        .is_some_and(|value| value.trim().is_empty())
    {
        return Err("label is empty".into());
    }
    let expected = written.expected.map(expected).transpose()?;
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
    Ok(Look {
        label: written.label,
        shape: written.shape,
        expected: expected.unwrap_or_default(),
        reads: columns(written.reads)?,
        shows: shows.collect::<Result<_, _>>()?,
        stack: written.stack,
    })
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
