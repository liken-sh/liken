// The titles of one studio, as the More from strip of a title's page
// reads them.

use super::Slot;

/// One studio a title names and every title of the catalog whose body
/// names it too, across the libraries, newest release first. The title
/// the page is about is one of them.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Studio {
    pub name: String,
    pub titles: Vec<Slot>,
}
