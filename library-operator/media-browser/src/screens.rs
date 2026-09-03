// A kind is a plugin in the scanner and a screen design in the browser.
// This module holds the entries of the navigation stack: the libraries
// list at the bottom, and the screens each kind descends into. A screen
// holds the rows it read, decides what a press does, and composes the
// primitives in the views module. A new kind adds a screen here and,
// where it needs one, a primitive there. It adds no row to a table.

pub mod facts;
pub mod foot;
pub mod libraries;
pub mod loading;
pub mod movie;
pub mod person;
pub mod series;
pub mod stripes;
pub mod volume;
pub mod wall;

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_winit::core::{Element, Theme};

use crate::catalog::{Selection, Source, Title};
use crate::posters::Posters;
use crate::views::Card;
use crate::views::curtain::Curtain;

/// One entry of the navigation stack.
pub enum Screen {
    /// Every library in the catalog. The browser opens on this screen.
    Libraries(libraries::Libraries),
    /// One library's titles, as a wall of art under a band.
    Wall(wall::Wall),
    /// One movie's page. It is boxed because it is much the largest
    /// variant, and every stack entry would otherwise carry its size.
    Movie(Box<movie::Movie>),
    /// One series' page, boxed for the reason a movie's page is.
    Series(Box<series::Series>),
    /// One person's page, boxed for the reason a movie's page is.
    Person(Box<person::Person>),
}

/// What a press asks the browser to do. A screen reads the catalog and
/// moves its own focus. Only the browser holds the stack and the bus, so
/// a screen names the screen it opens and never pushes one itself.
pub enum Step {
    /// The press changed the screen alone, or changed nothing.
    Stay,
    /// Push this screen over the one that answered.
    Open(Screen),
    /// Put this screen in the place of the one that answered, so back
    /// climbs to the screen that one was opened from.
    Replace(Screen),
    /// Ask the operator to play what the person chose.
    Play {
        /// The library the choice resolves against.
        library: String,
        /// What the person chose.
        selection: Selection,
    },
}

impl Screen {
    /// Fold one press into the screen and answer what it asks the
    /// browser for.
    pub fn key(&mut self, key: &str, source: &mut dyn Source) -> Step {
        match self {
            Self::Libraries(screen) => screen.key(key, source),
            Self::Wall(screen) => screen.key(key, source),
            Self::Movie(screen) => screen.key(key, source),
            Self::Series(screen) => screen.key(key, source),
            Self::Person(screen) => screen.key(key, source),
        }
    }

    /// Read this screen's rows again. A change that landed while the
    /// screen was covered was folded into the screen shown at the time,
    /// so the uncovered screen reads for itself.
    pub fn reread(&mut self, source: &mut dyn Source) {
        match self {
            Self::Libraries(screen) => screen.reread(source),
            Self::Wall(screen) => screen.reread(source),
            Self::Movie(screen) => screen.reread(source),
            Self::Series(screen) => screen.reread(source),
            Self::Person(screen) => screen.reread(source),
        }
    }

    /// Whether a rest of focus on this screen is worth a prefetch. Only a
    /// wall whose select opens a page over art answers true, so a press
    /// on any other screen schedules no frame.
    pub fn prefetches(&self) -> bool {
        match self {
            Self::Wall(screen) => screen.prefetches(),
            Self::Person(_) => true,
            _ => false,
        }
    }

    /// The library and the backdrop path of the page under the focused
    /// item, so the store decodes it while focus rests and the page
    /// opens with it drawn. A screen that opens no page over art answers
    /// nothing.
    pub fn resting(&self, source: &mut dyn Source) -> Option<(String, String)> {
        match self {
            Self::Wall(screen) => screen.resting(source),
            Self::Person(screen) => screen.resting(source),
            _ => None,
        }
    }

    /// Read the files this screen draws that live on a library
    /// volume and not in the catalog. Only a person's page holds one, and
    /// every other screen reads nothing.
    pub fn volume<P: Posters>(&mut self, posters: &P) {
        if let Self::Person(screen) = self {
            screen.read_biography(posters);
        }
    }

    /// The view of this screen, with its art drawn from the store. Only
    /// the two screens a title plays from draw the loading state, and
    /// every other screen ignores it.
    pub fn view<'a, P: Posters>(
        &'a self,
        posters: &'a RefCell<P>,
        curtain: Option<Curtain>,
    ) -> Element<'a, Infallible, Theme, Renderer> {
        match self {
            Self::Libraries(screen) => screen.view(posters),
            Self::Wall(screen) => screen.view(posters),
            Self::Movie(screen) => screen.view(posters, curtain),
            Self::Series(screen) => screen.view(posters, curtain),
            Self::Person(screen) => screen.view(posters),
        }
    }
}

/// One title, as a wall slot or a strip poster draws it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Item {
    /// The item's provider-scoped id, which a descent carries.
    pub id: String,
    /// The name a person reads, which is the caption under every slot.
    pub name: String,
    /// The line under the focused slot: the title, the year, the
    /// runtime, and the content rating, each only where the row carries
    /// it.
    pub line: facts::Line,
    /// The art path the poster store resolves, empty where the item has
    /// none.
    pub art: String,
}

impl Item {
    /// One title as a slot. The line is built once here, at the read,
    /// and not on every frame.
    pub fn of(title: Title) -> Self {
        let line = facts::Line::of(&[
            &title.title,
            facts::year(&title.released),
            &facts::runtime(title.duration),
            &title.rating,
        ]);
        Self {
            id: title.id,
            name: title.title,
            line,
            art: title.art,
        }
    }
}

impl Card for Item {
    fn art(&self) -> &str {
        &self.art
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn line_fitting(&self, chars: usize) -> &str {
        self.line.fitting(chars)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_slot_carries_the_facts_its_row_holds() {
        let item = Item::of(Title {
            id: "movie:sample:1".into(),
            title: "Specimen 0001".into(),
            released: "1987-04-02".into(),
            art: "posters/1.jpg".into(),
            duration: 5_820,
            rating: "PG-13".into(),
        });
        assert_eq!(item.line.words(), "Specimen 0001 · 1987 · 1h 37m · PG-13");
        assert_eq!(item.name, "Specimen 0001");
        assert_eq!(item.art, "posters/1.jpg");
    }

    #[test]
    fn a_narrow_band_drops_a_slot_s_facts_from_the_end() {
        let item = Item::of(Title {
            id: "movie:sample:1".into(),
            title: "Specimen 0001".into(),
            released: "1987-04-02".into(),
            art: "posters/1.jpg".into(),
            duration: 5_820,
            rating: "PG-13".into(),
        });
        assert_eq!(
            item.line_fitting(37),
            "Specimen 0001 · 1987 · 1h 37m · PG-13"
        );
        assert_eq!(item.line_fitting(36), "Specimen 0001 · 1987 · 1h 37m");
        assert_eq!(item.line_fitting(24), "Specimen 0001 · 1987");
        assert_eq!(item.line_fitting(19), "Specimen 0001");
        assert_eq!(item.line_fitting(4), "Specimen 0001");
    }

    #[test]
    fn a_slot_leaves_out_what_its_row_does_not_hold() {
        let item = Item::of(Title {
            id: "movie:sample:2".into(),
            title: "Specimen 0002".into(),
            ..Title::default()
        });
        assert_eq!(item.line.words(), "Specimen 0002");
        assert_eq!(item.line_fitting(4), "Specimen 0002");
    }
}
