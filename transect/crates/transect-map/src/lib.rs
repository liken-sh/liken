//! The map and the pages it draws: the elevations, the cards, a page
//! for each object, the lens tabs, and quick search.

// Askama expands an included template in the crate that derives the
// outer template, and templates in crates that depend on this one include
// this crate's templates. So these templates name this crate's items by
// crate path, as `transect_map::objects::Shape`, and this line makes
// that path resolve inside this crate too.
extern crate self as transect_map;

pub mod cards;
pub mod lenses;
pub mod map;
pub mod objects;
pub mod search;
pub mod shapes;
