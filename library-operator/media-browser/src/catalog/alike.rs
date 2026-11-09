// A film that shares people with another, as the More like this strip of
// a movie's page reads it.

use super::Slot;

/// One film that shares people with the film a page is about: its slot,
/// its score, and the IMDb rating the strip breaks a tie of scores by.
/// A shared director scores 3, each actor billed in the first four of
/// both films scores 2, and a shared leading genre adds 1.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Alike {
    pub slot: Slot,
    pub score: i64,
    pub rating: f64,
}
