//! The map and what it draws: the elevations, the cards, a page per object, the lens tabs, and quick search.

// A template expands in whichever crate derives it, and a crate above this
// one includes these templates, so the templates name this crate's items by
// the crate's name, as `transect_map::objects::Shape`.
extern crate self as transect_map;

pub mod cards;
pub mod lenses;
pub mod map;
pub mod objects;
pub mod search;
