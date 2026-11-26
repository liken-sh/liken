//! The drawing of an elevation of rows: the object's own values above a
//! table with a row for each item of a list in its status. The status
//! of a `Session` is the only record of a turn after its `Job` is
//! deleted, so the table shows every value of every turn, and a cell
//! with no value stays empty.

use askama::Template;

use super::super::board::Board;
use transect_graph::links::Reference;
use transect_resources::knowledge::path;
use transect_resources::knowledge::rows;

/// The table of one elevation of rows.
#[derive(Debug, PartialEq, Template)]
#[template(path = "map/known/rows.html")]
pub struct Rows {
    /// The path of the list, which the table's caption names.
    pub from: String,
    /// Each value of the object above the table, with its label.
    pub header: Vec<(String, String)>,
    pub labels: Vec<String>,
    /// The cells of each item, in the order of the list, up to `MOST`.
    pub rows: Vec<Vec<RowCell>>,
    /// How many items the table leaves out after the first `MOST`.
    pub more: usize,
}

/// The most rows a table draws.  A `Session` that runs for weeks can
/// record hundreds of turns, and a page that draws them all is too long
/// to read.
pub const MOST: usize = 50;

/// One cell: the values of an item at the column's path, each with the
/// page of a `Job`'s lines when the column names a `Job` and a
/// `LogSource` can show them.  A path through a list reads several
/// values, so a cell can name several `Job`s.
#[derive(Debug, PartialEq)]
pub struct RowCell {
    pub values: Vec<Reference>,
    /// The raw value, when the cell writes it in a format.
    pub raw: Option<String>,
    /// Whether the value is one that the column says is a failure.
    pub failed: bool,
}

impl RowCell {
    pub fn is_empty(&self) -> bool {
        self.values.is_empty()
    }
}

impl Rows {
    /// The table of `rows` for the page's object.
    pub fn build(board: &Board, rows: &rows::Rows) -> Self {
        let data = board.data;
        let header = rows.header.iter().map(|field| {
            let value = path::read(data, &field.path).join(", ");
            let value = field.format.write(&value).unwrap_or(value);
            (field.label.clone(), value)
        });
        let items = path::values(data, &rows.from);
        let more = items.len().saturating_sub(MOST);
        let cells = items.into_iter().take(MOST).map(|item| {
            let cells = rows.columns.iter().map(|column| {
                let read = path::read(item, &column.path);
                if column.logs {
                    let names = read.into_iter().map(|name| Reference {
                        href: (board.logs)(&name),
                        text: name,
                    });
                    return RowCell {
                        values: names.collect(),
                        raw: None,
                        failed: false,
                    };
                }
                let failed = read.iter().any(|value| column.failed.contains(value));
                let raw = read.join(", ");
                let (text, raw) = match column.format.write(&raw) {
                    Some(text) => (text, Some(raw)),
                    None => (raw, None),
                };
                let values = (!text.is_empty()).then_some(Reference { text, href: None });
                RowCell {
                    values: values.into_iter().collect(),
                    raw,
                    failed,
                }
            });
            cells.collect()
        });
        Self {
            from: rows.from.clone(),
            header: header.collect(),
            labels: rows.columns.iter().map(|c| c.label.clone()).collect(),
            rows: cells.collect(),
            more,
        }
    }

    /// The links of the table, which the boxes under the page leave out.
    pub fn references(&self) -> Vec<&Reference> {
        Vec::new()
    }
}
