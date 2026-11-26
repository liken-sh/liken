//! Rows are the form of an elevation that draws a table from a list in
//! the object's status, one row for each item. A `Session` records each
//! turn in `status.turns` and its `Job`s are deleted when their time to
//! live ends, so the status is the only record in the cluster of how a
//! turn ended.

use serde::Deserialize;

/// The table of a rows elevation. The parser guarantees that `from`
/// reads each item of a list.
#[derive(Clone, Debug, PartialEq)]
pub struct Rows {
    /// The path of the list in the object, with `[]`, such as
    /// `status.turns[]`.
    pub from: String,
    /// Values of the object, shown above the table.
    pub header: Vec<Field>,
    /// The values of each item, by a path relative to the item.
    pub columns: Vec<Field>,
}

/// One value that the table reads, with what it is called.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Field {
    pub label: String,
    pub path: String,
    /// Whether the value is the name of a `Job` in the object's
    /// namespace, whose lines the cell links to.
    pub logs: bool,
    /// How the table writes the value.  The raw value stays in the
    /// cell's hover.
    pub format: Format,
    /// The values that say an item failed, such as `Failed` for the
    /// outcome of a turn.  The table draws such a cell in the color of
    /// a fault.
    pub failed: Vec<String>,
}

/// How a table writes a value of a field.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Format {
    /// As the object has it.
    #[default]
    Text,
    /// An RFC 3339 time, as a short date and time in UTC, such as
    /// "Jan 1, 09:00 UTC".
    Time,
    /// A number of US dollars, with cents, such as "$0.42".
    Dollars,
}

/// `rows`, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    from: Option<String>,
    #[serde(default)]
    header: Vec<WrittenField>,
    #[serde(default)]
    columns: Vec<WrittenField>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WrittenField {
    label: String,
    path: String,
    #[serde(default)]
    logs: bool,
    format: Option<String>,
    #[serde(default)]
    failed: Vec<String>,
}

/// The table of `written`. The error names the first fault.
pub fn read(written: Written) -> Result<Rows, String> {
    let Some(from) = written.from else {
        return Err("rows has no from, so it has no list to read".into());
    };
    if !from.contains("[]") {
        return Err(format!(
            "rows reads {from}, which has no [], so it reads no items of a list"
        ));
    }
    if written.columns.is_empty() {
        return Err("rows has no columns".into());
    }
    if let Some(at) = written.header.iter().position(|field| field.logs) {
        return Err(format!(
            "header[{at}] has logs, and only a column of an item names a Job"
        ));
    }
    let fields = |written: Vec<WrittenField>| -> Result<Vec<Field>, String> {
        let fields = written.into_iter().map(|field| {
            let format = match field.format.as_deref() {
                None | Some("text") => Format::Text,
                Some("time") => Format::Time,
                Some("dollars") => Format::Dollars,
                Some(other) => {
                    return Err(format!(
                        "{} has format {other:?}, and transect reads text, time, or dollars",
                        field.label
                    ));
                }
            };
            Ok(Field {
                label: field.label,
                path: field.path,
                logs: field.logs,
                format,
                failed: field.failed,
            })
        });
        fields.collect()
    };
    Ok(Rows {
        from,
        header: fields(written.header)?,
        columns: fields(written.columns)?,
    })
}

impl Format {
    /// `value` as the table writes it, or none when it is written as it
    /// is: a value of `Text`, or one that does not parse as the format.
    pub fn write(self, value: &str) -> Option<String> {
        match self {
            Self::Text => None,
            Self::Time => {
                let time: k8s_openapi::jiff::Timestamp = value.parse().ok()?;
                Some(time.strftime("%b %-d, %H:%M UTC").to_string())
            }
            Self::Dollars => {
                let dollars: f64 = value.parse().ok()?;
                Some(format!("${dollars:.2}"))
            }
        }
    }
}

#[cfg(test)]
mod tests;
